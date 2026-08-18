package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// RelayEnrollment is this installation's identity with the Podiom Push Relay.
//
// The relay mints both values when a daemon registers itself, and returns them exactly
// once — there is no endpoint that reads a credential back. Losing this file therefore
// orphans the relay-side tenant, and re-registering is rate limited, so it is treated as
// something to protect rather than something to regenerate.
type RelayEnrollment struct {
	// InstanceID names the tenant the credential authenticates. It is the relay's own
	// identifier and is not interchangeable with the installation id: that one tells the
	// mobile app which daemon to send an action back to, and never authorizes anything.
	InstanceID string `json:"instance_id"`
	// Credential authenticates every relay request. It is a bearer secret and is
	// deliberately not the Podiom gateway token — a relay compromise must not yield
	// access to the installation itself.
	Credential string `json:"credential"`
}

// LoadRelayEnrollment reads a stored enrollment.
//
// A missing file returns a zero enrollment and no error: that is a daemon that has not
// registered with the relay yet, which is the normal state until someone registers a
// device.
//
// Every other read failure is an error. Treating an unreadable file as "not enrolled"
// would re-register and abandon the existing tenant along with every device under it,
// and the relay caps registrations per address, so the recovery would be slow as well as
// lossy.
func LoadRelayEnrollment(path string) (RelayEnrollment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RelayEnrollment{}, nil
		}
		return RelayEnrollment{}, fmt.Errorf("read relay enrollment: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		// An interrupted write. Safe to treat as unenrolled: nothing was ever used.
		return RelayEnrollment{}, nil
	}
	var enrollment RelayEnrollment
	if err := json.Unmarshal(raw, &enrollment); err != nil {
		return RelayEnrollment{}, fmt.Errorf("parse relay enrollment: %w", err)
	}
	if enrollment.InstanceID == "" || enrollment.Credential == "" {
		return RelayEnrollment{}, fmt.Errorf("relay enrollment %q is incomplete", path)
	}
	return enrollment, nil
}

// SaveRelayEnrollment persists an enrollment, and must be called before the credential
// is used for anything: a credential spent but not stored is a tenant that cannot be
// reached again.
func SaveRelayEnrollment(path string, enrollment RelayEnrollment) error {
	if enrollment.InstanceID == "" || enrollment.Credential == "" {
		return errors.New("relay enrollment needs both an instance id and a credential")
	}
	body, err := json.MarshalIndent(enrollment, "", "  ")
	if err != nil {
		return fmt.Errorf("encode relay enrollment: %w", err)
	}
	// 0600 for the same reason as the gateway token: this one is a bearer secret.
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write relay enrollment: %w", err)
	}
	return nil
}
