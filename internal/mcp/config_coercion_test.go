package mcp

import (
	"reflect"
	"testing"
)

func TestEnvVarsFromLegacyCoercion(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want EnvVars
	}{
		{name: "non-string entries are dropped", in: []any{1, true}, want: nil},
		{name: "bare string remains untrimmed", in: "  FOO  ", want: EnvVars{{Name: "  FOO  "}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := envVarsFromLegacy(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("envVarsFromLegacy(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStringSliceCoercion(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    []string
		wantNil bool
	}{
		{name: "typed slice is returned", in: []string{"a"}, want: []string{"a"}},
		{name: "empty untyped slice is non-nil", in: []any{}, want: []string{}},
		{name: "nil input", in: nil, wantNil: true},
		{name: "unsupported input", in: 42, wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSlice(tt.in)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("stringSlice(%#v) = %#v, want nil", tt.in, got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("stringSlice(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}

	in := []string{"a"}
	out := stringSlice(in)
	out[0] = "MUTATED"
	if in[0] != "MUTATED" {
		t.Fatalf("stringSlice([]string) copied input; input = %#v", in)
	}
}

func TestTomlValueCoercion(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "number is quoted", in: 3, want: `"3"`},
		{name: "nil is quoted", in: nil, want: `"\u003cnil\u003e"`},
		{name: "boolean remains boolean", in: true, want: "true"},
		{name: "string is quoted", in: "value", want: `"value"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tomlValue(tt.in); got != tt.want {
				t.Fatalf("tomlValue(%#v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTomlKeyCoercion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty key is quoted", in: "", want: `""`},
		{name: "safe key is unquoted", in: "server_name", want: "server_name"},
		{name: "unsafe key is quoted", in: "server.name", want: `"server.name"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tomlKey(tt.in); got != tt.want {
				t.Fatalf("tomlKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
