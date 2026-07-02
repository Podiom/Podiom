package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDeleteSessionCascadesMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := db.CreateSession(ctx, Session{AgentName: "jared", Provider: "claude", PermissionMode: "approve", Origin: OriginCLI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.AppendMessages(ctx, sess.ID, []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
	}); err != nil {
		t.Fatalf("append messages: %v", err)
	}

	if err := db.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := db.GetSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
	msgs, err := db.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages should be cascade-deleted, got %+v", msgs)
	}

	if err := db.DeleteSession(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: err = %v, want ErrNotFound", err)
	}
}
