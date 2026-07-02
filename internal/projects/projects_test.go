package projects

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLedgerCreateAndListRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)

	if list, err := l.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty ledger: list=%v err=%v", list, err)
	}

	created, err := l.Create(Project{ID: "mission-control", Name: "Mission Control", Stack: []string{"Next.js"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Path != "mission-control" || created.Status != "active" {
		t.Fatalf("defaults not applied: %+v", created)
	}
	// Project directory is scaffolded.
	if _, err := os.Stat(filepath.Join(dir, "mission-control")); err != nil {
		t.Fatalf("project dir not created: %v", err)
	}
	// projects.yaml is written and reloads.
	reloaded := New(dir)
	list, err := reloaded.List()
	if err != nil || len(list) != 1 || list[0].ID != "mission-control" {
		t.Fatalf("reload failed: list=%+v err=%v", list, err)
	}
}

func TestLedgerRejectsDuplicateAndBadID(t *testing.T) {
	l := New(t.TempDir())
	if _, err := l.Create(Project{ID: "ok"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := l.Create(Project{ID: "ok"}); err == nil {
		t.Fatal("expected duplicate id error")
	}
	if _, err := l.Create(Project{ID: "../escape"}); err == nil {
		t.Fatal("expected invalid id error")
	}
}

func TestLedgerReadsLegacyRepoValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`projects:
  - id: empty
    name: Empty
    path: empty
    status: active
    stack: []
    repo: ""
    roadmap: []
    notes: ""
  - id: legacy
    name: Legacy
    path: legacy
    status: active
    stack: []
    repo: https://github.com/Podiom/Podiom.git
    roadmap: []
    notes: ""
  - id: object
    name: Object
    path: object
    status: active
    stack: []
    repo:
      provider: github
      mode: snapshot
      owner: openai
      name: codex
      full_name: openai/codex
      html_url: https://github.com/openai/codex
      default_branch: main
      ref: main
      source_kind: archive
    roadmap: []
    notes: ""
`)
	if err := os.WriteFile(filepath.Join(dir, "projects.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := New(dir).List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list[0].Repo != nil {
		t.Fatalf("empty repo should decode as nil: %+v", list[0].Repo)
	}
	if list[1].Repo == nil || list[1].Repo.FullName != "Podiom/Podiom" || list[1].Repo.SourceKind != "archive" {
		t.Fatalf("legacy repo not normalized: %+v", list[1].Repo)
	}
	if list[2].Repo == nil || list[2].Repo.FullName != "openai/codex" {
		t.Fatalf("object repo not decoded: %+v", list[2].Repo)
	}
}
