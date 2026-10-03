package server

import (
	"reflect"
	"testing"
)

func TestAddString(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  []string
	}{
		{"trimmed duplicate", " gmail ", []string{"gmail"}},
		{"whitespace only", " \t\n ", []string{"gmail"}},
		{"trimmed new value", " calendar ", []string{"gmail", "calendar"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := addString([]string{"gmail"}, tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("addString = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRemoveString(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		value  string
		want   []string
	}{
		{"does not trim", []string{"gmail"}, " gmail ", []string{"gmail"}},
		{"all entries removed", []string{"gmail", "gmail"}, "gmail", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := removeString(tc.values, tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("removeString = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestMCPTestErrorClass(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{"command not found precedes timeout", "no such file: timeout", "command_not_found"},
		{"https URL is not HTTP error", "https://example.com failed", "test_error"},
		{"HTTP with trailing space", "http 500", "http_error"},
		{"timeout", "timeout", "timeout"},
		{"RPC error", "rpc error", "rpc_error"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpTestErrorClass(tc.msg); got != tc.want {
				t.Fatalf("mcpTestErrorClass(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}
