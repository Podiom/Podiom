package store

import (
	"context"
	"database/sql"
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

func TestUpdateSessionContextRoundTrips(t *testing.T) {
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
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// New sessions start un-observed (0/0) so the ring stays hidden until a turn.
	if created.ContextTokens != 0 || created.ContextLimit != 0 {
		t.Fatalf("new session context = %d/%d, want 0/0", created.ContextTokens, created.ContextLimit)
	}
	if err := db.UpdateSessionContext(ctx, created.ID, 81000, 200000); err != nil {
		t.Fatalf("update context: %v", err)
	}
	got, err := db.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ContextTokens != 81000 || got.ContextLimit != 200000 {
		t.Fatalf("stored context = %d/%d, want 81000/200000", got.ContextTokens, got.ContextLimit)
	}
}

func TestAppendMessagesStoresMessageKind(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var column string
	if err := db.db.QueryRowContext(ctx, `SELECT name FROM pragma_table_info('messages') WHERE name = 'kind'`).Scan(&column); err != nil {
		if err == sql.ErrNoRows {
			t.Fatal("messages.kind column was not migrated")
		}
		t.Fatalf("inspect messages.kind: %v", err)
	}

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	inserted, err := db.AppendMessages(ctx, created.ID, []Message{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Kind: KindError, Content: "boom"},
	})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}
	if inserted[0].Kind != KindMessage {
		t.Fatalf("default kind = %q, want %q", inserted[0].Kind, KindMessage)
	}
	if inserted[1].Kind != KindError {
		t.Fatalf("error kind = %q, want %q", inserted[1].Kind, KindError)
	}
	history, err := db.ListMessages(ctx, created.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(history) != 2 || history[0].Kind != KindMessage || history[1].Kind != KindError {
		t.Fatalf("unexpected message kinds: %+v", history)
	}
}
