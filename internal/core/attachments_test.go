package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

func TestAttachmentCleanupRemovesExpiredDraftsAndFilesystemOrphans(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "viewer", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "viewer", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := c.CreateAttachment(ctx, CreateAttachmentInput{
		SessionID: session.ID,
		Name:      "draft.png",
		MIMEType:  "image/png",
		Original:  []byte("original"),
		Visual:    []byte("visual"),
		Width:     2,
		Height:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.DB().ExecContext(ctx, `UPDATE attachments SET created_at = '2000-01-01 00:00:00' WHERE id = ?`, draft.ID); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(c.SessionAttachmentsDir(session.ID), "orphan")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.CleanupAttachments(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.GetAttachment(ctx, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired draft remains: %v", err)
	}
	for _, dir := range []string{c.attachmentDir(session.ID, draft.ID), orphan} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("cleanup left %s: %v", dir, err)
		}
	}
}

func TestAttachmentExtension(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     string
	}{
		{name: "jpeg", mimeType: "image/jpeg", want: ".jpg"},
		{name: "png", mimeType: "image/png", want: ".png"},
		{name: "gif", mimeType: "image/gif", want: ".gif"},
		{name: "webp", mimeType: "image/webp", want: ".webp"},
		{name: "case must match exactly", mimeType: "image/JPEG", want: ".bin"},
		{name: "parameters are not stripped", mimeType: "image/jpeg; charset=utf-8", want: ".bin"},
		{name: "empty", mimeType: "", want: ".bin"},
		{name: "unknown type", mimeType: "application/pdf", want: ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attachmentExtension(tt.mimeType); got != tt.want {
				t.Fatalf("attachmentExtension(%q) = %q, want %q", tt.mimeType, got, tt.want)
			}
		})
	}
}
