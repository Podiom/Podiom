package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

// LoadOrCreateInstallationID returns this Podiom installation's stable identity,
// generating and persisting one on first call.
//
// The id is a random uuid, deliberately derived from nothing: it must not change
// when the daemon's address does. An identity based on hostname, IP, port or a Home
// Assistant ingress path would make moving Podiom to a new machine — or simply
// reaching it over a different network path — look like a different installation, so
// notifications and registered devices would appear to belong to a stranger.
//
// It lives in a file rather than the database so it survives a database reset or a
// restore from backup independently, and so it can be read at startup before the
// store is known to be usable. The same reasoning the gateway token already follows.
func LoadOrCreateInstallationID(path string) (string, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
		// An empty file is an interrupted first write, so generating one is right.
	case errors.Is(err, os.ErrNotExist):
		// First run.
	default:
		// The file exists but cannot be read — a permissions problem, or a disk
		// error. Generating a new id here would silently change this installation's
		// identity, making every registered device and stored notification look like
		// it belongs to someone else. Refuse instead, and let the caller degrade.
		return "", fmt.Errorf("read installation id: %w", err)
	}

	id := uuid.NewString()
	// 0600: the id is not a secret in the way a token is, but it identifies this
	// installation to the push relay, so it is not world-readable either.
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write installation id: %w", err)
	}
	return id, nil
}
