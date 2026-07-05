// Package config resolves Podiom's storage root, loads and validates the system
// configuration (config.yaml), and performs first-run scaffolding of the
// ~/.podiom/ directory tree. All Podiom state lives under a single root so the
// layout is predictable and backup-friendly on every OS (R9.1 / R10.2 / D14).
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvHome is the environment variable that overrides the storage root. Keeping
// the root overridable (rather than hard-coding ~/.podiom/) is what lets Podiom
// later run as a Home Assistant add-on with a mapped volume without a rewrite
// (Principle 7 / D18).
const EnvHome = "PODIOM_HOME"

// Paths holds every well-known location under the storage root. Resolve these
// once at startup and pass Paths around rather than recomputing strings.
type Paths struct {
	Home           string // the storage root itself (e.g. ~/.podiom)
	ConfigYAML     string // config.yaml
	MCPYAML        string // mcp.yaml
	BaseAgents     string // AGENTS.md (Podiom-owned base instructions)
	DB             string // podiom.db (SQLite)
	AgentsDir      string // agents/
	ProjectsDir    string // projects/
	ProjectsYAML   string // projects/projects.yaml
	SchedulesDir   string // schedules/
	ProfilesDir    string // profiles/
	LogsDir        string // logs/
	ArchiveDir     string // archive/ (archived tasks and other exports)
	PushDir        string // push/ (VAPID keypair for Web Push)
	MarketplaceDir string // marketplace/ (skill-registry secrets + cache state)
	GatewayToken   string // gateway.token (the API/WS gateway token, HA7/HA8)
	Onboarding     string // onboarding.json (first-run completion state)
}

// ResolveHome returns the absolute storage root. Precedence:
//  1. $PODIOM_HOME if set (with ~ expansion),
//  2. ~/.podiom otherwise.
//
// The result is always absolute: a daemon may chdir (or be launched from an
// arbitrary cwd), so a relative PODIOM_HOME like "podiom-data" must be anchored
// at resolution time rather than re-interpreted later (R10.2).
func ResolveHome() (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvHome)); v != "" {
		expanded, err := expandTilde(v)
		if err != nil {
			return "", err
		}
		return filepath.Abs(expanded)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".podiom"), nil
}

// NewPaths derives all well-known paths from a resolved storage root.
func NewPaths(home string) Paths {
	return Paths{
		Home:           home,
		ConfigYAML:     filepath.Join(home, "config.yaml"),
		MCPYAML:        filepath.Join(home, "mcp.yaml"),
		BaseAgents:     filepath.Join(home, "AGENTS.md"),
		DB:             filepath.Join(home, "podiom.db"),
		AgentsDir:      filepath.Join(home, "agents"),
		ProjectsDir:    filepath.Join(home, "projects"),
		ProjectsYAML:   filepath.Join(home, "projects", "projects.yaml"),
		SchedulesDir:   filepath.Join(home, "schedules"),
		ProfilesDir:    filepath.Join(home, "profiles"),
		LogsDir:        filepath.Join(home, "logs"),
		ArchiveDir:     filepath.Join(home, "archive"),
		PushDir:        filepath.Join(home, "push"),
		MarketplaceDir: filepath.Join(home, "marketplace"),
		GatewayToken:   filepath.Join(home, "gateway.token"),
		Onboarding:     filepath.Join(home, "onboarding.json"),
	}
}

// expandTilde expands a leading ~ or ~/ to the user's home directory. Bare
// environment values like "~" or "~/podiom" are common, so handle them rather
// than passing a literal tilde down to the filesystem.
func expandTilde(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return filepath.Clean(p), nil
}
