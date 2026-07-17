package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/google/uuid"
)

// CreateSession inserts a durable session. If ID is empty, a UUID is assigned.
func (s *Store) CreateSession(ctx context.Context, sess Session) (Session, error) {
	if sess.ID == "" {
		sess.ID = uuid.NewString()
	}
	if sess.PlanState == "" {
		sess.PlanState = PlanNone
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions
		(id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin, schedule_id, run_id, task_id, goal_id, project_id, rolling_summary, provider_handle,
		 plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID,
		sess.AgentName,
		sess.Name,
		sess.Description,
		boolInt(sess.AutoNamed),
		sess.Provider,
		sess.Profile,
		sess.Model,
		sess.Effort,
		sess.PermissionMode,
		sess.Origin,
		sess.ScheduleID,
		sess.RunID,
		sess.TaskID,
		sess.GoalID,
		sess.ProjectID,
		sess.RollingSummary,
		sess.ProviderHandle,
		sess.PlanState,
		boolInt(sess.PlanExplicit),
		sess.PlanInfo.FilePath,
		sess.PlanInfo.Markdown,
		sess.PlanInfo.SubmittedAt,
		sess.PlanInfo.UpdatedAt,
	)
	if err != nil {
		return Session{}, fmt.Errorf("create session %q: %w", sess.ID, err)
	}
	return s.GetSession(ctx, sess.ID)
}

// GetSession fetches a session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin,
		COALESCE(schedule_id, ''), COALESCE(run_id, ''), COALESCE(task_id, ''), COALESCE(goal_id, ''), project_id, rolling_summary, provider_handle,
		plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
		COALESCE(dreamed_at, ''), COALESCE(context_tokens, 0), COALESCE(context_limit, 0),
		COALESCE(usage_input_tokens, 0), COALESCE(usage_output_tokens, 0), COALESCE(usage_cache_read_tokens, 0), COALESCE(usage_cache_write_tokens, 0),
		created_at, updated_at
		FROM sessions WHERE id = ?`, id)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
		}
		return Session{}, err
	}
	return sess, nil
}

// ListSessions returns all sessions ordered newest first.
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin,
		COALESCE(schedule_id, ''), COALESCE(run_id, ''), COALESCE(task_id, ''), COALESCE(goal_id, ''), project_id, rolling_summary, provider_handle,
		plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
		COALESCE(dreamed_at, ''), COALESCE(context_tokens, 0), COALESCE(context_limit, 0),
		COALESCE(usage_input_tokens, 0), COALESCE(usage_output_tokens, 0), COALESCE(usage_cache_read_tokens, 0), COALESCE(usage_cache_write_tokens, 0),
		created_at, updated_at
		FROM sessions ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
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

// ListSessionsByAgent returns all sessions for one agent, oldest first, so they
// can be archived in a stable historical order.
func (s *Store) ListSessionsByAgent(ctx context.Context, agentName string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin,
		COALESCE(schedule_id, ''), COALESCE(run_id, ''), COALESCE(task_id, ''), COALESCE(goal_id, ''), project_id, rolling_summary, provider_handle,
		plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
		COALESCE(dreamed_at, ''), COALESCE(context_tokens, 0), COALESCE(context_limit, 0),
		COALESCE(usage_input_tokens, 0), COALESCE(usage_output_tokens, 0), COALESCE(usage_cache_read_tokens, 0), COALESCE(usage_cache_write_tokens, 0),
		created_at, updated_at
		FROM sessions WHERE agent_name = ? ORDER BY created_at, id`, agentName)
	if err != nil {
		return nil, fmt.Errorf("list sessions for agent %q: %w", agentName, err)
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

// ListSessionsBySchedule returns sessions produced by a given schedule, newest
// first, so the user can review "all runs of <schedule>" (R7.9).
func (s *Store) ListSessionsBySchedule(ctx context.Context, scheduleName string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin,
		COALESCE(schedule_id, ''), COALESCE(run_id, ''), COALESCE(task_id, ''), COALESCE(goal_id, ''), project_id, rolling_summary, provider_handle,
		plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
		COALESCE(dreamed_at, ''), COALESCE(context_tokens, 0), COALESCE(context_limit, 0),
		COALESCE(usage_input_tokens, 0), COALESCE(usage_output_tokens, 0), COALESCE(usage_cache_read_tokens, 0), COALESCE(usage_cache_write_tokens, 0),
		created_at, updated_at
		FROM sessions WHERE schedule_id = ? ORDER BY created_at DESC, id DESC`, scheduleName)
	if err != nil {
		return nil, fmt.Errorf("list sessions for schedule %q: %w", scheduleName, err)
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

// ListSessionsByTask returns sessions started from a roadmap task, newest first.
func (s *Store) ListSessionsByTask(ctx context.Context, taskID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, agent_name, name, description, auto_named, provider, profile, model, effort, permission_mode, origin,
		COALESCE(schedule_id, ''), COALESCE(run_id, ''), COALESCE(task_id, ''), COALESCE(goal_id, ''), project_id, rolling_summary, provider_handle,
		plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
		COALESCE(dreamed_at, ''), COALESCE(context_tokens, 0), COALESCE(context_limit, 0),
		COALESCE(usage_input_tokens, 0), COALESCE(usage_output_tokens, 0), COALESCE(usage_cache_read_tokens, 0), COALESCE(usage_cache_write_tokens, 0),
		created_at, updated_at
		FROM sessions WHERE task_id = ? ORDER BY created_at DESC, id DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list sessions for task %q: %w", taskID, err)
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

// UpdateSessionSettings stores mutable per-session provider settings.
func (s *Store) UpdateSessionSettings(ctx context.Context, id, model, effort string, permissionMode config.PermissionMode) (Session, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET model = ?, effort = ?, permission_mode = ?, updated_at = datetime('now')
		WHERE id = ?`, model, effort, permissionMode, id)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q settings: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// UpdateSessionRuntime stores the current backing target and mutable runtime
// settings. Clearing providerHandle forces the next turn to replay Podiom's
// canonical history into a fresh provider session/thread.
func (s *Store) UpdateSessionRuntime(ctx context.Context, id string, provider config.Provider, profile, model, effort string, permissionMode config.PermissionMode, providerHandle string) (Session, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET provider = ?, profile = ?, model = ?, effort = ?, permission_mode = ?,
			provider_handle = ?, updated_at = datetime('now')
		WHERE id = ?`, provider, profile, model, effort, permissionMode, providerHandle, id)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q runtime: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// UpdateSessionMetadata stores the display name and description for a session.
func (s *Store) UpdateSessionMetadata(ctx context.Context, id, name, description string, autoNamed bool) (Session, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET name = ?, description = ?, auto_named = ?, updated_at = datetime('now')
		WHERE id = ?`, name, description, boolInt(autoNamed), id)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q metadata: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// UpdateSessionGoalBinding changes the lead conversation's agent/project
// binding and clears its provider handle so the next turn starts in the right
// workspace. Existing canonical messages remain intact.
func (s *Store) UpdateSessionGoalBinding(ctx context.Context, id, agentName, projectID string) (Session, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET agent_name = ?, project_id = ?, provider_handle = '', context_tokens = 0, updated_at = datetime('now')
		WHERE id = ?`, agentName, projectID, id)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q goal binding: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// UpdateSessionProviderHandle stores the latest provider-owned resume handle.
func (s *Store) UpdateSessionProviderHandle(ctx context.Context, id, handle string) (Session, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET provider_handle = ?, updated_at = datetime('now')
		WHERE id = ?`, handle, id)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q provider handle: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// UpdateSessionContext stores the latest context-window utilization observed for
// a session. Called once per turn from the provider stream; it avoids a re-fetch
// (returns error only) since callers just persist the numbers for later reads.
func (s *Store) UpdateSessionContext(ctx context.Context, id string, tokens, limit int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET context_tokens = ?, context_limit = ?, updated_at = datetime('now')
		WHERE id = ?`, tokens, limit, id)
	if err != nil {
		return fmt.Errorf("update session %q context: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return nil
}

// AddSessionUsage increments a session's cumulative billed-token totals by one
// turn's delta and returns the new lifetime totals. Called once per completed
// turn from the provider stream. Unlike UpdateSessionContext (a snapshot
// overwrite) these accumulate over the session's lifetime. A zero delta is a
// no-op that returns the current totals unchanged.
func (s *Store) AddSessionUsage(ctx context.Context, id string, delta SessionUsage) (SessionUsage, error) {
	if delta.Total() == 0 {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			return SessionUsage{}, err
		}
		return sess.Usage, nil
	}
	row := s.db.QueryRowContext(ctx, `UPDATE sessions
		SET usage_input_tokens = usage_input_tokens + ?,
			usage_output_tokens = usage_output_tokens + ?,
			usage_cache_read_tokens = usage_cache_read_tokens + ?,
			usage_cache_write_tokens = usage_cache_write_tokens + ?,
			updated_at = datetime('now')
		WHERE id = ?
		RETURNING usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens`,
		delta.InputTokens, delta.OutputTokens, delta.CacheReadTokens, delta.CacheWriteTokens, id)
	var total SessionUsage
	if err := row.Scan(&total.InputTokens, &total.OutputTokens, &total.CacheReadTokens, &total.CacheWriteTokens); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionUsage{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
		}
		return SessionUsage{}, fmt.Errorf("add session %q usage: %w", id, err)
	}
	return total, nil
}

// UpdateSessionPlanState stores the current plan gate state and displayable
// plan artifact. Empty info fields clear the stored artifact.
func (s *Store) UpdateSessionPlanState(ctx context.Context, id string, state PlanState, explicit bool, info PlanInfo) (Session, error) {
	if state == "" {
		state = PlanNone
	}
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET plan_state = ?, plan_explicit = ?, plan_file_path = ?, plan_markdown = ?,
			plan_submitted_at = ?, plan_updated_at = ?, updated_at = datetime('now')
		WHERE id = ?`,
		state,
		boolInt(explicit),
		info.FilePath,
		info.Markdown,
		info.SubmittedAt,
		info.UpdatedAt,
		id,
	)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q plan state: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// UpdateRollingSummary stores the current replay summary for a session.
func (s *Store) UpdateRollingSummary(ctx context.Context, id, summary string) (Session, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions
		SET rolling_summary = ?, updated_at = datetime('now')
		WHERE id = ?`, summary, id)
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rolling summary: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Session{}, fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return s.GetSession(ctx, id)
}

// AppendMessages appends messages to the canonical history with strictly
// increasing sequence numbers assigned inside one transaction.
func (s *Store) AppendMessages(ctx context.Context, sessionID string, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin append messages: %w", err)
	}
	defer tx.Rollback()

	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`,
		sessionID,
	).Scan(&next); err != nil {
		return nil, fmt.Errorf("next message seq: %w", err)
	}

	inserted := make([]Message, 0, len(messages))
	for _, msg := range messages {
		msg.SessionID = sessionID
		msg.Seq = next
		if msg.Kind == "" {
			msg.Kind = KindMessage
		}
		next++
		res, err := tx.ExecContext(ctx, `INSERT INTO messages (session_id, seq, role, kind, content)
			VALUES (?, ?, ?, ?, ?)`, msg.SessionID, msg.Seq, msg.Role, msg.Kind, msg.Content)
		if err != nil {
			return nil, fmt.Errorf("append message %d to session %q: %w", msg.Seq, sessionID, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("read appended message id: %w", err)
		}
		msg.ID = id
		inserted = append(inserted, msg)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET updated_at = datetime('now') WHERE id = ?`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("touch session %q: %w", sessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit append messages: %w", err)
	}
	return inserted, nil
}

// ListMessages returns a session's canonical history in sequence order.
func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, seq, role, kind, content, created_at
		FROM messages WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list messages for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Seq, &msg.Role, &msg.Kind, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, err
		}
		if msg.Kind == "" {
			msg.Kind = KindMessage
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// DeleteSession removes a single session. Its message history is removed by the
// messages.session_id ON DELETE CASCADE foreign key.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete session %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return fmt.Errorf("session %q: %w", id, ErrNotFound)
	}
	return nil
}

// DeleteSessionsByTask removes every session started from a roadmap task.
// Message history is removed by the messages.session_id ON DELETE CASCADE
// foreign key.
func (s *Store) DeleteSessionsByTask(ctx context.Context, taskID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("delete sessions for task %q: %w", taskID, err)
	}
	return nil
}

// DeleteSessionsByAgent removes all sessions for one agent. Message history is
// removed by the messages.session_id ON DELETE CASCADE foreign key.
func (s *Store) DeleteSessionsByAgent(ctx context.Context, agentName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete sessions for agent %q: %w", agentName, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE agent_name = ?`, agentName); err != nil {
		return fmt.Errorf("delete sessions for agent %q: %w", agentName, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete sessions for agent %q: %w", agentName, err)
	}
	return nil
}

func scanSession(row scanner) (Session, error) {
	var sess Session
	if err := row.Scan(
		&sess.ID,
		&sess.AgentName,
		&sess.Name,
		&sess.Description,
		&sess.AutoNamed,
		&sess.Provider,
		&sess.Profile,
		&sess.Model,
		&sess.Effort,
		&sess.PermissionMode,
		&sess.Origin,
		&sess.ScheduleID,
		&sess.RunID,
		&sess.TaskID,
		&sess.GoalID,
		&sess.ProjectID,
		&sess.RollingSummary,
		&sess.ProviderHandle,
		&sess.PlanState,
		&sess.PlanExplicit,
		&sess.PlanInfo.FilePath,
		&sess.PlanInfo.Markdown,
		&sess.PlanInfo.SubmittedAt,
		&sess.PlanInfo.UpdatedAt,
		&sess.DreamedAt,
		&sess.ContextTokens,
		&sess.ContextLimit,
		&sess.Usage.InputTokens,
		&sess.Usage.OutputTokens,
		&sess.Usage.CacheReadTokens,
		&sess.Usage.CacheWriteTokens,
		&sess.CreatedAt,
		&sess.UpdatedAt,
	); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
