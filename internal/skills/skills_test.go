package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkill creates <root>/<name>/SKILL.md with a frontmatter description.
func writeSkill(t *testing.T, root, name, desc, extra string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rootsIn(t *testing.T) Roots {
	t.Helper()
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	r, err := DefaultRoots()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{r.Agents, r.Native[SourceClaude], r.Native[SourceCodex]} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func find(skills []Skill, name string) *Skill {
	for i := range skills {
		if skills[i].Name == name {
			return &skills[i]
		}
	}
	return nil
}

func TestScanSourcesAndDedup(t *testing.T) {
	r := rootsIn(t)
	// A genuinely shared skill (real folder in the union).
	writeSkill(t, r.Agents, "shared-one", "shared skill", "")
	// A claude skill, also union-linked → should show both agents + claude.
	writeSkill(t, r.Native[SourceClaude], "from-claude", "claude skill", "")
	if _, err := RelinkRoots(r); err != nil {
		t.Fatal(err)
	}
	// A codex-only skill (not yet linked).
	writeSkill(t, r.Native[SourceCodex], "from-codex", "codex skill", "")

	got, err := ScanRoots(r)
	if err != nil {
		t.Fatal(err)
	}

	shared := find(got, "shared-one")
	if shared == nil || len(shared.Sources) != 1 || shared.Sources[0] != SourceAgents {
		t.Fatalf("shared-one sources = %+v, want [agents]", shared)
	}
	if shared.Conflict {
		t.Errorf("shared-one should not be a conflict")
	}

	claude := find(got, "from-claude")
	if claude == nil {
		t.Fatal("from-claude missing")
	}
	// After relink the union has a symlink → ~/.claude/skills/from-claude, so it
	// appears under both agents and claude, deduped to one underlying skill.
	if len(claude.Sources) != 2 {
		t.Errorf("from-claude sources = %v, want agents+claude", claude.Sources)
	}
	if claude.Conflict {
		t.Errorf("union-linked skill must not be a conflict")
	}

	codex := find(got, "from-codex")
	if codex == nil || codex.Sources[0] != SourceCodex {
		t.Fatalf("from-codex sources = %+v, want [codex]", codex)
	}
	if codex.Description != "codex skill" {
		t.Errorf("description = %q", codex.Description)
	}
}

func TestConflictDetection(t *testing.T) {
	r := rootsIn(t)
	writeSkill(t, r.Native[SourceClaude], "dup", "claude variant", "claude body")
	writeSkill(t, r.Native[SourceCodex], "dup", "codex variant", "codex body")

	got, err := ScanRoots(r)
	if err != nil {
		t.Fatal(err)
	}
	dup := find(got, "dup")
	if dup == nil {
		t.Fatal("dup missing")
	}
	if !dup.Conflict {
		t.Errorf("expected conflict for differing same-named skills")
	}
	if len(dup.Contents) != 2 {
		t.Errorf("conflict should expose per-source bodies, got %d", len(dup.Contents))
	}
}

func TestEnsureClaudeWorkspaceLink(t *testing.T) {
	r := rootsIn(t)
	ws := t.TempDir()
	if err := EnsureClaudeWorkspaceLink(ws); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, ".claude", "skills")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("workspace .claude/skills not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".claude/skills is not a symlink")
	}
	target, err := os.Readlink(link)
	if err != nil || target != r.Agents {
		t.Fatalf("link target = %q, want union %q", target, r.Agents)
	}
	// Idempotent: a second call must not error or clobber.
	if err := EnsureClaudeWorkspaceLink(ws); err != nil {
		t.Fatalf("second call errored: %v", err)
	}
}

func TestRelinkIdempotentAndNoClobber(t *testing.T) {
	r := rootsIn(t)
	writeSkill(t, r.Native[SourceClaude], "linkme", "to link", "")
	// A genuinely shared real folder that must never be clobbered.
	writeSkill(t, r.Agents, "real-shared", "real", "original")

	if _, err := RelinkRoots(r); err != nil {
		t.Fatal(err)
	}
	// Second run must be a no-op (idempotent).
	rep, err := RelinkRoots(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range rep.Actions {
		if a.Status == "linked" {
			t.Errorf("second relink re-created %s (not idempotent)", a.Name)
		}
	}
	// The union link for linkme must resolve to the real claude folder.
	target, err := filepath.EvalSymlinks(filepath.Join(r.Agents, "linkme"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(r.Native[SourceClaude], "linkme"))
	if target != want {
		t.Errorf("linkme resolves to %q, want %q", target, want)
	}
	// real-shared must still be a real directory, untouched.
	info, err := os.Lstat(filepath.Join(r.Agents, "real-shared"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("real-shared was replaced by a symlink (clobbered)")
	}
}
