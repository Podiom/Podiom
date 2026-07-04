// Command ingress-sim is a local stand-in for Home Assistant's Ingress proxy,
// making the HA acceptance checks exercisable without an HA install: it serves
// the app under a rewritten sub-path (/api/hassio_ingress/<token>/...), strips
// that prefix before forwarding to podiomd, and sets the X-Ingress-Path header
// exactly like the real proxy. WebSockets pass through.
//
// Usage:
//
//	go run ./scripts/ingress-sim [-listen 127.0.0.1:8123] [-upstream 127.0.0.1:8787] [-token faketoken]
//
// Then open http://127.0.0.1:8123/api/hassio_ingress/faketoken/ and verify the
// SPA renders, the WebSocket connects, and the token screen works (acceptance
// checks 5 and 6). Anything outside the ingress path 404s, mimicking HA.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8123", "address to serve the simulated HA on")
	upstream := flag.String("upstream", "127.0.0.1:8787", "podiomd address to forward to")
	token := flag.String("token", "faketoken", "fake ingress token used in the sub-path")
	flag.Parse()

	base := "/api/hassio_ingress/" + *token
	target := &url.URL{Scheme: "http", Host: *upstream}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			// Strip the ingress prefix and tell the app where it lives — the
			// two things the real Ingress proxy does that break naive SPAs.
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, base)
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
			pr.Out.Header.Set("X-Ingress-Path", base)
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) { proxy.ServeHTTP(w, r) })
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/", http.StatusTemporaryRedirect)
	})

	fmt.Printf("ingress-sim: http://%s%s/ -> http://%s/\n", *listen, base, *upstream)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
