package core

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
)

func TestAnalyzeProjectRepoSendsReadOnlySnapshotContextAndPatchesLedger(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{`{"name":"Gleaming Nest","description":"A cozy Svelte and Go project planner.","stack":["Go","Svelte","TypeScript","SQLite"],"notes":"Podiom coordinates agent work across projects. The repo has Go backend packages and a Svelte frontend. Build with go test and npm run build."}`}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "analyst", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := projects.SnapshotRepo("Podiom", "Podiom", "https://github.com/Podiom/Podiom", "main", "main")
	if _, err := c.CreateProject(ctx, projects.Project{ID: "podiom", Name: "Podiom", Description: "GitHub fallback.", Repo: &repo}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	updated, err := c.AnalyzeProjectRepo(ctx, "podiom", "")
	if err != nil {
		t.Fatalf("analyze project: %v", err)
	}
	if updated.Name != "Gleaming Nest" || updated.Description != "A cozy Svelte and Go project planner." {
		t.Fatalf("unexpected project metadata: %+v", updated)
	}
	if !reflect.DeepEqual(updated.Stack, []string{"Go", "Svelte", "TypeScript", "SQLite"}) {
		t.Fatalf("stack = %#v", updated.Stack)
	}
	if !strings.Contains(updated.Notes, "Go backend") {
		t.Fatalf("notes not patched: %q", updated.Notes)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake requests = %d, want 1", len(fake.Requests))
	}
	req := fake.Requests[0]
	repoRoot := filepath.Join(c.paths.ProjectsDir, "podiom", "repo")
	if !strings.Contains(req.Message, repoRoot) || !strings.Contains(req.Message, "Do not read the whole repository") {
		t.Fatalf("prompt missing analysis constraints:\n%s", req.Message)
	}
	if !reflect.DeepEqual(req.Settings.ExtraWorkspaceDirs, []string{repoRoot}) {
		t.Fatalf("extra workspace dirs = %#v", req.Settings.ExtraWorkspaceDirs)
	}
	if !req.Settings.Unattended {
		t.Fatal("analysis should run unattended")
	}
	if !reflect.DeepEqual(req.Settings.AllowedTools, []string{"Read", "Glob", "Grep"}) {
		t.Fatalf("allowed tools = %#v", req.Settings.AllowedTools)
	}
	if req.Relay == nil {
		t.Fatal("analysis should install an unattended allow-list relay")
	}
}

func TestAnalyzeProjectRepoParsesFencedJSONAndPreservesFallbackDescription(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"```json\n{\"name\":\"Fenced Project Name Extra Words Removed\",\"description\":\"\",\"stack\":[\" Go \",\"\",\"Svelte\"],\"notes\":\"  Useful notes.  \"}\n```"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "analyst", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := projects.SnapshotRepo("acme", "demo", "https://github.com/acme/demo", "main", "main")
	if _, err := c.CreateProject(ctx, projects.Project{ID: "demo", Name: "demo", Description: "GitHub fallback.", Repo: &repo}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	updated, err := c.AnalyzeProjectRepo(ctx, "demo", "analyst")
	if err != nil {
		t.Fatalf("analyze project: %v", err)
	}
	if updated.Name != "Fenced Project Name Extra Words Removed" {
		t.Fatalf("name = %q", updated.Name)
	}
	if updated.Description != "GitHub fallback." {
		t.Fatalf("description = %q", updated.Description)
	}
	if !reflect.DeepEqual(updated.Stack, []string{"Go", "Svelte"}) {
		t.Fatalf("stack = %#v", updated.Stack)
	}
	if updated.Notes != "Useful notes." {
		t.Fatalf("notes = %q", updated.Notes)
	}
}

func TestAnalyzeProjectRepoGarbageLeavesProjectUnchanged(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"not json"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "analyst", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := projects.SnapshotRepo("acme", "demo", "https://github.com/acme/demo", "main", "main")
	before, err := c.CreateProject(ctx, projects.Project{ID: "demo", Name: "demo", Description: "Fallback.", Stack: []string{"Old"}, Notes: "Old notes.", Repo: &repo})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := c.AnalyzeProjectRepo(ctx, "demo", "analyst"); err == nil {
		t.Fatal("expected garbage analysis to fail")
	}
	after, err := c.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("project changed after failed analysis:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestUniqueProjectIDSluggingAndCollision(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if got := c.UniqueProjectID("My Repo!"); got != "my-repo" {
		t.Fatalf("slug = %q, want my-repo", got)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "podiom", Name: "Podiom"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if got := c.UniqueProjectID("Podiom"); got != "podiom-2" {
		t.Fatalf("collision slug = %q, want podiom-2", got)
	}
}
