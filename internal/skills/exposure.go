package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

// Sync provisions the skill roots and rebuilds the union in one call. It is the
// entry point used at daemon start and by the installer/CLI so the union is
// current without manual steps (S12/S24).
func Sync() (Report, error) {
	if err := Provision(); err != nil {
		return Report{}, err
	}
	return Relink()
}

// EnsureClaudeWorkspaceLink makes the skills union discoverable to a
// Podiom-launched Claude turn by placing <workspaceDir>/.claude/skills as a
// symlink to ~/.agents/skills (S6/S10). Claude is then launched with
// `--add-dir <workspaceDir>` so it scans that .claude/skills scope.
//
// A single link to the union root keeps the agent current as the union changes,
// and crucially uses no CLAUDE_CONFIG_DIR — skill exposure stays independent of
// auth/profiles (S7). It is idempotent and never clobbers an existing entry.
func EnsureClaudeWorkspaceLink(workspaceDir string) error {
	roots, err := DefaultRoots()
	if err != nil {
		return err
	}
	claudeDir := filepath.Join(workspaceDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", claudeDir, err)
	}
	link := filepath.Join(claudeDir, "skills")

	if info, err := os.Lstat(link); err == nil {
		// Already present — leave whatever is there (idempotent, no clobber).
		_ = info
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return makeLink(roots.Agents, link)
}
