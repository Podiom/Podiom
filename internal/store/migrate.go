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
	{
		version: 19,
		name:    "message_reasoning_kind",
		sql: `CREATE TABLE messages_new (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
				seq        INTEGER NOT NULL,
				role       TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
				kind       TEXT NOT NULL DEFAULT 'message' CHECK (kind IN ('message', 'error', 'reasoning')),
				content    TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				UNIQUE (session_id, seq)
			);

			INSERT INTO messages_new (id, session_id, seq, role, kind, content, created_at)
			SELECT id, session_id, seq, role, kind, content, created_at FROM messages;

			DROP TABLE messages;
			ALTER TABLE messages_new RENAME TO messages;
			CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);`,
	},
	{
		version: 20,
		name:    "goal_user_feedback_events",
		sql: `DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TABLE goal_events_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'user_feedback', 'access_requested', 'access_decided',
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
		END;`,
	},
	{
		version: 21,
		name:    "goal_yolo_tool_audit",
		// Goals run the whole chain in yolo mode, so tool calls no longer pass
		// through the permission broker. 'tool_use' goal events record every
		// provider tool invocation observed in the stream (the goal audit trail).
		// tasks.goal_id links a roadmap task's runs back to its goal so those runs
		// are forced yolo and their tool calls land on the goal timeline; it is a
		// plain column (no FK) because deleting a goal must not cascade-delete its
		// tasks.
		sql: `DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TABLE goal_events_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'user_feedback', 'access_requested', 'access_decided',
				'status_change', 'completion_proposed', 'rate_limited', 'rate_limit_resolved', 'tool_use')),
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

		ALTER TABLE tasks ADD COLUMN goal_id TEXT NOT NULL DEFAULT '';
		CREATE INDEX idx_tasks_goal ON tasks(goal_id);`,
	},
	{
		version: 22,
		name:    "agent_questions",
		// Agents running unattended (goal planning/reviews, scheduled runs) can now
		// ask the user a question with selectable answers. Because there is no human
		// in the loop, the question is recorded here (defer-and-resume) rather than
		// blocking the run; the answer is fed into the next run. 'question_asked' /
		// 'question_answered' goal events surface the exchange on the goal timeline,
		// so goal_events' CHECK constraint needs the table-rebuild dance.
		sql: `DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TABLE goal_events_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'user_feedback', 'access_requested', 'access_decided',
				'status_change', 'completion_proposed', 'rate_limited', 'rate_limit_resolved', 'tool_use',
				'question_asked', 'question_answered')),
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

		CREATE TABLE agent_questions (
			id             TEXT PRIMARY KEY,
			origin         TEXT NOT NULL CHECK (origin IN ('goal', 'schedule')),
			ref_id         TEXT NOT NULL,
			session_id     TEXT NOT NULL DEFAULT '',
			questions_json TEXT NOT NULL DEFAULT '[]',
			status         TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'answered', 'dismissed')),
			answers_json   TEXT NOT NULL DEFAULT '{}',
			created_at     TEXT NOT NULL DEFAULT (datetime('now')),
			answered_at    TEXT
		);

		CREATE INDEX idx_agent_questions_ref ON agent_questions(origin, ref_id, status);`,
	},
	{
		version: 23,
		name:    "editable_unread_goal_feedback",
		sql: `DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TRIGGER goal_events_append_only
		BEFORE UPDATE ON goal_events
		WHEN NOT (
			OLD.kind = 'user_feedback'
			AND NEW.id = OLD.id
			AND NEW.kind = OLD.kind
			AND NEW.goal_id = OLD.goal_id
			AND COALESCE(NEW.session_id, '') = COALESCE(OLD.session_id, '')
			AND NEW.payload_json = OLD.payload_json
			AND NEW.created_at = OLD.created_at
		)
		BEGIN
			SELECT RAISE(ABORT, 'goal events are append-only');
		END;`,
	},
	{
		version: 24,
		name:    "goal_runs_and_lead_session",
		sql: `ALTER TABLE goals ADD COLUMN lead_session_id TEXT NOT NULL DEFAULT '';

		CREATE TABLE goal_runs (
			id              TEXT PRIMARY KEY,
			goal_id         TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id      TEXT NOT NULL DEFAULT '',
			turn_message_id INTEGER,
			kind            TEXT NOT NULL CHECK (kind IN ('planning', 'review', 'task', 'schedule', 'conversation')),
			agent_name      TEXT NOT NULL DEFAULT '',
			source_id       TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'running'
				CHECK (status IN ('running', 'succeeded', 'failed', 'rate_limited', 'interrupted')),
			legacy          INTEGER NOT NULL DEFAULT 0,
			error           TEXT NOT NULL DEFAULT '',
			started_at      TEXT NOT NULL DEFAULT (datetime('now')),
			finished_at     TEXT
		);

		CREATE INDEX idx_goal_runs_goal ON goal_runs(goal_id, started_at DESC);
		CREATE INDEX idx_goal_runs_session ON goal_runs(session_id, started_at DESC);
		CREATE UNIQUE INDEX idx_goal_runs_running_session ON goal_runs(session_id)
			WHERE status = 'running' AND session_id != '';
		CREATE UNIQUE INDEX idx_goal_runs_running_lead ON goal_runs(goal_id)
			WHERE status = 'running' AND kind IN ('planning', 'review');

		INSERT INTO goal_runs
			(id, goal_id, session_id, turn_message_id, kind, agent_name, source_id, status, legacy, started_at, finished_at)
		SELECT
			'legacy:' || ge.goal_id || ':' || ge.session_id,
			ge.goal_id,
			ge.session_id,
			CASE WHEN (SELECT COUNT(*) FROM messages m WHERE m.session_id = ge.session_id AND m.role = 'user') = 1
				THEN (SELECT MIN(m.id) FROM messages m WHERE m.session_id = ge.session_id AND m.role = 'user')
				ELSE NULL END,
			CASE
				WHEN MAX(CASE WHEN ge.kind = 'planning_started' THEN 1 ELSE 0 END) = 1 THEN 'planning'
				WHEN MAX(CASE WHEN ge.kind = 'review_started' THEN 1 ELSE 0 END) = 1 THEN 'review'
				WHEN COALESCE(s.origin, '') = 'roadmap' THEN 'task'
				WHEN COALESCE(s.origin, '') = 'schedule' THEN 'schedule'
				ELSE 'conversation'
			END,
			COALESCE(s.agent_name, ''),
			CASE WHEN COALESCE(s.origin, '') = 'roadmap' THEN COALESCE(s.task_id, '')
				WHEN COALESCE(s.origin, '') = 'schedule' THEN COALESCE(s.schedule_id, '')
				ELSE '' END,
			CASE WHEN MAX(CASE WHEN ge.kind = 'rate_limited' THEN 1 ELSE 0 END) = 1 THEN 'rate_limited' ELSE 'succeeded' END,
			1,
			MIN(ge.created_at),
			MAX(ge.created_at)
		FROM goal_events ge
		LEFT JOIN sessions s ON s.id = ge.session_id
		WHERE COALESCE(ge.session_id, '') != ''
			AND ge.kind NOT IN ('created', 'user_feedback', 'access_decided', 'status_change', 'rate_limit_resolved', 'question_answered')
		GROUP BY ge.goal_id, ge.session_id;

		DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TABLE goal_events_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			run_id       TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'user_feedback', 'access_requested', 'access_decided',
				'status_change', 'completion_proposed', 'rate_limited', 'rate_limit_resolved', 'tool_use',
				'question_asked', 'question_answered')),
			body         TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL DEFAULT (datetime('now'))
		);

		INSERT INTO goal_events_new (id, goal_id, session_id, run_id, kind, body, payload_json, created_at)
		SELECT id, goal_id, session_id,
			CASE WHEN COALESCE(session_id, '') != ''
				AND kind NOT IN ('created', 'user_feedback', 'access_decided', 'status_change', 'rate_limit_resolved', 'question_answered')
				THEN 'legacy:' || goal_id || ':' || session_id ELSE NULL END,
			kind, body, payload_json, created_at
		FROM goal_events;

		DROP TABLE goal_events;
		ALTER TABLE goal_events_new RENAME TO goal_events;
		CREATE INDEX idx_goal_events_goal ON goal_events(goal_id, id DESC);
		CREATE INDEX idx_goal_events_run ON goal_events(run_id, id);

		CREATE TRIGGER goal_events_append_only
		BEFORE UPDATE ON goal_events
		WHEN NOT (
			OLD.kind = 'user_feedback'
			AND NEW.id = OLD.id
			AND NEW.kind = OLD.kind
			AND NEW.goal_id = OLD.goal_id
			AND COALESCE(NEW.session_id, '') = COALESCE(OLD.session_id, '')
			AND COALESCE(NEW.run_id, '') = COALESCE(OLD.run_id, '')
			AND NEW.payload_json = OLD.payload_json
			AND NEW.created_at = OLD.created_at
		)
		BEGIN
			SELECT RAISE(ABORT, 'goal events are append-only');
		END;

		CREATE TABLE goal_rate_limits_new (
			id                TEXT PRIMARY KEY,
			goal_id           TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id        TEXT NOT NULL,
			run_id            TEXT NOT NULL UNIQUE,
			phase             TEXT NOT NULL CHECK (phase IN ('planning', 'review')),
			provider          TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
			profile           TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			effort            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolved')),
			resolved_provider TEXT NOT NULL DEFAULT '' CHECK (resolved_provider IN ('', 'claude', 'codex')),
			resolved_profile  TEXT NOT NULL DEFAULT '',
			resolved_model    TEXT NOT NULL DEFAULT '',
			resolved_effort   TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at       TEXT
		);

		INSERT INTO goal_rate_limits_new
			(id, goal_id, session_id, run_id, phase, provider, profile, model, effort, error, status,
			 resolved_provider, resolved_profile, resolved_model, resolved_effort, created_at, resolved_at)
		SELECT id, goal_id, session_id,
			COALESCE((SELECT gr.id FROM goal_runs gr WHERE gr.goal_id = goal_rate_limits.goal_id
				AND gr.session_id = goal_rate_limits.session_id ORDER BY gr.started_at DESC LIMIT 1), 'legacy-rate:' || id),
			phase, provider, profile, model, effort, error, status, resolved_provider, resolved_profile,
			resolved_model, resolved_effort, created_at, resolved_at
		FROM goal_rate_limits;

		DROP TABLE goal_rate_limits;
		ALTER TABLE goal_rate_limits_new RENAME TO goal_rate_limits;
		CREATE INDEX idx_goal_rate_limits_goal ON goal_rate_limits(goal_id, created_at DESC);
		CREATE INDEX idx_goal_rate_limits_status ON goal_rate_limits(status);

		UPDATE goals SET lead_session_id = COALESCE((
			SELECT s.id FROM sessions s
			WHERE s.goal_id = goals.id AND s.origin = 'goal' AND s.agent_name = goals.lead_agent
			ORDER BY s.created_at DESC, s.id DESC LIMIT 1
		), '');`,
	},
	{
		// Provider validity is enforced in Go against the config provider
		// registry (config.KnownProvider); baking the provider list into CHECK
		// constraints made every new provider a schema migration. This rebuilds
		// the five affected tables identically minus the provider/
		// resolved_provider CHECK clauses. All other constraints, indexes, and
		// triggers are recreated verbatim.
		version: 25,
		name:    "drop_provider_checks",
		sql: `CREATE TABLE agents_new (
			name            TEXT PRIMARY KEY,
			provider        TEXT NOT NULL,
			profile         TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			effort          TEXT NOT NULL DEFAULT '',
			permission_mode TEXT NOT NULL CHECK (permission_mode IN ('approve', 'yolo')),
			fallback_json   TEXT NOT NULL DEFAULT '[]',
			mcp_config      TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
			mcp_servers_json TEXT NOT NULL DEFAULT '[]',
			avatar_updated_at TEXT NOT NULL DEFAULT ''
		);

		INSERT INTO agents_new
			(name, provider, profile, model, effort, permission_mode, fallback_json, mcp_config,
			 created_at, updated_at, mcp_servers_json, avatar_updated_at)
		SELECT name, provider, profile, model, effort, permission_mode, fallback_json, mcp_config,
			 created_at, updated_at, mcp_servers_json, avatar_updated_at
		FROM agents;

		DROP TABLE agents;
		ALTER TABLE agents_new RENAME TO agents;

		CREATE TABLE sessions_new (
			id                TEXT PRIMARY KEY,
			agent_name        TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE RESTRICT,
			provider          TEXT NOT NULL,
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
			goal_id           TEXT,
			usage_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_output_tokens INTEGER NOT NULL DEFAULT 0,
			usage_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			usage_cache_write_tokens INTEGER NOT NULL DEFAULT 0
		);

		INSERT INTO sessions_new
			(id, agent_name, provider, profile, model, effort, permission_mode, origin, schedule_id,
			 run_id, rolling_summary, provider_handle, created_at, updated_at, name, description,
			 auto_named, task_id, project_id, dreamed_at, plan_state, plan_explicit, plan_file_path,
			 plan_markdown, plan_submitted_at, plan_updated_at, context_tokens, context_limit, goal_id,
			 usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens)
		SELECT id, agent_name, provider, profile, model, effort, permission_mode, origin, schedule_id,
			 run_id, rolling_summary, provider_handle, created_at, updated_at, name, description,
			 auto_named, task_id, project_id, dreamed_at, plan_state, plan_explicit, plan_file_path,
			 plan_markdown, plan_submitted_at, plan_updated_at, context_tokens, context_limit, goal_id,
			 usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens
		FROM sessions;

		DROP TABLE sessions;
		ALTER TABLE sessions_new RENAME TO sessions;

		CREATE INDEX idx_sessions_agent_name ON sessions(agent_name);
		CREATE INDEX idx_sessions_dreamed ON sessions(agent_name, dreamed_at);
		CREATE INDEX idx_sessions_goal_id ON sessions(goal_id);
		CREATE INDEX idx_sessions_origin ON sessions(origin);
		CREATE INDEX idx_sessions_plan_state ON sessions(plan_state);
		CREATE INDEX idx_sessions_project_id ON sessions(project_id);
		CREATE INDEX idx_sessions_schedule_id ON sessions(schedule_id);
		CREATE INDEX idx_sessions_task_id ON sessions(task_id);

		CREATE TRIGGER sessions_origin_immutable
		BEFORE UPDATE OF origin ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'session origin is immutable');
		END;

		CREATE TABLE tasks_new (
			id             TEXT PRIMARY KEY,
			project_id     TEXT NOT NULL DEFAULT '',
			title          TEXT NOT NULL,
			body           TEXT NOT NULL DEFAULT '',
			assigned_agent TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL CHECK (status IN ('backlog', 'in_progress', 'review', 'done')),
			pickup_at      TEXT,
			created_at     TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
			plan_required  INTEGER NOT NULL DEFAULT 0,
			provider       TEXT NOT NULL DEFAULT '',
			profile        TEXT NOT NULL DEFAULT '',
			model          TEXT NOT NULL DEFAULT '',
			effort         TEXT NOT NULL DEFAULT '',
			goal_id        TEXT NOT NULL DEFAULT ''
		);

		INSERT INTO tasks_new
			(id, project_id, title, body, assigned_agent, status, pickup_at, created_at, updated_at,
			 plan_required, provider, profile, model, effort, goal_id)
		SELECT id, project_id, title, body, assigned_agent, status, pickup_at, created_at, updated_at,
			 plan_required, provider, profile, model, effort, goal_id
		FROM tasks;

		DROP TABLE tasks;
		ALTER TABLE tasks_new RENAME TO tasks;

		CREATE INDEX idx_tasks_goal ON tasks(goal_id);
		CREATE INDEX idx_tasks_project ON tasks(project_id);
		CREATE INDEX idx_tasks_status ON tasks(status);

		CREATE TABLE goals_new (
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
			updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
			provider         TEXT NOT NULL DEFAULT '',
			profile          TEXT NOT NULL DEFAULT '',
			model            TEXT NOT NULL DEFAULT '',
			effort           TEXT NOT NULL DEFAULT '',
			lead_session_id  TEXT NOT NULL DEFAULT ''
		);

		INSERT INTO goals_new
			(id, title, description, success_criteria, metrics_json, review_every, lead_agent,
			 project_id, status, next_review_at, closing_report, created_at, updated_at,
			 provider, profile, model, effort, lead_session_id)
		SELECT id, title, description, success_criteria, metrics_json, review_every, lead_agent,
			 project_id, status, next_review_at, closing_report, created_at, updated_at,
			 provider, profile, model, effort, lead_session_id
		FROM goals;

		DROP TABLE goals;
		ALTER TABLE goals_new RENAME TO goals;

		CREATE INDEX idx_goals_due_review ON goals(status, next_review_at);
		CREATE INDEX idx_goals_status ON goals(status);

		CREATE TABLE goal_rate_limits_new (
			id                TEXT PRIMARY KEY,
			goal_id           TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id        TEXT NOT NULL,
			run_id            TEXT NOT NULL UNIQUE,
			phase             TEXT NOT NULL CHECK (phase IN ('planning', 'review')),
			provider          TEXT NOT NULL,
			profile           TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			effort            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolved')),
			resolved_provider TEXT NOT NULL DEFAULT '',
			resolved_profile  TEXT NOT NULL DEFAULT '',
			resolved_model    TEXT NOT NULL DEFAULT '',
			resolved_effort   TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at       TEXT
		);

		INSERT INTO goal_rate_limits_new
			(id, goal_id, session_id, run_id, phase, provider, profile, model, effort, error, status,
			 resolved_provider, resolved_profile, resolved_model, resolved_effort, created_at, resolved_at)
		SELECT id, goal_id, session_id, run_id, phase, provider, profile, model, effort, error, status,
			 resolved_provider, resolved_profile, resolved_model, resolved_effort, created_at, resolved_at
		FROM goal_rate_limits;

		DROP TABLE goal_rate_limits;
		ALTER TABLE goal_rate_limits_new RENAME TO goal_rate_limits;

		CREATE INDEX idx_goal_rate_limits_goal ON goal_rate_limits(goal_id, created_at DESC);
		CREATE INDEX idx_goal_rate_limits_status ON goal_rate_limits(status);`,
	},
	{
		version: 26,
		name:    "interview_origin",
		sql: `CREATE TABLE sessions_new (
			id                TEXT PRIMARY KEY,
			agent_name        TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE RESTRICT,
			provider          TEXT NOT NULL,
			profile           TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			effort            TEXT NOT NULL DEFAULT '',
			permission_mode   TEXT NOT NULL CHECK (permission_mode IN ('approve', 'yolo')),
			origin            TEXT NOT NULL CHECK (origin IN ('web', 'cli', 'onboarding', 'interview', 'schedule', 'roadmap', 'goal')),
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
			goal_id           TEXT,
			usage_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_output_tokens INTEGER NOT NULL DEFAULT 0,
			usage_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			usage_cache_write_tokens INTEGER NOT NULL DEFAULT 0
		);

		INSERT INTO sessions_new
			(id, agent_name, provider, profile, model, effort, permission_mode, origin, schedule_id,
			 run_id, rolling_summary, provider_handle, created_at, updated_at, name, description,
			 auto_named, task_id, project_id, dreamed_at, plan_state, plan_explicit, plan_file_path,
			 plan_markdown, plan_submitted_at, plan_updated_at, context_tokens, context_limit, goal_id,
			 usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens)
		SELECT id, agent_name, provider, profile, model, effort, permission_mode, origin, schedule_id,
			 run_id, rolling_summary, provider_handle, created_at, updated_at, name, description,
			 auto_named, task_id, project_id, dreamed_at, plan_state, plan_explicit, plan_file_path,
			 plan_markdown, plan_submitted_at, plan_updated_at, context_tokens, context_limit, goal_id,
			 usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens
		FROM sessions;

		DROP TABLE sessions;
		ALTER TABLE sessions_new RENAME TO sessions;

		CREATE INDEX idx_sessions_agent_name ON sessions(agent_name);
		CREATE INDEX idx_sessions_dreamed ON sessions(agent_name, dreamed_at);
		CREATE INDEX idx_sessions_goal_id ON sessions(goal_id);
		CREATE INDEX idx_sessions_origin ON sessions(origin);
		CREATE INDEX idx_sessions_plan_state ON sessions(plan_state);
		CREATE INDEX idx_sessions_project_id ON sessions(project_id);
		CREATE INDEX idx_sessions_schedule_id ON sessions(schedule_id);
		CREATE INDEX idx_sessions_task_id ON sessions(task_id);

		CREATE TRIGGER sessions_origin_immutable
		BEFORE UPDATE OF origin ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'session origin is immutable');
		END;`,
	},
	{
		version: 27,
		name:    "message_photo_attachments",
		sql: `CREATE TABLE attachments (
			id         TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			message_id INTEGER REFERENCES messages(id) ON DELETE CASCADE,
			position   INTEGER NOT NULL DEFAULT 0,
			name       TEXT NOT NULL,
			mime_type  TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			width      INTEGER NOT NULL,
			height     INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX idx_attachments_session ON attachments(session_id, created_at);
		CREATE INDEX idx_attachments_message ON attachments(message_id, position);`,
	},
	{
		// Permission-mode validity is enforced in Go against the config registry
		// (config.KnownPermission); baking the mode list into CHECK constraints
		// made every new mode a schema migration — the same reasoning that
		// dropped the provider CHECKs in migration 25. This rebuilds agents and
		// sessions identically minus the permission_mode CHECK clauses, which is
		// what admits the new 'auto' mode. All other constraints, indexes, and
		// triggers are recreated verbatim.
		version: 28,
		name:    "drop_permission_mode_checks",
		sql: `CREATE TABLE agents_new (
			name            TEXT PRIMARY KEY,
			provider        TEXT NOT NULL,
			profile         TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			effort          TEXT NOT NULL DEFAULT '',
			permission_mode TEXT NOT NULL,
			fallback_json   TEXT NOT NULL DEFAULT '[]',
			mcp_config      TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
			mcp_servers_json TEXT NOT NULL DEFAULT '[]',
			avatar_updated_at TEXT NOT NULL DEFAULT ''
		);

		INSERT INTO agents_new
			(name, provider, profile, model, effort, permission_mode, fallback_json, mcp_config,
			 created_at, updated_at, mcp_servers_json, avatar_updated_at)
		SELECT name, provider, profile, model, effort, permission_mode, fallback_json, mcp_config,
			 created_at, updated_at, mcp_servers_json, avatar_updated_at
		FROM agents;

		DROP TABLE agents;
		ALTER TABLE agents_new RENAME TO agents;

		CREATE TABLE sessions_new (
			id                TEXT PRIMARY KEY,
			agent_name        TEXT NOT NULL REFERENCES agents(name) ON UPDATE CASCADE ON DELETE RESTRICT,
			provider          TEXT NOT NULL,
			profile           TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			effort            TEXT NOT NULL DEFAULT '',
			permission_mode   TEXT NOT NULL,
			origin            TEXT NOT NULL CHECK (origin IN ('web', 'cli', 'onboarding', 'interview', 'schedule', 'roadmap', 'goal')),
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
			goal_id           TEXT,
			usage_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_output_tokens INTEGER NOT NULL DEFAULT 0,
			usage_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			usage_cache_write_tokens INTEGER NOT NULL DEFAULT 0
		);

		INSERT INTO sessions_new
			(id, agent_name, provider, profile, model, effort, permission_mode, origin, schedule_id,
			 run_id, rolling_summary, provider_handle, created_at, updated_at, name, description,
			 auto_named, task_id, project_id, dreamed_at, plan_state, plan_explicit, plan_file_path,
			 plan_markdown, plan_submitted_at, plan_updated_at, context_tokens, context_limit, goal_id,
			 usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens)
		SELECT id, agent_name, provider, profile, model, effort, permission_mode, origin, schedule_id,
			 run_id, rolling_summary, provider_handle, created_at, updated_at, name, description,
			 auto_named, task_id, project_id, dreamed_at, plan_state, plan_explicit, plan_file_path,
			 plan_markdown, plan_submitted_at, plan_updated_at, context_tokens, context_limit, goal_id,
			 usage_input_tokens, usage_output_tokens, usage_cache_read_tokens, usage_cache_write_tokens
		FROM sessions;

		DROP TABLE sessions;
		ALTER TABLE sessions_new RENAME TO sessions;

		CREATE INDEX idx_sessions_agent_name ON sessions(agent_name);
		CREATE INDEX idx_sessions_dreamed ON sessions(agent_name, dreamed_at);
		CREATE INDEX idx_sessions_goal_id ON sessions(goal_id);
		CREATE INDEX idx_sessions_origin ON sessions(origin);
		CREATE INDEX idx_sessions_plan_state ON sessions(plan_state);
		CREATE INDEX idx_sessions_project_id ON sessions(project_id);
		CREATE INDEX idx_sessions_schedule_id ON sessions(schedule_id);
		CREATE INDEX idx_sessions_task_id ON sessions(task_id);

		CREATE TRIGGER sessions_origin_immutable
		BEFORE UPDATE OF origin ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'session origin is immutable');
		END;`,
	},
	{
		// The lead agent's stated next step: what it will do before the next
		// review, and why. Plain column adds — no CHECK constraint is involved,
		// so no table rebuild. next_step_at is nullable (the next_review_at
		// precedent) because "never stated" and "stated at the epoch" must not
		// look alike.
		version: 29,
		name:    "goal_next_step",
		sql: `ALTER TABLE goals ADD COLUMN next_step     TEXT NOT NULL DEFAULT '';
		ALTER TABLE goals ADD COLUMN next_step_why TEXT NOT NULL DEFAULT '';
		ALTER TABLE goals ADD COLUMN next_step_at  TEXT;`,
	},
	{
		// A turn now stores its interim assistant prose ("narration") as its own
		// rows instead of gluing it onto the answer, so the kind CHECK has to
		// admit one more value. Widened rather than dropped: unlike providers and
		// permission modes there is no Go validator for message kinds, so this
		// constraint is the only guard. Rebuild preserves id — attachments
		// reference messages(id) — and deliberately leaves attachments alone;
		// with foreign_keys OFF a RENAME does not rewrite its REFERENCES clause.
		version: 30,
		name:    "message_narration_kind",
		sql: `CREATE TABLE messages_new (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
				seq        INTEGER NOT NULL,
				role       TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
				kind       TEXT NOT NULL DEFAULT 'message' CHECK (kind IN ('message', 'error', 'reasoning', 'narration')),
				content    TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				UNIQUE (session_id, seq)
			);

			INSERT INTO messages_new (id, session_id, seq, role, kind, content, created_at)
			SELECT id, session_id, seq, role, kind, content, created_at FROM messages;

			DROP TABLE messages;
			ALTER TABLE messages_new RENAME TO messages;
			CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);`,
	},
	{
		// Work a goal's agent decided only the user can do (post from a personal
		// account, sign something, make a call) had no home: access requests are
		// capability grants, podiom_ask_user is a decision that pauses reviews, and
		// next_step is the agent's own move. Action items are that fourth channel —
		// an instruction from the agent and a verdict from the user — and because
		// they surface on the goal timeline, goal_events' CHECK needs the
		// table-rebuild dance again (migration 24's, trigger and both indexes
		// included).
		//
		// The FK cascade is the whole cleanup story: unlike agent_questions (which
		// keys on origin/ref_id and needs DeleteAgentQuestions), deleting a goal
		// takes its action items with it.
		version: 31,
		name:    "goal_action_items",
		sql: `DROP TRIGGER IF EXISTS goal_events_append_only;

		CREATE TABLE goal_events_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT,
			run_id       TEXT,
			kind         TEXT NOT NULL CHECK (kind IN ('created', 'planning_started', 'review_started',
				'progress', 'metric_update', 'plan_change', 'user_feedback', 'access_requested', 'access_decided',
				'status_change', 'completion_proposed', 'rate_limited', 'rate_limit_resolved', 'tool_use',
				'question_asked', 'question_answered', 'action_requested', 'action_responded')),
			body         TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL DEFAULT (datetime('now'))
		);

		INSERT INTO goal_events_new (id, goal_id, session_id, run_id, kind, body, payload_json, created_at)
		SELECT id, goal_id, session_id, run_id, kind, body, payload_json, created_at FROM goal_events;

		DROP TABLE goal_events;
		ALTER TABLE goal_events_new RENAME TO goal_events;
		CREATE INDEX idx_goal_events_goal ON goal_events(goal_id, id DESC);
		CREATE INDEX idx_goal_events_run ON goal_events(run_id, id);

		CREATE TRIGGER goal_events_append_only
		BEFORE UPDATE ON goal_events
		WHEN NOT (
			OLD.kind = 'user_feedback'
			AND NEW.id = OLD.id
			AND NEW.kind = OLD.kind
			AND NEW.goal_id = OLD.goal_id
			AND COALESCE(NEW.session_id, '') = COALESCE(OLD.session_id, '')
			AND COALESCE(NEW.run_id, '') = COALESCE(OLD.run_id, '')
			AND NEW.payload_json = OLD.payload_json
			AND NEW.created_at = OLD.created_at
		)
		BEGIN
			SELECT RAISE(ABORT, 'goal events are append-only');
		END;

		CREATE TABLE goal_action_items (
			id           TEXT PRIMARY KEY,
			goal_id      TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
			session_id   TEXT NOT NULL DEFAULT '',
			run_id       TEXT NOT NULL DEFAULT '',
			agent_name   TEXT NOT NULL DEFAULT '',
			title        TEXT NOT NULL,
			instructions TEXT NOT NULL DEFAULT '',
			why          TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'open'
				CHECK (status IN ('open', 'done', 'blocked', 'declined')),
			response     TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL DEFAULT (datetime('now')),
			responded_at TEXT
		);

		CREATE INDEX idx_goal_action_items_goal ON goal_action_items(goal_id, status, created_at DESC);`,
	},
	{
		version: 32,
		name:    "session_source_control_warning",
		sql: `ALTER TABLE sessions
			ADD COLUMN source_control_warning TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 33,
		name:    "task_created_by",
		// Records which agent session authored a task, so work an agent decided to
		// create is traceable back to the conversation it came out of. Empty means
		// the user created it in the web UI or CLI, which is why no backfill is
		// needed: every pre-existing task is user-authored.
		sql: `ALTER TABLE tasks ADD COLUMN created_by_session TEXT NOT NULL DEFAULT '';
		ALTER TABLE tasks ADD COLUMN created_by_agent TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 34,
		name:    "workspace_file_snapshots",
		// These rows intentionally do not reference sessions. A snapshot link can
		// be embedded in a task, schedule, goal, or other artifact that outlives
		// the session which created it.
		sql: `CREATE TABLE workspace_file_snapshots (
			id                 TEXT PRIMARY KEY,
			creator_session_id TEXT NOT NULL DEFAULT '',
			creator_agent      TEXT NOT NULL DEFAULT '',
			project_id         TEXT NOT NULL DEFAULT '',
			source_path        TEXT NOT NULL,
			filename           TEXT NOT NULL,
			label              TEXT NOT NULL,
			content            TEXT NOT NULL,
			size_bytes         INTEGER NOT NULL,
			created_at         TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX idx_workspace_file_snapshots_creator
			ON workspace_file_snapshots(creator_session_id, created_at);`,
	},
	{
		version: 35,
		name:    "schedule_runs_webhook_trigger",
		// A schedule can now also be fired by an external POST to its webhook
		// endpoint. Migration 4's CHECK only permits 'cron' and 'manual', and
		// SQLite cannot alter a CHECK, so the table is rebuilt with the widened
		// constraint. Existing rows carry over untouched.
		sql: `CREATE TABLE schedule_runs_new (
			id            TEXT PRIMARY KEY,
			schedule_name TEXT NOT NULL,
			session_id    TEXT REFERENCES sessions(id) ON DELETE SET NULL,
			trigger       TEXT NOT NULL CHECK (trigger IN ('cron', 'manual', 'webhook')),
			status        TEXT NOT NULL CHECK (status IN ('running', 'success', 'error')),
			error         TEXT NOT NULL DEFAULT '',
			started_at    TEXT NOT NULL DEFAULT (datetime('now')),
			finished_at   TEXT
		);

		INSERT INTO schedule_runs_new (id, schedule_name, session_id, trigger, status, error, started_at, finished_at)
			SELECT id, schedule_name, session_id, trigger, status, error, started_at, finished_at FROM schedule_runs;

		DROP TABLE schedule_runs;
		ALTER TABLE schedule_runs_new RENAME TO schedule_runs;

		CREATE INDEX idx_schedule_runs_name ON schedule_runs(schedule_name, started_at DESC);`,
	},
	{
		version: 36,
		name:    "session_archived_at",
		// Set when an unattended run finishes, when a goal reaches a terminal
		// state, or when the user archives a session by hand. Empty means the
		// session is live and belongs in the main list, which is why no backfill
		// is needed: every pre-existing session stays visible exactly as it is.
		sql: `ALTER TABLE sessions ADD COLUMN archived_at TEXT NOT NULL DEFAULT '';`,
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
