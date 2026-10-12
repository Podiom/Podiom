package server

import (
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

func TestAccessRequestSummaryRendersPrefixPerKind(t *testing.T) {
	tests := []struct {
		name    string
		kind    store.AccessRequestKind
		payload string
		want    string
	}{
		{
			name:    "mcp server",
			kind:    store.AccessMCPServer,
			payload: `{"server_name":"github"}`,
			want:    "MCP server github",
		},
		{
			name:    "skill with id",
			kind:    store.AccessSkill,
			payload: `{"id":"pdf-tools"}`,
			want:    "skill pdf-tools",
		},
		{
			name:    "cli tool",
			kind:    store.AccessCLITool,
			payload: `{"tool":"ffmpeg"}`,
			want:    "host tool ffmpeg",
		},
		{
			name:    "env var reports the name only",
			kind:    store.AccessEnvVar,
			payload: `{"var_name":"OPENAI_API_KEY"}`,
			want:    "credential OPENAI_API_KEY",
		},
		{
			name:    "permission mode",
			kind:    store.AccessPermissionMode,
			payload: `{"mode":"auto-edit"}`,
			want:    "permission mode auto-edit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := accessRequestSummary(store.AccessRequest{Kind: tt.kind, Payload: tt.payload})
			if got != tt.want {
				t.Fatalf("accessRequestSummary(%s, %s) = %q, want %q", tt.kind, tt.payload, got, tt.want)
			}
		})
	}
}

func TestAccessRequestSummarySkillEmptyIDFallsBackToSkillInstall(t *testing.T) {
	for _, payload := range []string{`{}`, `{"id":""}`} {
		got := accessRequestSummary(store.AccessRequest{Kind: store.AccessSkill, Payload: payload})
		if got != "skill install" {
			t.Fatalf("payload %s: got %q, want %q", payload, got, "skill install")
		}
	}
}

func TestAccessRequestSummaryCLIToolEmptyPayloadKeepsDanglingPrefix(t *testing.T) {
	// Contrast with AccessSkill: a missing tool name is not replaced by a fallback.
	got := accessRequestSummary(store.AccessRequest{Kind: store.AccessCLITool, Payload: `{}`})
	if got != "host tool " {
		t.Fatalf("got %q, want %q", got, "host tool ")
	}
}

func TestAccessRequestSummaryMalformedPayloadRendersPrefixWithEmptyValue(t *testing.T) {
	// json.Unmarshal's error is discarded, so the map stays empty and the
	// prefix is rendered with an empty value. For AccessSkill the empty id
	// selects the "skill install" fallback instead.
	cases := []struct {
		name    string
		kind    store.AccessRequestKind
		payload string
		want    string
	}{
		{"mcp server", store.AccessMCPServer, `{not json`, "MCP server "},
		{"cli tool", store.AccessCLITool, `not json at all`, "host tool "},
		{"env var", store.AccessEnvVar, ``, "credential "},
		{"permission mode", store.AccessPermissionMode, `[1,2]`, "permission mode "},
		{"skill", store.AccessSkill, `{"id":`, "skill install"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := accessRequestSummary(store.AccessRequest{Kind: tc.kind, Payload: tc.payload})
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccessRequestSummaryUnknownKindReturnsKindString(t *testing.T) {
	got := accessRequestSummary(store.AccessRequest{Kind: store.AccessRequestKind("future_kind"), Payload: `{}`})
	if got != "future_kind" {
		t.Fatalf("got %q, want %q", got, "future_kind")
	}
}
