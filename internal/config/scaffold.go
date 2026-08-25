package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// Embedded templates written to the storage root on first run. Keeping these as
// real files in the repo (rather than string literals) means the shipped default
// config.yaml is the same artifact we document against.
var (
	//go:embed config.default.yaml
	defaultConfigYAML []byte

	//go:embed templates/AGENTS.base.md
	baseAgentsMD []byte

	//go:embed templates/projects.empty.yaml
	emptyProjectsYAML []byte

	//go:embed templates/SOUL.md
	agentSoulMD []byte
)

// ScaffoldResult reports what first-run scaffolding actually created, so the
// daemon can log a useful "initialized fresh ~/.podiom" message.
// RefreshedBaseAgents is the one "changed an existing file" signal: it reports
// that an upgrade replaced the base AGENTS.md, which is otherwise invisible.
type ScaffoldResult struct {
	CreatedHome         bool
	CreatedConfig       bool
	CreatedBaseAgents   bool
	RefreshedBaseAgents bool
	CreatedProjects     bool
}

// Scaffold ensures the storage root and its directory tree exist and writes the
// Podiom-owned seed files. It is idempotent, and it distinguishes two kinds of
// file:
//
//   - config.yaml and projects.yaml are seeded once and then belong to the user;
//     Scaffold never overwrites them, so a fresh install ends up with a real,
//     self-documenting config to edit and an upgrade never discards edits (R9.1).
//   - The base AGENTS.md is Podiom-generated, not a seed. It is rewritten from
//     the embedded copy whenever it differs, so instruction changes reach existing
//     installs instead of freezing at whichever version scaffolded the home
//     (R5.14). User instructions belong in agents/<name>/AGENTS.md, which Scaffold
//     never touches.
func Scaffold(p Paths) (ScaffoldResult, error) {
	var res ScaffoldResult

	if _, err := os.Stat(p.Home); os.IsNotExist(err) {
		res.CreatedHome = true
	}

	// The unassigned plans directory is where plan-mode sessions with no project
	// write their plans (see core.planDirForContext); scaffold it up front so the
	// path always exists.
	unassignedPlansDir := filepath.Join(p.ProjectsDir, "unassigned", "plans")
	dirs := []string{p.Home, p.AgentsDir, p.ToolsetDir, p.ProjectsDir, unassignedPlansDir, p.SchedulesDir, p.ProfilesDir, p.LogsDir, p.AttachmentsDir, p.PushDir}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return res, fmt.Errorf("create dir %s: %w", d, err)
		}
	}

	wrote, err := writeIfAbsent(p.ConfigYAML, defaultConfigYAML, 0o644)
	if err != nil {
		return res, err
	}
	res.CreatedConfig = wrote

	created, refreshed, err := writeManaged(p.BaseAgents, baseAgentsMD, 0o644)
	if err != nil {
		return res, err
	}
	res.CreatedBaseAgents = created
	res.RefreshedBaseAgents = refreshed

	wrote, err = writeIfAbsent(p.ProjectsYAML, emptyProjectsYAML, 0o644)
	if err != nil {
		return res, err
	}
	res.CreatedProjects = wrote

	return res, nil
}

// writeManaged writes data to path unless the file already holds exactly those
// bytes, reporting whether it created the file and whether it replaced different
// content. It is for files Podiom generates rather than seeds: the content is
// always the embedded copy, so an upgrade carries its changes into every install.
//
// Comparing before writing matters because Scaffold runs on every daemon start.
// The overwhelmingly common case is an unchanged file, and that case should cost
// a read rather than a write — it keeps mtime meaningful (it marks the upgrade
// that last changed the content) and leaves the refresh log line rare enough to
// be worth reading.
func writeManaged(path string, data []byte, perm os.FileMode) (created, refreshed bool, err error) {
	switch existing, readErr := os.ReadFile(path); {
	case readErr == nil:
		if bytes.Equal(existing, data) {
			return false, false, nil
		}
		refreshed = true
	case os.IsNotExist(readErr):
		created = true
	default:
		return false, false, fmt.Errorf("read %s: %w", path, readErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, false, fmt.Errorf("create parent of %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return false, false, fmt.Errorf("write %s: %w", path, err)
	}
	return created, refreshed, nil
}

// writeIfAbsent writes data to path only if no file is already there, reporting
// whether it wrote. Existing files (which the user may have edited) are left
// untouched.
func writeIfAbsent(path string, data []byte, perm os.FileMode) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create parent of %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// AgentSoulTemplate returns the default SOUL.md skeleton used when a new agent
// is created. A copy is returned so callers can safely modify the bytes.
func AgentSoulTemplate() []byte {
	out := make([]byte, len(agentSoulMD))
	copy(out, agentSoulMD)
	return out
}
