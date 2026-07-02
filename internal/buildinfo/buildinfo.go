// Package buildinfo holds version/commit values stamped at build time via
// -ldflags. Both binaries import it so they report a consistent version.
package buildinfo

// These are overridden at build time, e.g.:
//
//	go build -ldflags "-X github.com/Podiom/Podiom/internal/buildinfo.Version=1.0.0 \
//	  -X github.com/Podiom/Podiom/internal/buildinfo.Commit=$(git rev-parse --short HEAD)"
var (
	Version = "dev"
	Commit  = "none"
)
