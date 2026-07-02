package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mar-schmidt/Podium/internal/adapter"
	"github.com/mar-schmidt/Podium/internal/config"
)

// isolateHome points the implicit-default credential lookups at empty temp dirs
// so tracker tests never touch the developer's real ~/.claude or ~/.codex (which
// would make a live network call). Named profiles still use their explicit dirs.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
}

// newClaudeServer serves the claude fixture and counts hits.
func newClaudeServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		http.ServeFile(w, r, "testdata/claude_usage.json")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTrackerTargetsIncludeDefaults(t *testing.T) {
	tr := New(Options{Profiles: func() []config.Profile { return nil }})
	tgs := tr.targets()
	if len(tgs) != 2 {
		t.Fatalf("targets = %d", len(tgs))
	}
	if !tgs[0].isDefault || !tgs[1].isDefault {
		t.Errorf("defaults not flagged: %+v", tgs)
	}
}

func TestTrackerRefreshAndPrune(t *testing.T) {
	isolateHome(t)
	var hits int32
	srv := newClaudeServer(t, &hits)
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	// A named claude profile whose config dir has valid creds.
	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}
	tr := New(Options{
		Profiles:   func() []config.Profile { return profiles },
		HTTPClient: srv.Client(),
	})

	snaps := tr.Refresh(context.Background(), true)
	// claude default + codex default + work
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %d", len(snaps))
	}
	var work Snapshot
	for _, s := range snaps {
		if s.Profile == "work" {
			work = s
		}
	}
	if work.Status != StatusOK {
		t.Fatalf("work status = %q err=%q", work.Status, work.Error)
	}

	// Remove the named profile; it should be pruned from the cache.
	profiles = nil
	snaps = tr.Refresh(context.Background(), true)
	for _, s := range snaps {
		if s.Profile == "work" {
			t.Fatalf("work not pruned")
		}
	}
}

func TestTrackerDedupeByPath(t *testing.T) {
	isolateHome(t)
	var hits int32
	srv := newClaudeServer(t, &hits)
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	// Two named profiles pointing at the same config dir -> one fetch.
	profiles := []config.Profile{
		{Name: "a", Provider: config.ProviderClaude, ConfigDir: dir},
		{Name: "b", Provider: config.ProviderClaude, ConfigDir: dir},
	}
	tr := New(Options{Profiles: func() []config.Profile { return profiles }, HTTPClient: srv.Client()})
	tr.Refresh(context.Background(), true)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 fetch for shared path, got %d", got)
	}
}

func TestTrackerRateLimitGate(t *testing.T) {
	isolateHome(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}
	tr := New(Options{Profiles: func() []config.Profile { return profiles }, HTTPClient: srv.Client()})

	tr.Refresh(context.Background(), true)
	first := atomic.LoadInt32(&hits)
	// A forced refresh inside the gate must NOT hit the server again.
	tr.Refresh(context.Background(), true)
	if got := atomic.LoadInt32(&hits); got != first {
		t.Fatalf("forced refresh bypassed rate gate: %d -> %d", first, got)
	}
	for _, s := range tr.Snapshots() {
		if s.Profile == "work" && s.Status != StatusRateLimited {
			t.Errorf("work status = %q, want rate_limited", s.Status)
		}
	}
}

func TestTrackerBackoffProgression(t *testing.T) {
	tr := New(Options{Profiles: func() []config.Profile { return nil }})
	path := "/x"
	tr.recordBackoff(path, Snapshot{Status: StatusError})
	first := time.Until(tr.backoff[path])
	tr.recordBackoff(path, Snapshot{Status: StatusError})
	second := time.Until(tr.backoff[path])
	if second <= first {
		t.Fatalf("backoff not increasing: %s -> %s", first, second)
	}
	tr.recordBackoff(path, Snapshot{Status: StatusOK})
	if !tr.backoff[path].IsZero() {
		t.Fatalf("backoff not reset on success")
	}
}

func TestTrackerIngestPassive(t *testing.T) {
	tr := New(Options{Profiles: func() []config.Profile { return nil }})
	rs := adapter.RateStatus{
		UsedPercent: 55,
		Windows: []adapter.RateWindow{
			{Key: WindowPrimary, UsedPercent: 55, ResetsAt: time.Now().Add(time.Hour), WindowSeconds: 18000},
			{Key: WindowSecondary, UsedPercent: 20},
		},
	}
	tr.IngestPassive("codex", config.ProviderCodex, rs)
	var found bool
	for _, s := range tr.Snapshots() {
		if s.Profile == "codex" {
			found = true
			if s.Source != SourcePassive {
				t.Errorf("source = %q", s.Source)
			}
			if len(s.Windows) != 2 {
				t.Errorf("windows = %d", len(s.Windows))
			}
		}
	}
	if !found {
		t.Fatal("passive snapshot not cached")
	}
}

func TestTrackerNoTokenInJSON(t *testing.T) {
	isolateHome(t)
	var hits int32
	srv := newClaudeServer(t, &hits)
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	profiles := []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: dir}}
	tr := New(Options{Profiles: func() []config.Profile { return profiles }, HTTPClient: srv.Client()})
	snaps := tr.Refresh(context.Background(), true)
	raw, _ := json.Marshal(snaps)
	if strings.Contains(string(raw), testAccessToken) || strings.Contains(string(raw), "refresh-secret") {
		t.Fatalf("token leaked into snapshot JSON")
	}
}
