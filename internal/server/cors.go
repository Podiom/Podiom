package server

import "net/http"

// The Podiom UI is normally same-origin: podiomd serves the SPA, so the browser
// never needs CORS and none is offered. The native apps break that assumption —
// they load the same SPA from the app bundle and reach the daemon across the
// network, which makes every REST call cross-origin.
//
// The gateway token rides X-Podiom-Token, a non-simple header, so the WebView
// sends a preflight OPTIONS first. Without a response carrying
// Access-Control-Allow-*, the WebView blocks the real request and the whole API
// surface is unreachable from the apps.
//
// nativeOrigins is the fixed, closed set of origins a Capacitor WebView
// presents (capacitor.config.ts pins the schemes that produce them; the two are
// a matched pair). Allowlisting them does not widen the browser-facing attack
// surface: none is reachable as a page origin in an ordinary browser, and every
// /api/ request still has to carry a valid gateway token — CORS decides who may
// read a response, never who is authenticated.
var nativeOrigins = map[string]bool{
	"capacitor://localhost": true, // iOS  (iosScheme: "capacitor")
	"https://localhost":     true, // Android (androidScheme: "https")
	"http://localhost":      true, // Android with a plain-http scheme override
}

// withCORS answers preflights and tags responses for the native app origins. It
// wraps *outside* withAuth: a preflight carries no credentials by design, so
// letting it reach the token check would 401 it and the real request would
// never be sent.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !nativeOrigins[origin] {
			// Same-origin browser traffic, or an origin we do not serve. Either
			// way, emit no CORS headers and let the request through unchanged.
			next.ServeHTTP(w, r)
			return
		}

		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		// The response varies by Origin, so any cache in between must key on it.
		h.Add("Vary", "Origin")
		// Deliberately no Access-Control-Allow-Credentials: the token is an
		// explicit header, never an ambient cookie, so the app does not need
		// credentialed mode and enabling it would only add CSRF surface.

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "X-Podiom-Token, Authorization, Content-Type")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
