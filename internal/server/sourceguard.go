package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

// ingressProxyAddr is the fixed address HA's Ingress proxy connects from; per
// HA's Ingress security requirements the app must accept only this source (and
// its own loopback callers) in HA mode (HA6).
const ingressProxyAddr = "172.30.32.2/32"

// sourceGuard rejects requests whose RemoteAddr is outside the allowlist. It
// wraps the entire handler chain (static assets included). Loopback is always
// allowed: the daemon's spawned MCP helpers and the same-machine CLI connect
// over 127.0.0.1 regardless of deployment.
type sourceGuard struct {
	allowed []netip.Prefix
	next    http.Handler
}

// buildSourceGuard returns next unchanged when no restriction applies (no
// allow_from config and not HA mode) — today's open behavior. Otherwise the
// allowlist is loopback + allowFrom (+ the Ingress proxy in HA mode).
func buildSourceGuard(next http.Handler, allowFrom []string, haMode bool) (http.Handler, error) {
	if len(allowFrom) == 0 && !haMode {
		return next, nil
	}
	entries := []string{"127.0.0.0/8", "::1/128"}
	if haMode {
		entries = append(entries, ingressProxyAddr)
	}
	entries = append(entries, allowFrom...)
	allowed, err := parsePrefixes(entries)
	if err != nil {
		return nil, err
	}
	return &sourceGuard{allowed: allowed, next: next}, nil
}

func (g *sourceGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		http.Error(w, "forbidden source", http.StatusForbidden)
		return
	}
	addr = addr.Unmap()
	for _, p := range g.allowed {
		if p.Contains(addr) {
			g.next.ServeHTTP(w, r)
			return
		}
	}
	http.Error(w, "forbidden source", http.StatusForbidden)
}

// parsePrefixes accepts CIDRs and bare IPs (treated as single-address prefixes).
func parsePrefixes(entries []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		if p, err := netip.ParsePrefix(e); err == nil {
			prefixes = append(prefixes, p)
			continue
		}
		addr, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("allow_from entry %q is neither an IP nor a CIDR", e)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}
