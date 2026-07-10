package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/google/uuid"
)

// CreateGoalRateLimitBlock inserts a pending goal rate-limit block. SessionID
// is unique so reconciliation/backfill can safely call this more than once for
// the same failed session; the existing row is returned on conflict.
func (s *Store) CreateGoalRateLimitBlock(ctx context.Context, block GoalRateLimitBlock) (GoalRateLimitBlock, error) {
	if block.ID == "" {
		block.ID = uuid.NewString()
	}
	if block.Status == "" {
		block.Status = GoalRateLimitPending
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO goal_rate_limits
		(id, goal_id, session_id, phase, provider, profile, model, effort, error, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO NOTHING`,
		block.ID, block.GoalID, block.SessionID, block.Phase, block.Provider, block.Profile,
		block.Model, block.Effort, block.Error, block.Status)
	if err != nil {
		return GoalRateLimitBlock{}, fmt.Errorf("create goal rate limit for %q: %w", block.GoalID, err)
	}
	existing, err := s.GetGoalRateLimitBlock(ctx, block.ID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return GoalRateLimitBlock{}, err
	}
	return s.GetGoalRateLimitBlockBySession(ctx, block.SessionID)
}

// GetGoalRateLimitBlock fetches one rate-limit block by ID.
func (s *Store) GetGoalRateLimitBlock(ctx context.Context, id string) (GoalRateLimitBlock, error) {
	row := s.db.QueryRowContext(ctx, goalRateLimitSelect+` WHERE id = ?`, id)
	block, err := scanGoalRateLimitBlock(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalRateLimitBlock{}, fmt.Errorf("goal rate limit %q: %w", id, ErrNotFound)
		}
		return GoalRateLimitBlock{}, err
	}
	return block, nil
}

// GetGoalRateLimitBlockBySession fetches the block created for a failed session.
func (s *Store) GetGoalRateLimitBlockBySession(ctx context.Context, sessionID string) (GoalRateLimitBlock, error) {
	row := s.db.QueryRowContext(ctx, goalRateLimitSelect+` WHERE session_id = ?`, sessionID)
	block, err := scanGoalRateLimitBlock(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalRateLimitBlock{}, fmt.Errorf("goal rate limit for session %q: %w", sessionID, ErrNotFound)
		}
		return GoalRateLimitBlock{}, err
	}
	return block, nil
}

// ListGoalRateLimitBlocks returns all rate-limit blocks for a goal, newest first.
func (s *Store) ListGoalRateLimitBlocks(ctx context.Context, goalID string) ([]GoalRateLimitBlock, error) {
	rows, err := s.db.QueryContext(ctx, goalRateLimitSelect+` WHERE goal_id = ? ORDER BY created_at DESC, id DESC`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal rate limits for %q: %w", goalID, err)
	}
	defer rows.Close()
	return scanGoalRateLimitBlocks(rows)
}

// PendingGoalRateLimit returns the newest pending rate-limit block for a goal.
func (s *Store) PendingGoalRateLimit(ctx context.Context, goalID string) (GoalRateLimitBlock, error) {
	row := s.db.QueryRowContext(ctx, goalRateLimitSelect+`
		WHERE goal_id = ? AND status = 'pending'
		ORDER BY created_at DESC, id DESC LIMIT 1`, goalID)
	block, err := scanGoalRateLimitBlock(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalRateLimitBlock{}, fmt.Errorf("pending goal rate limit for %q: %w", goalID, ErrNotFound)
		}
		return GoalRateLimitBlock{}, err
	}
	return block, nil
}

// ListPendingGoalRateLimits returns pending blocks across all goals, newest first.
func (s *Store) ListPendingGoalRateLimits(ctx context.Context) ([]GoalRateLimitBlock, error) {
	rows, err := s.db.QueryContext(ctx, goalRateLimitSelect+` WHERE status = 'pending' ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list pending goal rate limits: %w", err)
	}
	defer rows.Close()
	return scanGoalRateLimitBlocks(rows)
}

// ResolveGoalRateLimitBlock marks a pending block resolved with the selected
// target that will be persisted on the goal.
func (s *Store) ResolveGoalRateLimitBlock(ctx context.Context, id string, provider config.Provider, profile, model, effort string) (GoalRateLimitBlock, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_rate_limits
		SET status = 'resolved', resolved_provider = ?, resolved_profile = ?,
			resolved_model = ?, resolved_effort = ?, resolved_at = datetime('now')
		WHERE id = ? AND status = 'pending'`,
		provider, profile, model, effort, id)
	if err != nil {
		return GoalRateLimitBlock{}, fmt.Errorf("resolve goal rate limit %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return GoalRateLimitBlock{}, fmt.Errorf("resolve goal rate limit %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return GoalRateLimitBlock{}, fmt.Errorf("goal rate limit %q: %w", id, ErrNotFound)
	}
	return s.GetGoalRateLimitBlock(ctx, id)
}

const goalRateLimitSelect = `SELECT id, goal_id, session_id, phase, provider, profile, model, effort, error,
	status, resolved_provider, resolved_profile, resolved_model, resolved_effort, created_at, COALESCE(resolved_at, '')
	FROM goal_rate_limits`

func scanGoalRateLimitBlocks(rows *sql.Rows) ([]GoalRateLimitBlock, error) {
	var blocks []GoalRateLimitBlock
	for rows.Next() {
		block, err := scanGoalRateLimitBlock(rows)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

func scanGoalRateLimitBlock(row scanner) (GoalRateLimitBlock, error) {
	var block GoalRateLimitBlock
	if err := row.Scan(
		&block.ID,
		&block.GoalID,
		&block.SessionID,
		&block.Phase,
		&block.Provider,
		&block.Profile,
		&block.Model,
		&block.Effort,
		&block.Error,
		&block.Status,
		&block.ResolvedProvider,
		&block.ResolvedProfile,
		&block.ResolvedModel,
		&block.ResolvedEffort,
		&block.CreatedAt,
		&block.ResolvedAt,
	); err != nil {
		return GoalRateLimitBlock{}, err
	}
	return block, nil
}
