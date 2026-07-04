// Package hamode detects whether podiomd is running as a Home Assistant app
// (add-on). Detection is deliberately passive — the daemon never calls the
// Supervisor API itself (the add-on's token-sync service owns that, HA8);
// podiomd only adapts behavior: the Ingress source-IP guard (HA6), the
// deployment hint injected into the SPA's index.html (HA10), and refusing
// self-update in favor of app updates (HA26).
package hamode

import "os"

// EnvSupervisorToken is injected by the Home Assistant Supervisor into add-on
// containers with API access; its presence is the HA-mode signal.
const EnvSupervisorToken = "SUPERVISOR_TOKEN"

// Detect reports whether the daemon is running inside a Home Assistant add-on.
func Detect() bool {
	return os.Getenv(EnvSupervisorToken) != ""
}
