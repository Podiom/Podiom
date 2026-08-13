package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestSnapshotWorkspaceFileUsesAgentRootAndKeepsImmutableContent(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "writer", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	path := filepath.Join(c.AgentPaths("writer").Workspace, "copy", "reddit.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "# Launch\n\nHere is the post.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := c.SnapshotWorkspaceFile(ctx, session.ID, "copy/reddit.md", "  Reddit   post  ")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if result.Snapshot.Content != original || result.Snapshot.SourcePath != "copy/reddit.md" || result.Snapshot.Label != "Reddit post" {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	wantLink := "[Reddit post](api/workspace-files/" + result.Snapshot.ID + ")"
	if result.MarkdownLink != wantLink {
		t.Fatalf("markdown link = %q, want %q", result.MarkdownLink, wantLink)
	}
	second, err := c.SnapshotWorkspaceFile(ctx, session.ID, "copy/reddit.md", "Reddit post")
	if err != nil {
		t.Fatalf("snapshot same source again: %v", err)
	}
	if second.Snapshot.ID == result.Snapshot.ID {
		t.Fatal("snapshots of the same source were unexpectedly deduplicated")
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetWorkspaceFileSnapshot(ctx, result.Snapshot.ID)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if got.Content != original {
		t.Fatalf("snapshot content changed with source: %q", got.Content)
	}
}

func TestSnapshotWorkspaceFileUsesRepositoryBackedProjectRoot(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	project, err := c.CreateProject(ctx, projects.Project{
		ID:   "launch",
		Name: "Launch",
		Repo: &projects.Repo{Provider: "github", Owner: "acme", Name: "launch"},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	root := c.projectCodeDir(project)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "brief.txt"), []byte("project copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "writer", Origin: store.OriginWeb, ProjectID: project.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	result, err := c.SnapshotWorkspaceFile(ctx, session.ID, "docs/brief.txt", "")
	if err != nil {
		t.Fatalf("snapshot project file: %v", err)
	}
	if result.Snapshot.ProjectID != project.ID || result.Snapshot.Content != "project copy" || result.Snapshot.Filename != "brief.txt" {
		t.Fatalf("project snapshot = %+v", result.Snapshot)
	}
}

func TestSnapshotWorkspaceFileRejectsUnsafeOrNonTextFiles(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "writer", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}
	root := c.AgentPaths("writer").Workspace
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "invalid.txt"), []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nul.txt"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", maxWorkspaceFileSnapshotBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"absolute", outside},
		{"traversal", "../outside.txt"},
		{"missing", "missing.txt"},
		{"escaping symlink", "escape.txt"},
		{"directory", "folder"},
		{"invalid utf8", "invalid.txt"},
		{"nul", "nul.txt"},
		{"oversized", "large.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.SnapshotWorkspaceFile(ctx, session.ID, tc.path, ""); err == nil {
				t.Fatalf("snapshot %q unexpectedly succeeded", tc.path)
			}
		})
	}
}
