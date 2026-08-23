package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeLegacyManifest(t *testing.T, agentsDir, agent string, entries []ManifestEntry) string {
	t.Helper()
	root := filepath.Join(agentsDir, agent, "tools")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestMigrateFoldsPerAgentManifests(t *testing.T) {
	home := t.TempDir()
	toolset := filepath.Join(home, "toolset")
	agentsDir := filepath.Join(home, "agents")

	atlasManifest := writeLegacyManifest(t, agentsDir, "atlas", []ManifestEntry{
		{Tool: "lychee", Installer: "npm", Package: "lychee", Version: "0.15", RequestID: "req-1", GoalID: "goal-1", InstalledAt: "2026-01-01T00:00:00Z"},
	})
	novaManifest := writeLegacyManifest(t, agentsDir, "nova", []ManifestEntry{
		{Tool: "ruff", Installer: "uv", Package: "ruff", InstalledAt: "2026-01-02T00:00:00Z"},
		// Same tool as atlas: the first one wins, one shared copy is the point.
		{Tool: "lychee", Installer: "npm", Package: "lychee", InstalledAt: "2026-01-02T00:00:00Z"},
	})

	leftovers, err := Migrate(toolset, agentsDir)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(leftovers) != 2 {
		t.Fatalf("leftovers = %v, want both agent tool dirs", leftovers)
	}

	list, err := List(toolset)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("toolset = %+v, want lychee and ruff once each", list)
	}
	byTool := map[string]ToolStatus{}
	for _, e := range list {
		byTool[e.Tool] = e
	}
	lychee, ok := byTool["lychee"]
	if !ok {
		t.Fatalf("lychee missing: %+v", list)
	}
	if !lychee.NeedsReinstall {
		t.Error("a migrated entry has no files yet and must be marked needs_reinstall")
	}
	if lychee.Broken {
		t.Error("a migrated entry is pending, not broken — reporting both reads as two problems")
	}
	if lychee.InstalledBy != "atlas" {
		t.Errorf("installed_by = %q, want the agent it came from", lychee.InstalledBy)
	}
	if lychee.RequestID != "req-1" || lychee.GoalID != "goal-1" {
		t.Errorf("approval history lost: %+v", lychee)
	}
	// The spec survives intact, which is what makes one reinstall enough.
	if spec := lychee.Spec(); spec.Installer != InstallerNPM || spec.Package != "lychee" || spec.Version != "0.15" {
		t.Errorf("spec = %+v", spec)
	}

	// The per-agent manifests are gone, so a second run is a no-op.
	for _, p := range []string{atlasManifest, novaManifest} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("legacy manifest %s survived: %v", p, err)
		}
	}
	leftovers, err = Migrate(toolset, agentsDir)
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("second run = %v %v, want a no-op", leftovers, err)
	}
	if again, _ := List(toolset); len(again) != 2 {
		t.Fatalf("second run changed the toolset: %+v", again)
	}
}

func TestMigrateWithNothingToDo(t *testing.T) {
	home := t.TempDir()
	// No agents directory at all — a fresh install.
	leftovers, err := Migrate(filepath.Join(home, "toolset"), filepath.Join(home, "agents"))
	if err != nil || leftovers != nil {
		t.Fatalf("migrate = %v %v, want a clean no-op", leftovers, err)
	}
	if _, err := os.Stat(filepath.Join(home, "toolset", "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("migration with nothing to fold must not create a manifest")
	}
}

// TestMigrateKeepsExistingToolsetEntries guards the case where a tool has
// already been installed into the toolset: the live install must win over a
// stale per-agent record of the same name.
func TestMigrateKeepsExistingToolsetEntries(t *testing.T) {
	home := t.TempDir()
	toolset := filepath.Join(home, "toolset")
	agentsDir := filepath.Join(home, "agents")

	d := DirsFor(toolset)
	if err := recordInstall(d, ManifestEntry{Tool: "lychee", Installer: "npm", Package: "lychee", Version: "1.0"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	writeLegacyManifest(t, agentsDir, "atlas", []ManifestEntry{
		{Tool: "lychee", Installer: "npm", Package: "lychee", Version: "0.1"},
	})

	if _, err := Migrate(toolset, agentsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	list, _ := List(toolset)
	if len(list) != 1 || list[0].Version != "1.0" || list[0].NeedsReinstall {
		t.Fatalf("toolset = %+v, want the existing install untouched", list)
	}
}
