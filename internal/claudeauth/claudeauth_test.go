package claudeauth

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseCredentials(t *testing.T) {
	tests := []struct {
		name             string
		wantCredentials  Credentials
		wantErrorMessage string
		raw              []byte
	}{
		{name: "valid input",
			wantCredentials:  Credentials{"test-access-token", []string{"user:read", "messages:write"}, 4102444800000, "test"},
			wantErrorMessage: "",
			raw:              []byte(`{"claudeAiOauth": {"accessToken": "test-access-token", "refreshToken": "test-refresh-token", "expiresAt": 4102444800000, "scopes": ["user:read", "messages:write"], "subscriptionType": "test"}}`)},

		{name: "empty input",
			wantCredentials:  Credentials{},
			wantErrorMessage: "parse claude credentials: unexpected end of JSON input",
			raw:              []byte{}},

		{name: "missing the oauth key entirely",
			wantCredentials:  Credentials{},
			wantErrorMessage: "",
			raw:              []byte(`{}`)},

		{name: "valid json but not json object",
			wantCredentials:  Credentials{},
			wantErrorMessage: "parse claude credentials: json: cannot unmarshal string into Go value of type claudeauth.credentialsFile",
			raw:              []byte(`"not an object"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCredentials, gotError := parseCredentials(tt.raw)
			if gotError == nil && tt.wantErrorMessage != "" {
				t.Fatalf("expected error, want: %v, got: %v", tt.wantErrorMessage, nil)
			}
			if gotError != nil && gotError.Error() != tt.wantErrorMessage {
				t.Fatalf("unexpected error, want: %v, got: %v", tt.wantErrorMessage, gotError.Error())
			}
			if !reflect.DeepEqual(gotCredentials, tt.wantCredentials) {
				t.Errorf("unexpected credentials, want: %v, got: %v", tt.wantCredentials, gotCredentials)
			}
		})
	}
}

func TestKeychainService(t *testing.T) {
	// A custom CLAUDE_CONFIG_DIR maps to the base service name suffixed with the
	// first 8 hex chars of sha256(absolute dir), matching the Claude Code CLI.
	if got := KeychainService("/Users/marcus/.claude-personal"); got != "Claude Code-credentials-9f8d6274" {
		t.Errorf("custom service = %q, want Claude Code-credentials-9f8d6274", got)
	}

	// The default account uses the bare service name — both for the implicit
	// default (empty dir) and an explicit path to ~/.claude.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := KeychainService(""); got != KeychainBase {
		t.Errorf("default service = %q, want %q", got, KeychainBase)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := KeychainService(filepath.Join(home, ".claude")); got != KeychainBase {
		t.Errorf("explicit default service = %q, want %q", got, KeychainBase)
	}
}

func TestCredentialsHasScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		scope  string
		want   bool
	}{
		{name: "exact match", scopes: []string{"user:inference"}, scope: "user:inference", want: true},
		{name: "trimmed case insensitive match", scopes: []string{" User:Inference "}, scope: "user:inference", want: true},
		{name: "absent", scopes: []string{"user:read"}, scope: "user:inference", want: false},
		{name: "empty scopes", scopes: nil, scope: "user:inference", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Credentials{Scopes: tt.scopes}).HasScope(tt.scope); got != tt.want {
				t.Fatalf("HasScope(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestCredentialsExpired(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{name: "zero never expires", expiresAt: 0, want: false},
		{name: "past", expiresAt: now.Add(-time.Hour).UnixMilli(), want: true},
		{name: "future", expiresAt: now.Add(time.Hour).UnixMilli(), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Credentials{ExpiresAt: tt.expiresAt}).Expired(); got != tt.want {
				t.Fatalf("Expired() = %v, want %v", got, tt.want)
			}
		})
	}
}
