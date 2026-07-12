package main

import "testing"

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
