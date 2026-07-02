package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreateSessionStoresProjectID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
		ProjectID:      "mission-control",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.ProjectID != "mission-control" {
		t.Fatalf("created project id = %q, want mission-control", created.ProjectID)
	}
	got, err := db.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ProjectID != "mission-control" {
		t.Fatalf("stored project id = %q, want mission-control", got.ProjectID)
	}
}
