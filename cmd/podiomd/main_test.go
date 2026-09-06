package main

import (
	"context"
	"testing"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

func TestResolveDaemonAddrUsesEnvironmentBeforeConfig(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		wantBind   string
		wantPort   int
		wantSource string
		wantErr    bool
		defaultCfg bool
	}{
		{name: "config fallback", wantBind: "0.0.0.0", wantPort: 8787, wantSource: "config"},
		{name: "new default config", wantBind: "0.0.0.0", wantPort: 8787, wantSource: "default", defaultCfg: true},
		{name: "environment override", env: "127.0.0.1:8799", wantBind: "127.0.0.1", wantPort: 8799, wantSource: "env"},
		{name: "IPv6 environment override", env: "[::1]:8800", wantBind: "::1", wantPort: 8800, wantSource: "env"},
		{name: "missing port", env: "127.0.0.1", wantErr: true},
		{name: "invalid port", env: "127.0.0.1:nope", wantErr: true},
		{name: "out of range port", env: "127.0.0.1:70000", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bind, port, source, err := resolveDaemonAddr("0.0.0.0", 8787, tt.env, tt.defaultCfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveDaemonAddr() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if bind != tt.wantBind || port != tt.wantPort || source != tt.wantSource {
				t.Fatalf("resolveDaemonAddr() = (%q, %d, %q), want (%q, %d, %q)", bind, port, source, tt.wantBind, tt.wantPort, tt.wantSource)
			}
		})
	}
}

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
