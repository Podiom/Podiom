package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
)

// setClaudeExpiry rewrites an existing credentials file's expiry, standing in for
// the provider CLI refreshing (or failing to refresh) its own token.
func setClaudeExpiry(t *testing.T, dir string, expiresAt int64) {
	t.Helper()
	path := filepath.Join(dir, ".credentials.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload["claudeAiOauth"].(map[string]any)["expiresAt"] = expiresAt
	out, _ := json.Marshal(payload)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestTrackerRenewsExpiredToken is the headline behaviour: an expired token is
// renewed and re-fetched inside one Refresh, so the stale state never reaches a
// client.
func TestTrackerRenewsExpiredToken(t *testing.T) {
	isolateHome(t)
	var hits int32
	srv := newClaudeServer(t, &hits)
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(-time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}

	var renews int32
	tr := New(Options{
		Profiles:   func() []config.Profile { return profiles },
		HTTPClient: srv.Client(),
		Renew: func(context.Context, config.Provider, string) error {
			atomic.AddInt32(&renews, 1)
			setClaudeExpiry(t, dir, time.Now().Add(time.Hour).UnixMilli())
			return nil
		},
	})

	tr.Refresh(context.Background(), true)
	if got := atomic.LoadInt32(&renews); got != 1 {
		t.Fatalf("renew calls = %d, want 1", got)
	}
	snap := snapshotFor(t, tr, "work")
	if snap.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok", snap.Status, snap.Error)
	}
	if snap.Stale {
		t.Error("a renewed snapshot must not be marked stale")
	}
	if !snap.NextRetryAt.IsZero() {
		t.Errorf("next_retry_at = %v, want zero for a healthy snapshot", snap.NextRetryAt)
	}
}

// TestTrackerRenewCooldown keeps a token that cannot be renewed from spawning a
// provider CLI on every poll.
func TestTrackerRenewCooldown(t *testing.T) {
	isolateHome(t)
	dir := writeClaudeCreds(t, time.Now().Add(-time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}

	var renews int32
	tr := New(Options{
		Profiles: func() []config.Profile { return profiles },
		Renew: func(context.Context, config.Provider, string) error {
			atomic.AddInt32(&renews, 1)
			return nil // the CLI could not refresh; creds stay expired
		},
	})

	tr.Refresh(context.Background(), true)
	tr.Refresh(context.Background(), true)
	tr.Refresh(context.Background(), true)
	if got := atomic.LoadInt32(&renews); got != 1 {
		t.Fatalf("renew calls = %d, want 1 (cooldown)", got)
	}

	snap := snapshotFor(t, tr, "work")
	if snap.Status != StatusStaleCredentials {
		t.Fatalf("status = %q, want stale_credentials", snap.Status)
	}
	// The client must be able to tell when the tracker will look again.
	if snap.NextRetryAt.IsZero() {
		t.Error("next_retry_at not published for a stale snapshot")
	}
}

// TestTrackerDoesNotRenewWithoutCredentials guards the CLI spawn: an absent or
// plan-less account needs the user, not a refresh.
func TestTrackerDoesNotRenewWithoutCredentials(t *testing.T) {
	isolateHome(t)
	profiles := []config.Profile{{Name: "empty", Provider: config.ProviderClaude, ConfigDir: t.TempDir()}}
	var renews int32
	tr := New(Options{
		Profiles: func() []config.Profile { return profiles },
		Renew: func(context.Context, config.Provider, string) error {
			atomic.AddInt32(&renews, 1)
			return nil
		},
	})
	tr.Refresh(context.Background(), true)
	if got := atomic.LoadInt32(&renews); got != 0 {
		t.Fatalf("renew calls = %d, want 0 for missing credentials", got)
	}
	if snap := snapshotFor(t, tr, "empty"); snap.Status != StatusNoCredentials {
		t.Fatalf("status = %q, want no_credentials", snap.Status)
	}
}

// TestTrackerCarriesWindowsWhenRenewalFails keeps real numbers on screen instead
// of dropping the row to a status line.
func TestTrackerCarriesWindowsWhenRenewalFails(t *testing.T) {
	isolateHome(t)
	var hits int32
	srv := newClaudeServer(t, &hits)
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}
	tr := New(Options{Profiles: func() []config.Profile { return profiles }, HTTPClient: srv.Client()})

	tr.Refresh(context.Background(), true)
	fresh := snapshotFor(t, tr, "work")
	if fresh.Status != StatusOK || len(fresh.Windows) == 0 {
		t.Fatalf("first round = %q with %d windows, want ok with windows", fresh.Status, len(fresh.Windows))
	}
	if fresh.WindowsFetchedAt.IsZero() {
		t.Fatal("windows_fetched_at not stamped on a fresh snapshot")
	}

	// The token expires and nothing can renew it (Renew is nil).
	setClaudeExpiry(t, dir, time.Now().Add(-time.Hour).UnixMilli())
	tr.Refresh(context.Background(), true)

	stale := snapshotFor(t, tr, "work")
	if stale.Status != StatusStaleCredentials {
		t.Fatalf("status = %q, want stale_credentials", stale.Status)
	}
	if !stale.Stale {
		t.Error("carried windows must be flagged stale")
	}
	if len(stale.Windows) != len(fresh.Windows) {
		t.Fatalf("windows = %d, want the %d carried over", len(stale.Windows), len(fresh.Windows))
	}
	if !stale.WindowsFetchedAt.Equal(fresh.WindowsFetchedAt) {
		t.Errorf("windows_fetched_at = %v, want the original %v", stale.WindowsFetchedAt, fresh.WindowsFetchedAt)
	}
	if stale.Plan != fresh.Plan {
		t.Errorf("plan = %q, want carried %q", stale.Plan, fresh.Plan)
	}
}

func TestCarryWindowsRules(t *testing.T) {
	now := time.Now()
	prev := Snapshot{
		Status:           StatusOK,
		Plan:             "max",
		Windows:          []Window{{Key: WindowFiveHour, UsedPercent: 42}},
		Credits:          &Credits{Enabled: true},
		FetchedAt:        now.Add(-time.Minute),
		WindowsFetchedAt: now.Add(-time.Minute),
	}
	aged := prev
	aged.WindowsFetchedAt = now.Add(-2 * staleCarryMax)

	cases := []struct {
		name      string
		prev      Snapshot
		next      Snapshot
		wantCarry bool
	}{
		{"stale credentials carry", prev, Snapshot{Status: StatusStaleCredentials, FetchedAt: now}, true},
		{"unauthorized carries", prev, Snapshot{Status: StatusUnauthorized, FetchedAt: now}, true},
		{"error carries", prev, Snapshot{Status: StatusError, FetchedAt: now}, true},
		{"no_credentials does not carry", prev, Snapshot{Status: StatusNoCredentials, FetchedAt: now}, false},
		{"unsupported does not carry", prev, Snapshot{Status: StatusUnsupported, FetchedAt: now}, false},
		{"aged windows are dropped", aged, Snapshot{Status: StatusStaleCredentials, FetchedAt: now}, false},
		{"nothing to carry", Snapshot{}, Snapshot{Status: StatusStaleCredentials, FetchedAt: now}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := carryWindows(tc.prev, tc.next)
			if carried := len(got.Windows) > 0; carried != tc.wantCarry {
				t.Fatalf("carried = %v, want %v", carried, tc.wantCarry)
			}
			if got.Stale != tc.wantCarry {
				t.Errorf("stale = %v, want %v", got.Stale, tc.wantCarry)
			}
		})
	}

	t.Run("fresh ok snapshot is never stale", func(t *testing.T) {
		fresh := Snapshot{Status: StatusOK, Windows: prev.Windows, FetchedAt: now}
		got := carryWindows(prev, fresh)
		if got.Stale {
			t.Error("fresh snapshot flagged stale")
		}
		if !got.WindowsFetchedAt.Equal(now) {
			t.Errorf("windows_fetched_at = %v, want %v", got.WindowsFetchedAt, now)
		}
	})
}

