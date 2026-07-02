package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/web"
)

// spaHandler serves the embedded single-page app. Real asset requests are served
// directly; any other path falls back to index.html so client-side routing works
// on deep links / refreshes. API and WebSocket routes are registered separately
// on the mux and never reach here.
func spaHandler() http.Handler {
	dist, err := web.DistFS()
	if err != nil {
		// Should never happen: the placeholder index.html guarantees a non-empty
		// embed. Serve a clear error rather than panicking the daemon.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded web assets unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(dist, clean); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path: serve index.html for client-side routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
