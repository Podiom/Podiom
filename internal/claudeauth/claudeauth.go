// Package claudeauth reads Claude Code's per-profile OAuth credentials. It is
// shared by usage metering and the adapter's model discovery so both present the
// same credential to api.anthropic.com. It never writes or refreshes tokens, and
// token fields are never marshaled or logged.
package claudeauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// UserAgent mimics the Claude Code CLI. OAuth-scoped api.anthropic.com
	// endpoints gate on a claude-code User-Agent; we send a stable one.
	UserAgent = "claude-code/1.0.0 (podiom)"
	// OAuthBeta is the anthropic-beta flag those endpoints require.
	OAuthBeta = "oauth-2025-04-20"
)

// Credentials is the subset of ~/.claude/.credentials.json we need. Token fields
// live only on this struct; they are never marshaled or logged.
type Credentials struct {
	AccessToken      string
	Scopes           []string
	ExpiresAt        int64 // ms epoch
	SubscriptionType string
}

type credentialsFile struct {
	OAuth struct {
		AccessToken      string   `json:"accessToken"`
		RefreshToken     string   `json:"refreshToken"`
		ExpiresAt        int64    `json:"expiresAt"`
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// ConfigDir resolves the Claude config directory for a profile. An empty
// configDir falls back to $CLAUDE_CONFIG_DIR then ~/.claude. A leading ~ is
// expanded so profile dirs like "~/.claude-work" resolve correctly.
func ConfigDir(configDir string) string {
	dir := configDir
	if dir == "" {
		dir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".claude")
	}
	return expandHome(dir)
}

// CredentialPath resolves the credentials file for a profile.
func CredentialPath(configDir string) string {
	return filepath.Join(ConfigDir(configDir), ".credentials.json")
}

// isDefaultDir reports whether configDir resolves to the CLI's default ~/.claude
// (where macOS stores credentials in the Keychain, not a file).
func isDefaultDir(configDir string) bool {
	if configDir == "" && os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return filepath.Clean(ConfigDir(configDir)) == filepath.Join(home, ".claude")
}

// ReadCredentials reads a profile's Claude OAuth credentials read-only. On macOS
// Claude Code stores tokens in the login Keychain rather than a file — for the
// default account and for every custom CLAUDE_CONFIG_DIR alike — so an absent
// credentials file falls back to the profile's Keychain entry. os.IsNotExist
// errors are surfaced so callers can map them to no_credentials.
func ReadCredentials(configDir string) (Credentials, error) {
	path := CredentialPath(configDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && runtime.GOOS == "darwin" {
			if creds, kerr := readKeychainCredentials(configDir); kerr == nil {
				return creds, nil
			}
		}
		return Credentials{}, err
	}
	return parseCredentials(raw)
}

func parseCredentials(raw []byte) (Credentials, error) {
	var file credentialsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return Credentials{}, fmt.Errorf("parse claude credentials: %w", err)
	}
	return Credentials{
		AccessToken:      file.OAuth.AccessToken,
		Scopes:           file.OAuth.Scopes,
		ExpiresAt:        file.OAuth.ExpiresAt,
		SubscriptionType: file.OAuth.SubscriptionType,
	}, nil
}

// KeychainBase is the macOS Keychain generic-password service under which Claude
// Code stores the default account's OAuth credentials.
const KeychainBase = "Claude Code-credentials"

// KeychainService returns the Keychain service name for a config dir. The default
// account uses the bare base name; a custom CLAUDE_CONFIG_DIR uses
// "<base>-<first 8 hex of sha256(absolute dir)>", matching how the Claude Code
// CLI names its per-profile Keychain entries.
func KeychainService(configDir string) string {
	if isDefaultDir(configDir) {
		return KeychainBase
	}
	sum := sha256.Sum256([]byte(ConfigDir(configDir)))
	return KeychainBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// readKeychainCredentials reads a Claude account's token from the macOS login
// Keychain via the `security` CLI. The returned blob has the same shape as
// .credentials.json. The token is only ever passed to the parser.
func readKeychainCredentials(configDir string) (Credentials, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", KeychainService(configDir), "-w").Output()
	if err != nil {
		return Credentials{}, err
	}
	return parseCredentials(out)
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

// HasScope reports whether the credentials carry the given OAuth scope.
func (c Credentials) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if strings.EqualFold(strings.TrimSpace(s), scope) {
			return true
		}
	}
	return false
}

// Expired reports whether the access token's expiry has passed.
func (c Credentials) Expired() bool {
	return c.ExpiresAt > 0 && time.UnixMilli(c.ExpiresAt).Before(time.Now())
}

// AccessToken returns a profile's non-expired OAuth access token. It never writes
// or refreshes tokens; an absent or expired token is returned as an error so
// callers can degrade gracefully. The token is only ever used as a Bearer
// credential.
func AccessToken(configDir string) (string, error) {
	creds, err := ReadCredentials(configDir)
	if err != nil {
		return "", err
	}
	if creds.AccessToken == "" {
		return "", fmt.Errorf("no claude oauth access token")
	}
	if creds.Expired() {
		return "", fmt.Errorf("claude oauth token expired")
	}
	return creds.AccessToken, nil
}
