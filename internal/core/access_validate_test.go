package core

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

func TestValidateAccessPayload(t *testing.T) {
	tests := []struct {
		name    string
		kind    store.AccessRequestKind
		payload map[string]string
		wantErr string
	}{
		{
			name:    "MCP server accepts a name",
			kind:    store.AccessMCPServer,
			payload: map[string]string{"server_name": "filesystem"},
		},
		{
			name:    "MCP server rejects a missing name",
			kind:    store.AccessMCPServer,
			payload: map[string]string{},
			wantErr: `mcp_server request needs payload field "server_name"`,
		},
		{
			name:    "MCP server rejects a blank name",
			kind:    store.AccessMCPServer,
			payload: map[string]string{"server_name": "   "},
			wantErr: `mcp_server request needs payload field "server_name"`,
		},
		{
			name:    "skill accepts an ID",
			kind:    store.AccessSkill,
			payload: map[string]string{"id": "code-review"},
		},
		{
			name:    "skill accepts a URL",
			kind:    store.AccessSkill,
			payload: map[string]string{"url": "https://example.com/skill"},
		},
		{
			name:    "skill accepts an ID and URL",
			kind:    store.AccessSkill,
			payload: map[string]string{"id": "code-review", "url": "https://example.com/skill"},
		},
		{
			name:    "skill rejects blank ID and URL",
			kind:    store.AccessSkill,
			payload: map[string]string{"id": " ", "url": "\t"},
			wantErr: `skill request needs payload field "id" or "url"`,
		},
		{
			name:    "CLI tool accepts a valid spec",
			kind:    store.AccessCLITool,
			payload: map[string]string{"tool": "jq"},
		},
		{
			name:    "CLI tool propagates validation errors",
			kind:    store.AccessCLITool,
			payload: map[string]string{},
			wantErr: `tool install needs field "tool"`,
		},
		{
			name:    "environment variable accepts a bare name",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API_KEY"},
		},
		{
			name:    "environment variable rejects a missing name",
			kind:    store.AccessEnvVar,
			payload: map[string]string{},
			wantErr: `env_var request needs payload field "var_name"`,
		},
		{
			name:    "environment variable rejects an empty value field",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API_KEY", "value": ""},
			wantErr: "env_var requests must never carry the secret value",
		},
		{
			name:    "environment variable rejects assignment syntax",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "FOO=bar"},
			wantErr: "env_var var_name must be a bare variable name",
		},
		{
			name:    "environment variable rejects spaces",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API KEY"},
			wantErr: "env_var var_name must be a bare variable name",
		},
		{
			name:    "environment variable rejects tabs",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API\tKEY"},
			wantErr: "env_var var_name must be a bare variable name",
		},
		{
			name:    "permission mode accepts surrounding whitespace",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": " auto "},
		},
		{
			name:    "permission mode rejects an unknown mode",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": "admin"},
			wantErr: `permission_mode request needs payload field "mode" of approve|auto|yolo`,
		},
		{
			name:    "unknown kind is rejected",
			kind:    store.AccessRequestKind("unknown"),
			payload: map[string]string{},
			wantErr: `unknown access request kind "unknown"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAccessPayload(tt.kind, tt.payload)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAccessPayload() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateAccessPayload() error = nil, want containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateAccessPayload() error = %q, want containing %q", err, tt.wantErr)
			}
		})
	}
}
