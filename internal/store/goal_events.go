package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AppendGoalEvent adds one entry to a goal's timeline. Rows are immutable except
// for unread user feedback body edits; rows leave only through the goal's
// ON DELETE CASCADE.
func (s *Store) AppendGoalEvent(ctx context.Context, ev GoalEvent) (GoalEvent, error) {
	if ev.Payload == "" {
		ev.Payload = "{}"
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO goal_events
		(goal_id, session_id, run_id, kind, body, payload_json)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`,
		ev.GoalID, ev.SessionID, ev.RunID, ev.Kind, ev.Body, ev.Payload,
	)
	if err != nil {
		return GoalEvent{}, fmt.Errorf("append goal event for %q: %w", ev.GoalID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return GoalEvent{}, fmt.Errorf("append goal event for %q id: %w", ev.GoalID, err)
	}
	return s.GetGoalEvent(ctx, id)
}

// AppendGoalEventWithMetrics appends a timeline entry and replaces the goal's
// metric values in one transaction, so a metric_update event and the projection
// it drives can never diverge (§2.2: events are the single write path for
// metrics).
func (s *Store) AppendGoalEventWithMetrics(ctx context.Context, ev GoalEvent, metrics []GoalMetric) (GoalEvent, error) {
	if ev.Payload == "" {
		ev.Payload = "{}"
	}
	metricsJSON, err := marshalMetrics(metrics)
	if err != nil {
		return GoalEvent{}, fmt.Errorf("append goal event for %q: %w", ev.GoalID, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalEvent{}, fmt.Errorf("begin goal event for %q: %w", ev.GoalID, err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `INSERT INTO goal_events
		(goal_id, session_id, run_id, kind, body, payload_json)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`,
		ev.GoalID, ev.SessionID, ev.RunID, ev.Kind, ev.Body, ev.Payload,
	)
	if err != nil {
		return GoalEvent{}, fmt.Errorf("append goal event for %q: %w", ev.GoalID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return GoalEvent{}, fmt.Errorf("append goal event for %q id: %w", ev.GoalID, err)
	}

	upd, err := tx.ExecContext(ctx, `UPDATE goals
		SET metrics_json = ?, updated_at = datetime('now')
		WHERE id = ?`, metricsJSON, ev.GoalID)
	if err != nil {
		return GoalEvent{}, fmt.Errorf("apply metrics for goal %q: %w", ev.GoalID, err)
	}
	changed, err := upd.RowsAffected()
	if err != nil {
		return GoalEvent{}, fmt.Errorf("apply metrics for goal %q rows affected: %w", ev.GoalID, err)
	}
	if changed == 0 {
		return GoalEvent{}, fmt.Errorf("goal %q: %w", ev.GoalID, ErrNotFound)
	}

	if err := tx.Commit(); err != nil {
		return GoalEvent{}, fmt.Errorf("commit goal event for %q: %w", ev.GoalID, err)
	}
	return s.GetGoalEvent(ctx, id)
}

// GetGoalEvent fetches a single timeline entry by its sequence ID.
func (s *Store) GetGoalEvent(ctx context.Context, id int64) (GoalEvent, error) {
	row := s.db.QueryRowContext(ctx, goalEventSelect+` WHERE id = ?`, id)
	ev, err := scanGoalEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalEvent{}, fmt.Errorf("goal event %d: %w", id, ErrNotFound)
		}
		return GoalEvent{}, err
	}
	return ev, nil
}

// UpdateUnreadGoalFeedback edits a user feedback event only until a later
// planning/review session has started, which is when feedback has been assembled
// into an agent prompt.
func (s *Store) UpdateUnreadGoalFeedback(ctx context.Context, goalID string, eventID int64, body string) (GoalEvent, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_events
		SET body = ?
		WHERE id = ?
			AND goal_id = ?
			AND kind = ?
			AND NOT EXISTS (
				SELECT 1 FROM goal_events later
				WHERE later.goal_id = goal_events.goal_id
					AND later.id > goal_events.id
					AND later.kind IN (?, ?)
			)`,
		body, eventID, goalID, GoalEventUserFeedback, GoalEventPlanningStarted, GoalEventReviewStarted,
	)
	if err != nil {
		return GoalEvent{}, fmt.Errorf("update goal feedback %d for %q: %w", eventID, goalID, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return GoalEvent{}, fmt.Errorf("update goal feedback %d for %q rows affected: %w", eventID, goalID, err)
	}
	if changed == 0 {
		return GoalEvent{}, fmt.Errorf("goal feedback %d for %q: %w", eventID, goalID, ErrNotFound)
	}
	return s.GetGoalEvent(ctx, eventID)
}

// ListGoalEvents returns a goal's timeline, newest first. A limit <= 0 returns
// all entries. before > 0 returns only entries older than that event ID — the
// pagination cursor for "load more".
func (s *Store) ListGoalEvents(ctx context.Context, goalID string, limit int, before int64) ([]GoalEvent, error) {
	query := goalEventSelect + ` WHERE goal_id = ?`
	args := []any{goalID}
	if before > 0 {
		query += ` AND id < ?`
		args = append(args, before)
	}
	query += ` ORDER BY id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goal events for %q: %w", goalID, err)
	}
	defer rows.Close()

	var events []GoalEvent
	for rows.Next() {
		ev, err := scanGoalEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// ListGoalContextEvents returns a goal's timeline for replay into a review
// prompt, newest first, excluding 'tool_use' entries — a busy goal can emit
// hundreds of tool-call events per run, and they would crowd out the lifecycle
// events (progress, plan_change, access decisions) the review actually needs.
// A limit <= 0 returns all matching entries.
func (s *Store) ListGoalContextEvents(ctx context.Context, goalID string, limit int) ([]GoalEvent, error) {
	query := goalEventSelect + ` WHERE goal_id = ? AND kind != ? ORDER BY id DESC`
	args := []any{goalID, GoalEventToolUse}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goal context events for %q: %w", goalID, err)
	}
	defer rows.Close()

	var events []GoalEvent
	for rows.Next() {
		ev, err := scanGoalEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// ListGoalEventsByKind returns a goal's timeline entries of one kind, newest
// first. It is used for durable user feedback context without depending on how
// noisy the rest of the activity stream is.
func (s *Store) ListGoalEventsByKind(ctx context.Context, goalID string, kind GoalEventKind, limit int) ([]GoalEvent, error) {
	query := goalEventSelect + ` WHERE goal_id = ? AND kind = ? ORDER BY id DESC`
	args := []any{goalID, kind}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goal events for %q kind %q: %w", goalID, kind, err)
	}
	defer rows.Close()

	var events []GoalEvent
	for rows.Next() {
		ev, err := scanGoalEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

const goalEventSelect = `SELECT id, goal_id, COALESCE(session_id, ''), COALESCE(run_id, ''), kind, body, payload_json, created_at FROM goal_events`

func scanGoalEvent(row scanner) (GoalEvent, error) {
	var ev GoalEvent
	if err := row.Scan(
		&ev.ID,
		&ev.GoalID,
		&ev.SessionID,
		&ev.RunID,
		&ev.Kind,
		&ev.Body,
		&ev.Payload,
		&ev.CreatedAt,
	); err != nil {
		return GoalEvent{}, err
	}
	return ev, nil
}
