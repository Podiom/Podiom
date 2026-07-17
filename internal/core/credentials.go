package core

import (
	"context"
	"errors"

	"github.com/Podiom/Podiom/internal/creds"
)

// ErrCredentialsUnavailable is returned when no credentials store was wired in
// (e.g. unit tests constructing Core without one).
var ErrCredentialsUnavailable = errors.New("credentials store unavailable")

// ListCredentials returns all stored credentials. Callers exposing these
// outward must serialize a value-free view — the secret value never leaves
// the daemon.
func (c *Core) ListCredentials(ctx context.Context) ([]creds.Credential, error) {
	if c.credentials == nil {
		return nil, ErrCredentialsUnavailable
	}
	return c.credentials.List()
}

// StoreCredential validates and upserts a user-supplied credential. The value
// is never logged; only the name is.
func (c *Core) StoreCredential(ctx context.Context, cred creds.Credential) error {
	if c.credentials == nil {
		return ErrCredentialsUnavailable
	}
	if err := c.credentials.Set(cred); err != nil {
		return err
	}
	c.log.Info("credential stored", "event", "credentials", "name", cred.Name, "goal", cred.GoalID)
	return nil
}

// DeleteCredential removes a stored credential by name.
func (c *Core) DeleteCredential(ctx context.Context, name string) error {
	if c.credentials == nil {
		return ErrCredentialsUnavailable
	}
	if err := c.credentials.Delete(name); err != nil {
		return err
	}
	c.log.Info("credential deleted", "event", "credentials", "name", name)
	return nil
}
