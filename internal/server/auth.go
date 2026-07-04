package server

import (
	"net/http"
	"strings"

	"nhooyr.io/websocket"
)

// withAuth enforces the gateway token on the API surface (HA7). Everything
// under /api/ requires the token; everything else — static SPA assets, the
// token-entry view they contain, /healthz, and the /terminal/ onboarding
// sub-paths (whose auth is HA Ingress itself, HA24) — is exempt, keeping the
// unauthenticated surface minimal (HA10). A nil Keeper (tests constructing a
// bare server) disables enforcement.
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.tokens == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.haOnboardingBootstrapRoute(r) && !s.tokens.Authorize(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"gateway token required (podiom token show)"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) haOnboardingBootstrapRoute(r *http.Request) bool {
	if !s.haMode {
		return false
	}
	switch r.URL.Path {
	case "/api/onboarding":
		return r.Method == http.MethodGet
	case "/api/onboarding/complete":
		return r.Method == http.MethodPost
	case "/api/onboarding/token":
		return r.Method == http.MethodGet
	default:
		return false
	}
}

// handleAuthCheck answers 204 for any authenticated request. The middleware
// already rejected unauthenticated callers, so the body is empty on purpose:
// the endpoint exists so the web UI can validate an entered token and
// disambiguate "daemon down" from "token rejected" after a WebSocket close.
func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// wsCloseTokenRotated is the application close code sent to live WebSocket
// connections when the token rotates, so open tabs drop to the token screen
// immediately rather than retrying forever (HA12).
const wsCloseTokenRotated websocket.StatusCode = 4401

// handleTokenRotate rotates the gateway token (HA12): the previous value is
// invalid from this moment, live WebSocket clients are force-closed, and the
// new value is returned to the (already-authenticated) caller — this is what
// `podiom token rotate` uses. There is deliberately no endpoint that *reads*
// the token: the web UI must never be able to fetch its own credential (HA10).
func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.tokens == nil {
		http.Error(w, "gateway token disabled", http.StatusServiceUnavailable)
		return
	}
	token, err := s.tokens.Rotate()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	// Log the event, never the value (HA21).
	s.log.Info("gateway token rotated", "event", "config")
	s.closeAllWS(wsCloseTokenRotated, "gateway token rotated")
	writeJSON(w, map[string]string{"token": token}, nil)
}
