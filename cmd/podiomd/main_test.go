package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

func TestInternalCallbackAddrNormalizesWildcardBinds(t *testing.T) {
	tests := []struct {
		name string
		bind string
		want string
	}{
		{name: "empty", bind: "", want: "127.0.0.1:8099"},
		{name: "ipv4 wildcard", bind: "0.0.0.0", want: "127.0.0.1:8099"},
		{name: "ipv6 wildcard", bind: "::", want: "[::1]:8099"},
		{name: "loopback", bind: "127.0.0.1", want: "127.0.0.1:8099"},
		{name: "concrete lan", bind: "192.168.1.20", want: "192.168.1.20:8099"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := internalCallbackAddr(tt.bind, 8099); got != tt.want {
				t.Fatalf("internalCallbackAddr(%q) = %q, want %q", tt.bind, got, tt.want)
			}
		})
	}
}

// TestParseHostPort covers the daemon's read of PODIOM_ADDR. The variable is
// unvalidated environment input, and a silently mis-parsed address would put the
// daemon on a port the CLI is not looking at — the exact split PODIOM_ADDR was
// reported for.
func TestParseHostPort(t *testing.T) {
	tests := []struct {
		name       string
		addr       string
		wantBind   string
		wantPort   int
		wantErrSub string
	}{
		{name: "loopback", addr: "127.0.0.1:8799", wantBind: "127.0.0.1", wantPort: 8799},
		{name: "hostname", addr: "podiom.local:8799", wantBind: "podiom.local", wantPort: 8799},
		{name: "ipv4 wildcard", addr: "0.0.0.0:8799", wantBind: "0.0.0.0", wantPort: 8799},
		{name: "ipv6 loopback", addr: "[::1]:8799", wantBind: "::1", wantPort: 8799},
		{name: "min port", addr: "127.0.0.1:1", wantBind: "127.0.0.1", wantPort: 1},
		{name: "max port", addr: "127.0.0.1:65535", wantBind: "127.0.0.1", wantPort: 65535},
		{name: "no host", addr: ":8799", wantErrSub: "no host to bind"},
		{name: "no port", addr: "127.0.0.1", wantErrSub: "missing port"},
		{name: "non-numeric port", addr: "127.0.0.1:http", wantErrSub: "not a number"},
		{name: "port above range", addr: "127.0.0.1:65536", wantErrSub: "port out of range"},
		{name: "zero port", addr: "127.0.0.1:0", wantErrSub: "port out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bind, port, err := parseHostPort(tt.addr)
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("parseHostPort(%q) = (%q, %d, nil), want error containing %q",
						tt.addr, bind, port, tt.wantErrSub)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("parseHostPort(%q) error = %q, want it to contain %q",
						tt.addr, err.Error(), tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHostPort(%q) returned unexpected error: %v", tt.addr, err)
			}
			if bind != tt.wantBind || port != tt.wantPort {
				t.Fatalf("parseHostPort(%q) = (%q, %d), want (%q, %d)",
					tt.addr, bind, port, tt.wantBind, tt.wantPort)
			}
		})
	}
}

// TestRelayInterfacesKeepAMissingRelayNil is a nil-interface invariant, not a style
// preference. A nil *notify.RelayChannel assigned straight into an interface field yields
// a non-nil interface holding a nil pointer, so the server's "no relay configured" guards
// would not fire and the first device registration would call a method on a nil receiver.
//
// The state is reachable: an unreadable installation id disables native push by design
// and leaves that pointer nil.
func TestRelayInterfacesKeepAMissingRelayNil(t *testing.T) {
	registrar, channel := relayInterfaces(nil)
	if registrar != nil {
		t.Error("DeviceRegistrar is not nil for a missing relay; the server's nil guard will not fire")
	}
	if channel != nil {
		t.Error("Channel is not nil for a missing relay; the test push will call a nil receiver")
	}
}

// TestRelayInterfacesPassThroughARealRelay is the other half: a configured relay must
// actually reach the server, or native push is silently off.
func TestRelayInterfacesPassThroughARealRelay(t *testing.T) {
	relay := notify.NewRelayChannel(noDevices{}, "https://push.example", t.TempDir()+"/relay.json", "install-1", nil)
	if relay == nil {
		t.Fatal("NewRelayChannel returned nil for a valid configuration")
	}
	registrar, channel := relayInterfaces(relay)
	if registrar == nil || channel == nil {
		t.Fatalf("a configured relay must reach the server; got registrar=%v channel=%v", registrar, channel)
	}
}

// noDevices is the smallest DeviceStore that satisfies NewRelayChannel's constructor
// check. The test never delivers anything, so it never reads a device.
type noDevices struct{}

func (noDevices) ListNotificationDevices(context.Context, bool) ([]store.NotificationDevice, error) {
	return nil, nil
}

func (noDevices) SetNotificationDeviceStatus(context.Context, string, string) error { return nil }
