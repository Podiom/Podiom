package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/store"
	"github.com/google/uuid"
)

const attachmentDraftTTL = 24 * time.Hour

type CreateAttachmentInput struct {
	SessionID string
	Name      string
	MIMEType  string
	Original  []byte
	Visual    []byte
	Width     int
	Height    int
}

// CreateAttachment persists an original photo and its normalized JPEG visual.
// The returned draft must be bound by a later user turn or explicitly deleted.
func (c *Core) CreateAttachment(ctx context.Context, in CreateAttachmentInput) (store.Attachment, error) {
	if _, err := c.store.GetSession(ctx, in.SessionID); err != nil {
		return store.Attachment{}, err
	}
	if len(in.Original) == 0 || len(in.Visual) == 0 {
		return store.Attachment{}, fmt.Errorf("original and normalized image are required")
	}
	name := cleanAttachmentName(in.Name)
	if name == "" {
		name = "photo"
	}
	attachment := store.Attachment{
		ID:        uuid.NewString(),
		SessionID: in.SessionID,
		Name:      name,
		MIMEType:  in.MIMEType,
		SizeBytes: int64(len(in.Original)),
		Width:     in.Width,
		Height:    in.Height,
	}

	c.attachmentMu.Lock()
	defer c.attachmentMu.Unlock()
	dir := c.attachmentDir(attachment.SessionID, attachment.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return store.Attachment{}, fmt.Errorf("create attachment directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := writeFileAtomic(filepath.Join(dir, "original"+attachmentExtension(in.MIMEType)), in.Original, 0o600); err != nil {
		return store.Attachment{}, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "visual.jpg"), in.Visual, 0o600); err != nil {
		return store.Attachment{}, err
	}
	created, err := c.store.CreateAttachment(ctx, attachment)
	if err != nil {
		return store.Attachment{}, err
	}
	cleanup = false
	return created, nil
}

func (c *Core) ReadAttachment(ctx context.Context, id string, normalized bool) (store.Attachment, []byte, error) {
	attachment, err := c.store.GetAttachment(ctx, id)
	if err != nil {
		return store.Attachment{}, nil, err
	}
	path := c.attachmentOriginalPath(attachment)
	if normalized {
		path = c.AttachmentVisualPath(attachment)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return store.Attachment{}, nil, fmt.Errorf("read attachment %q: %w", id, err)
	}
	return attachment, data, nil
}

func (c *Core) DeleteDraftAttachment(ctx context.Context, id string) error {
	c.attachmentMu.Lock()
	defer c.attachmentMu.Unlock()
	attachment, err := c.store.DeleteDraftAttachment(ctx, id)
	if err != nil {
		return err
	}
	return os.RemoveAll(c.attachmentDir(attachment.SessionID, attachment.ID))
}

func (c *Core) AttachmentVisualPath(attachment store.Attachment) string {
	return filepath.Join(c.attachmentDir(attachment.SessionID, attachment.ID), "visual.jpg")
}

func (c *Core) SessionAttachmentsDir(sessionID string) string {
	return filepath.Join(c.paths.AttachmentsDir, sessionID)
}

func (c *Core) attachmentDir(sessionID, attachmentID string) string {
	return filepath.Join(c.SessionAttachmentsDir(sessionID), attachmentID)
}

func (c *Core) attachmentOriginalPath(attachment store.Attachment) string {
	return filepath.Join(c.attachmentDir(attachment.SessionID, attachment.ID), "original"+attachmentExtension(attachment.MIMEType))
}

func attachmentExtension(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

func cleanAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len(name) > 255 {
		name = name[:255]
	}
	return strings.TrimSpace(name)
}

// CleanupAttachments removes stale drafts and filesystem directories without a
// matching database row. It is serialized with upload/delete operations.
func (c *Core) CleanupAttachments(ctx context.Context, now time.Time) error {
	c.attachmentMu.Lock()
	defer c.attachmentMu.Unlock()
	stale, err := c.store.ListDraftAttachmentsBefore(ctx, now.Add(-attachmentDraftTTL))
	if err != nil {
		return err
	}
	for _, attachment := range stale {
		if _, err := c.store.DeleteDraftAttachment(ctx, attachment.ID); err != nil {
			return err
		}
		_ = os.RemoveAll(c.attachmentDir(attachment.SessionID, attachment.ID))
	}
	all, err := c.store.ListAttachments(ctx)
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(all))
	for _, attachment := range all {
		wanted[filepath.Clean(c.attachmentDir(attachment.SessionID, attachment.ID))] = true
	}
	sessions, err := os.ReadDir(c.paths.AttachmentsDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, session := range sessions {
		if !session.IsDir() || strings.HasPrefix(session.Name(), ".") {
			continue
		}
		sessionDir := filepath.Join(c.paths.AttachmentsDir, session.Name())
		entries, _ := os.ReadDir(sessionDir)
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			dir := filepath.Clean(filepath.Join(sessionDir, entry.Name()))
			if !wanted[dir] {
				_ = os.RemoveAll(dir)
			}
		}
		if remaining, _ := os.ReadDir(sessionDir); len(remaining) == 0 {
			_ = os.Remove(sessionDir)
		}
	}
	return nil
}

func (c *Core) attachmentCleanupLoop() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for now := range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := c.CleanupAttachments(ctx, now.UTC()); err != nil {
			c.log.Warn("attachment cleanup failed", "error", err)
		}
		cancel()
	}
}

func (c *Core) copySessionAttachments(sessionID, destination string) error {
	source := c.SessionAttachmentsDir(sessionID)
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return copyAttachmentDir(source, destination)
}

func copyAttachmentDir(source, destination string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create attachment archive directory: %w", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read attachment directory: %w", err)
	}
	for _, entry := range entries {
		src := filepath.Join(source, entry.Name())
		dst := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			if err := copyAttachmentDir(src, dst); err != nil {
				return err
			}
			continue
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
