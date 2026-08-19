package usage

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

const (
	defaultInterval   = 5 * time.Minute
	defaultMinGap     = 30 * time.Second
	defaultConcurrent = 4
	backoffBase       = time.Minute
	backoffCap        = 15 * time.Minute
	rateGateFallback  = 5 * time.Minute
	// staleRetry is the first re-check delay after a renewal failed to produce a
	// usable token. It doubles per consecutive stale round, capped at the poll
	// interval, so a token that cannot be renewed stops being hammered.
	staleRetry = time.Minute
	// renewCooldown is the minimum gap between renewal attempts for one
	// credential path. Renewal spawns the provider CLI, so it stays rare.
	renewCooldown = 2 * time.Minute
	// staleCarryMax is how long a snapshot's windows may be carried over into a
	// failed round before we stop showing them; utilization drifts.
	staleCarryMax = time.Hour
	// minWake floors the poll loop's sleep so a deadline already in the past
	// cannot spin it.
	minWake = 5 * time.Second
)

// Options configures a Tracker.
type Options struct {
	// Profiles returns the always-current configured profile list. Required.
	Profiles func() []config.Profile
	// HTTPClient is used for all provider fetches; defaults to a 15s client.
	HTTPClient *http.Client
	Logger     *slog.Logger
	// Interval between poll ticks; defaults to 5m.
	Interval time.Duration
	// MinGap is the minimum age before a non-forced Refresh re-fetches; 30s.
	MinGap time.Duration
	// Renew asks the provider CLI to refresh its own expired token, after which
	// the tracker re-reads credentials and re-fetches. Podiom never performs the
	// token exchange itself. Optional; nil disables auto-renewal.
	Renew func(ctx context.Context, provider config.Provider, dir string) error
}

// Tracker owns the in-memory per-profile usage cache and its polling loop.
type Tracker struct {
	profiles   func() []config.Profile
	hc         *http.Client
	log        *slog.Logger
	interval   time.Duration
	minGap     time.Duration
	concurrent int
	renew      func(ctx context.Context, provider config.Provider, dir string) error

	mu    sync.Mutex
	cache map[string]Snapshot // keyed by snapshot key (profile)
	// rateGate/backoff/failStreak/renewAt/threshold are keyed by resolved
	// credential path.
	rateGate  map[string]time.Time
	backoff   map[string]time.Time
	failure   map[string]int
	renewAt   map[string]time.Time // earliest next renewal attempt
	threshold map[string]int       // key "<profile>|<window>" -> highest crossed bucket

	nudge chan bool // buffered; carries the force flag for an out-of-band refresh
	stop  chan struct{}
	done  chan struct{}
}

// target is one profile we track, resolved to its credential path.
type target struct {
	key       string
	provider  config.Provider
	dir       string
	path      string
	isDefault bool
}

// New constructs a Tracker. Call Start to begin polling.
func New(opts Options) *Tracker {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	minGap := opts.MinGap
	if minGap <= 0 {
		minGap = defaultMinGap
	}
	return &Tracker{
		profiles:   opts.Profiles,
		hc:         hc,
		log:        log.With("component", "usage"),
		interval:   interval,
		minGap:     minGap,
		concurrent: defaultConcurrent,
		renew:      opts.Renew,
		cache:      map[string]Snapshot{},
		rateGate:   map[string]time.Time{},
		backoff:    map[string]time.Time{},
		failure:    map[string]int{},
		renewAt:    map[string]time.Time{},
		threshold:  map[string]int{},
		nudge:      make(chan bool, 1),
	}
}

// Start kicks off an immediate refresh and then a periodic poll loop.
func (t *Tracker) Start() {
	if t.stop != nil {
		return
	}
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	t.log.Info("usage tracker started", "event", "usage", "stage", "start",
		"interval", t.interval.String(), "targets", len(t.targets()))
	go t.loop()
}

// Stop halts the poll loop. Safe to call once.
func (t *Tracker) Stop() {
	if t.stop == nil {
		return
	}
	close(t.stop)
	<-t.done
	t.stop = nil
	t.log.Info("usage tracker stopped", "event", "usage", "stage", "stop")
}

