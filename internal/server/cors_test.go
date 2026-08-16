package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/gateway"
)

// teapot stands in for the rest of the handler chain: any status other than
// 418 means the request did not reach it.
func teapot() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
}

func TestCORSPreflightFromNativeOrigins(t *testing.T) {
	h := withCORS(teapot())
	for origin := range nativeOrigins {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/api/agents", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", "GET")
			req.Header.Set("Access-Control-Request-Headers", "x-podiom-token")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			// The preflight must be answered here, not passed down the chain:
			// reaching withAuth would 401 it for lacking the very token the
			// preflight exists to ask permission for.
			if rr.Code != http.StatusNoContent {
				t.Fatalf("preflight status = %d, want 204", rr.Code)
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("allow-origin = %q, want %q", got, origin)
			}
			allowed := rr.Header().Get("Access-Control-Allow-Headers")
			if !strings.Contains(strings.ToLower(allowed), "x-podiom-token") {
				t.Errorf("allow-headers = %q, want it to include X-Podiom-Token", allowed)
			}
			if !strings.Contains(rr.Header().Get("Access-Control-Allow-Methods"), http.MethodPost) {
				t.Errorf("allow-methods = %q, want it to include POST", rr.Header().Get("Access-Control-Allow-Methods"))
			}
			if !strings.Contains(rr.Header().Get("Vary"), "Origin") {
				t.Errorf("Vary = %q, want it to include Origin", rr.Header().Get("Vary"))
			}
		})
	}
}

func TestCORSActualRequestIsTagged(t *testing.T) {
	h := withCORS(teapot())
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.Header.Set("Origin", "capacitor://localhost")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want the request to reach the next handler", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "capacitor://localhost" {
		t.Errorf("allow-origin = %q, want the echoed origin", got)
	}
	// Credentialed mode is deliberately off: the gateway token is an explicit
	// header, so the app never needs it and enabling it would add CSRF surface.
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-credentials = %q, want it unset", got)
	}
}

func TestCORSIgnoresUnknownAndAbsentOrigins(t *testing.T) {
	h := withCORS(teapot())
	cases := []struct {
		name   string
		origin string
	}{
		{"hostile page", "https://evil.example"},
		{"look-alike scheme", "capacitor://elsewhere"},
		{"localhost with a port is a different origin", "http://localhost:5173"},
		{"same-origin browser request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			// Traffic still flows — CORS is not an authorization layer — but it
			// is not tagged, so a browser will refuse to expose the response.
			if rr.Code != http.StatusTeapot {
				t.Fatalf("status = %d, want the request to reach the next handler", rr.Code)
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("allow-origin = %q, want no CORS headers", got)
			}
		})
	}
}

// Through the real middleware chain: a preflight is answered before auth, but
// the request that follows is not exempt. CORS decides who may read a response,
// never who is authenticated.
func TestCORSInChainDoesNotBypassTokenAuth(t *testing.T) {
	s, keeper := newAuthedServer(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/api/agents", nil)
	preflight.Header.Set("Origin", "capacitor://localhost")
	preflight.Header.Set("Access-Control-Request-Method", "GET")
	if rr := serve(s, preflight); rr.Code != http.StatusNoContent {
		t.Fatalf("preflight through chain = %d, want 204 (not 401)", rr.Code)
	}

	tokenless := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	tokenless.Header.Set("Origin", "capacitor://localhost")
	if rr := serve(s, tokenless); rr.Code != http.StatusUnauthorized {
		t.Fatalf("tokenless request from an allowed origin = %d, want 401", rr.Code)
	}

	authed := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	authed.Header.Set("Origin", "capacitor://localhost")
	authed.Header.Set(gateway.Header, keeper.Current())
	rr := serve(s, authed)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("authed request from an allowed origin = 401, want it served")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "capacitor://localhost" {
		t.Errorf("allow-origin on the real response = %q, want the echoed origin", got)
	}
}
