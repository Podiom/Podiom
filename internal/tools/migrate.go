package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Migrate folds any per-agent tool manifests (the pre-toolset layout,
// agents/<name>/tools/manifest.json) into the shared toolset manifest and
// returns the leftover per-agent directories so the caller can tell the user
// where they are.
//
// The files themselves are deliberately not moved. npm prefixes and uv
// environments record absolute paths and would break in a new location, so a
// half-moved install would be worse than none. Instead each entry arrives with
// NeedsReinstall set: the installer, package and version are intact, so one
// install call — from the UI or from the agent that wants it — restores the
// tool into the shared toolset.
//
// Removing the per-agent manifest is what makes this idempotent: a second
// daemon start finds nothing to migrate.
func Migrate(toolsetRoot, agentsDir string) (leftovers []string, err error) {
	agents, err := os.ReadDir(agentsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agents dir: %w", err)
	}

	toolset := DirsFor(toolsetRoot)
	existing, err := loadManifest(toolset)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(existing))
	for _, e := range existing {
		have[e.Tool] = true
	}

	changed := false
	for _, agent := range agents {
		if !agent.IsDir() {
			continue
		}
		legacyRoot := filepath.Join(agentsDir, agent.Name(), "tools")
		legacy := DirsFor(legacyRoot)
		if _, statErr := os.Stat(legacy.Manifest); errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		entries, lerr := loadManifest(legacy)
		if lerr != nil {
			// One unreadable manifest must not block the others, or the whole
			// migration wedges on a single corrupted file.
			return leftovers, lerr
		}
		for _, e := range entries {
			if have[e.Tool] {
				continue
			}
			if e.InstalledBy == "" {
				e.InstalledBy = agent.Name()
			}
			e.NeedsReinstall = true
			existing = append(existing, e)
			have[e.Tool] = true
			changed = true
		}
		if rerr := os.Remove(legacy.Manifest); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return leftovers, fmt.Errorf("remove legacy manifest %s: %w", legacy.Manifest, rerr)
		}
		leftovers = append(leftovers, legacyRoot)
	}

	if changed {
		if err := saveManifest(toolset, existing); err != nil {
			return leftovers, err
		}
	}
	return leftovers, nil
}