func (t *Tracker) loop() {
	defer close(t.done)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-t.stop
		cancel()
	}()

	force := false
	for {
		snaps := t.Refresh(ctx, force)
		force = false
		timer := time.NewTimer(t.nextWake(snaps))
		select {
		case <-t.stop:
			timer.Stop()
			return
		case f := <-t.nudge:
			timer.Stop()
			force = f
		case <-timer.C:
		}
	}
}

// nextWake returns how long to sleep before the next poll: the poll interval,
// shortened to the soonest pending retry deadline so a stale token or a lifted
// rate gate is re-checked promptly instead of waiting out a full interval.
//
// Deadlines already in the past are ignored. They mean the gate has lifted and
// the path was re-fetched (or is about to be) — honouring them would shorten
// every wake to the floor and spin the loop.
func (t *Tracker) nextWake(snaps []Snapshot) time.Duration {
	wake := t.interval
	now := time.Now()
	for _, s := range snaps {
		if s.NextRetryAt.IsZero() {
			continue
		}
		d := s.NextRetryAt.Sub(now)
		if d > 0 && d < wake {
			wake = d
		}
	}
	if wake < minWake {
		wake = minWake
	}
	return wake
}

// Kick asks the poll loop to refresh now instead of waiting for its next wake.
// It never blocks; a kick arriving while one is already queued is dropped.
func (t *Tracker) Kick() { t.kick(false) }

// KickNow is Kick for events that just changed credentials on disk (a completed
// sign-in): it also bypasses cache freshness and error backoff so the new state
// lands immediately rather than after the next minimum gap.
func (t *Tracker) KickNow() { t.kick(true) }

func (t *Tracker) kick(force bool) {
	select {
	case t.nudge <- force:
	default:
	}
}

// Snapshots returns the cached snapshots sorted by provider then profile.
func (t *Tracker) Snapshots() []Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Snapshot, 0, len(t.cache))
	for _, s := range t.cache {
		out = append(out, s)
	}
	sortSnapshots(out)
	return out
}

func sortSnapshots(s []Snapshot) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Provider != s[j].Provider {
			return s[i].Provider < s[j].Provider
		}
		return s[i].Profile < s[j].Profile
	})
}

// targets enumerates the per-provider implicit defaults plus every named profile.
func (t *Tracker) targets() []target {
	var out []target
	for _, id := range config.ProviderIDs() {
		out = append(out, target{key: string(id), provider: id, isDefault: true})
	}
	if t.profiles != nil {
		for _, p := range t.profiles() {
			out = append(out, target{key: p.Name, provider: p.Provider, dir: p.Dir()})
		}
	}
	for i := range out {
		out[i].path = credentialPath(out[i].provider, out[i].dir)
	}
	return out
}

// credentialPath resolves the on-disk credential file for a provider/dir. It is
// the dedupe key: two profiles resolving to the same path share one fetch.
func credentialPath(provider config.Provider, dir string) string {
	if p, ok := usageProviders[provider]; ok {
		return p.credentialPath(dir)
	}
	return dir
}

