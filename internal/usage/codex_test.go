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

const testCodexToken = "codex-access-SECRET-do-not-log"

func writeCodexAuth(t *testing.T, tokenAccount bool) string {
	t.Helper()
	dir := t.TempDir()
	var payload map[string]any
	if tokenAccount {
		payload = map[string]any{
			"tokens": map[string]any{
				"access_token":  testCodexToken,
				"refresh_token": "codex-refresh-secret",
				"account_id":    "acct-123",
			},
			"last_refresh": time.Now().Format(time.RFC3339),
		}
	} else {
		payload = map[string]any{"OPENAI_API_KEY": "sk-openai-secret"}
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// codexServerFetch points codexBaseURLDefault-equivalent by writing a config.toml
// override into the home dir so FetchCodex hits the test server.
func withCodexBase(t *testing.T, dir, base string) {
	t.Helper()
	toml := "chatgpt_base_url = \"" + base + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFetchCodexOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testCodexToken {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-123" {
			t.Errorf("account id = %q", got)
		}
		if r.URL.Path != codexUsagePath {
			t.Errorf("path = %q", r.URL.Path)
		}
		http.ServeFile(w, r, "testdata/codex_usage.json")
	}))
	defer srv.Close()

	dir := writeCodexAuth(t, true)
	withCodexBase(t, dir, srv.URL)

	snap := FetchCodex(context.Background(), srv.Client(), dir)
	if snap.Status != StatusOK {
		t.Fatalf("status = %q err=%q", snap.Status, snap.Error)
	}
	if snap.Plan != "plus" {
		t.Errorf("plan = %q", snap.Plan)
	}
	if len(snap.Windows) != 3 {
		t.Fatalf("windows = %d: %+v", len(snap.Windows), snap.Windows)
	}
	if snap.Windows[0].Key != WindowPrimary || snap.Windows[0].UsedPercent != 30 {
		t.Errorf("primary = %+v", snap.Windows[0])
	}
	if snap.Windows[1].Key != WindowSecondary || snap.Windows[1].WindowSeconds != 604800 {
		t.Errorf("secondary = %+v", snap.Windows[1])
	}
	if snap.Windows[2].Key != "gpt-5-codex" {
		t.Errorf("additional = %+v", snap.Windows[2])
	}
	if snap.Credits == nil || !snap.Credits.Enabled || snap.Credits.Balance != 4.25 {
		t.Errorf("credits = %+v", snap.Credits)
	}
	assertNoTokenCodex(t, snap)
}

func TestFetchCodexBaseURLOverrideHonored(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		http.ServeFile(w, r, "testdata/codex_usage.json")
	}))
	defer srv.Close()
	dir := writeCodexAuth(t, true)
	withCodexBase(t, dir, srv.URL)
	_ = FetchCodex(context.Background(), srv.Client(), dir)
	if !hit {
		t.Fatal("base url override not honored")
	}
}

func TestFetchCodexMissing(t *testing.T) {
	snap := FetchCodex(context.Background(), http.DefaultClient, t.TempDir())
	if snap.Status != StatusNoCredentials {
		t.Fatalf("status = %q", snap.Status)
	}
}

func TestFetchCodexAPIKeyOnly(t *testing.T) {
	dir := writeCodexAuth(t, false)
	snap := FetchCodex(context.Background(), http.DefaultClient, dir)
	if snap.Status != StatusUnsupported {
		t.Fatalf("status = %q", snap.Status)
	}
}

func TestFetchCodexRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	dir := writeCodexAuth(t, true)
	withCodexBase(t, dir, srv.URL)
	snap := FetchCodex(context.Background(), srv.Client(), dir)
	if snap.Status != StatusRateLimited {
		t.Fatalf("status = %q", snap.Status)
	}
}

func assertNoTokenCodex(t *testing.T, snap Snapshot) {
	t.Helper()
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), testCodexToken) || strings.Contains(string(raw), "codex-refresh-secret") {
		t.Errorf("marshaled snapshot contains token: %s", raw)
	}
}
