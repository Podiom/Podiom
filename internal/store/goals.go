package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateGoal inserts a goal. If ID is empty a UUID is assigned and the status
// defaults to active. NextReviewAt is stored as given — computing it from
// ReviewEvery is the caller's (core's) job.
func (s *Store) CreateGoal(ctx context.Context, goal Goal) (Goal, error) {
	if goal.ID == "" {
		goal.ID = uuid.NewString()
	}
	if goal.Status == "" {
		goal.Status = GoalActive
	}
	metrics, err := marshalMetrics(goal.Metrics)
	if err != nil {
		return Goal{}, fmt.Errorf("create goal %q: %w", goal.ID, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO goals
		(id, title, description, success_criteria, metrics_json, review_every, lead_agent, project_id, status, next_review_at, closing_report)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		goal.ID, goal.Title, goal.Description, goal.SuccessCriteria, metrics, goal.ReviewEvery,
		goal.LeadAgent, goal.ProjectID, goal.Status, goal.NextReviewAt, goal.ClosingReport,
	)
	if err != nil {
		return Goal{}, fmt.Errorf("create goal %q: %w", goal.ID, err)
	}
	return s.GetGoal(ctx, goal.ID)
}

// GetGoal fetches a goal by ID.
func (s *Store) GetGoal(ctx context.Context, id string) (Goal, error) {
	row := s.db.QueryRowContext(ctx, goalSelect+` WHERE id = ?`, id)
	goal, err := scanGoal(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Goal{}, fmt.Errorf("goal %q: %w", id, ErrNotFound)
		}
		return Goal{}, err
	}
	return goal, nil
}

// ListGoals returns goals, newest first, optionally filtered by status.
func (s *Store) ListGoals(ctx context.Context, status string) ([]Goal, error) {
	query := goalSelect
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()
	return scanGoals(rows)
}

// UpdateGoal stores the mutable fields of a goal. Which fields a given caller
// may change (user vs agent tool) is policy enforced at the API layer.
func (s *Store) UpdateGoal(ctx context.Context, goal Goal) (Goal, error) {
	metrics, err := marshalMetrics(goal.Metrics)
	if err != nil {
		return Goal{}, fmt.Errorf("update goal %q: %w", goal.ID, err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE goals
		SET title = ?, description = ?, success_criteria = ?, metrics_json = ?, review_every = ?,
			lead_agent = ?, project_id = ?, status = ?, next_review_at = NULLIF(?, ''),
			closing_report = ?, updated_at = datetime('now')
		WHERE id = ?`,
		goal.Title, goal.Description, goal.SuccessCriteria, metrics, goal.ReviewEvery,
		goal.LeadAgent, goal.ProjectID, goal.Status, goal.NextReviewAt, goal.ClosingReport, goal.ID,
	)
	if err != nil {
		return Goal{}, fmt.Errorf("update goal %q: %w", goal.ID, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Goal{}, fmt.Errorf("update goal %q rows affected: %w", goal.ID, err)
	}
	if changed == 0 {
		return Goal{}, fmt.Errorf("goal %q: %w", goal.ID, ErrNotFound)
	}
	return s.GetGoal(ctx, goal.ID)
}

// DeleteGoal removes a goal. Its timeline and access requests go with it (ON
// DELETE CASCADE); sessions started for the goal are left intact — their
// goal_id simply becomes a dangling reference — so deleting a goal never
// destroys the durable record of work done (the tasks precedent).
func (s *Store) DeleteGoal(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete goal %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete goal %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return fmt.Errorf("goal %q: %w", id, ErrNotFound)
	}
	return nil
}

// ListDueGoalReviews returns active goals whose next review time has arrived,
// so the scheduler can fire unattended review sessions. Pausing or closing a
// goal stops reviews atomically because the filter is on live status.
func (s *Store) ListDueGoalReviews(ctx context.Context, cutoffRFC3339 string) ([]Goal, error) {
	rows, err := s.db.QueryContext(ctx, goalSelect+`
		WHERE status = 'active' AND next_review_at IS NOT NULL AND next_review_at <= ?
		ORDER BY next_review_at`, cutoffRFC3339)
	if err != nil {
		return nil, fmt.Errorf("list due goal reviews: %w", err)
	}
	defer rows.Close()
	return scanGoals(rows)
}

// SetGoalNextReview persists the next scheduled review time ("" clears it).
// The scheduler advances this BEFORE running a review, so a long or crashed
// review can neither double-fire nor stall the cadence.
func (s *Store) SetGoalNextReview(ctx context.Context, id, at string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE goals
		SET next_review_at = NULLIF(?, ''), updated_at = datetime('now')
		WHERE id = ?`, at, id)
	if err != nil {
		return fmt.Errorf("set goal %q next review: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set goal %q next review rows affected: %w", id, err)
	}
	if changed == 0 {
		return fmt.Errorf("goal %q: %w", id, ErrNotFound)
	}
	return nil
}

const goalSelect = `SELECT id, title, description, success_criteria, metrics_json, review_every,
	lead_agent, project_id, status, COALESCE(next_review_at, ''), closing_report, created_at, updated_at FROM goals`

func scanGoals(rows *sql.Rows) ([]Goal, error) {
	var goals []Goal
	for rows.Next() {
		goal, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		goals = append(goals, goal)
	}
	return goals, rows.Err()
}

func scanGoal(row scanner) (Goal, error) {
	var goal Goal
	var metrics string
	if err := row.Scan(
		&goal.ID,
		&goal.Title,
		&goal.Description,
		&goal.SuccessCriteria,
		&metrics,
		&goal.ReviewEvery,
		&goal.LeadAgent,
		&goal.ProjectID,
		&goal.Status,
		&goal.NextReviewAt,
		&goal.ClosingReport,
		&goal.CreatedAt,
		&goal.UpdatedAt,
	); err != nil {
		return Goal{}, err
	}
	if err := json.Unmarshal([]byte(metrics), &goal.Metrics); err != nil {
		return Goal{}, fmt.Errorf("goal %q metrics: %w", goal.ID, err)
	}
	return goal, nil
}

func marshalMetrics(metrics []GoalMetric) (string, error) {
	if metrics == nil {
		metrics = []GoalMetric{}
	}
	b, err := json.Marshal(metrics)
	if err != nil {
		return "", fmt.Errorf("marshal metrics: %w", err)
	}
	return string(b), nil
}
