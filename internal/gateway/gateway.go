// Package gateway implements the Podiom gateway token: the secret that every
// API and WebSocket client must present to operate the daemon (HA7). The token
// is auto-generated on first start and stored as a single line under the
// storage root (HA8); it is the auth primitive shared by both deployment
// methods (standalone and the Home Assistant app) and by the future remote
// mode (HA3/HA11). Log existence and rotation events only — the value itself
// must never reach any log (HA21).
package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// Header carries the token on plain HTTP requests. A custom header (rather
	// than Authorization) survives proxies — HA Ingress in particular — without
	// being consumed or rewritten; Authorization: Bearer is accepted too.
	Header = "X-Podiom-Token"

	// WSProtocol is the application subprotocol the web UI offers on the
	// WebSocket handshake alongside the token entry. Browsers require the
	// server to echo one offered protocol, and it must not be the secret one.
	WSProtocol = "podiom.v1"

	// wsTokenPrefix marks the token entry in Sec-WebSocket-Protocol. The
	// browser WebSocket API cannot set headers, so the token rides the
	// subprotocol list on the handshake (HA7).
	wsTokenPrefix = "podiom-token."

	// tokenBytes of entropy per token; base64url-encoded to 43 characters.
	// Auto-generation is what guarantees strength (HA8) — no user-chosen values.
	tokenBytes = 32
)

// Keeper holds the current gateway token and keeps the on-disk copy in sync.
// It is safe for concurrent use by the HTTP handlers and the rotate endpoint.
type Keeper struct {
	mu    sync.RWMutex
	path  string
	token string
}

// LoadOrCreate returns a Keeper for the token file at path, generating and
// persisting a fresh token when the file is missing or empty. The second
// return reports whether a new token was created (callers log the event —
// never the value).
func LoadOrCreate(path string) (*Keeper, bool, error) {
	if tok, err := ReadTokenFile(path); err == nil && tok != "" {
		return &Keeper{path: path, token: tok}, false, nil
	}
	tok, err := generate()
	if err != nil {
		return nil, false, err
	}
	if err := writeTokenFile(path, tok); err != nil {
		return nil, false, err
	}
	return &Keeper{path: path, token: tok}, true, nil
}

// Current returns the active token value.
func (k *Keeper) Current() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.token
}

// Rotate generates a new token, persists it, and makes it the active one.
// Prior tokens are invalid from this moment (HA12); the caller is responsible
// for disconnecting live clients.
func (k *Keeper) Rotate() (string, error) {
	tok, err := generate()
	if err != nil {
		return "", err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := writeTokenFile(k.path, tok); err != nil {
		return "", err
	}
	k.token = tok
	return tok, nil
}

// Authorize reports whether r presents the current token via any accepted
// carrier: the X-Podiom-Token header, an Authorization bearer, or the
// WebSocket-handshake subprotocol entry. Comparison is constant-time.
func (k *Keeper) Authorize(r *http.Request) bool {
	if k.match(r.Header.Get(Header)) {
		return true
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		if bearer, ok := strings.CutPrefix(auth, "Bearer "); ok && k.match(bearer) {
			return true
		}
	}
	return k.match(WSProtocolToken(r))
}

// WSProtocolToken extracts the token entry from the request's
// Sec-WebSocket-Protocol list, or returns "" when none is present.
func WSProtocolToken(r *http.Request) string {
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, proto := range strings.Split(header, ",") {
			if tok, ok := strings.CutPrefix(strings.TrimSpace(proto), wsTokenPrefix); ok {
				return tok
			}
		}
	}
	return ""
}

// WSProtocolEntry renders the subprotocol list entry for a token; the web UI
// mirrors this format when opening its socket.
func WSProtocolEntry(token string) string { return wsTokenPrefix + token }

func (k *Keeper) match(candidate string) bool {
	if candidate == "" {
		return false
	}
	k.mu.RLock()
	current := k.token
	k.mu.RUnlock()
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(current)) == 1
}

// ReadTokenFile returns the token stored at path. It is a plain function (no
// Keeper) so the CLI and the daemon's spawned MCP helpers can pick the token
// up from disk — the zero-friction same-machine path (HA9).
func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func generate() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate gateway token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// writeTokenFile persists the token atomically (write-temp + rename) with
// 0600 permissions: a half-written token file would lock every client out.
func writeTokenFile(path, token string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create token dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".gateway-token-*")
	if err != nil {
		return fmt.Errorf("write gateway token: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod gateway token: %w", err)
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write gateway token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write gateway token: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("persist gateway token %s: %w", path, err)
	}
	return nil
}
