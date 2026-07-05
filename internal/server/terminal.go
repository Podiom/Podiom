package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// The terminal sub-paths (HA15/HA22) /terminal/onboard and /terminal/shell are
// proxied to the container's shared ttyd. Each entry lands the user directly in
// the right flow: the proxy resolves the flow from the path and injects it as a
// ttyd --url-arg query arg on the forwarded requests, so the wrapper script
// receives it as argv. The client's own query string is dropped — args are
// trusted only when minted here.
//
// These routes are exempt from gateway-token auth by design: whoever reaches
// them holds an HA-authenticated Ingress session, and the terminal is
// Podiom-root anyway (HA24). They still sit inside the source-IP guard.

type terminalProxy struct {
	proxy *httputil.ReverseProxy
}

// newTerminalProxy builds the /terminal/ handler forwarding to upstream (the
// local ttyd, e.g. http://127.0.0.1:7681). WebSocket upgrades pass through
// httputil.ReverseProxy natively.
func newTerminalProxy(upstream string) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("terminal proxy upstream %q: %w", upstream, err)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			flow, rest, _ := splitTerminalPath(pr.In.URL.Path)
			pr.Out.URL.Path = "/" + rest
			pr.Out.URL.RawPath = ""
			// Drop the client query entirely and mint the ttyd url-args.
			q := url.Values{}
			q["arg"] = []string{flow, "--return-url=" + returnURL(pr.In)}
			pr.Out.URL.RawQuery = q.Encode()
		},
	}
	return &terminalProxy{proxy: rp}, nil
}

func (t *terminalProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flow, rest, ok := splitTerminalPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Canonicalize the entry URL to a trailing slash so the ttyd page's
	// relative sub-resources (its token and ws endpoints) resolve under this
	// sub-path. The Location header is written directly because it must stay
	// relative: http.Redirect would absolutize it against the daemon-local
	// path, losing the Ingress prefix the daemon never sees.
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		w.Header().Set("Location", flow+"/")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}
	t.proxy.ServeHTTP(w, r)
}

// splitTerminalPath decomposes /terminal/<flow>[/<rest>...] for supported
// terminal flows. ttyd sub-resources such as ws and token are returned as rest.
func splitTerminalPath(p string) (flow, rest string, ok bool) {
	trimmed := strings.TrimPrefix(p, "/terminal/")
	if trimmed == p {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 3)
	flow = parts[0]
	if flow != "onboard" && flow != "shell" {
		return "", "", false
	}
	if len(parts) == 1 {
		return flow, "", true
	}
	return flow, strings.TrimPrefix(trimmed[len(flow)+1:], "/"), true
}

// returnURL is the absolute link back to the Podiom SPA, printed by the
// terminal wrapper after login (HA22). It is derived from the incoming
// request's host and Ingress path — the daemon behind the proxy is the only
// place that knows the externally visible base.
func returnURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	base := r.Header.Get("X-Ingress-Path")
	return scheme + "://" + r.Host + base + "/"
}
