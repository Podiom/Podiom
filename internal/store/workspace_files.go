package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CreateWorkspaceFileSnapshot persists an immutable text snapshot.
func (s *Store) CreateWorkspaceFileSnapshot(ctx context.Context, snapshot WorkspaceFileSnapshot) (WorkspaceFileSnapshot, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_file_snapshots
		(id, creator_session_id, creator_agent, project_id, source_path, filename, label, content, size_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID, snapshot.CreatorSessionID, snapshot.CreatorAgent, snapshot.ProjectID,
		snapshot.SourcePath, snapshot.Filename, snapshot.Label, snapshot.Content, snapshot.SizeBytes)
	if err != nil {
		return WorkspaceFileSnapshot{}, fmt.Errorf("create workspace file snapshot %q: %w", snapshot.ID, err)
	}
	return s.GetWorkspaceFileSnapshot(ctx, snapshot.ID)
}

// GetWorkspaceFileSnapshot returns one durable snapshot by id.
func (s *Store) GetWorkspaceFileSnapshot(ctx context.Context, id string) (WorkspaceFileSnapshot, error) {
	var snapshot WorkspaceFileSnapshot
	err := s.db.QueryRowContext(ctx, `SELECT id, creator_session_id, creator_agent, project_id,
		source_path, filename, label, content, size_bytes, created_at
		FROM workspace_file_snapshots WHERE id = ?`, id).Scan(
		&snapshot.ID,
		&snapshot.CreatorSessionID,
		&snapshot.CreatorAgent,
		&snapshot.ProjectID,
		&snapshot.SourcePath,
		&snapshot.Filename,
		&snapshot.Label,
		&snapshot.Content,
		&snapshot.SizeBytes,
		&snapshot.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceFileSnapshot{}, fmt.Errorf("workspace file snapshot %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return WorkspaceFileSnapshot{}, fmt.Errorf("get workspace file snapshot %q: %w", id, err)
	}
	return snapshot, nil
}
