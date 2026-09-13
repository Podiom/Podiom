package main

import (
	"testing"

	podiommcp "github.com/Podiom/Podiom/internal/mcp"
)

func TestParseEnvFlags(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want podiommcp.EnvVars
	}{
		{
			name: "NAME=VALUE stores the value",
			in:   []string{"TOKEN=secret"},
			want: podiommcp.EnvVars{{Name: "TOKEN", Value: "secret"}},
		},
		{
			name: "bare NAME leaves value empty",
			in:   []string{"TOKEN"},
			want: podiommcp.EnvVars{{Name: "TOKEN", Value: ""}},
		},
		{
			name: "name is trimmed, value is not",
			in:   []string{"  TOKEN = secret "},
			want: podiommcp.EnvVars{{Name: "TOKEN", Value: " secret "}},
		},
		{
			name: "splits on first equals only",
			in:   []string{"K=a=b"},
			want: podiommcp.EnvVars{{Name: "K", Value: "a=b"}},
		},
		{
			name: "NAME= is an explicit empty value",
			in:   []string{"TOKEN="},
			want: podiommcp.EnvVars{{Name: "TOKEN", Value: ""}},
		},
		{
			name: "nil in gives nil out",
			in:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEnvFlags(tt.in)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("parseEnvFlags(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseEnvFlags(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i].Name != tt.want[i].Name {
					t.Errorf("parseEnvFlags(%q)[%d].Name = %q, want %q", tt.in, i, got[i].Name, tt.want[i].Name)
				}
				if got[i].Value != tt.want[i].Value {
					t.Errorf("parseEnvFlags(%q)[%d].Value = %q, want %q", tt.in, i, got[i].Value, tt.want[i].Value)
				}
			}
		})
	}
}
