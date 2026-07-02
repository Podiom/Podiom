package usage

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mar-schmidt/Podium/internal/adapter"
	"github.com/mar-schmidt/Podium/internal/config"
	podiumlog "github.com/mar-schmidt/Podium/internal/logging"
)

const (
	defaultInterval   = 5 * time.Minute
	defaultMinGap     = 30 * time.Second
	defaultConcurrent = 4
	backoffBase       = time.Minute
	backoffCap        = 15 * time.Minute
	rateGateFallback  = 5 * time.Minute
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
}

// Tracker owns the in-memory per-profile usage cache and its polling loop.
type Tracker struct {
	profiles   func() []config.Profile
	hc         *http.Client
	log        *slog.Logger
	interval   time.Duration
	minGap     time.Duration
	concurrent int

	mu    sync.Mutex
	cache map[string]Snapshot // keyed by snapshot key (profile)
	// rateGate/backoff/failStreak/threshold are keyed by resolved credential path.
	rateGate  map[string]time.Time
	backoff   map[string]time.Time
	failure   map[string]int
	threshold map[string]int // key "<profile>|<window>" -> highest crossed bucket

	stop chan struct{}
	done chan struct{}
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
		cache:      map[string]Snapshot{},
		rateGate:   map[string]time.Time{},
		backoff:    map[string]time.Time{},
		failure:    map[string]int{},
		threshold:  map[string]int{},
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

	t.Refresh(ctx, false)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			t.Refresh(ctx, false)
		}
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

// targets enumerates the two implicit defaults plus every named profile.
func (t *Tracker) targets() []target {
	out := []target{
		{key: string(config.ProviderClaude), provider: config.ProviderClaude, isDefault: true},
		{key: string(config.ProviderCodex), provider: config.ProviderCodex, isDefault: true},
	}
	if t.profiles != nil {
		for _, p := range t.profiles() {
			dir := p.ConfigDir
			if p.Provider == config.ProviderCodex {
				dir = p.HomeDir
			}
			out = append(out, target{key: p.Name, provider: p.Provider, dir: dir})
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
	switch provider {
	case config.ProviderClaude:
		return claudeCredentialPath(dir)
	case config.ProviderCodex:
		return filepath.Join(codexHomeDir(dir), "auth.json")
	default:
		return dir
	}
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
			t.logFetch(tg, snap, force, time.Since(started))
			results <- result{path: path, snap: snap}
		}(path, primary)
	}

	go func() { wg.Wait(); close(results) }()

	fetched := map[string]Snapshot{}
	for r := range results {
		fetched[r.path] = r.snap
		t.recordBackoff(r.path, r.snap)
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

func (t *Tracker) recordBackoff(path string, snap Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch snap.Status {
	case StatusOK, StatusNoCredentials, StatusStaleCredentials, StatusUnsupported:
		delete(t.rateGate, path)
		delete(t.backoff, path)
		delete(t.failure, path)
	case StatusRateLimited:
		gate := snap.NextRetryAt
		if gate.IsZero() {
			gate = time.Now().Add(rateGateFallback)
		}
		t.rateGate[path] = gate
		delete(t.backoff, path)
		delete(t.failure, path)
	default: // StatusError, StatusUnauthorized
		n := t.failure[path] + 1
		t.failure[path] = n
		wait := backoffBase << (n - 1)
		if wait > backoffCap || wait <= 0 {
			wait = backoffCap
		}
		t.backoff[path] = time.Now().Add(wait)
	}
}

func (t *Tracker) fetch(ctx context.Context, provider config.Provider, dir string) Snapshot {
	switch provider {
	case config.ProviderClaude:
		return FetchClaude(ctx, t.hc, dir)
	case config.ProviderCodex:
		return FetchCodex(ctx, t.hc, dir)
	default:
		return Snapshot{Provider: provider, Status: StatusUnsupported, Error: "unknown provider", FetchedAt: time.Now()}
	}
}

func (t *Tracker) logFetch(tg target, snap Snapshot, force bool, dur time.Duration) {
	base := []any{"event", "usage", "profile", tg.key, "provider", string(tg.provider)}
	switch snap.Status {
	case StatusOK:
		t.log.Info("usage fetch completed", append(base,
			"stage", "fetch", "status", string(snap.Status), "source", snap.Source,
			"plan", snap.Plan, "windows", len(snap.Windows),
			"max_used_percent", snap.MaxUsedPercent(), "forced", force,
			podiumlog.DurationMS("duration_ms", dur))...)
	case StatusNoCredentials, StatusStaleCredentials, StatusUnsupported:
		t.log.Warn("usage credentials unavailable", append(base,
			"stage", "fetch", "status", string(snap.Status), "reason", podiumlog.Redact(snap.Error))...)
	case StatusRateLimited:
		t.log.Warn("usage endpoint rate limited", append(base,
			"stage", "fetch", "status", string(snap.Status), "next_retry_at", snap.NextRetryAt,
			podiumlog.DurationMS("duration_ms", dur))...)
	default:
		t.log.Warn("usage fetch failed", append(base,
			"stage", "fetch", "status", string(snap.Status), "error", podiumlog.Redact(snap.Error),
			podiumlog.DurationMS("duration_ms", dur))...)
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
