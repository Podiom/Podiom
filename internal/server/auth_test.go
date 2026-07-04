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

func TestAuthMiddlewareMatrix(t *testing.T) {
	s, keeper := newAuthedServer(t)
	token := keeper.Current()

	cases := []struct {
		name   string
		path   string
		token  string
		reject bool
	}{
		{"api without token", "/api/agents", "", true},
		{"api with token", "/api/agents", token, false},
		{"api with wrong token", "/api/agents", "wrong", true},
		{"healthz exempt", "/healthz", "", false},
		{"spa root exempt", "/", "", false},
		{"spa asset path exempt", "/assets/whatever.js", "", false},
		{"auth check without token", "/api/auth/check", "", true},
		{"auth check with token", "/api/auth/check", token, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
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
