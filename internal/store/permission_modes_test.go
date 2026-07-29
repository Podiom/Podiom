package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPermissionModeChecksDropped verifies migration 28: permission-mode
// validity is a Go concern (config.KnownPermission), so the new 'auto' mode —
// and any future one — must insert cleanly into agents and sessions, while the
// other constraints and triggers survive the rebuild.
func TestPermissionModeChecksDropped(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	for _, mode := range []string{"approve", "auto", "yolo", "future-mode"} {
		agent := "agent-" + mode
		session := "session-" + mode
		mustExec(`INSERT INTO agents (name, provider, permission_mode) VALUES (?, 'claude', ?)`, agent, mode)
		mustExec(`INSERT INTO sessions (id, agent_name, provider, permission_mode, origin)
			VALUES (?, ?, 'claude', ?, 'web')`, session, agent, mode)
	}

	// Sibling CHECKs on the rebuilt sessions table must still hold.
	if _, err := st.db.Exec(`INSERT INTO sessions (id, agent_name, provider, permission_mode, origin)
		VALUES ('bad-origin', 'agent-auto', 'claude', 'auto', 'nonsense')`); err == nil {
		t.Fatal("origin CHECK constraint no longer enforced after rebuild")
	}
	if _, err := st.db.Exec(`INSERT INTO sessions (id, agent_name, provider, permission_mode, origin, plan_state)
		VALUES ('bad-plan', 'agent-auto', 'claude', 'auto', 'web', 'nonsense')`); err == nil {
		t.Fatal("plan_state CHECK constraint no longer enforced after rebuild")
	}

	// Trigger must survive the rebuild.
	if _, err := st.db.Exec(`UPDATE sessions SET origin = 'cli' WHERE id = 'session-auto'`); err == nil {
		t.Fatal("sessions_origin_immutable trigger lost in rebuild")
	} else if !strings.Contains(err.Error(), "session origin is immutable") {
		t.Fatalf("unexpected trigger error: %v", err)
	}
}

// TestPermissionModeRowsSurviveMigration checks the rebuild preserves existing
// rows: an approve agent/session written before the migration must come out the
// other side unchanged, since migration 28 copies rather than defaults.
func TestPermissionModeRowsSurviveMigration(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.db.Exec(`INSERT INTO agents (name, provider, permission_mode, model)
		VALUES ('kept', 'claude', 'yolo', 'claude-opus-4-8')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO sessions (id, agent_name, provider, permission_mode, origin, name)
		VALUES ('kept-session', 'kept', 'claude', 'yolo', 'web', 'Kept')`); err != nil {
		t.Fatal(err)
	}

	var mode, model string
	if err := st.db.QueryRow(`SELECT permission_mode, model FROM agents WHERE name = 'kept'`).Scan(&mode, &model); err != nil {
		t.Fatal(err)
	}
	if mode != "yolo" || model != "claude-opus-4-8" {
		t.Fatalf("agent row altered: mode=%q model=%q", mode, model)
	}

	var sessMode, sessName string
	if err := st.db.QueryRow(`SELECT permission_mode, name FROM sessions WHERE id = 'kept-session'`).Scan(&sessMode, &sessName); err != nil {
		t.Fatal(err)
	}
	if sessMode != "yolo" || sessName != "Kept" {
		t.Fatalf("session row altered: mode=%q name=%q", sessMode, sessName)
	}
}
