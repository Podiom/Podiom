package main

import (
	"testing"

	podiommcp "github.com/Podiom/Podiom/internal/mcp"
)

func TestFormatFallback(t *testing.T) {
	tests := []struct {
		name  string
		chain []string
		want  string
	}{
		{name: "nil", chain: nil, want: "-"},
		{name: "empty", chain: []string{}, want: "-"},
		{name: "multiple", chain: []string{"primary", "backup"}, want: "primary,backup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatFallback(tt.chain); got != tt.want {
				t.Fatalf("formatFallback(%v) = %q, want %q", tt.chain, got, tt.want)
			}
		})
	}
}

func TestSourceList(t *testing.T) {
	tests := []struct {
		name    string
		sources []podiommcp.Source
		want    string
	}{
		{name: "nil", sources: nil, want: ""},
		{name: "empty", sources: []podiommcp.Source{}, want: ""},
		{name: "multiple", sources: []podiommcp.Source{podiommcp.SourcePodiom, podiommcp.SourceClaude}, want: "podiom,claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceList(tt.sources); got != tt.want {
				t.Fatalf("sourceList(%v) = %q, want %q", tt.sources, got, tt.want)
			}
		})
	}
}

func TestEnvList(t *testing.T) {
	tests := []struct {
		name string
		envs []podiommcp.EnvStatus
		want string
	}{
		{name: "nil", envs: nil, want: "-"},
		{name: "empty", envs: []podiommcp.EnvStatus{}, want: "-"},
		{name: "multiple", envs: []podiommcp.EnvStatus{{Name: "TOKEN", Set: true}, {Name: "OPTIONAL", Set: false}}, want: "TOKEN=set,OPTIONAL=unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := envList(tt.envs); got != tt.want {
				t.Fatalf("envList(%v) = %q, want %q", tt.envs, got, tt.want)
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
		found  bool
	}{
		{name: "nil", values: nil, want: "podiom", found: false},
		{name: "present", values: []string{"claude", "codex", "podiom"}, want: "codex", found: true},
		{name: "absent", values: []string{"claude", "codex"}, want: "podiom", found: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsString(tt.values, tt.want); got != tt.found {
				t.Fatalf("containsString(%v, %q) = %t, want %t", tt.values, tt.want, got, tt.found)
			}
		})
	}
}
