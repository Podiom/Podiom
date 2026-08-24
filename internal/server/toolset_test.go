package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	podiomtools "github.com/Podiom/Podiom/internal/tools"
)

// installRedirect sends every download to the local test server regardless of
// the https URL in the request payload, so the https-only rule stays enforced
// while the bytes come from a test server.
type installRedirect struct{ base string }

func (t installRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.base, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// TestToolsetInstallListRemove walks the full REST surface with the binary
// installer: install, see it listed with its provenance and version evidence,
// then remove it and find both the file and the manifest row gone.
func TestToolsetInstallListRemove(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()

	payload := []byte("#!/bin/sh\necho toolbin 1.0\n")
	sum := sha256.Sum256(payload)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer up.Close()
	restore := podiomtools.SetHTTPClientForTest(&http.Client{Transport: installRedirect{base: up.URL}})
	defer restore()

	body := `{"tool":"toolbin","installer":"binary","url":"https://example.test/toolbin","sha256":"` +
		hex.EncodeToString(sum[:]) + `","installed_by":"atlas","session_id":"s1"}`
	rr := httptest.NewRecorder()
	srv.handleToolset(rr, httptest.NewRequest(http.MethodPost, "/api/toolset", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	var installed map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&installed); err != nil {
		t.Fatalf("decode install: %v", err)
	}
	if installed["status"] != "installed" || !strings.Contains(installed["version"], "toolbin 1.0") {
		t.Fatalf("install response = %+v", installed)
	}

	// It landed in the shared toolset, not under any agent.
	wantPath := filepath.Join(srv.paths.ToolsetDir, "bin", "toolbin")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("executable not at %s: %v", wantPath, err)
	}

	rr = httptest.NewRecorder()
	srv.handleToolset(rr, httptest.NewRequest(http.MethodGet, "/api/toolset", nil))
	var list []podiomtools.ToolStatus
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].Tool != "toolbin" || list[0].Broken {
		t.Fatalf("list = %+v", list)
	}
	if list[0].InstalledBy != "atlas" || list[0].SessionID != "s1" {
		t.Fatalf("provenance = %+v", list[0])
	}

	rr = httptest.NewRecorder()
	srv.handleToolset(rr, httptest.NewRequest(http.MethodDelete, "/api/toolset/toolbin", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("executable still present after remove: %v", err)
	}
	if got, _ := podiomtools.List(srv.paths.ToolsetDir); len(got) != 0 {
		t.Fatalf("manifest after remove = %+v", got)
	}
}

// TestToolsetRejectsBadInstalls checks that the payload rules are enforced at
// the API boundary, before anything runs — in particular that a reserved name
// cannot be installed, since the toolset sits ahead of the system PATH for
// every agent.
func TestToolsetRejectsBadInstalls(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"reserved name", `{"tool":"node","installer":"npm","package":"node"}`, "reserved name"},
		{"no installer", `{"tool":"rg"}`, "installer is required"},
		{"plain http", `{"tool":"rg","installer":"binary","url":"http://x/rg","sha256":"` + strings.Repeat("ab", 32) + `"}`, "https"},
		{"bad digest", `{"tool":"rg","installer":"binary","url":"https://x/rg","sha256":"nope"}`, "64 hex"},
		{"unknown installer", `{"tool":"rg","installer":"brew","package":"rg"}`, "unknown installer"},
		{"escaping archive path", `{"tool":"rg","installer":"archive","url":"https://x/rg.tgz","sha256":"` + strings.Repeat("ab", 32) + `","path":"../../etc/passwd"}`, "escapes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.handleToolset(rr, httptest.NewRequest(http.MethodPost, "/api/toolset", strings.NewReader(tc.body)))
			if !strings.Contains(strings.ToLower(rr.Body.String()), strings.ToLower(tc.want)) {
				t.Fatalf("body = %s, want mention of %q", rr.Body.String(), tc.want)
			}
			if got, _ := podiomtools.List(srv.paths.ToolsetDir); len(got) != 0 {
				t.Fatalf("rejected install must record nothing: %+v", got)
			}
		})
	}
}
