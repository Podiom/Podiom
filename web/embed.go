// Package web embeds the built Svelte SPA so podiomd ships as a single binary
// with no separate frontend process (D21 / Principle 7). The `vite build` output
// lands in web/dist/; `go:embed all:dist` bakes it into the binary at compile
// time. A placeholder dist/index.html is committed so the Go module always builds
// even before the frontend has been built — the real build overwrites it.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns the embedded built SPA rooted at the dist directory (so "/"
// maps to dist/index.html).
func DistFS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
