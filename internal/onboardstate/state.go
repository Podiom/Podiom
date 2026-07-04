// Package onboardstate persists the one-bit first-run completion marker shared
// by the CLI wizard and the Home Assistant pre-dashboard web flow.
package onboardstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is written to $PODIOM_HOME/onboarding.json.
type State struct {
	Completed   bool      `json:"completed"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// Read loads the onboarding state. A missing file means onboarding has not
// completed yet.
func Read(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read onboarding state: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse onboarding state: %w", err)
	}
	return st, nil
}

// MarkComplete records that the shared onboarding wizard finished.
func MarkComplete(path string, now time.Time) (State, error) {
	if now.IsZero() {
		now = time.Now()
	}
	st := State{Completed: true, CompletedAt: now.UTC()}
	if err := Write(path, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// Write stores the onboarding state using owner-only permissions.
func Write(path string, st State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create onboarding state dir: %w", err)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal onboarding state: %w", err)
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".onboarding-*.json")
	if err != nil {
		return fmt.Errorf("create onboarding state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write onboarding state temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod onboarding state temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close onboarding state temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace onboarding state: %w", err)
	}
	return nil
}
