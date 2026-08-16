// Package discovery advertises this daemon on the local network over
// mDNS/DNS-SD, so the Podiom mobile apps can find an instance instead of asking
// the user to type an IP address (R8).
//
// It only ever announces; nothing here browses, and no inbound request is
// served as a result. What the announcement carries is deliberately dull — a
// hostname-derived label, the port, and the build version — because anything on
// the LAN can read it. Reaching the daemon still requires the gateway token.
package discovery

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/libp2p/zeroconf/v2"
)

// Service is the DNS-SD service type the apps browse for. It is also listed in
// the iOS app's NSBonjourServices; the two must agree or the browse silently
// returns nothing.
const Service = "_podiom._tcp"

// domain is the standard mDNS domain. Anything else would not be multicast DNS.
const domain = "local."

// Responder is a running advertisement. A nil Responder is safe to Shutdown, so
// callers do not have to branch on whether advertising was enabled.
type Responder struct {
	server *zeroconf.Server
	log    *slog.Logger
}

// Advertise publishes this daemon as a Podiom instance on every suitable
// interface.
//
// bind is the daemon's configured listen address. Advertising is skipped when
// it is loopback: the announcement would be broadcast to the LAN while naming
// an address no other device can reach, which is worse than not appearing at
// all — a phone would list the instance and then fail to connect to it.
//
// A failure to advertise is never fatal. Discovery is a convenience; the daemon
// serves its API regardless, and the apps always allow a typed address.
func Advertise(bind string, port int, version string, log *slog.Logger) *Responder {
	if log == nil {
		log = slog.Default()
	}
	if loopbackBind(bind) {
		log.Info("local discovery disabled", "event", "discovery", "reason", "daemon is bound to loopback", "bind", bind)
		return nil
	}

	instance := instanceName()
	text := []string{
		"version=" + version,
		// path is where the API sits under the advertised host, so a future
		// deployment behind a sub-path prefix can say so without the client
		// having to guess.
		"path=/",
	}

	server, err := zeroconf.Register(instance, Service, domain, port, text, nil)
	if err != nil {
		log.Warn("local discovery unavailable", "event", "discovery", "error", err)
		return nil
	}
	log.Info("advertising on the local network",
		"event", "discovery", "instance", instance, "service", Service, "port", port)
	return &Responder{server: server, log: log}
}

// Shutdown withdraws the advertisement, sending the goodbye packets that stop
// clients from offering an instance that is no longer listening.
func (r *Responder) Shutdown() {
	if r == nil || r.server == nil {
		return
	}
	r.server.Shutdown()
	r.log.Info("stopped advertising on the local network", "event", "discovery")
}

// instanceName is what a user picks from a list, so it answers "which machine
// is this?" — "Podiom on macbook" rather than a bare hostname or an address.
func instanceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "Podiom"
	}
	// Hosts report themselves inconsistently — "macbook", "macbook.local",
	// "Mac.localdomain", or a full FQDN. Only the first label names the
	// machine, and the rest is noise in a list the user has to read.
	if i := strings.Index(host, "."); i > 0 {
		host = host[:i]
	}
	if host == "" {
		return "Podiom"
	}
	return fmt.Sprintf("Podiom on %s", host)
}

// loopbackBind reports whether the daemon is reachable only from its own host.
// An empty or wildcard bind means every interface, which is reachable.
func loopbackBind(bind string) bool {
	if bind == "" {
		return false
	}
	ip := net.ParseIP(bind)
	if ip == nil {
		// A hostname rather than an address. Resolving it here would be
		// guesswork, so assume it is reachable and let the advertisement stand.
		return false
	}
	if ip.IsUnspecified() {
		return false
	}
	return ip.IsLoopback()
}
