package main

import (
	"strings"
	"testing"
)

func TestRelayEndpointWarning(t *testing.T) {
	tests := []struct {
		name            string
		endpoint        string
		wantContains    string
		wantNoWarning   bool
	}{
		{
			name:         "invalid URL",
			endpoint:     "http://%zz",
			wantContains: "not a valid URL",
		},
		{
			name:          "HTTPS relay",
			endpoint:      "https://relay.example.com",
			wantNoWarning: true,
		},
		{
			name:          "localhost HTTP",
			endpoint:      "http://localhost:8787",
			wantNoWarning: true,
		},
		{
			name:          "IPv4 loopback HTTP",
			endpoint:      "http://127.0.0.1:8787",
			wantNoWarning: true,
		},
		{
			name:          "IPv6 loopback HTTP",
			endpoint:      "http://[::1]:8787",
			wantNoWarning: true,
		},
		{
			name:         "non-loopback HTTP",
			endpoint:     "http://relay.example.com",
			wantContains: "not https",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relayEndpointWarning(tt.endpoint)
			if tt.wantNoWarning {
				if got != "" {
					t.Fatalf("relayEndpointWarning(%q) = %q, want no warning", tt.endpoint, got)
				}
				return
			}
			if !strings.Contains(got, tt.wantContains) {
				t.Fatalf("relayEndpointWarning(%q) = %q, want it to contain %q", tt.endpoint, got, tt.wantContains)
			}
		})
	}
}
