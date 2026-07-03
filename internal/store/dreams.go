package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateDream inserts a dream record in the running state. If ID is empty a UUID
// is assigned. Counts, note, and new items are filled in by FinishDream once the
// consolidation completes.
func (s *Store) CreateDream(ctx context.Context, d Dream) (Dream, error) {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Status == "" {
		d.Status = DreamRunning
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO dreams
		(id, agent_name, trigger, status, session_count)
		VALUES (?, ?, ?, ?, ?)`,
		d.ID, d.AgentName, d.Trigger, d.Status, d.SessionCount,
	)
	if err != nil {
		return Dream{}, fmt.Errorf("create dream %q: %w", d.ID, err)
	}
	return s.GetDream(ctx, d.ID)
}

// FinishDream records the terminal status, error, distillation counts, journal
// note, and new items for a dream.
func (s *Store) FinishDream(ctx context.Context, id string, status DreamStatus, dreamErr, note string, kept, merged, pruned int, newItems []DreamNewItem) (Dream, error) {
	if newItems == nil {
		newItems = []DreamNewItem{}
	}
	itemsJSON, err := json.Marshal(newItems)
	if err != nil {
		return Dream{}, fmt.Errorf("encode dream %q new items: %w", id, err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE dreams
		SET status = ?, error = ?, note = ?, kept = ?, merged = ?, pruned = ?,
			new_items_json = ?, finished_at = datetime('now')
		WHERE id = ?`,
		status, dreamErr, note, kept, merged, pruned, string(itemsJSON), id,
	)
	if err != nil {
		return Dream{}, fmt.Errorf("finish dream %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Dream{}, fmt.Errorf("finish dream %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Dream{}, fmt.Errorf("dream %q: %w", id, ErrNotFound)
	}
	return s.GetDream(ctx, id)
}

// GetDream fetches a single dream by ID.
func (s *Store) GetDream(ctx context.Context, id string) (Dream, error) {
	row := s.db.QueryRowContext(ctx, dreamSelect+` WHERE id = ?`, id)
	d, err := scanDream(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Dream{}, fmt.Errorf("dream %q: %w", id, ErrNotFound)
		}
		return Dream{}, err
	}
	return d, nil
}

// ListDreams returns the most recent dreams for an agent, newest first. A limit
// <= 0 returns all dreams. This is the "dream journal" the UI renders.
func (s *Store) ListDreams(ctx context.Context, agentName string, limit int) ([]Dream, error) {
	query := dreamSelect + ` WHERE agent_name = ? ORDER BY ran_at DESC, id DESC`
	args := []any{agentName}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list dreams for agent %q: %w", agentName, err)
	}
	defer rows.Close()

	var dreams []Dream
	for rows.Next() {
		d, err := scanDream(rows)
		if err != nil {
			return nil, err
		}
		dreams = append(dreams, d)
	}
	return dreams, rows.Err()
}

// LastSuccessfulDream returns the most recent successful dream for an agent. It
// returns ErrNotFound when the agent has never dreamed successfully — used both
// to show "last dreamed" and to decide whether a nightly dream is due.
func (s *Store) LastSuccessfulDream(ctx context.Context, agentName string) (Dream, error) {
	row := s.db.QueryRowContext(ctx,
		dreamSelect+` WHERE agent_name = ? AND status = 'success' ORDER BY ran_at DESC, id DESC LIMIT 1`,
		agentName)
	d, err := scanDream(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Dream{}, fmt.Errorf("last dream for agent %q: %w", agentName, ErrNotFound)
		}
		return Dream{}, err
	}
	return d, nil
}

// undreamedPredicate matches sessions that are eligible to be dreamed: not yet
// consolidated and carrying a real exchange (at least one user message and one
// assistant message). A created-but-unused session, or a failed turn with no
// reply, is not dreamable material.
const undreamedPredicate = `dreamed_at IS NULL
	AND EXISTS (SELECT 1 FROM messages m WHERE m.session_id = sessions.id AND m.role = 'user')
	AND EXISTS (SELECT 1 FROM messages m WHERE m.session_id = sessions.id AND m.role = 'assistant')`

// ListUndreamedSessions returns an agent's dreamable sessions, oldest first, so a
// dream consolidates them in the order they happened.
func (s *Store) ListUndreamedSessions(ctx context.Context, agentName string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin,
		COALESCE(schedule_id, ''), COALESCE(run_id, ''), COALESCE(task_id, ''), project_id, rolling_summary, provider_handle,
		plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
		COALESCE(dreamed_at, ''), created_at, updated_at
		FROM sessions WHERE agent_name = ? AND `+undreamedPredicate+` ORDER BY created_at, id`, agentName)
	if err != nil {
		return nil, fmt.Errorf("list undreamed sessions for agent %q: %w", agentName, err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// CountUndreamedSessions returns how many dreamable sessions an agent has waiting.
func (s *Store) CountUndreamedSessions(ctx context.Context, agentName string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE agent_name = ? AND `+undreamedPredicate,
		agentName).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count undreamed sessions for agent %q: %w", agentName, err)
	}
	return n, nil
}

// MarkSessionsDreamed stamps every given session as consolidated, in one
// transaction, using a single timestamp so a dream's sessions share a marker.
func (s *Store) MarkSessionsDreamed(ctx context.Context, ids []string, at string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark sessions dreamed: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE sessions SET dreamed_at = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare mark dreamed: %w", err)
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, at, id); err != nil {
			return fmt.Errorf("mark session %q dreamed: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark sessions dreamed: %w", err)
	}
	return nil
}

const dreamSelect = `SELECT id, agent_name, ran_at, COALESCE(finished_at, ''), trigger, status, error,
	session_count, kept, merged, pruned, note, new_items_json FROM dreams`

func scanDream(row scanner) (Dream, error) {
	var (
		d         Dream
		itemsJSON string
	)
	if err := row.Scan(
		&d.ID,
		&d.AgentName,
		&d.RanAt,
		&d.FinishedAt,
		&d.Trigger,
		&d.Status,
		&d.Error,
		&d.SessionCount,
		&d.Kept,
		&d.Merged,
		&d.Pruned,
		&d.Note,
		&itemsJSON,
	); err != nil {
		return Dream{}, err
	}
	if itemsJSON != "" {
		if err := json.Unmarshal([]byte(itemsJSON), &d.NewItems); err != nil {
			return Dream{}, fmt.Errorf("decode dream %q new items: %w", d.ID, err)
		}
	}
	return d, nil
}
