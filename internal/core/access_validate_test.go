package core

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

func TestValidateAccessPayload(t *testing.T) {
	tests := []struct {
		name        string
		kind        store.AccessRequestKind
		payload     map[string]string
		wantErrText string
	}{
		// AccessMCPServer
		{
			name:        "mcp_server valid",
			kind:        store.AccessMCPServer,
			payload:     map[string]string{"server_name": "github-mcp"},
			wantErrText: "",
		},
		{
			name:        "mcp_server missing server_name",
			kind:        store.AccessMCPServer,
			payload:     map[string]string{},
			wantErrText: `mcp_server request needs payload field "server_name"`,
		},
		{
			name:        "mcp_server empty server_name",
			kind:        store.AccessMCPServer,
			payload:     map[string]string{"server_name": ""},
			wantErrText: `mcp_server request needs payload field "server_name"`,
		},
		{
			name:        "mcp_server whitespace-only server_name",
			kind:        store.AccessMCPServer,
			payload:     map[string]string{"server_name": "   "},
			wantErrText: `mcp_server request needs payload field "server_name"`,
		},

		// AccessSkill (all 4 combinations of id and url)
		{
			name:        "skill both id and url missing/blank",
			kind:        store.AccessSkill,
			payload:     map[string]string{"id": "", "url": ""},
			wantErrText: `skill request needs payload field "id" or "url"`,
		},
		{
			name:        "skill both id and url whitespace",
			kind:        store.AccessSkill,
			payload:     map[string]string{"id": "   ", "url": "   "},
			wantErrText: `skill request needs payload field "id" or "url"`,
		},
		{
			name:        "skill id only",
			kind:        store.AccessSkill,
			payload:     map[string]string{"id": "git-commit", "url": ""},
			wantErrText: "",
		},
		{
			name:        "skill url only",
			kind:        store.AccessSkill,
			payload:     map[string]string{"id": "", "url": "https://example.com/skill.git"},
			wantErrText: "",
		},
		{
			name:        "skill both id and url provided",
			kind:        store.AccessSkill,
			payload:     map[string]string{"id": "git-commit", "url": "https://example.com/skill.git"},
			wantErrText: "",
		},

		// AccessEnvVar
		{
			name:        "env_var valid bare name",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "GITHUB_TOKEN"},
			wantErrText: "",
		},
		{
			name:        "env_var missing var_name",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{},
			wantErrText: `env_var request needs payload field "var_name"`,
		},
		{
			name:        "env_var whitespace var_name",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "   "},
			wantErrText: `env_var request needs payload field "var_name"`,
		},
		{
			name:        "env_var carries secret value",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "GITHUB_TOKEN", "value": "ghp_secret"},
			wantErrText: "env_var requests must never carry the secret value",
		},
		{
			name:        "env_var carries empty value key",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "GITHUB_TOKEN", "value": ""},
			wantErrText: "env_var requests must never carry the secret value",
		},
		{
			name:        "env_var var_name with equals sign",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "FOO=bar"},
			wantErrText: "env_var var_name must be a bare variable name",
		},
		{
			name:        "env_var var_name with space",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "FOO BAR"},
			wantErrText: "env_var var_name must be a bare variable name",
		},
		{
			name:        "env_var var_name with tab",
			kind:        store.AccessEnvVar,
			payload:     map[string]string{"var_name": "FOO\tBAR"},
			wantErrText: "env_var var_name must be a bare variable name",
		},

		// AccessPermissionMode
		{
			name:        "permission_mode valid bare mode",
			kind:        store.AccessPermissionMode,
			payload:     map[string]string{"mode": "approve"},
			wantErrText: "",
		},
		{
			name:        "permission_mode valid with surrounding whitespace",
			kind:        store.AccessPermissionMode,
			payload:     map[string]string{"mode": " auto "},
			wantErrText: "",
		},
		{
			name:        "permission_mode unknown mode",
			kind:        store.AccessPermissionMode,
			payload:     map[string]string{"mode": "grant_all"},
			wantErrText: `permission_mode request needs payload field "mode" of`,
		},
		{
			name:        "permission_mode missing mode",
			kind:        store.AccessPermissionMode,
			payload:     map[string]string{},
			wantErrText: `permission_mode request needs payload field "mode" of`,
		},

		// AccessCLITool
		{
			name:        "cli_tool valid host-only spec",
			kind:        store.AccessCLITool,
			payload:     map[string]string{"tool": "ffmpeg"},
			wantErrText: "",
		},
		{
			name:        "cli_tool invalid spec propagates error",
			kind:        store.AccessCLITool,
			payload:     map[string]string{},
			wantErrText: `tool install needs field "tool"`,
		},

		// Default rejection of unrecognized kind
		{
			name:        "unknown access request kind rejected",
			kind:        store.AccessRequestKind("unknown_kind"),
			payload:     map[string]string{"foo": "bar"},
			wantErrText: `unknown access request kind "unknown_kind"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAccessPayload(tt.kind, tt.payload)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("validateAccessPayload(%q, %v) unexpected error: %v", tt.kind, tt.payload, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateAccessPayload(%q, %v) = nil, want error containing %q", tt.kind, tt.payload, tt.wantErrText)
			}
			if !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("validateAccessPayload(%q, %v) error = %q, want containing %q", tt.kind, tt.payload, err.Error(), tt.wantErrText)
			}
		})
	}
}
