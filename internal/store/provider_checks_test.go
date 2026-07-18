package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestProviderChecksDropped verifies migration 25: provider validity is a Go
// concern (config.KnownProvider), so a row with a not-yet-registered provider
// must insert cleanly into every table that used to CHECK-constrain it, while
// the other constraints and triggers survive the rebuild.
func TestProviderChecksDropped(t *testing.T) {
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

	mustExec(`INSERT INTO agents (name, provider, permission_mode) VALUES ('fut-agent', 'future', 'approve')`)
	mustExec(`INSERT INTO sessions (id, agent_name, provider, permission_mode, origin)
		VALUES ('fut-session', 'fut-agent', 'future', 'approve', 'web')`)
	mustExec(`INSERT INTO tasks (id, title, status, provider) VALUES ('fut-task', 'T', 'backlog', 'future')`)
	mustExec(`INSERT INTO goals (id, title, lead_agent, provider) VALUES ('fut-goal', 'G', 'fut-agent', 'future')`)
	mustExec(`INSERT INTO goal_rate_limits (id, goal_id, session_id, run_id, phase, provider, resolved_provider)
		VALUES ('fut-rl', 'fut-goal', 'fut-session', 'fut-run', 'planning', 'future', 'future')`)

	// Non-provider CHECKs must still hold.
	if _, err := st.db.Exec(`INSERT INTO sessions (id, agent_name, provider, permission_mode, origin)
		VALUES ('bad-origin', 'fut-agent', 'future', 'approve', 'nonsense')`); err == nil {
		t.Fatal("origin CHECK constraint no longer enforced after rebuild")
	}

	// Triggers must survive the rebuild.
	if _, err := st.db.Exec(`UPDATE sessions SET origin = 'cli' WHERE id = 'fut-session'`); err == nil {
		t.Fatal("sessions_origin_immutable trigger lost in rebuild")
	} else if !strings.Contains(err.Error(), "session origin is immutable") {
		t.Fatalf("unexpected trigger error: %v", err)
	}
}
