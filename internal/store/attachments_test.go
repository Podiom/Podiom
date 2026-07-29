package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAttachmentBindingIsAtomicOrderedAndSessionScoped(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatal(err)
	}
	newSession := func() Session {
		session, err := db.CreateSession(ctx, Session{AgentName: "jared", Provider: "claude", PermissionMode: "approve", Origin: OriginWeb})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	first := newSession()
	second := newSession()
	create := func(id, sessionID, name string) Attachment {
		attachment, err := db.CreateAttachment(ctx, Attachment{ID: id, SessionID: sessionID, Name: name, MIMEType: "image/png", SizeBytes: 12, Width: 4, Height: 3})
		if err != nil {
			t.Fatal(err)
		}
		return attachment
	}
	a := create("a", first.ID, "first.png")
	b := create("b", first.ID, "second.png")
	foreign := create("foreign", second.ID, "foreign.png")

	if _, err := db.AppendUserMessage(ctx, first.ID, "must roll back", []string{a.ID, foreign.ID}); err == nil {
		t.Fatal("cross-session binding unexpectedly succeeded")
	}
	if history, err := db.ListMessages(ctx, first.ID); err != nil || len(history) != 0 {
		t.Fatalf("failed binding was not atomic: history=%+v err=%v", history, err)
	}

	inserted, err := db.AppendUserMessage(ctx, first.ID, "look", []string{b.ID, a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(inserted) != 1 || len(inserted[0].Attachments) != 2 {
		t.Fatalf("inserted message attachments = %+v", inserted)
	}
	history, err := db.ListMessages(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || len(history[0].Attachments) != 2 || history[0].Attachments[0].Name != "second.png" || history[0].Attachments[1].Name != "first.png" {
		t.Fatalf("ordered attachment history = %+v", history)
	}
	if _, err := db.AppendUserMessage(ctx, first.ID, "reuse", []string{a.ID}); err == nil {
		t.Fatal("bound attachment reuse unexpectedly succeeded")
	}
}

func TestAttachmentsCascadeWithSession(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(ctx, Session{AgentName: "jared", Provider: "claude", PermissionMode: "approve", Origin: OriginWeb})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttachment(ctx, Attachment{ID: "photo", SessionID: session.ID, Name: "photo.jpg", MIMEType: "image/jpeg", SizeBytes: 10, Width: 2, Height: 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if attachments, err := db.ListAttachments(ctx); err != nil || len(attachments) != 0 {
		t.Fatalf("attachments after session delete = %+v, err=%v", attachments, err)
	}
}
