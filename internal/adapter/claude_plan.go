package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Claude's native plan mode writes the plan to a file and gives no in-band
// signal that it is done: measured against Claude Code 2.1.220, ExitPlanMode is
// absent from the tool pool in headless `-p` runs ("ExitPlanMode exists but is
// not enabled in this context"), and no flag re-enables it. So Podiom observes
// the plans directory rather than being told.
//
// Podiom deliberately does not configure `plansDirectory`. Writing into the
// user's Claude settings to make detection easier would be intrusive, and it is
// unnecessary: the default location is derived from CLAUDE_CONFIG_DIR, which
// Podiom already sets per profile.

// claudePlansDir resolves where this turn's plan will be written, honouring a
// plansDirectory the *user* has configured and otherwise falling back to
// Claude's default of <config dir>/plans.
//
// profileDir is the value Podiom exports as CLAUDE_CONFIG_DIR; empty means
// Podiom leaves the variable unset and Claude uses ~/.claude.
func claudePlansDir(profileDir, workspaceDir string) string {
	configDir := strings.TrimSpace(profileDir)
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configDir = filepath.Join(home, ".claude")
	}
	// Project-local settings win over user settings, matching Claude's own
	// precedence.
	for _, settings := range []string{
		filepath.Join(workspaceDir, ".claude", "settings.json"),
		filepath.Join(configDir, "settings.json"),
	} {
		if dir := claudeSettingsPlansDir(settings); dir != "" {
			if filepath.IsAbs(dir) {
				return filepath.Clean(dir)
			}
			// Claude documents plansDirectory as relative to the project root.
			return filepath.Clean(filepath.Join(workspaceDir, dir))
		}
	}
	return filepath.Join(configDir, "plans")
}

// claudeSettingsPlansDir reads plansDirectory out of one settings file. A
// missing or malformed file is not an error: it just means "not configured".
func claudeSettingsPlansDir(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var settings struct {
		PlansDirectory string `json:"plansDirectory"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return ""
	}
	return strings.TrimSpace(settings.PlansDirectory)
}

// planSnapshot records the modification times of the plan files present before
// a turn, so the turn's own plan can be told apart from pre-existing ones.
type planSnapshot struct {
	dir   string
	seen  map[string]time.Time
	taken time.Time
}

// snapshotPlans records dir's current *.md files. A missing directory is normal
// — Claude creates it when it writes the first plan.
func snapshotPlans(dir string) planSnapshot {
	snap := planSnapshot{dir: dir, seen: map[string]time.Time{}, taken: time.Now()}
	if dir == "" {
		return snap
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return snap
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		snap.seen[entry.Name()] = info.ModTime()
	}
	return snap
}

// detectPlan returns the plan written during the turn, or nil if there is none.
//
// Matching is by modification time rather than by filename. Claude derives the
// plan's slug from the session, so revising a plan overwrites the same file —
// a name-only check would see the first plan and miss every revision.
//
// The directory is shared by every session using the same Claude profile, so
// when several files changed during the turn the newest wins. Attributing the
// wrong plan is worse than attributing none, so ambiguity beyond that resolves
// to nil rather than to a guess.
func detectPlan(snap planSnapshot) *PlanProposal {
	if snap.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(snap.dir)
	if err != nil {
		return nil
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	var changed []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		before, existed := snap.seen[entry.Name()]
		switch {
		case !existed && !info.ModTime().Before(snap.taken):
			// New file written during the turn.
		case existed && info.ModTime().After(before):
			// Revision overwriting the previous plan.
		default:
			continue
		}
		changed = append(changed, candidate{filepath.Join(snap.dir, entry.Name()), info.ModTime()})
	}
	if len(changed) == 0 {
		return nil
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].modTime.After(changed[j].modTime) })
	raw, err := os.ReadFile(changed[0].path)
	if err != nil {
		return nil
	}
	markdown := strings.TrimSpace(string(raw))
	if markdown == "" {
		return nil
	}
	return &PlanProposal{Markdown: markdown, FilePath: changed[0].path}
}
