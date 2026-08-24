package core

import (
	podiomtools "github.com/Podiom/Podiom/internal/tools"
)

// migrateAgentTools folds any pre-toolset per-agent tool manifests into the
// shared toolset manifest (see tools.Migrate). Best-effort by design: a
// migration problem must not stop the daemon from serving, and the entries it
// could not fold are still readable on disk.
//
// The leftover directories are logged rather than deleted. Podiom did not put
// anything in them that it can safely reason about any more — npm prefixes and
// uv environments point at their old absolute paths — so removing them is the
// user's call, and they need to know where to look.
func (c *Core) migrateAgentTools() {
	leftovers, err := podiomtools.Migrate(c.paths.ToolsetDir, c.paths.AgentsDir)
	if err != nil {
		c.log.Warn("per-agent tool migration failed", "event", "toolset", "error", err)
		return
	}
	if len(leftovers) == 0 {
		return
	}
	c.log.Info("migrated per-agent tools into the shared toolset",
		"event", "toolset",
		"agents", len(leftovers),
		"toolset", c.paths.ToolsetDir,
		"leftover_dirs", leftovers,
		"note", "these tools are listed as needing reinstall; the old directories are no longer used and can be deleted",
	)
}
