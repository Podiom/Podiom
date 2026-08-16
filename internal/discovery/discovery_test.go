package discovery

import (
	"os"
	"strings"
	"testing"
)

func TestLoopbackBindSkipsAdvertising(t *testing.T) {
	cases := []struct {
		name     string
		bind     string
		loopback bool
	}{
		// Advertising these would list an instance on the LAN at an address no
		// other device can reach.
		{"default loopback", "127.0.0.1", true},
		{"loopback range", "127.0.0.53", true},
		{"ipv6 loopback", "::1", true},

		{"wildcard", "0.0.0.0", false},
		{"ipv6 wildcard", "::", false},
		{"lan address", "192.168.1.50", false},
		{"container address", "172.17.0.2", false},
		{"unset means every interface", "", false},
		{"hostname is not resolved here", "podiom.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := loopbackBind(tc.bind); got != tc.loopback {
				t.Errorf("loopbackBind(%q) = %v, want %v", tc.bind, got, tc.loopback)
			}
		})
	}
}

func TestInstanceNameNamesTheMachine(t *testing.T) {
	name := instanceName()
	if !strings.HasPrefix(name, "Podiom on ") {
		t.Errorf("instanceName() = %q, want it to name the host", name)
	}
	// The label is read by a human choosing between instances, so it carries
	// the machine name and none of the DNS suffix hosts append inconsistently
	// (".local" on macOS, ".localdomain" elsewhere, or a full FQDN).
	if strings.Contains(name, ".") {
		t.Errorf("instanceName() = %q, want the DNS suffix trimmed", name)
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		label, _, _ := strings.Cut(host, ".")
		if label != "" && !strings.Contains(name, label) {
			t.Errorf("instanceName() = %q, want it to contain the host label %q", name, label)
		}
	}
}

// A nil Responder is what Advertise returns whenever it declines or fails, and
// the daemon shuts down unconditionally.
func TestNilResponderShutdownIsSafe(t *testing.T) {
	var r *Responder
	r.Shutdown()
}

func TestAdvertiseDeclinesOnLoopback(t *testing.T) {
	if got := Advertise("127.0.0.1", 8787, "test", nil); got != nil {
		got.Shutdown()
		t.Fatal("Advertise on loopback returned a responder, want nil")
	}
}
