package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAccessToken = "sk-ant-oat-SECRETTOKENVALUE-do-not-log"

// writeClaudeCreds writes a .credentials.json into dir and returns dir.
func writeClaudeCreds(t *testing.T, expiresAt int64, scopes []string) string {
	t.Helper()
	dir := t.TempDir()
	payload := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      testAccessToken,
			"refreshToken":     "refresh-secret",
			"expiresAt":        expiresAt,
			"scopes":           scopes,
			"subscriptionType": "max",
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFetchClaudeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAccessToken {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != claudeOAuthBeta {
			t.Errorf("anthropic-beta = %q", got)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "claude-code/") {
			t.Errorf("user-agent = %q", r.Header.Get("User-Agent"))
		}
		http.ServeFile(w, r, "testdata/claude_usage.json")
	}))
	defer srv.Close()
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	snap := FetchClaude(context.Background(), srv.Client(), dir)

	if snap.Status != StatusOK {
		t.Fatalf("status = %q err=%q", snap.Status, snap.Error)
	}
	if snap.Plan != "max" {
		t.Errorf("plan = %q", snap.Plan)
	}
	if len(snap.Windows) != 4 {
		t.Fatalf("windows = %d", len(snap.Windows))
	}
	if snap.Windows[0].Key != WindowFiveHour || snap.Windows[0].UsedPercent != 42.5 {
		t.Errorf("five_hour window = %+v", snap.Windows[0])
	}
	if snap.Windows[0].WindowSeconds != fiveHourSeconds {
		t.Errorf("five_hour seconds = %d", snap.Windows[0].WindowSeconds)
	}
	if snap.Credits == nil || !snap.Credits.Enabled || snap.Credits.MonthlyLimit != 100 {
		t.Errorf("credits = %+v", snap.Credits)
	}
	if snap.Source != SourceOAuth {
		t.Errorf("source = %q", snap.Source)
	}
	assertNoToken(t, snap)
}

func TestFetchClaudeMissing(t *testing.T) {
	snap := FetchClaude(context.Background(), http.DefaultClient, t.TempDir())
	if snap.Status != StatusNoCredentials {
		t.Fatalf("status = %q", snap.Status)
	}
}

func TestClaudeKeychainService(t *testing.T) {
	// A custom CLAUDE_CONFIG_DIR maps to the base service name suffixed with the
	// first 8 hex chars of sha256(absolute dir), matching the Claude Code CLI.
	if got := claudeKeychainService("/Users/marcus/.claude-personal"); got != "Claude Code-credentials-9f8d6274" {
		t.Errorf("custom service = %q, want Claude Code-credentials-9f8d6274", got)
	}

	// The default account uses the bare service name — both for the implicit
	// default (empty dir) and an explicit path to ~/.claude.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := claudeKeychainService(""); got != claudeKeychainBase {
		t.Errorf("default service = %q, want %q", got, claudeKeychainBase)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := claudeKeychainService(filepath.Join(home, ".claude")); got != claudeKeychainBase {
		t.Errorf("explicit default service = %q, want %q", got, claudeKeychainBase)
	}
}

func TestFetchClaudeExpired(t *testing.T) {
	dir := writeClaudeCreds(t, time.Now().Add(-time.Hour).UnixMilli(), []string{"user:profile"})
	snap := FetchClaude(context.Background(), http.DefaultClient, dir)
	if snap.Status != StatusStaleCredentials {
		t.Fatalf("status = %q", snap.Status)
	}
}

func TestFetchClaudeMissingScope(t *testing.T) {
	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:inference"})
	snap := FetchClaude(context.Background(), http.DefaultClient, dir)
	if snap.Status != StatusUnsupported {
		t.Fatalf("status = %q", snap.Status)
	}
}

func TestFetchClaudeUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	snap := FetchClaude(context.Background(), srv.Client(), dir)
	if snap.Status != StatusUnauthorized {
		t.Fatalf("status = %q", snap.Status)
	}
}

func TestFetchClaudeRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	restore := claudeUsageURL
	claudeUsageURL = srv.URL
	defer func() { claudeUsageURL = restore }()

	dir := writeClaudeCreds(t, time.Now().Add(time.Hour).UnixMilli(), []string{"user:profile"})
	snap := FetchClaude(context.Background(), srv.Client(), dir)
	if snap.Status != StatusRateLimited {
		t.Fatalf("status = %q", snap.Status)
	}
	wait := time.Until(snap.NextRetryAt)
	if wait < 50*time.Second || wait > 70*time.Second {
		t.Errorf("next_retry_at ~= %s from now", wait)
	}
}

// assertNoToken guards the security invariant: no token substring leaks into
// the snapshot's error string or its marshaled JSON.
func assertNoToken(t *testing.T, snap Snapshot) {
	t.Helper()
	if strings.Contains(snap.Error, testAccessToken) {
		t.Errorf("error contains token: %q", snap.Error)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), testAccessToken) || strings.Contains(string(raw), "refresh-secret") {
		t.Errorf("marshaled snapshot contains token: %s", raw)
	}
}
