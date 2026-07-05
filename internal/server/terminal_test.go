package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// terminalUpstream records what the proxy forwards to a fake ttyd.
func terminalUpstream(t *testing.T) (*httptest.Server, *[]*url.URL) {
	t.Helper()
	var seen []*url.URL
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := *r.URL
		seen = append(seen, &u)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, &seen
}

func TestTerminalProxyInjectsArgsAndStripsClientQuery(t *testing.T) {
	ts, seen := terminalUpstream(t)
	h, err := newTerminalProxy(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Client-supplied args must be dropped — only proxy-minted args reach ttyd
	// (HA23: the path, not the query, selects the flow).
	req := httptest.NewRequest(http.MethodGet, "/terminal/onboard/?arg=evil", nil)
	req.Host = "ha.example"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/tok123")
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got := (*seen)[0]
	if got.Path != "/" {
		t.Fatalf("upstream path = %q, want /", got.Path)
	}
	args := got.Query()["arg"]
	want := []string{"onboard", "--return-url=https://ha.example/api/hassio_ingress/tok123/"}
	if len(args) != len(want) || args[0] != want[0] || args[1] != want[1] {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestTerminalProxyOnboardAndShellFlows(t *testing.T) {
	ts, seen := terminalUpstream(t)
	h, err := newTerminalProxy(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		arg  string
	}{
		{"/terminal/onboard/ws", "onboard"},
		{"/terminal/shell/token", "shell"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.path, rr.Code)
		}
		got := (*seen)[len(*seen)-1]
		args := got.Query()["arg"]
		if len(args) != 2 || args[0] != tc.arg {
			t.Fatalf("%s: args = %v", tc.path, args)
		}
	}
}

func TestTerminalProxyCanonicalizesEntryURL(t *testing.T) {
	ts, _ := terminalUpstream(t)
	h, err := newTerminalProxy(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/terminal/onboard", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", rr.Code)
	}
	// Relative redirect so the Ingress prefix (invisible to the daemon) is kept.
	if loc := rr.Header().Get("Location"); loc != "onboard/" {
		t.Fatalf("location = %q, want onboard/", loc)
	}

	req = httptest.NewRequest(http.MethodGet, "/terminal/shell", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if loc := rr.Header().Get("Location"); loc != "shell/" {
		t.Fatalf("shell location = %q, want shell/", loc)
	}
}

func TestTerminalProxyRejectsUnknownAndRemovedCLIFlows(t *testing.T) {
	ts, _ := terminalUpstream(t)
	h, err := newTerminalProxy(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/terminal/bash/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown cli: status = %d, want 404", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/terminal/claude/", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("removed claude flow: status = %d, want 404", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/terminal/codex/", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("removed codex flow: status = %d, want 404", rr.Code)
	}
}
