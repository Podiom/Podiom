package claudeauth

import (
	"os"
	"path/filepath"
	"testing"
)

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
