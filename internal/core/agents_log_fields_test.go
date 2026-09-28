package core

import (
	"reflect"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

func TestAgentLogFields(t *testing.T) {
	tests := []struct {
		name  string
		agent store.Agent
		want  map[string]string
	}{
		{
			name: "fully populated agent",
			agent: store.Agent{
				Name:           "reviewer",
				Provider:       config.ProviderClaude,
				Profile:        "work",
				Model:          "opus",
				Effort:         "high",
				PermissionMode: config.PermissionAuto,
				Fallback:       []string{"codex", "gemini"},
				MCPServers:     []string{"github", "fs", "search"},
			},
			want: map[string]string{
				"provider":       "claude",
				"profile":        "work",
				"model":          "opus",
				"effort":         "high",
				"permission":     "auto",
				"fallback_count": "2",
				"mcp_count":      "3",
			},
		},
		{
			name:  "nil slices count as zero",
			agent: store.Agent{Provider: config.ProviderCodex, Fallback: nil, MCPServers: nil},
			want: map[string]string{
				"provider":       "codex",
				"profile":        "",
				"model":          "",
				"effort":         "",
				"permission":     "",
				"fallback_count": "0",
				"mcp_count":      "0",
			},
		},
		{
			name:  "empty non-nil slices count as zero",
			agent: store.Agent{Fallback: []string{}, MCPServers: []string{}},
			want: map[string]string{
				"provider":       "",
				"profile":        "",
				"model":          "",
				"effort":         "",
				"permission":     "",
				"fallback_count": "0",
				"mcp_count":      "0",
			},
		},
		{
			name:  "counts come from their own slices",
			agent: store.Agent{Fallback: []string{"codex"}, MCPServers: []string{"a", "b", "c", "d"}},
			want: map[string]string{
				"provider":       "",
				"profile":        "",
				"model":          "",
				"effort":         "",
				"permission":     "",
				"fallback_count": "1",
				"mcp_count":      "4",
			},
		},
		{
			name:  "zero-value agent returns every key",
			agent: store.Agent{},
			want: map[string]string{
				"provider":       "",
				"profile":        "",
				"model":          "",
				"effort":         "",
				"permission":     "",
				"fallback_count": "0",
				"mcp_count":      "0",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentLogFields(tt.agent)
			if len(got) != 7 {
				t.Fatalf("agentLogFields returned %d keys, want exactly 7: %v", len(got), got)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("agentLogFields() = %v, want %v", got, tt.want)
			}
		})
	}
}
