package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/providerlogin"
	"github.com/Podiom/Podiom/internal/store"
)

// fakeClaudeLogin narrates the manual-redirect URL, then accepts a pasted code
// from stdin — the shape of the real `claude auth login`. `auth status` reports
// logged in only once the login has written its marker into the profile dir.
const fakeClaudeLogin = `#!/usr/bin/env sh
case "$1 $2" in
  "--version ") echo "claude 1.2.3"; exit 0 ;;
  "auth status")
    if [ -n "$CLAUDE_CONFIG_DIR" ] && [ -f "$CLAUDE_CONFIG_DIR/logged-in" ]; then
      echo '{"loggedIn":true}'; exit 0
    fi
    echo '{"loggedIn":false}'; exit 1 ;;
  "auth login")
    echo "Opening browser to sign in…"
    echo "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?state=abc"
    printf 'Paste code here if prompted > '
    read -r line
    case "$line" in
      good*) [ -n "$CLAUDE_CONFIG_DIR" ] && touch "$CLAUDE_CONFIG_DIR/logged-in"
             echo "Login successful."; exit 0 ;;
      *) echo "Invalid code. Please make sure the full code was copied." >&2; exit 1 ;;
    esac ;;
esac
exit 1
`

// newLoginTestServer wires a Server with a fake claude CLI on PATH and one
// named profile pointing at its own directory.
func newLoginTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake()})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(fakeClaudeLogin), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_BIN", filepath.Join(binDir, "claude"))

	profileDir := filepath.Join(home, "profiles", "claude-work")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	coreSvc.SetProfiles([]config.Profile{
		{Name: "work", Provider: config.ProviderClaude, ConfigDir: profileDir},
	})
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	ts := httptest.NewServer(srv.httpSrv.Handler)
	t.Cleanup(func() {
		srv.logins.Shutdown()
		ts.Close()
	})
	return ts, profileDir
}

func postJSON(t *testing.T, url, body string) (*http.Response, string) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer res.Body.Close()
	buf := make([]byte, 8192)
	n, _ := res.Body.Read(buf)
	return res, string(buf[:n])
}

func getSession(t *testing.T, url string) providerlogin.Session {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get %s = %d", url, res.StatusCode)
	}
	var sess providerlogin.Session
	if err := json.NewDecoder(res.Body).Decode(&sess); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return sess
}

func waitForPhase(t *testing.T, url string, want providerlogin.Phase) providerlogin.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last providerlogin.Session
	for time.Now().Before(deadline) {
		last = getSession(t, url)
		if last.Phase == want {
			return last
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("phase = %q (message %q), want %q", last.Phase, last.Message, want)
	return last
}

func TestProviderLoginEndToEndForProfile(t *testing.T) {
	ts, profileDir := newLoginTestServer(t)

	res, body := postJSON(t, ts.URL+"/api/provider-login", `{"provider":"claude","profile":"work"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start = %d: %s", res.StatusCode, body)
	}
	var started providerlogin.Session
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}
	if !started.NeedsCode {
		t.Fatalf("NeedsCode = false, want true for Claude")
	}

	sessURL := ts.URL + "/api/provider-login/" + started.ID
	sess := waitForPhase(t, sessURL, providerlogin.PhaseAwaitingCode)
	if !strings.HasPrefix(sess.URL, "https://claude.com/cai/oauth/authorize") {
		t.Fatalf("URL = %q, want the authorize URL", sess.URL)
	}

	res, body = postJSON(t, sessURL+"/code", `{"code":"good#abc"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit = %d: %s", res.StatusCode, body)
	}
	waitForPhase(t, sessURL, providerlogin.PhaseSucceeded)

	// The login wrote into the profile's own directory, not the global one.
	if _, err := os.Stat(filepath.Join(profileDir, "logged-in")); err != nil {
		t.Fatalf("profile dir not authenticated: %v", err)
	}

	// And the status endpoint now reports that profile signed in.
	statuses := fetchProviderStatus(t, ts.URL+"/api/provider-status?refresh=1")
	var found bool
	for _, st := range statuses {
		if st.Provider != config.ProviderClaude {
			continue
		}
		if st.Profile == "work" {
			found = true
			if !st.LoggedIn || !st.SupportsLogin {
				t.Fatalf("work profile = %+v, want logged in and login-capable", st)
			}
		}
		if st.Profile == "" && st.LoggedIn {
			t.Fatalf("default profile reported logged in via another profile's dir: %+v", st)
		}
	}
	if !found {
		t.Fatalf("no status row for the work profile: %+v", statuses)
	}
}

func fetchProviderStatus(t *testing.T, url string) []providerAuthStatus {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var out []providerAuthStatus
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return out
}

func TestProviderLoginRejectsUnknownProfileAndProvider(t *testing.T) {
	ts, _ := newLoginTestServer(t)

	for _, tc := range []struct{ name, body string }{
		{"unknown provider", `{"provider":"nope","profile":""}`},
		{"unknown profile", `{"provider":"claude","profile":"ghost"}`},
		{"profile from another provider", `{"provider":"codex","profile":"work"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, body := postJSON(t, ts.URL+"/api/provider-login", tc.body)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", res.StatusCode, body)
			}
		})
	}
}

func TestProviderLoginCodeValidationAndCancel(t *testing.T) {
	ts, _ := newLoginTestServer(t)

	res, body := postJSON(t, ts.URL+"/api/provider-login", `{"provider":"claude","profile":"work"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start = %d: %s", res.StatusCode, body)
	}
	var started providerlogin.Session
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatal(err)
	}
	sessURL := ts.URL + "/api/provider-login/" + started.ID
	waitForPhase(t, sessURL, providerlogin.PhaseAwaitingCode)

	// A code carrying a newline would inject a second answer into the CLI.
	if res, body := postJSON(t, sessURL+"/code", `{"code":"good#abc\nsecond"}`); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("multiline code = %d (%s), want 400", res.StatusCode, body)
	}

	req, _ := http.NewRequest(http.MethodDelete, sessURL, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", res.StatusCode)
	}
	if sess := getSession(t, sessURL); sess.Phase != providerlogin.PhaseFailed {
		t.Fatalf("phase = %q, want failed after cancel", sess.Phase)
	}
}

func TestProviderLoginUnknownSessionIs404(t *testing.T) {
	ts, _ := newLoginTestServer(t)
	res, err := http.Get(ts.URL + "/api/provider-login/does-not-exist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
