package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateTask inserts a roadmap task. If ID is empty a UUID is assigned and the
// status defaults to backlog.
func (s *Store) CreateTask(ctx context.Context, task Task) (Task, error) {
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if task.Status == "" {
		task.Status = TaskBacklog
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks
		(id, project_id, title, body, assigned_agent, provider, profile, model, effort, status, plan_required, pickup_at, goal_id, created_by_session, created_by_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)`,
		task.ID, task.ProjectID, task.Title, task.Body, task.AssignedAgent, task.Provider, task.Profile, task.Model, task.Effort,
		task.Status, boolInt(task.PlanRequired), task.PickupAt, task.GoalID, task.CreatedBySession, task.CreatedByAgent,
	)
	if err != nil {
		return Task{}, fmt.Errorf("create task %q: %w", task.ID, err)
	}
	return s.GetTask(ctx, task.ID)
}

// GetTask fetches a task by ID.
func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id)
	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task %q: %w", id, ErrNotFound)
		}
		return Task{}, err
	}
	return task, nil
}

// ListTasks returns all tasks, newest first.
func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+` ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

// UpdateTask stores the mutable fields of a task (assignment, status, body,
// title, pickup time). created_by_session/created_by_agent are deliberately
// absent from the SET clause: authorship is a creation fact, and letting an
// update rewrite it would let a later agent claim a task the user made.
func (s *Store) UpdateTask(ctx context.Context, task Task) (Task, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE tasks
		SET project_id = ?, title = ?, body = ?, assigned_agent = ?,
			provider = ?, profile = ?, model = ?, effort = ?,
			status = ?, plan_required = ?,
			pickup_at = NULLIF(?, ''), goal_id = ?, updated_at = datetime('now')
		WHERE id = ?`,
		task.ProjectID, task.Title, task.Body, task.AssignedAgent,
		task.Provider, task.Profile, task.Model, task.Effort,
		task.Status, boolInt(task.PlanRequired), task.PickupAt, task.GoalID, task.ID,
	)
	if err != nil {
		return Task{}, fmt.Errorf("update task %q: %w", task.ID, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Task{}, fmt.Errorf("update task %q rows affected: %w", task.ID, err)
	}
	if changed == 0 {
		return Task{}, fmt.Errorf("task %q: %w", task.ID, ErrNotFound)
	}
	return s.GetTask(ctx, task.ID)
}

// UnassignTasksByAgent clears roadmap assignments for an agent that is being
// removed. Tasks deliberately do not have a foreign key to agents, so this keeps
// future task updates from carrying a stale assignee name forward.
func (s *Store) UnassignTasksByAgent(ctx context.Context, agentName string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks
		SET assigned_agent = '', updated_at = datetime('now')
		WHERE assigned_agent = ?`, agentName)
	if err != nil {
		return fmt.Errorf("unassign tasks for agent %q: %w", agentName, err)
	}
	return nil
}

// DeleteTask removes a task by ID. Sessions started from the task are left
// intact — their task_id simply becomes a dangling reference — so deleting a
// task never destroys the durable record of work done.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete task %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	return nil
}

// ListDueTasks returns backlog tasks with an assigned agent whose pickup time
// has arrived (pickup_at <= cutoff), so the scheduler can start them
// automatically.
func (s *Store) ListDueTasks(ctx context.Context, cutoffRFC3339 string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+`
		WHERE status = 'backlog' AND assigned_agent != '' AND pickup_at IS NOT NULL AND pickup_at <= ?
		ORDER BY pickup_at`, cutoffRFC3339)
	if err != nil {
		return nil, fmt.Errorf("list due tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

// ListTasksCreatedBySession returns the tasks an agent session authored, newest
// first. This is the upward half of session provenance: a session row already
// records what spawned it, this answers what it spawned. Deriving it from the
// tasks themselves (rather than keeping a separate log) means a deleted task
// simply stops being listed, instead of the record advertising something that no
// longer exists.
func (s *Store) ListTasksCreatedBySession(ctx context.Context, sessionID string) ([]Task, error) {
	if sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, taskSelect+`
		WHERE created_by_session = ? ORDER BY created_at DESC, id DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks created by session %q: %w", sessionID, err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

const taskSelect = `SELECT id, project_id, title, body, assigned_agent,
	COALESCE(provider, ''), COALESCE(profile, ''), COALESCE(model, ''), COALESCE(effort, ''),
	status, plan_required,
	COALESCE(pickup_at, ''), COALESCE(goal_id, ''),
	COALESCE(created_by_session, ''), COALESCE(created_by_agent, ''),
	created_at, updated_at FROM tasks`

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func scanTask(row scanner) (Task, error) {
	var task Task
	if err := row.Scan(
		&task.ID,
		&task.ProjectID,
		&task.Title,
		&task.Body,
		&task.AssignedAgent,
		&task.Provider,
		&task.Profile,
		&task.Model,
		&task.Effort,
		&task.Status,
		&task.PlanRequired,
		&task.PickupAt,
		&task.GoalID,
		&task.CreatedBySession,
		&task.CreatedByAgent,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return Task{}, err
	}
	return task, nil
}
