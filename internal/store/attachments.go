package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateAttachment records a validated photo whose bytes have already been
// written to Podiom's attachment store. New attachments are drafts until a user
// message binds them.
func (s *Store) CreateAttachment(ctx context.Context, attachment Attachment) (Attachment, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO attachments
		(id, session_id, message_id, name, mime_type, size_bytes, width, height)
		VALUES (?, ?, NULL, ?, ?, ?, ?, ?)`,
		attachment.ID, attachment.SessionID, attachment.Name, attachment.MIMEType,
		attachment.SizeBytes, attachment.Width, attachment.Height)
	if err != nil {
		return Attachment{}, fmt.Errorf("create attachment %q: %w", attachment.ID, err)
	}
	return s.GetAttachment(ctx, attachment.ID)
}

func (s *Store) GetAttachment(ctx context.Context, id string) (Attachment, error) {
	return scanAttachment(s.db.QueryRowContext(ctx, attachmentSelect+` WHERE id = ?`, id))
}

func (s *Store) ListAttachmentsForSession(ctx context.Context, sessionID string) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, attachmentSelect+` WHERE session_id = ? ORDER BY COALESCE(message_id, 0), position, created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list attachments for session %q: %w", sessionID, err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, attachment)
	}
	return out, rows.Err()
}

func (s *Store) ListAttachments(ctx context.Context) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, attachmentSelect+` ORDER BY session_id, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, attachment)
	}
	return out, rows.Err()
}

// DeleteDraftAttachment removes metadata only when the attachment has not been
// bound to a message. It returns the deleted row so core can remove its files.
func (s *Store) DeleteDraftAttachment(ctx context.Context, id string) (Attachment, error) {
	attachment, err := s.GetAttachment(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	if attachment.MessageID != 0 {
		return Attachment{}, fmt.Errorf("attachment %q is already bound to a message", id)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ? AND message_id IS NULL`, id)
	if err != nil {
		return Attachment{}, fmt.Errorf("delete draft attachment %q: %w", id, err)
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		return Attachment{}, fmt.Errorf("attachment %q is no longer a draft", id)
	}
	return attachment, nil
}

// ListDraftAttachmentsBefore supports filesystem garbage collection.
func (s *Store) ListDraftAttachmentsBefore(ctx context.Context, cutoff time.Time) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, attachmentSelect+`
		WHERE message_id IS NULL AND created_at < ? ORDER BY created_at`, cutoff.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("list stale draft attachments: %w", err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, attachment)
	}
	return out, rows.Err()
}

const attachmentSelect = `SELECT id, session_id, COALESCE(message_id, 0), position, name,
		mime_type, size_bytes, width, height, created_at FROM attachments`

type attachmentScanner interface {
	Scan(dest ...any) error
}

func scanAttachment(row attachmentScanner) (Attachment, error) {
	var attachment Attachment
	if err := row.Scan(&attachment.ID, &attachment.SessionID, &attachment.MessageID, &attachment.Position,
		&attachment.Name, &attachment.MIMEType, &attachment.SizeBytes,
		&attachment.Width, &attachment.Height, &attachment.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Attachment{}, fmt.Errorf("attachment: %w", ErrNotFound)
		}
		return Attachment{}, fmt.Errorf("scan attachment: %w", err)
	}
	return attachment, nil
}