// Refresh fetches usage for all targets and returns the resulting snapshots.
// force bypasses cache freshness and error backoff but never the 429 rate gate.
func (t *Tracker) Refresh(ctx context.Context, force bool) []Snapshot {
	targets := t.targets()

	// Group targets by resolved credential path so a shared path is fetched once.
	groups := map[string][]target{}
	order := []string{}
	for _, tg := range targets {
		if _, ok := groups[tg.path]; !ok {
			order = append(order, tg.path)
		}
		groups[tg.path] = append(groups[tg.path], tg)
	}

	type result struct {
		path string
		snap Snapshot
	}
	results := make(chan result, len(order))
	sem := make(chan struct{}, t.concurrent)
	var wg sync.WaitGroup

	for _, path := range order {
		grp := groups[path]
		primary := grp[0]
		if snap, skip := t.gateCheck(path, force); skip {
			results <- result{path: path, snap: snap}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(path string, tg target) {
			defer wg.Done()
			defer func() { <-sem }()
			started := time.Now()
			snap := t.fetch(ctx, tg.provider, tg.dir)
			if t.claimRenew(path, snap) {
				snap = t.renewAndRefetch(ctx, tg, snap)
			}
			t.logFetch(tg, snap, force, time.Since(started))
			results <- result{path: path, snap: snap}
		}(path, primary)
	}

	go func() { wg.Wait(); close(results) }()

	fetched := map[string]Snapshot{}
	gates := map[string]time.Time{}
	for r := range results {
		fetched[r.path] = r.snap
		if gate := t.recordBackoff(r.path, r.snap); !gate.IsZero() {
			gates[r.path] = gate
		}
	}

	// Rebuild the cache from the current target set (prunes removed profiles),
	// fanning each path's snapshot out to every profile sharing it.
	t.mu.Lock()
	next := make(map[string]Snapshot, len(targets))
	for _, tg := range targets {
		snap, ok := fetched[tg.path]
		if !ok {
			// Path was gate-skipped without a prior snapshot; synthesize.
			snap = Snapshot{Provider: tg.provider, Status: StatusError, FetchedAt: time.Now()}
		}
		snap.Profile = tg.key
		snap.Default = tg.isDefault
		if gate, ok := gates[tg.path]; ok && snap.NextRetryAt.IsZero() {
			snap.NextRetryAt = gate
		}
		snap = carryWindows(t.cache[tg.key], snap)
		next[tg.key] = snap
		t.auditThreshold(tg.key, snap)
	}
	t.cache = next
	t.mu.Unlock()

	return t.Snapshots()
}

// gateCheck decides whether a path should be skipped this round. When skipped it
// returns the last snapshot annotated with the current gate status.
func (t *Tracker) gateCheck(path string, force bool) (Snapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()

	last, hasLast := t.lastForPathLocked(path)

	// 429 rate gate is never bypassed, even by force.
	if gate := t.rateGate[path]; !gate.IsZero() && now.Before(gate) {
		t.log.Debug("usage fetch skipped", "event", "usage", "stage", "skip",
			"reason", "rate_limit_gate", "next_retry_at", gate)
		if hasLast {
			last.Status = StatusRateLimited
			last.NextRetryAt = gate
			return last, true
		}
		return Snapshot{Status: StatusRateLimited, NextRetryAt: gate, FetchedAt: now}, true
	}
	if force {
		return Snapshot{}, false
	}
	if bo := t.backoff[path]; !bo.IsZero() && now.Before(bo) {
		t.log.Debug("usage fetch skipped", "event", "usage", "stage", "skip",
			"reason", "backoff", "next_retry_at", bo)
		if hasLast {
			// Publish the live deadline, not the one this snapshot was stamped
			// with: clients render it, and the poll loop wakes on it.
			last.NextRetryAt = bo
			return last, true
		}
	}
	if hasLast && now.Sub(last.FetchedAt) < t.minGap && last.Source != SourcePassive {
		t.log.Debug("usage fetch skipped", "event", "usage", "stage", "skip", "reason", "fresh_cache")
		return last, true
	}
	return Snapshot{}, false
}

// lastForPathLocked returns the most recent cached snapshot for any profile
// resolving to path. Caller holds t.mu.
func (t *Tracker) lastForPathLocked(path string) (Snapshot, bool) {
	var best Snapshot
	var found bool
	for _, tg := range t.targets() {
		if tg.path != path {
			continue
		}
		if s, ok := t.cache[tg.key]; ok {
			if !found || s.FetchedAt.After(best.FetchedAt) {
				best, found = s, true
			}
		}
	}
	return best, found
}

// recordBackoff updates the per-path gates for a fetch result and returns the
// deadline the caller should publish as NextRetryAt, or the zero time when the
// path is not gated.
func (t *Tracker) recordBackoff(path string, snap Snapshot) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch snap.Status {
	case StatusOK, StatusNoCredentials, StatusUnsupported:
		delete(t.rateGate, path)
		delete(t.backoff, path)
		delete(t.failure, path)
		return time.Time{}
	case StatusStaleCredentials:
		// A renewal has already run this round (or is on cooldown) and the token
		// is still unusable. Re-check sooner than a full interval — the check is
		// local and cheap — but back off so an unrenewable token stops being
		// hammered.
		delete(t.rateGate, path)
		n := t.failure[path] + 1
		t.failure[path] = n
		wait := staleRetry << (n - 1)
		if wait > t.interval || wait <= 0 {
			wait = t.interval
		}
		gate := time.Now().Add(wait)
		t.backoff[path] = gate
		return gate
	case StatusRateLimited:
		gate := snap.NextRetryAt
		if gate.IsZero() {
			gate = time.Now().Add(rateGateFallback)
		}
		t.rateGate[path] = gate
		delete(t.backoff, path)
		delete(t.failure, path)
		return gate
	default: // StatusError, StatusUnauthorized
		n := t.failure[path] + 1
		t.failure[path] = n
		wait := backoffBase << (n - 1)
		if wait > backoffCap || wait <= 0 {
			wait = backoffCap
		}
		gate := time.Now().Add(wait)
		t.backoff[path] = gate
		return gate
	}
}

