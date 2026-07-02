package store

import "fmt"

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
}

// migrate applies every migration whose version has not yet been recorded. Each
// migration plus its bookkeeping insert runs in a single transaction, so a crash
// mid-migration leaves the schema consistent (no half-applied versions).
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

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		tx, err := s.db.Begin()
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