// TestStaleBackoffProgression: a token that will not renew must stop being
// re-checked every minute forever.
func TestStaleBackoffProgression(t *testing.T) {
	tr := New(Options{Profiles: func() []config.Profile { return nil }})
	path := "/x"

	first := tr.recordBackoff(path, Snapshot{Status: StatusStaleCredentials})
	second := tr.recordBackoff(path, Snapshot{Status: StatusStaleCredentials})
	if !second.After(first) {
		t.Fatalf("stale retry not backing off: %v -> %v", first, second)
	}
	if d := time.Until(first); d > staleRetry+time.Second {
		t.Errorf("first stale retry in %s, want about %s", d, staleRetry)
	}
	for range 20 {
		tr.recordBackoff(path, Snapshot{Status: StatusStaleCredentials})
	}
	if d := time.Until(tr.backoff[path]); d > tr.interval+time.Second {
		t.Errorf("stale retry grew past the poll interval: %s", d)
	}
	if gate := tr.recordBackoff(path, Snapshot{Status: StatusOK}); !gate.IsZero() {
		t.Errorf("gate = %v, want zero after recovery", gate)
	}
	if !tr.backoff[path].IsZero() {
		t.Error("stale backoff not cleared on recovery")
	}
}

func TestNextWakeShortensToSoonestRetry(t *testing.T) {
	tr := New(Options{Profiles: func() []config.Profile { return nil }, Interval: 5 * time.Minute})

	if got := tr.nextWake(nil); got != 5*time.Minute {
		t.Fatalf("nextWake(nil) = %s, want the poll interval", got)
	}
	snaps := []Snapshot{
		{NextRetryAt: time.Now().Add(4 * time.Minute)},
		{NextRetryAt: time.Now().Add(90 * time.Second)},
		{}, // no deadline
	}
	if got := tr.nextWake(snaps); got > 91*time.Second || got < 89*time.Second {
		t.Fatalf("nextWake = %s, want about 90s", got)
	}
	// A deadline already past is ignored rather than shortening every wake to the
	// floor — that spun the poll loop at minWake while any path sat in backoff.
	past := []Snapshot{{NextRetryAt: time.Now().Add(-time.Hour)}}
	if got := tr.nextWake(past); got != 5*time.Minute {
		t.Fatalf("nextWake(past) = %s, want the full interval", got)
	}
	// A deadline just barely ahead still gets the floor.
	soon := []Snapshot{{NextRetryAt: time.Now().Add(time.Millisecond)}}
	if got := tr.nextWake(soon); got != minWake {
		t.Fatalf("nextWake(soon) = %s, want the %s floor", got, minWake)
	}
}