// claimRenew reports whether this round should ask the provider CLI to renew
// path's token, and claims the cooldown when it does so concurrent rounds cannot
// both spawn a CLI for the same credentials.
func (t *Tracker) claimRenew(path string, snap Snapshot) bool {
	if t.renew == nil {
		return false
	}
	// Only a token that exists but no longer works can be renewed. Absent or
	// plan-less credentials need the user, not a refresh.
	if snap.Status != StatusStaleCredentials && snap.Status != StatusUnauthorized {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if until, ok := t.renewAt[path]; ok && now.Before(until) {
		return false
	}
	t.renewAt[path] = now.Add(renewCooldown)
	return true
}

// renewAndRefetch asks the provider CLI to refresh its own token and re-fetches
// once. The CLI owns the token exchange and its own credential store; Podiom
// only re-reads the result, whatever it turns out to be — a renewal that is
// definitively rejected clears the credentials and surfaces as no_credentials,
// which is the honest state and the one the UI already offers a sign-in for.
func (t *Tracker) renewAndRefetch(ctx context.Context, tg target, prev Snapshot) Snapshot {
	started := time.Now()
	err := t.renew(ctx, tg.provider, tg.dir)
	next := t.fetch(ctx, tg.provider, tg.dir)
	fields := []any{"event", "usage", "stage", "renew", "profile", tg.key,
		"provider", string(tg.provider), "before", string(prev.Status),
		"after", string(next.Status), podiomlog.DurationMS("duration_ms", time.Since(started))}
	if err != nil {
		fields = append(fields, "error", podiomlog.Redact(err.Error()))
	}
	if next.Status == StatusOK {
		t.log.Info("usage credentials renewed", fields...)
	} else {
		t.log.Warn("usage credential renewal did not help", fields...)
	}
	return next
}

// carryWindows keeps the last known windows visible while a round fails, so the
// UI dims real numbers instead of dropping to a bare status line. prev is the
// snapshot being replaced.
func carryWindows(prev, next Snapshot) Snapshot {
	if next.Status == StatusOK && len(next.Windows) > 0 {
		next.Stale = false
		next.WindowsFetchedAt = next.FetchedAt
		return next
	}
	if len(next.Windows) > 0 {
		// A gate-skipped round republishes its own earlier windows; they are real
		// but no longer fresh.
		next.Stale = true
		if next.WindowsFetchedAt.IsZero() {
			next.WindowsFetchedAt = next.FetchedAt
		}
		return next
	}
	if !carryableStatus(next.Status) || len(prev.Windows) == 0 {
		return next
	}
	at := prev.WindowsFetchedAt
	if at.IsZero() {
		at = prev.FetchedAt
	}
	if time.Since(at) > staleCarryMax {
		return next
	}
	next.Windows = prev.Windows
	next.Credits = prev.Credits
	next.Stale = true
	next.WindowsFetchedAt = at
	if next.Plan == "" {
		next.Plan = prev.Plan
	}
	return next
}

// carryableStatus reports whether a failure is transient enough that the
// previous windows are still worth showing. Absent credentials or an account
// without plan windows are not: there is nothing to come back to.
func carryableStatus(s Status) bool {
	switch s {
	case StatusStaleCredentials, StatusUnauthorized, StatusRateLimited, StatusError:
		return true
	default:
		return false
	}
}

func (t *Tracker) fetch(ctx context.Context, provider config.Provider, dir string) Snapshot {
	if p, ok := usageProviders[provider]; ok {
		return p.fetch(ctx, t.hc, dir)
	}
	return Snapshot{Provider: provider, Status: StatusUnsupported, Error: "unknown provider", FetchedAt: time.Now()}
}

func (t *Tracker) logFetch(tg target, snap Snapshot, force bool, dur time.Duration) {
	base := []any{"event", "usage", "profile", tg.key, "provider", string(tg.provider)}
	switch snap.Status {
	case StatusOK:
		t.log.Info("usage fetch completed", append(base,
			"stage", "fetch", "status", string(snap.Status), "source", snap.Source,
			"plan", snap.Plan, "windows", len(snap.Windows),
			"max_used_percent", snap.MaxUsedPercent(), "forced", force,
			podiomlog.DurationMS("duration_ms", dur))...)
	case StatusNoCredentials, StatusStaleCredentials, StatusUnsupported:
		t.log.Warn("usage credentials unavailable", append(base,
			"stage", "fetch", "status", string(snap.Status), "reason", podiomlog.Redact(snap.Error))...)
	case StatusRateLimited:
		t.log.Warn("usage endpoint rate limited", append(base,
			"stage", "fetch", "status", string(snap.Status), "next_retry_at", snap.NextRetryAt,
			podiomlog.DurationMS("duration_ms", dur))...)
	default:
		t.log.Warn("usage fetch failed", append(base,
			"stage", "fetch", "status", string(snap.Status), "error", podiomlog.Redact(snap.Error),
			podiomlog.DurationMS("duration_ms", dur))...)
	}
}

// auditThreshold emits an edge-triggered warn when a window first crosses 80% or
// 95% for the current reset period, giving the daemon log a throttling audit
// trail. Buckets reset when utilization drops back below 80%.
func (t *Tracker) auditThreshold(key string, snap Snapshot) {
	for _, w := range snap.Windows {
		id := key + "|" + w.Key
		bucket := 0
		switch {
		case w.UsedPercent >= 95:
			bucket = 95
		case w.UsedPercent >= 80:
			bucket = 80
		}
		prev := t.threshold[id]
		if bucket > prev {
			t.log.Warn("usage window high", "event", "usage", "stage", "threshold",
				"profile", key, "window", w.Key, "used_percent", w.UsedPercent, "resets_at", w.ResetsAt)
		}
		if bucket == 0 {
			delete(t.threshold, id)
		} else {
			t.threshold[id] = bucket
		}
	}
}

// IngestPassive merges provider rate data captured mid-turn from the app-server
// (Phase 6) into the cache, but only when it is newer than the last OAuth fetch.
func (t *Tracker) IngestPassive(profile string, provider config.Provider, rs adapter.RateStatus) {
	windows := passiveWindows(rs)
	if len(windows) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.cache[profile]
	// Only overwrite when we have nothing fresh from OAuth for this window set.
	if ok && prev.Source == SourceOAuth && time.Since(prev.FetchedAt) < t.minGap {
		return
	}
	snap := prev
	snap.Profile = profile
	snap.Provider = provider
	snap.Status = StatusOK
	snap.Source = SourcePassive
	snap.Windows = windows
	snap.FetchedAt = time.Now()
	snap.Stale = false
	snap.WindowsFetchedAt = snap.FetchedAt
	snap.NextRetryAt = time.Time{}
	t.cache[profile] = snap
	t.auditThreshold(profile, snap)
	t.log.Debug("usage passive update", "event", "usage", "stage", "ingest_passive",
		"profile", profile, "windows", len(windows), "max_used_percent", snap.MaxUsedPercent())
}

// passiveWindows converts an adapter.RateStatus into usage windows. It prefers
// structured windows (Phase 6) and falls back to the single max percent.
func passiveWindows(rs adapter.RateStatus) []Window {
	if len(rs.Windows) > 0 {
		out := make([]Window, 0, len(rs.Windows))
		for _, w := range rs.Windows {
			label := passiveLabel(w.Key)
			win := Window{Key: w.Key, Label: label, UsedPercent: w.UsedPercent, WindowSeconds: w.WindowSeconds}
			if !w.ResetsAt.IsZero() {
				win.ResetsAt = w.ResetsAt
			}
			out = append(out, win)
		}
		return out
	}
	if rs.UsedPercent > 0 {
		return []Window{{Key: WindowPrimary, Label: "5-hour", UsedPercent: rs.UsedPercent}}
	}
	return nil
}

func passiveLabel(key string) string {
	switch key {
	case WindowPrimary:
		return "5-hour"
	case WindowSecondary:
		return "Weekly"
	default:
		return key
	}
}
