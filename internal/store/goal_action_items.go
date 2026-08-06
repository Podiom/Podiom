package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateGoalActionItem files a step the agent handed back to the user. Items are
// always created open; only the user moves them out of that state.
func (s *Store) CreateGoalActionItem(ctx context.Context, item GoalActionItem) (GoalActionItem, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if item.Status == "" {
		item.Status = GoalActionOpen
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO goal_action_items
		(id, goal_id, session_id, run_id, agent_name, title, instructions, why, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.GoalID, item.SessionID, item.RunID, item.AgentName,
		item.Title, item.Instructions, item.Why, item.Status); err != nil {
		return GoalActionItem{}, fmt.Errorf("create action item for goal %q: %w", item.GoalID, err)
	}
	return s.GetGoalActionItem(ctx, item.ID)
}

// GetGoalActionItem fetches one action item by id.
func (s *Store) GetGoalActionItem(ctx context.Context, id string) (GoalActionItem, error) {
	row := s.db.QueryRowContext(ctx, goalActionItemSelect+` WHERE id = ?`, id)
	item, err := scanGoalActionItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalActionItem{}, fmt.Errorf("action item %q: %w", id, ErrNotFound)
		}
		return GoalActionItem{}, err
	}
	return item, nil
}

// ListOpenGoalActionItems returns the goal's unanswered action items, oldest
// first — the longest-waiting ask leads both the carousel and the review prompt.
func (s *Store) ListOpenGoalActionItems(ctx context.Context, goalID string) ([]GoalActionItem, error) {
	// created_at has one-second resolution and ids are uuids, so insertion order
	// (rowid) is the tiebreaker — without it, items filed in the same second come
	// back in random order and "oldest first" stops meaning anything.
	rows, err := s.db.QueryContext(ctx, goalActionItemSelect+`
		WHERE goal_id = ? AND status = 'open'
		ORDER BY created_at, rowid`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list open action items for goal %q: %w", goalID, err)
	}
	defer rows.Close()
	return scanGoalActionItems(rows)
}

// ListRespondedGoalActionItems returns the most recently answered action items,
// newest first, so the next run can act on the user's verdicts.
func (s *Store) ListRespondedGoalActionItems(ctx context.Context, goalID string, limit int) ([]GoalActionItem, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, goalActionItemSelect+`
		WHERE goal_id = ? AND status != 'open'
		ORDER BY responded_at DESC, rowid DESC LIMIT ?`, goalID, limit)
	if err != nil {
		return nil, fmt.Errorf("list responded action items for goal %q: %w", goalID, err)
	}
	defer rows.Close()
	return scanGoalActionItems(rows)
}

// CountOpenGoalActionItems returns how many action items a goal is waiting on,
// for the goals list triage.
func (s *Store) CountOpenGoalActionItems(ctx context.Context, goalID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal_action_items WHERE goal_id = ? AND status = 'open'`,
		goalID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count open action items for goal %q: %w", goalID, err)
	}
	return n, nil
}

// RespondGoalActionItem records the user's verdict and note. Guarded on the open
// state so a verdict is given exactly once — a second response is ErrNotFound
// rather than a silent overwrite of what the agent may already have read.
func (s *Store) RespondGoalActionItem(ctx context.Context, id string, status GoalActionItemStatus, response string) (GoalActionItem, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE goal_action_items
		SET status = ?, response = ?, responded_at = datetime('now')
		WHERE id = ? AND status = 'open'`, status, response, id)
	if err != nil {
		return GoalActionItem{}, fmt.Errorf("respond to action item %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return GoalActionItem{}, fmt.Errorf("respond to action item %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return GoalActionItem{}, fmt.Errorf("open action item %q: %w", id, ErrNotFound)
	}
	return s.GetGoalActionItem(ctx, id)
}

const goalActionItemSelect = `SELECT id, goal_id, session_id, run_id, agent_name,
	title, instructions, why, status, response, created_at, COALESCE(responded_at, '')
	FROM goal_action_items`

func scanGoalActionItems(rows *sql.Rows) ([]GoalActionItem, error) {
	var out []GoalActionItem
	for rows.Next() {
		item, err := scanGoalActionItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanGoalActionItem(row scanner) (GoalActionItem, error) {
	var item GoalActionItem
	if err := row.Scan(
		&item.ID,
		&item.GoalID,
		&item.SessionID,
		&item.RunID,
		&item.AgentName,
		&item.Title,
		&item.Instructions,
		&item.Why,
		&item.Status,
		&item.Response,
		&item.CreatedAt,
		&item.RespondedAt,
	); err != nil {
		return GoalActionItem{}, err
	}
	return item, nil
}