// TestGateSkipPublishesLiveDeadline: a snapshot republished by a backoff skip
// must carry the current deadline, not the stale one it was stamped with. The
// poll loop wakes on this value, so a past deadline used to spin it.
func TestGateSkipPublishesLiveDeadline(t *testing.T) {
	isolateHome(t)
	dir := writeClaudeCreds(t, time.Now().Add(-time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}
	tr := New(Options{Profiles: func() []config.Profile { return profiles }})

	tr.Refresh(context.Background(), true) // -> stale, backoff armed
	path := credentialPath(config.ProviderClaude, dir)
	gate := tr.backoff[path]
	if gate.IsZero() {
		t.Fatal("no backoff armed for a stale path")
	}
	// Force the cached snapshot to carry an obsolete deadline, then let a
	// non-forced round skip the path.
	tr.mu.Lock()
	snap := tr.cache["work"]
	snap.NextRetryAt = time.Now().Add(-time.Hour)
	tr.cache["work"] = snap
	tr.mu.Unlock()

	tr.Refresh(context.Background(), false)
	got := snapshotFor(t, tr, "work")
	if !got.NextRetryAt.Equal(gate) {
		t.Fatalf("next_retry_at = %v, want the live gate %v", got.NextRetryAt, gate)
	}
	if d := tr.nextWake(tr.Snapshots()); d <= minWake {
		t.Errorf("nextWake = %s, want the loop to wait for the real gate", d)
	}
}

// TestTrackerKickRefreshesWithoutWaiting proves the loop no longer has to wait
// out its interval when something changes credentials on disk.
func TestTrackerKickRefreshesWithoutWaiting(t *testing.T) {
	isolateHome(t)
	fetched := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case fetched <- struct{}{}:
		default:
		}
		http.ServeFile(w, r, "testdata/claude_usage.json")
	}))
	defer srv.Close()
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}
	tr := New(Options{
		Profiles:   func() []config.Profile { return profiles },
		HTTPClient: srv.Client(),
		Interval:   time.Hour, // only a kick can produce a second fetch
	})
	tr.Start()
	defer tr.Stop()

	waitFetch := func(what string) {
		t.Helper()
		select {
		case <-fetched:
		case <-time.After(5 * time.Second):
			t.Fatalf("no fetch for %s", what)
		}
	}
	waitFetch("the initial refresh")
	// KickNow bypasses the minimum gap, which the initial refresh just armed.
	tr.KickNow()
	waitFetch("the kick")
}

func snapshotFor(t *testing.T, tr *Tracker, profile string) Snapshot {
	t.Helper()
	for _, s := range tr.Snapshots() {
		if s.Profile == profile {
			return s
		}
	}
	t.Fatalf("no snapshot for profile %q", profile)
	return Snapshot{}
}
