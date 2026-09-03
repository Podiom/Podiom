package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateGoalRun starts one exact goal-linked turn. The partial unique index on
// running session IDs prevents two reviews from sharing a conversation at once.
func (s *Store) CreateGoalRun(ctx context.Context, run GoalRun) (GoalRun, error) {
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	if run.Status == "" {
		run.Status = GoalRunRunning
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO goal_runs
		(id, goal_id, session_id, turn_message_id, kind, agent_name, source_id, status, legacy, error)
		VALUES (?, ?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?)`,
		run.ID, run.GoalID, run.SessionID, run.TurnMessageID, run.Kind, run.AgentName,
		run.SourceID, run.Status, boolInt(run.Legacy), run.Error)
	if err != nil {
		return GoalRun{}, fmt.Errorf("create goal run for %q: %w", run.GoalID, err)
	}
	return s.GetGoalRun(ctx, run.ID)
}

func (s *Store) GetGoalRun(ctx context.Context, id string) (GoalRun, error) {
	run, err := scanGoalRun(s.db.QueryRowContext(ctx, goalRunSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return GoalRun{}, fmt.Errorf("goal run %q: %w", id, ErrNotFound)
	}
	return run, err
}

func (s *Store) GetRunningGoalRunBySession(ctx context.Context, sessionID string) (GoalRun, error) {
	run, err := scanGoalRun(s.db.QueryRowContext(ctx, goalRunSelect+` WHERE session_id = ? AND status = ? ORDER BY started_at DESC LIMIT 1`, sessionID, GoalRunRunning))
	if errors.Is(err, sql.ErrNoRows) {
		return GoalRun{}, fmt.Errorf("running goal run for session %q: %w", sessionID, ErrNotFound)
	}
	return run, err
}

func (s *Store) GetRunningGoalRunByGoal(ctx context.Context, goalID string) (GoalRun, error) {
	run, err := scanGoalRun(s.db.QueryRowContext(ctx, goalRunSelect+` WHERE goal_id = ? AND status = ? ORDER BY started_at DESC LIMIT 1`, goalID, GoalRunRunning))
	if errors.Is(err, sql.ErrNoRows) {
		return GoalRun{}, fmt.Errorf("running goal run for goal %q: %w", goalID, ErrNotFound)
	}
	return run, err
}

func (s *Store) ListGoalRuns(ctx context.Context, goalID string, limit int) ([]GoalRun, error) {
	query := goalRunSelect + ` WHERE goal_id = ? ORDER BY started_at DESC, id DESC`
	args := []any{goalID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goal runs for %q: %w", goalID, err)
	}
	defer rows.Close()
	var out []GoalRun
	for rows.Next() {
		run, err := scanGoalRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) SetGoalRunTurn(ctx context.Context, id string, messageID int64) (GoalRun, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_runs SET turn_message_id = ? WHERE id = ? AND status = ?`, messageID, id, GoalRunRunning)
	if err != nil {
		return GoalRun{}, fmt.Errorf("set goal run %q turn: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return GoalRun{}, fmt.Errorf("goal run %q: %w", id, ErrNotFound)
	}
	return s.GetGoalRun(ctx, id)
}

func (s *Store) FinishGoalRun(ctx context.Context, id string, status GoalRunStatus, runErr string) (GoalRun, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_runs
		SET status = ?, error = ?, finished_at = datetime('now')
		WHERE id = ? AND status = ?`, status, runErr, id, GoalRunRunning)
	if err != nil {
		return GoalRun{}, fmt.Errorf("finish goal run %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return GoalRun{}, fmt.Errorf("goal run %q: %w", id, ErrNotFound)
	}
	return s.GetGoalRun(ctx, id)
}

// SetGoalRunSummary stores the tokens billed by this exact turn and its short
// user-facing outcome. It is valid after the lifecycle row has finished.
func (s *Store) SetGoalRunSummary(ctx context.Context, id string, usage SessionUsage, outcome string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_runs SET input_tokens = ?, output_tokens = ?,
		cache_read_tokens = ?, cache_write_tokens = ?, outcome = ? WHERE id = ?`,
		usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, outcome, id)
	if err != nil {
		return fmt.Errorf("set goal run %q summary: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("goal run %q: %w", id, ErrNotFound)
	}
	return nil
}

// InterruptRunningGoalRuns releases durable run locks left by a daemon crash.
func (s *Store) InterruptRunningGoalRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE goal_runs
		SET status = ?, error = CASE WHEN error = '' THEN 'Daemon restarted before the run finished.' ELSE error END,
			finished_at = datetime('now')
		WHERE status = ?`, GoalRunInterrupted, GoalRunRunning)
	if err != nil {
		return fmt.Errorf("interrupt running goal runs: %w", err)
	}
	return nil
}

func (s *Store) ListGoalEventsByRun(ctx context.Context, goalID, runID string) ([]GoalEvent, error) {
	rows, err := s.db.QueryContext(ctx, goalEventSelect+` WHERE goal_id = ? AND run_id = ? ORDER BY id`, goalID, runID)
	if err != nil {
		return nil, fmt.Errorf("list events for goal run %q: %w", runID, err)
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

// ListMessagesForGoalRun returns only the canonical messages belonging to the
// run's user turn. Legacy ambiguous runs intentionally return full history.
func (s *Store) ListMessagesForGoalRun(ctx context.Context, run GoalRun) ([]Message, error) {
	if run.TurnMessageID == 0 {
		return s.ListMessages(ctx, run.SessionID)
	}
	var startSeq int
	if err := s.db.QueryRowContext(ctx, `SELECT seq FROM messages WHERE id = ? AND session_id = ?`, run.TurnMessageID, run.SessionID).Scan(&startSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("goal run %q transcript: %w", run.ID, ErrNotFound)
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, seq, role, kind, content, created_at
		FROM messages
		WHERE session_id = ? AND seq >= ?
			AND seq < COALESCE((SELECT MIN(seq) FROM messages WHERE session_id = ? AND role = ? AND seq > ?), 2147483647)
		ORDER BY seq`, run.SessionID, startSeq, run.SessionID, RoleUser, startSeq)
	if err != nil {
		return nil, fmt.Errorf("list goal run %q transcript: %w", run.ID, err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Seq, &msg.Role, &msg.Kind, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

const goalRunSelect = `SELECT id, goal_id, session_id, COALESCE(turn_message_id, 0), kind,
	agent_name, source_id, status, legacy, error, started_at, COALESCE(finished_at, ''),
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, outcome FROM goal_runs`

func scanGoalRun(row scanner) (GoalRun, error) {
	var run GoalRun
	var legacy int
	if err := row.Scan(&run.ID, &run.GoalID, &run.SessionID, &run.TurnMessageID, &run.Kind,
		&run.AgentName, &run.SourceID, &run.Status, &legacy, &run.Error, &run.StartedAt, &run.FinishedAt,
		&run.InputTokens, &run.OutputTokens, &run.CacheReadTokens, &run.CacheWriteTokens, &run.Outcome); err != nil {
		return GoalRun{}, err
	}
	run.Legacy = legacy != 0
	return run, nil
}
