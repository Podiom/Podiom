package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestWorkspaceFileSnapshotPersistsProvenanceAfterSessionDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "writer", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := db.CreateSession(ctx, Session{AgentName: "writer", Provider: "claude", PermissionMode: "approve", Origin: OriginWeb, ProjectID: "launch"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	want := WorkspaceFileSnapshot{
		ID:               "4ce94738-86da-4bfa-9fa2-bf0dc0b212e1",
		CreatorSessionID: session.ID,
		CreatorAgent:     "writer",
		ProjectID:        "launch",
		SourcePath:       "copy/reddit.md",
		Filename:         "reddit.md",
		Label:            "Reddit post",
		Content:          "Exact text.\n",
		SizeBytes:        12,
	}
	created, err := db.CreateWorkspaceFileSnapshot(ctx, want)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if created.CreatedAt == "" {
		t.Fatal("created snapshot has no timestamp")
	}
	if created.Content != want.Content || created.CreatorSessionID != session.ID || created.CreatorAgent != "writer" || created.ProjectID != "launch" {
		t.Fatalf("created snapshot = %+v", created)
	}

	if err := db.DeleteSession(ctx, session.ID); err != nil {
		t.Fatalf("delete creator session: %v", err)
	}
	got, err := db.GetWorkspaceFileSnapshot(ctx, want.ID)
	if err != nil {
		t.Fatalf("get snapshot after session deletion: %v", err)
	}
	if got.Content != want.Content || got.CreatorSessionID != session.ID || got.SourcePath != want.SourcePath {
		t.Fatalf("snapshot changed after session deletion: %+v", got)
	}
}
