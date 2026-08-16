package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
)

func newAuthedServer(t *testing.T) (*Server, *gateway.Keeper) {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Paths: paths, Tokens: keeper}), keeper
}

// serve runs a request through the full middleware chain (source guard + auth),
// which is what production traffic sees.
func serve(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	return rr
}

func serveLAN(s *Server, req *http.Request) *httptest.ResponseRecorder {
	req.RemoteAddr = "192.168.1.20:43210"
	rr := httptest.NewRecorder()
	s.lanSrv.Handler.ServeHTTP(rr, req)
	return rr
}

func TestAuthMiddlewareMatrix(t *testing.T) {
	s, keeper := newAuthedServer(t)
	token := keeper.Current()

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		reject bool
	}{
		{"api without token", http.MethodGet, "/api/agents", "", true},
		{"api with token", http.MethodGet, "/api/agents", token, false},
		{"api with wrong token", http.MethodGet, "/api/agents", "wrong", true},
		{"healthz exempt", http.MethodGet, "/healthz", "", false},
		{"spa root exempt", http.MethodGet, "/", "", false},
		{"spa asset path exempt", http.MethodGet, "/assets/whatever.js", "", false},
		{"auth check without token", http.MethodGet, "/api/auth/check", "", true},
		{"auth check with token", http.MethodGet, "/api/auth/check", token, false},
		// A schedule's webhook is the one write endpoint the gateway token does
		// not guard — an outside sender cannot hold that token. The handler
		// requires the schedule's own secret instead. Everything else about the
		// schedule surface, including the manual /run trigger, stays protected.
		{"schedule webhook exempt from the gateway token", http.MethodPost, "/api/schedules/nightly/webhook", "", false},
		{"schedule webhook only for POST", http.MethodGet, "/api/schedules/nightly/webhook", "", true},
		{"schedule manual run still protected", http.MethodPost, "/api/schedules/nightly/run", "", true},
		{"schedule read still protected", http.MethodGet, "/api/schedules/nightly", "", true},
		{"webhook exemption does not extend to deeper paths", http.MethodPost, "/api/schedules/a/b/webhook", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set(gateway.Header, tc.token)
			}
			rr := serve(s, req)
			if tc.reject && rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rr.Code)
			}
			if !tc.reject && rr.Code == http.StatusUnauthorized {
				t.Fatalf("status = 401, want authorized (path %s)", tc.path)
			}
		})
	}
}

func TestAuthDisabledWithoutKeeper(t *testing.T) {
	s := New(Options{Paths: config.NewPaths(t.TempDir())})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/check", nil)
	if rr := serve(s, req); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 with auth disabled", rr.Code)
	}
}

func TestHALANListenerIsAPIOnlyAndStrictlyAuthenticated(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Paths: paths, Tokens: keeper, HAMode: true, LANPort: 8100})
	if s.lanSrv == nil {
		t.Fatal("LAN listener was not configured")
	}

	cases := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"health is public", http.MethodGet, "/healthz", http.StatusOK},
		{"health rejects non-GET", http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{"SPA is absent", http.MethodGet, "/", http.StatusNotFound},
		{"terminal is absent", http.MethodGet, "/terminal/shell", http.StatusNotFound},
		{"normal API requires token", http.MethodGet, "/api/auth/check", http.StatusUnauthorized},
		{"HA bootstrap requires token", http.MethodGet, "/api/onboarding/token", http.StatusUnauthorized},
		{"schedule webhook requires token", http.MethodPost, "/api/schedules/nightly/webhook", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if rr := serveLAN(s, req); rr.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, tc.status, rr.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/check", nil)
	req.Header.Set(gateway.Header, keeper.Current())
	if rr := serveLAN(s, req); rr.Code != http.StatusNoContent {
		t.Fatalf("authenticated check: status = %d, want 204", rr.Code)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/auth/check", nil)
	preflight.Header.Set("Origin", "capacitor://localhost")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "x-podiom-token")
	if rr := serveLAN(s, preflight); rr.Code != http.StatusNoContent {
		t.Fatalf("native preflight: status = %d, want 204", rr.Code)
	}
}

func TestHALANListenerFailsClosedWithoutTokenKeeper(t *testing.T) {
	s := New(Options{Paths: config.NewPaths(t.TempDir()), HAMode: true, LANPort: 8100})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/check", nil)
	if rr := serveLAN(s, req); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestTokenRotateEndpoint(t *testing.T) {
	s, keeper := newAuthedServer(t)
	old := keeper.Current()

	// Rotation itself requires the current token.
	req := httptest.NewRequest(http.MethodPost, "/api/token/rotate", nil)
	if rr := serve(s, req); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated rotate: status = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/token/rotate", nil)
	req.Header.Set(gateway.Header, old)
	rr := serve(s, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate status = %d (body=%s)", rr.Code, rr.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" || out.Token == old {
		t.Fatalf("rotate returned %q, want a fresh token", out.Token)
	}

	// Old token is dead, new one works (HA12).
	req = httptest.NewRequest(http.MethodGet, "/api/auth/check", nil)
	req.Header.Set(gateway.Header, old)
	if rr := serve(s, req); rr.Code != http.StatusUnauthorized {
		t.Fatalf("old token after rotate: status = %d, want 401", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/auth/check", nil)
	req.Header.Set(gateway.Header, out.Token)
	if rr := serve(s, req); rr.Code != http.StatusNoContent {
		t.Fatalf("new token after rotate: status = %d, want 204", rr.Code)
	}
}

func TestUpdateEndpointsRefusedInHAMode(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Paths: paths, Tokens: keeper, HAMode: true})
	req := httptest.NewRequest(http.MethodGet, "/api/update", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set(gateway.Header, keeper.Current())
	if rr := serve(s, req); rr.Code != http.StatusForbidden {
		t.Fatalf("update in HA mode: status = %d, want 403", rr.Code)
	}
}
