package store

import (
	"context"
	"fmt"
)

// migration is one forward-only schema change. Migrations run in order and each
// runs at most once; their cumulative effect is recorded in schema_migrations.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations is the ordered list of schema changes. Append new migrations here —
// never edit or reorder an already-shipped one. Phase 0 only establishes the
// bookkeeping table; Phase 1 adds agents, sessions, and message history.
var migrations = []migration{
	{
		version: 1,
		name:    "create_schema_migrations",
		sql: `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at TEXT    NOT NULL DEFAULT (datetime('now'))
		);`,
	},
	{
		version: 2,
		name:    "core_domain",
		sql: `CREATE TABLE agents (
			name            TEXT PRIMARY KEY,
			provider        TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
			profile         TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			effort          TEXT NOT NULL DEFAULT '',
			permission_mode TEXT NOT NULL CHECK (permission_mode IN ('approve', 'yolo')),
			fallback_json   TEXT NOT NULL DEFAULT '[]',
			mcp_config      TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE sessions (
			id               TEXT PRIMARY KEY,
			agent_name       TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE RESTRICT,
			provider         TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
			profile          TEXT NOT NULL DEFAULT '',
			model            TEXT NOT NULL DEFAULT '',
			effort           TEXT NOT NULL DEFAULT '',
			permission_mode  TEXT NOT NULL CHECK (permission_mode IN ('approve', 'yolo')),
			origin           TEXT NOT NULL CHECK (origin IN ('web', 'cli', 'schedule', 'roadmap')),
			schedule_id      TEXT,
			run_id           TEXT,
			rolling_summary  TEXT NOT NULL DEFAULT '',
			provider_handle  TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TRIGGER sessions_origin_immutable
		BEFORE UPDATE OF origin ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'session origin is immutable');
		END;

		CREATE TABLE messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq        INTEGER NOT NULL,
			role       TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
			content    TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (session_id, seq)
		);

		CREATE INDEX idx_sessions_agent_name ON sessions(agent_name);
		CREATE INDEX idx_sessions_origin ON sessions(origin);
			CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);`,
	},
	{
		version: 3,
		name:    "session_metadata_and_settings",
		sql: `ALTER TABLE sessions ADD COLUMN name TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN description TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN auto_named INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 4,
		name:    "schedule_runs",
		sql: `CREATE TABLE schedule_runs (
			id            TEXT PRIMARY KEY,
			schedule_name TEXT NOT NULL,
			session_id    TEXT REFERENCES sessions(id) ON DELETE SET NULL,
			trigger       TEXT NOT NULL CHECK (trigger IN ('cron', 'manual')),
			status        TEXT NOT NULL CHECK (status IN ('running', 'success', 'error')),
			error         TEXT NOT NULL DEFAULT '',
			started_at    TEXT NOT NULL DEFAULT (datetime('now')),
			finished_at   TEXT
		);

		CREATE INDEX idx_schedule_runs_name ON schedule_runs(schedule_name, started_at DESC);
		CREATE INDEX idx_sessions_schedule_id ON sessions(schedule_id);`,
	},
	{
		version: 5,
		name:    "tasks",
		sql: `CREATE TABLE tasks (
			id             TEXT PRIMARY KEY,
			project_id     TEXT NOT NULL DEFAULT '',
			title          TEXT NOT NULL,
			body           TEXT NOT NULL DEFAULT '',
			assigned_agent TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL CHECK (status IN ('backlog', 'in_progress', 'review', 'done')),
			pickup_at      TEXT,
			created_at     TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX idx_tasks_project ON tasks(project_id);
		CREATE INDEX idx_tasks_status ON tasks(status);

		ALTER TABLE sessions ADD COLUMN task_id TEXT;
		CREATE INDEX idx_sessions_task_id ON sessions(task_id);`,
	},
	{
		version: 6,
		name:    "onboarding_origin",
		sql: `DROP TRIGGER IF EXISTS sessions_origin_immutable;

		CREATE TABLE sessions_new (
			id               TEXT PRIMARY KEY,
			agent_name       TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE RESTRICT,
			provider         TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
			profile          TEXT NOT NULL DEFAULT '',
			model            TEXT NOT NULL DEFAULT '',
			effort           TEXT NOT NULL DEFAULT '',
			permission_mode  TEXT NOT NULL CHECK (permission_mode IN ('approve', 'yolo')),
			origin           TEXT NOT NULL CHECK (origin IN ('web', 'cli', 'onboarding', 'schedule', 'roadmap')),
			schedule_id      TEXT,
			run_id           TEXT,
			rolling_summary  TEXT NOT NULL DEFAULT '',
			provider_handle  TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
			name             TEXT NOT NULL DEFAULT '',
			description      TEXT NOT NULL DEFAULT '',
			auto_named       INTEGER NOT NULL DEFAULT 0,
			task_id          TEXT
		);

		INSERT INTO sessions_new (
			id, agent_name, provider, profile, model, effort, permission_mode, origin,
			schedule_id, run_id, rolling_summary, provider_handle, created_at, updated_at,
			name, description, auto_named, task_id
		)
		SELECT
			id, agent_name, provider, profile, model, effort, permission_mode, origin,
			schedule_id, run_id, rolling_summary, provider_handle, created_at, updated_at,
			name, description, auto_named, task_id
		FROM sessions;

		DROP TABLE sessions;
		ALTER TABLE sessions_new RENAME TO sessions;

		CREATE TRIGGER sessions_origin_immutable
		BEFORE UPDATE OF origin ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'session origin is immutable');
		END;

		CREATE INDEX idx_sessions_agent_name ON sessions(agent_name);
		CREATE INDEX idx_sessions_origin ON sessions(origin);
		CREATE INDEX idx_sessions_schedule_id ON sessions(schedule_id);
		CREATE INDEX idx_sessions_task_id ON sessions(task_id);`,
	},
	{
		version: 7,
		name:    "session_project_id",
		sql: `ALTER TABLE sessions ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
		CREATE INDEX idx_sessions_project_id ON sessions(project_id);`,
	},
	{
		version: 8,
		name:    "push_subscriptions",
		// Generic push-subscription registry. `kind` selects the delivery
		// technology ('webpush' today; 'apns'/'fcm' later) and `payload` holds
		// the kind-specific credentials as JSON, so native device tokens reuse
		// this table without a schema change. `endpoint` is the natural identity
		// (the webpush endpoint URL, or a native device token) and is unique so
		// re-subscribing the same client upserts rather than duplicates.
		sql: `CREATE TABLE push_subscriptions (
			id         TEXT PRIMARY KEY,
			kind       TEXT NOT NULL,
			endpoint   TEXT NOT NULL UNIQUE,
			payload    TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX idx_push_subscriptions_kind ON push_subscriptions(kind);`,
	},
	{
		version: 9,
		name:    "agent_mcp_servers",
		sql:     `ALTER TABLE agents ADD COLUMN mcp_servers_json TEXT NOT NULL DEFAULT '[]';`,
	},
	{
		version: 10,
		name:    "agent_memory_dreaming",
		// Per-agent memory consolidation ("dreaming"). sessions.dreamed_at marks a
		// session as already folded into MEMORY.md (NULL = un-dreamed). The dreams
		// table is both the audit journal and the source of "last dream time"
		// (MAX(ran_at) WHERE status='success') and the NEW-item/since-date metadata
		// the UI renders — MEMORY.md itself stays clean, user-editable markdown.
		sql: `ALTER TABLE sessions ADD COLUMN dreamed_at TEXT;
		CREATE INDEX idx_sessions_dreamed ON sessions(agent_name, dreamed_at);

		CREATE TABLE dreams (
			id             TEXT PRIMARY KEY,
			agent_name     TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE CASCADE,
			ran_at         TEXT NOT NULL DEFAULT (datetime('now')),
			finished_at    TEXT,
			trigger        TEXT NOT NULL CHECK (trigger IN ('nightly', 'manual')),
			status         TEXT NOT NULL CHECK (status IN ('running', 'success', 'error')),
			error          TEXT NOT NULL DEFAULT '',
			session_count  INTEGER NOT NULL DEFAULT 0,
			kept           INTEGER NOT NULL DEFAULT 0,
			merged         INTEGER NOT NULL DEFAULT 0,
			pruned         INTEGER NOT NULL DEFAULT 0,
			note           TEXT NOT NULL DEFAULT '',
			new_items_json TEXT NOT NULL DEFAULT '[]'
		);

		CREATE INDEX idx_dreams_agent ON dreams(agent_name, ran_at DESC);`,
	},
	{
		version: 11,
		name:    "plan_mode",
		sql: `ALTER TABLE sessions ADD COLUMN plan_state TEXT NOT NULL DEFAULT 'none'
			CHECK (plan_state IN ('none', 'pending_submission', 'awaiting_approval'));
		ALTER TABLE sessions ADD COLUMN plan_explicit INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE sessions ADD COLUMN plan_file_path TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN plan_markdown TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN plan_submitted_at TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN plan_updated_at TEXT NOT NULL DEFAULT '';
		CREATE INDEX idx_sessions_plan_state ON sessions(plan_state);

		ALTER TABLE tasks ADD COLUMN plan_required INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 12,
		name:    "message_kind",
		sql: `ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'message'
			CHECK (kind IN ('message', 'error'));`,
	},
	{
		version: 13,
		name:    "session_context_window",
		// Per-session context-window utilization, refreshed each turn from the
		// provider stream. context_tokens is the last request's prompt size;
		// context_limit is the model's window (0 until first observed). Persisted so
		// the composer's context ring restores on reload/reconnect.
		sql: `ALTER TABLE sessions ADD COLUMN context_tokens INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN context_limit INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 14,
		name:    "goals",
		// Goals: user-stated outcomes driven autonomously by one lead agent.
		// goal_events is the append-only audit timeline (UPDATE rejected by
		// trigger; rows removed only via the goal's ON DELETE CASCADE — no
		// BEFORE DELETE trigger, which would break that cascade). The sessions
		// table is rebuilt (v6 pattern) to extend the origin CHECK with 'goal'
		// and add goal_id; the rebuild is only safe because migrations run with
		// foreign_keys OFF — with them ON, DROP TABLE sessions would cascade-
		// delete every row in messages.
		sql: `CREATE TABLE goals (
			id               TEXT PRIMARY KEY,
			title            TEXT NOT NULL,
			description      TEXT NOT NULL DEFAULT '',
			success_criteria TEXT NOT NULL DEFAULT '',
			metrics_json     TEXT NOT NULL DEFAULT '[]',
			review_every     TEXT NOT NULL DEFAULT '',
			lead_agent       TEXT NOT NULL,
			project_id       TEXT NOT NULL DEFAULT '',
			status           TEXT NOT NULL DEFAULT 'active'
				CHECK (status IN ('active', 'paused', 'review', 'done', 'abandoned')),
			next_review_at   TEXT,
			closing_report   TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX idx_goals_status ON goals(status);
		CREATE INDEX idx_goals_due_review ON goals(status, next_review_at);

		CREATE TABLE goal_events (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'access_requested', 'access_decided',
				'status_change', 'completion_proposed')),
			body         TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX idx_goal_events_goal ON goal_events(goal_id, id DESC);

		CREATE TRIGGER goal_events_append_only
		BEFORE UPDATE ON goal_events
		BEGIN
			SELECT RAISE(ABORT, 'goal events are append-only');
		END;

		CREATE TABLE access_requests (
			id              TEXT PRIMARY KEY,
			goal_id         TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			agent_name      TEXT NOT NULL,
			session_id      TEXT,
			kind            TEXT NOT NULL CHECK (kind IN ('mcp_server', 'skill', 'cli_tool', 'env_var', 'permission_mode')),
			payload_json    TEXT NOT NULL DEFAULT '{}',
			reason          TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'approved', 'denied', 'executed', 'failed')),
			decision_note   TEXT NOT NULL DEFAULT '',
			execution_error TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			decided_at      TEXT,
			executed_at     TEXT
		);

		CREATE INDEX idx_access_requests_goal ON access_requests(goal_id, created_at DESC);
		CREATE INDEX idx_access_requests_status ON access_requests(status);

		DROP TRIGGER IF EXISTS sessions_origin_immutable;

		CREATE TABLE sessions_new (
			id                TEXT PRIMARY KEY,
			agent_name        TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE RESTRICT,
			provider          TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
			profile           TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			effort            TEXT NOT NULL DEFAULT '',
			permission_mode   TEXT NOT NULL CHECK (permission_mode IN ('approve', 'yolo')),
			origin            TEXT NOT NULL CHECK (origin IN ('web', 'cli', 'onboarding', 'schedule', 'roadmap', 'goal')),
			schedule_id       TEXT,
			run_id            TEXT,
			rolling_summary   TEXT NOT NULL DEFAULT '',
			provider_handle   TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
			name              TEXT NOT NULL DEFAULT '',
			description       TEXT NOT NULL DEFAULT '',
			auto_named        INTEGER NOT NULL DEFAULT 0,
			task_id           TEXT,
			project_id        TEXT NOT NULL DEFAULT '',
			dreamed_at        TEXT,
			plan_state        TEXT NOT NULL DEFAULT 'none'
				CHECK (plan_state IN ('none', 'pending_submission', 'awaiting_approval')),
			plan_explicit     INTEGER NOT NULL DEFAULT 0,
			plan_file_path    TEXT NOT NULL DEFAULT '',
			plan_markdown     TEXT NOT NULL DEFAULT '',
			plan_submitted_at TEXT NOT NULL DEFAULT '',
			plan_updated_at   TEXT NOT NULL DEFAULT '',
			context_tokens    INTEGER NOT NULL DEFAULT 0,
			context_limit     INTEGER NOT NULL DEFAULT 0,
			goal_id           TEXT
		);

		INSERT INTO sessions_new (
			id, agent_name, provider, profile, model, effort, permission_mode, origin,
			schedule_id, run_id, rolling_summary, provider_handle, created_at, updated_at,
			name, description, auto_named, task_id, project_id, dreamed_at,
			plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
			context_tokens, context_limit
		)
		SELECT
			id, agent_name, provider, profile, model, effort, permission_mode, origin,
			schedule_id, run_id, rolling_summary, provider_handle, created_at, updated_at,
			name, description, auto_named, task_id, project_id, dreamed_at,
			plan_state, plan_explicit, plan_file_path, plan_markdown, plan_submitted_at, plan_updated_at,
			context_tokens, context_limit
		FROM sessions;

		DROP TABLE sessions;
		ALTER TABLE sessions_new RENAME TO sessions;

		CREATE TRIGGER sessions_origin_immutable
		BEFORE UPDATE OF origin ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'session origin is immutable');
		END;

		CREATE INDEX idx_sessions_agent_name ON sessions(agent_name);
		CREATE INDEX idx_sessions_origin ON sessions(origin);
		CREATE INDEX idx_sessions_schedule_id ON sessions(schedule_id);
		CREATE INDEX idx_sessions_task_id ON sessions(task_id);
		CREATE INDEX idx_sessions_project_id ON sessions(project_id);
		CREATE INDEX idx_sessions_dreamed ON sessions(agent_name, dreamed_at);
		CREATE INDEX idx_sessions_plan_state ON sessions(plan_state);
		CREATE INDEX idx_sessions_goal_id ON sessions(goal_id);`,
	},
	{
		version: 15,
		name:    "agent_avatar",
		// avatar_updated_at is the version stamp for an agent's uploaded profile
		// picture (empty = no picture). The image bytes live on disk in the
		// per-agent dir (agents/<name>/avatar.png); this column only records
		// existence and drives client-side cache-busting.
		sql: `ALTER TABLE agents ADD COLUMN avatar_updated_at TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 16,
		name:    "session_token_usage",
		// Cumulative billed-token totals per session, accumulated across every turn
		// from the provider stream's usage payload (unlike context_tokens, which is
		// a last-request snapshot). Broken out by class so the billable metric can be
		// re-tuned without a migration. Rolled up per goal (SUM over sessions.goal_id)
		// to answer "how much did this session/goal consume." NOTE: this is Podiom's
		// own token accounting, kept separate from internal/usage (which reads
		// provider plan-limit % and deliberately never persists tokens).
		sql: `ALTER TABLE sessions ADD COLUMN usage_input_tokens INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN usage_output_tokens INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN usage_cache_read_tokens INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN usage_cache_write_tokens INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 17,
		name:    "run_targets",
		sql: `ALTER TABLE tasks ADD COLUMN provider TEXT NOT NULL DEFAULT ''
				CHECK (provider IN ('', 'claude', 'codex'));
			ALTER TABLE tasks ADD COLUMN profile TEXT NOT NULL DEFAULT '';
			ALTER TABLE tasks ADD COLUMN model TEXT NOT NULL DEFAULT '';
			ALTER TABLE tasks ADD COLUMN effort TEXT NOT NULL DEFAULT '';

			ALTER TABLE goals ADD COLUMN provider TEXT NOT NULL DEFAULT ''
				CHECK (provider IN ('', 'claude', 'codex'));
			ALTER TABLE goals ADD COLUMN profile TEXT NOT NULL DEFAULT '';
			ALTER TABLE goals ADD COLUMN model TEXT NOT NULL DEFAULT '';
			ALTER TABLE goals ADD COLUMN effort TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 18,
		name:    "goal_rate_limit_recovery",
		sql: `DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TABLE goal_events_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'access_requested', 'access_decided',
				'status_change', 'completion_proposed', 'rate_limited', 'rate_limit_resolved')),
			body         TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL DEFAULT (datetime('now'))
		);

		INSERT INTO goal_events_new (id, goal_id, session_id, kind, body, payload_json, created_at)
		SELECT id, goal_id, session_id, kind, body, payload_json, created_at FROM goal_events;

		DROP TABLE goal_events;
		ALTER TABLE goal_events_new RENAME TO goal_events;

		CREATE INDEX idx_goal_events_goal ON goal_events(goal_id, id DESC);

		CREATE TRIGGER goal_events_append_only
		BEFORE UPDATE ON goal_events
		BEGIN
			SELECT RAISE(ABORT, 'goal events are append-only');
		END;

		CREATE TABLE goal_rate_limits (
			id                TEXT PRIMARY KEY,
			goal_id           TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id         TEXT NOT NULL UNIQUE,
			phase             TEXT NOT NULL CHECK (phase IN ('planning', 'review')),
			provider          TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
			profile           TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			effort            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			status            TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'resolved')),
			resolved_provider TEXT NOT NULL DEFAULT ''
				CHECK (resolved_provider IN ('', 'claude', 'codex')),
			resolved_profile  TEXT NOT NULL DEFAULT '',
			resolved_model    TEXT NOT NULL DEFAULT '',
			resolved_effort   TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at       TEXT
		);

		CREATE INDEX idx_goal_rate_limits_goal ON goal_rate_limits(goal_id, created_at DESC);
		CREATE INDEX idx_goal_rate_limits_status ON goal_rate_limits(status);`,
	},
}

