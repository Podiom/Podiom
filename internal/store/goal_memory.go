package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type GoalFeedbackDispositionInput struct {
	EventID       int64
	Disposition   GoalFeedbackDisposition
	MemoryItemIDs []string
	SupersededBy  int64
}

func (s *Store) GetGoalMemory(ctx context.Context, goalID string) (GoalMemory, error) {
	return scanGoalMemory(s.db.QueryRowContext(ctx, `SELECT goal_id, status, revision, document_json,
		block_reason, block_detail, last_run_id, outcome, updated_at, COALESCE(repaired_at, '')
		FROM goal_memories WHERE goal_id = ?`, goalID))
}

// GetGoalMemoryForDisplay keeps the repair controls reachable even when the
// stored JSON itself is corrupt. Review execution uses the strict getter.
func (s *Store) GetGoalMemoryForDisplay(ctx context.Context, goalID string) (GoalMemory, error) {
	var memory GoalMemory
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT goal_id, status, revision, document_json,
		block_reason, block_detail, last_run_id, outcome, updated_at, COALESCE(repaired_at, '')
		FROM goal_memories WHERE goal_id = ?`, goalID).Scan(&memory.GoalID, &memory.Status,
		&memory.Revision, &raw, &memory.BlockReason, &memory.BlockDetail, &memory.LastRunID,
		&memory.Outcome, &memory.UpdatedAt, &memory.RepairedAt)
	if err != nil {
		return GoalMemory{}, err
	}
	if err := json.Unmarshal([]byte(raw), &memory.Document); err != nil {
		memory.Status = GoalMemoryBlocked
		memory.BlockReason = "corrupt_memory"
		memory.BlockDetail = err.Error()
		memory.Document = GoalMemoryDocument{}
	}
	return memory, nil
}

func (s *Store) GoalMemoryRevision(ctx context.Context, goalID string) (int64, error) {
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM goal_memories WHERE goal_id = ?`, goalID).Scan(&revision)
	return revision, err
}

func scanGoalMemory(row scanner) (GoalMemory, error) {
	var memory GoalMemory
	var raw string
	if err := row.Scan(&memory.GoalID, &memory.Status, &memory.Revision, &raw,
		&memory.BlockReason, &memory.BlockDetail, &memory.LastRunID, &memory.Outcome,
		&memory.UpdatedAt, &memory.RepairedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalMemory{}, ErrNotFound
		}
		return GoalMemory{}, err
	}
	if err := json.Unmarshal([]byte(raw), &memory.Document); err != nil {
		return GoalMemory{}, fmt.Errorf("decode goal %q memory: %w", memory.GoalID, err)
	}
	return memory, nil
}

