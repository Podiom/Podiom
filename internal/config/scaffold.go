package config

import (
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
type ScaffoldResult struct {
	CreatedHome       bool
	CreatedConfig     bool
	CreatedBaseAgents bool
	CreatedProjects   bool
}

// Scaffold ensures the storage root and its directory tree exist and writes the
// Podiom-owned seed files (config.yaml, base AGENTS.md, empty projects.yaml) when
// absent. It is idempotent and never overwrites existing user files — a fresh
// install always ends up with a real, self-documenting config to edit (R9.1).
func Scaffold(p Paths) (ScaffoldResult, error) {
	var res ScaffoldResult

	if _, err := os.Stat(p.Home); os.IsNotExist(err) {
		res.CreatedHome = true
	}

	dirs := []string{p.Home, p.AgentsDir, p.ProjectsDir, p.SchedulesDir, p.ProfilesDir, p.LogsDir, p.PushDir}
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

	wrote, err = writeIfAbsent(p.BaseAgents, baseAgentsMD, 0o644)
	if err != nil {
		return res, err
	}
	res.CreatedBaseAgents = wrote

	wrote, err = writeIfAbsent(p.ProjectsYAML, emptyProjectsYAML, 0o644)
	if err != nil {
		return res, err
	}
	res.CreatedProjects = wrote

	return res, nil
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