// migrate applies every migration whose version has not yet been recorded. Each
// migration plus its bookkeeping insert runs in a single transaction, so a crash
// mid-migration leaves the schema consistent (no half-applied versions).
//
// Migrations run on one dedicated connection with foreign_keys OFF — SQLite's
// documented table-rebuild procedure. With enforcement ON, DROP TABLE on a
// parent performs an implicit DELETE FROM that fires ON DELETE CASCADE actions
// (e.g. rebuilding sessions would wipe messages). A foreign_key_check afterwards
// guarantees no migration left orphaned child rows; every other pooled
// connection keeps enforcement ON via the DSN pragma.
func (s *Store) migrate() error {
	// Ensure the bookkeeping table exists before we query it (the first migration
	// creates it; running its DDL up front is harmless and idempotent).
	if _, err := s.db.Exec(migrations[0].sql); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	applied, err := s.appliedVersions()
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for migration: %w", err)
	}
	// Restore enforcement before the connection returns to the pool.
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`) }()

	ran := false
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		ran = true
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (?, ?)`,
			m.version, m.name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}

	if ran {
		rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			return fmt.Errorf("post-migration foreign_key_check: %w", err)
		}
		defer rows.Close()
		if rows.Next() {
			var table, parent string
			var rowid, fkid any
			_ = rows.Scan(&table, &rowid, &parent, &fkid)
			return fmt.Errorf("post-migration foreign_key_check: table %q has rows violating its foreign key to %q", table, parent)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("post-migration foreign_key_check: %w", err)
		}
	}
	return nil
}

func (s *Store) appliedVersions() (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
