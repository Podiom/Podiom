package server

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Podiom/Podiom/web"
)

// ingressPathPattern accepts HA Ingress base paths (/api/hassio_ingress/<token>)
// and rejects anything that could break out of the injected attribute. The
// value comes from a proxy-controlled header, but only the Ingress proxy can
// reach the daemon in HA mode (HA6) — this is belt and braces.
var ingressPathPattern = regexp.MustCompile(`^/[A-Za-z0-9/_.-]*$`)

// spaHandler serves the embedded single-page app. Real asset requests are
// served directly; any other path falls back to index.html so client-side
// routing works on deep links / refreshes. index.html is served with two
// injections (HA14/HA10):
//
//   - a <base href> derived from the X-Ingress-Path header, so the relative
//     asset/API/WS URLs of the Vite build resolve under HA Ingress's rewritten
//     sub-path — including when the entry URL lacks a trailing slash;
//   - a <meta name="podiom-deployment"> hint ("ha" or "standalone") that tells
//     the token screen where the user retrieves the gateway token (HA's
//     Configuration page vs `podiom token show`) without any unauthenticated
//     API endpoint.
func (s *Server) spaHandler() http.Handler {
	dist, err := web.DistFS()
	if err != nil {
		// Should never happen: the placeholder index.html guarantees a non-empty
		// embed. Serve a clear error rather than panicking the daemon.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded web assets unavailable", http.StatusInternalServerError)
		})
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded index.html unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(dist))

	deployment := "standalone"
	if s.haMode {
		deployment = "ha"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean != "" && clean != "index.html" {
			if _, err := fs.Stat(dist, clean); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Root, index.html, or unknown path: serve the injected index.
		s.serveIndex(w, r, index, deployment)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request, index []byte, deployment string) {
	inject := `<meta name="podiom-deployment" content="` + deployment + `">`
	if base := r.Header.Get("X-Ingress-Path"); base != "" && ingressPathPattern.MatchString(base) {
		href := html.EscapeString(strings.TrimSuffix(base, "/") + "/")
		inject = `<base href="` + href + `">` + inject
	}
	body := bytes.Replace(index, []byte("<head>"), []byte("<head>"+inject), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}