func (s *Store) SetGoalMemoryValidating(ctx context.Context, goalID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_memories SET status = 'validating',
		block_reason = '', block_detail = '', updated_at = datetime('now') WHERE goal_id = ?`, goalID)
	if err != nil {
		return fmt.Errorf("validate goal %q memory: %w", goalID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("goal %q memory: %w", goalID, ErrNotFound)
	}
	return nil
}

// CommitGoalMemory atomically publishes a revision and acknowledges the raw
// feedback whose effect that revision now carries.
func (s *Store) CommitGoalMemory(ctx context.Context, goalID string, baseRevision int64, runID, outcome string, document GoalMemoryDocument, dispositions []GoalFeedbackDispositionInput, repaired bool) (GoalMemory, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return GoalMemory{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalMemory{}, err
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM goal_memories WHERE goal_id = ?`, goalID).Scan(&current); err != nil {
		return GoalMemory{}, err
	}
	if current != baseRevision {
		return GoalMemory{}, fmt.Errorf("goal memory changed: base revision %d, current revision %d", baseRevision, current)
	}
	next := current + 1
	for _, disposition := range dispositions {
		var kind GoalEventKind
		var pinned int
		if err := tx.QueryRowContext(ctx, `SELECT kind, pinned FROM goal_events WHERE id = ? AND goal_id = ?`, disposition.EventID, goalID).Scan(&kind, &pinned); err != nil {
			return GoalMemory{}, fmt.Errorf("feedback %d: %w", disposition.EventID, err)
		}
		if kind != GoalEventUserFeedback || pinned != 0 {
			return GoalMemory{}, fmt.Errorf("event %d is not ordinary goal feedback", disposition.EventID)
		}
		if disposition.Disposition == GoalFeedbackSuperseded {
			var supersedingKind GoalEventKind
			var supersedingPinned int
			if err := tx.QueryRowContext(ctx, `SELECT kind, pinned FROM goal_events WHERE id = ? AND goal_id = ?`,
				disposition.SupersededBy, goalID).Scan(&supersedingKind, &supersedingPinned); err != nil {
				return GoalMemory{}, fmt.Errorf("superseding feedback %d: %w", disposition.SupersededBy, err)
			}
			if supersedingKind != GoalEventUserFeedback || supersedingPinned != 0 {
				return GoalMemory{}, fmt.Errorf("event %d is not newer ordinary feedback", disposition.SupersededBy)
			}
		}
		ids, _ := json.Marshal(disposition.MemoryItemIDs)
		if _, err := tx.ExecContext(ctx, `INSERT INTO goal_feedback_receipts
			(goal_id, event_id, disposition, memory_item_ids_json, superseded_by, revision)
			VALUES (?, ?, ?, ?, NULLIF(?, 0), ?)
			ON CONFLICT(goal_id, event_id) DO UPDATE SET disposition = excluded.disposition,
			memory_item_ids_json = excluded.memory_item_ids_json, superseded_by = excluded.superseded_by,
			revision = excluded.revision, acknowledged_at = datetime('now')`, goalID, disposition.EventID,
			disposition.Disposition, string(ids), disposition.SupersededBy, next); err != nil {
			return GoalMemory{}, err
		}
	}
	query := `UPDATE goal_memories SET status = 'ready', revision = ?, document_json = ?,
		block_reason = '', block_detail = '', last_run_id = ?, outcome = ?, updated_at = datetime('now'),
		repaired_at = CASE WHEN ? THEN datetime('now') ELSE repaired_at END WHERE goal_id = ? AND revision = ?`
	res, err := tx.ExecContext(ctx, query, next, string(raw), runID, outcome, repaired, goalID, current)
	if err != nil {
		return GoalMemory{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return GoalMemory{}, fmt.Errorf("goal memory changed during commit")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO goal_memory_revisions
		(goal_id, revision, run_id, document_json, outcome) VALUES (?, ?, ?, ?, ?)`,
		goalID, next, runID, string(raw), outcome); err != nil {
		return GoalMemory{}, err
	}
	if err := tx.Commit(); err != nil {
		return GoalMemory{}, err
	}
	return s.GetGoalMemory(ctx, goalID)
}

func (s *Store) BlockGoalMemory(ctx context.Context, goalID, reason, detail string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE goal_memories SET status = 'blocked', block_reason = ?,
		block_detail = ?, updated_at = datetime('now') WHERE goal_id = ?`, reason, detail, goalID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE goals SET status = 'paused', next_review_at = NULL,
		updated_at = datetime('now') WHERE id = ?`, goalID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListPendingGoalFeedback(ctx context.Context, goalID string) ([]GoalEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT goal_events.id, goal_events.goal_id,
		COALESCE(goal_events.session_id, ''), COALESCE(goal_events.run_id, ''), goal_events.kind,
		goal_events.body, goal_events.payload_json, goal_events.created_at, goal_events.pinned
		FROM goal_events LEFT JOIN goal_feedback_receipts r
		ON r.goal_id = goal_events.goal_id AND r.event_id = goal_events.id
		WHERE goal_events.goal_id = ? AND goal_events.kind = ? AND goal_events.pinned = 0
		AND r.event_id IS NULL ORDER BY goal_events.id`, goalID, GoalEventUserFeedback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GoalEvent
	for rows.Next() {
		ev, err := scanGoalEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) ListGoalFeedbackReceipts(ctx context.Context, goalID string) ([]GoalFeedbackReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, disposition, memory_item_ids_json,
		COALESCE(superseded_by, 0), revision, acknowledged_at FROM goal_feedback_receipts
		WHERE goal_id = ? ORDER BY event_id`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GoalFeedbackReceipt
	for rows.Next() {
		var receipt GoalFeedbackReceipt
		var raw string
		if err := rows.Scan(&receipt.EventID, &receipt.Disposition, &raw, &receipt.SupersededBy,
			&receipt.Revision, &receipt.AcknowledgedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &receipt.MemoryItemIDs)
		out = append(out, receipt)
	}
	return out, rows.Err()
}
