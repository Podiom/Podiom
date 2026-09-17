package core

import (
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
			name:    "mcp server with name",
			kind:    store.AccessMCPServer,
			payload: map[string]string{"server_name": "github"},
		},
		{
			name:    "mcp server missing name",
			kind:    store.AccessMCPServer,
			payload: nil,
			wantErr: `mcp_server request needs payload field "server_name"`,
		},
		{
			// need() trims, so whitespace-only is the same as absent.
			name:    "mcp server whitespace name",
			kind:    store.AccessMCPServer,
			payload: map[string]string{"server_name": "   "},
			wantErr: `mcp_server request needs payload field "server_name"`,
		},

		{
			name:    "skill by id",
			kind:    store.AccessSkill,
			payload: map[string]string{"id": "marketplace/skill"},
		},
		{
			name:    "skill by url",
			kind:    store.AccessSkill,
			payload: map[string]string{"url": "https://example.com/skill.tar.gz"},
		},
		{
			name:    "skill by id and url",
			kind:    store.AccessSkill,
			payload: map[string]string{"id": "marketplace/skill", "url": "https://example.com/skill.tar.gz"},
		},
		{
			name:    "skill with neither id nor url",
			kind:    store.AccessSkill,
			payload: map[string]string{},
			wantErr: `skill request needs payload field "id" or "url"`,
		},
		{
			name:    "skill with blank id and blank url",
			kind:    store.AccessSkill,
			payload: map[string]string{"id": " ", "url": "\t"},
			wantErr: `skill request needs payload field "id" or "url"`,
		},

		{
			name:    "cli tool host-only spec",
			kind:    store.AccessCLITool,
			payload: map[string]string{"tool": "ripgrep"},
		},
		{
			// Invalid specs propagate from podiomtools.SpecFromPayload(...).Validate().
			name:    "cli tool missing tool name",
			kind:    store.AccessCLITool,
			payload: map[string]string{},
			wantErr: `tool install needs field "tool"`,
		},
		{
			name:    "cli tool installer missing package",
			kind:    store.AccessCLITool,
			payload: map[string]string{"tool": "ripgrep", "installer": "npm"},
			wantErr: `npm install needs field "package"`,
		},

		{
			name:    "env var by name",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API_KEY"},
		},
		{
			name:    "env var missing name",
			kind:    store.AccessEnvVar,
			payload: map[string]string{},
			wantErr: `env_var request needs payload field "var_name"`,
		},
		{
			name:    "env var whitespace name",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": " \t "},
			wantErr: `env_var request needs payload field "var_name"`,
		},
		{
			// The presence of the key is the violation, not its content: an
			// empty value must still fail or a blank-string refactor would
			// silently start accepting secrets (§6).
			name:    "env var carrying empty value",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API_KEY", "value": ""},
			wantErr: "env_var requests must never carry the secret value — name the variable and its purpose only",
		},
		{
			name:    "env var carrying value",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "API_KEY", "value": "sk-live-secret"},
			wantErr: "env_var requests must never carry the secret value — name the variable and its purpose only",
		},
		{
			name:    "env var name with equals",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "FOO=bar"},
			wantErr: "env_var var_name must be a bare variable name",
		},
		{
			name:    "env var name with space",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "FOO BAR"},
			wantErr: "env_var var_name must be a bare variable name",
		},
		{
			name:    "env var name with tab",
			kind:    store.AccessEnvVar,
			payload: map[string]string{"var_name": "FOO\tBAR"},
			wantErr: "env_var var_name must be a bare variable name",
		},

		{
			name:    "permission mode approve",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": "approve"},
		},
		{
			name:    "permission mode auto",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": "auto"},
		},
		{
			name:    "permission mode yolo",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": "yolo"},
		},
		{
			name:    "permission mode trimmed",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": " auto "},
		},
		{
			name:    "permission mode missing",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{},
			wantErr: `permission_mode request needs payload field "mode" of approve|auto|yolo`,
		},
		{
			name:    "permission mode unknown",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": "turbo"},
			wantErr: `permission_mode request needs payload field "mode" of approve|auto|yolo`,
		},
		{
			name:    "permission mode case sensitive",
			kind:    store.AccessPermissionMode,
			payload: map[string]string{"mode": "AUTO"},
			wantErr: `permission_mode request needs payload field "mode" of approve|auto|yolo`,
		},

		{
			name:    "unknown kind rejected, not a fallthrough",
			kind:    store.AccessRequestKind("teleport"),
			payload: map[string]string{"server_name": "github"},
			wantErr: `unknown access request kind "teleport"`,
		},
		{
			name:    "empty kind rejected",
			kind:    store.AccessRequestKind(""),
			payload: map[string]string{},
			wantErr: `unknown access request kind ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAccessPayload(tt.kind, tt.payload)
			switch {
			case tt.wantErr == "":
				if err != nil {
					t.Errorf("validateAccessPayload(%q, %v) = %v, want nil", tt.kind, tt.payload, err)
				}
			case err == nil:
				t.Errorf("validateAccessPayload(%q, %v) = nil, want error %q", tt.kind, tt.payload, tt.wantErr)
			case err.Error() != tt.wantErr:
				t.Errorf("validateAccessPayload(%q, %v) = %q, want %q", tt.kind, tt.payload, err, tt.wantErr)
			}
		})
	}
}
