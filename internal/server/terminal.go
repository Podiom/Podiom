package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

// The terminal onboarding sub-paths (HA15/HA22): /terminal/claude and
// /terminal/codex — optionally /terminal/<cli>/<profile> (HA23) — are proxied
// to the container's shared ttyd. Each entry lands the user directly in the
// right CLI's login flow: the proxy resolves cli/profile from the *path* and
// injects them as ttyd --url-arg query args on the forwarded requests, so the
// wrapper script receives them as argv while the browser URL stays a clean
// path segment (HA23's implementation note). The client's own query string is
// dropped — args are trusted only when minted here.
//
// These routes are exempt from gateway-token auth by design: whoever reaches
// them holds an HA-authenticated Ingress session, and the terminal is
// Podiom-root anyway (HA24). They still sit inside the source-IP guard.

var profileSegment = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

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
			cli, profile, rest, _ := splitTerminalPath(pr.In.URL.Path)
			pr.Out.URL.Path = "/" + rest
			pr.Out.URL.RawPath = ""
			// Drop the client query entirely and mint the ttyd url-args.
			q := url.Values{}
			args := []string{cli}
			if profile != "" {
				args = append(args, "--profile="+profile)
			}
			args = append(args, "--return-url="+returnURL(pr.In))
			q["arg"] = args
			pr.Out.URL.RawQuery = q.Encode()
		},
	}
	return &terminalProxy{proxy: rp}, nil
}

func (t *terminalProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cli, profile, rest, ok := splitTerminalPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if profile != "" && !profileSegment.MatchString(profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}
	// Canonicalize the entry URL to a trailing slash so the ttyd page's
	// relative sub-resources (its token and ws endpoints) resolve under this
	// sub-path. The Location header is written directly because it must stay
	// relative: http.Redirect would absolutize it against the daemon-local
	// path, losing the Ingress prefix the daemon never sees.
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		last := cli
		if profile != "" {
			last = profile
		}
		w.Header().Set("Location", last+"/")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}
	t.proxy.ServeHTTP(w, r)
}

// splitTerminalPath decomposes /terminal/<cli>[/<profile>][/<rest>...]. The
// profile is distinguished from ttyd sub-resources by reservation: "ws" and
// "token" are ttyd's own endpoints and never profile names.
func splitTerminalPath(p string) (cli, profile, rest string, ok bool) {
	trimmed := strings.TrimPrefix(p, "/terminal/")
	if trimmed == p {
		return "", "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 3)
	cli = parts[0]
	if cli != "claude" && cli != "codex" {
		return "", "", "", false
	}
	if len(parts) == 1 {
		return cli, "", "", true
	}
	second := parts[1]
	tail := ""
	if len(parts) == 3 {
		tail = parts[2]
	}
	if second == "" || isTTYDResource(second) {
		return cli, "", strings.TrimPrefix(trimmed[len(cli)+1:], "/"), true
	}
	return cli, second, tail, true
}

// isTTYDResource reports whether a path segment is one of ttyd's own
// endpoints/assets rather than a profile name.
func isTTYDResource(seg string) bool {
	return seg == "ws" || seg == "token" || strings.Contains(seg, ".")
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
