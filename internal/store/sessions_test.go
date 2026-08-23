package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionProjectOverrideMigrationBackfillsLinkedProjects(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE tasks (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '');
		CREATE TABLE goals (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '');
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY, origin TEXT NOT NULL, task_id TEXT, goal_id TEXT,
			project_id TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO tasks (id, project_id) VALUES ('task-1', 'task-project');
		INSERT INTO goals (id, project_id) VALUES ('goal-1', 'goal-project');
		INSERT INTO sessions (id, origin, task_id) VALUES ('roadmap', 'roadmap', 'task-1');
		INSERT INTO sessions (id, origin, goal_id) VALUES ('goal', 'goal', 'goal-1');
		INSERT INTO sessions (id, origin, project_id) VALUES ('schedule', 'schedule', 'schedule-project');
		INSERT INTO sessions (id, origin, project_id) VALUES ('web', 'web', 'web-project');
	`); err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	var migrationSQL string
	for _, migration := range migrations {
		if migration.version == 40 {
			migrationSQL = migration.sql
		}
	}
	if migrationSQL == "" {
		t.Fatal("missing session project override migration")
	}
	if _, err := db.Exec(migrationSQL); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	wants := map[string]string{
		"roadmap":  "task-project",
		"goal":     "goal-project",
		"schedule": "schedule-project",
		"web":      "",
	}
	for id, want := range wants {
		var got string
		if err := db.QueryRow(`SELECT inherited_project_id FROM sessions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got != want {
			t.Errorf("%s inherited project = %q, want %q", id, got, want)
		}
	}
}

func TestCreateSessionStoresProjectID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
		ProjectID:      "mission-control",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.ProjectID != "mission-control" {
		t.Fatalf("created project id = %q, want mission-control", created.ProjectID)
	}
	got, err := db.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ProjectID != "mission-control" {
		t.Fatalf("stored project id = %q, want mission-control", got.ProjectID)
	}
}

func TestSessionProjectOverrideRestoresInheritedProjectAndFencesOldTurn(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginRoadmap,
		ProjectID:      "alpha",
		ProviderHandle: "old-handle",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.InheritedProjectID != "alpha" {
		t.Fatalf("inherited project = %q, want alpha", created.InheritedProjectID)
	}
	if err := db.UpdateSessionContext(ctx, created.ID, 100, 200); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	overridden, err := db.UpdateSessionProject(ctx, created.ID, "beta", true)
	if err != nil {
		t.Fatalf("override project: %v", err)
	}
	if overridden.ProjectID != "beta" || !overridden.ProjectOverridden || overridden.InheritedProjectID != "alpha" {
		t.Fatalf("overridden session = %+v", overridden)
	}
	if overridden.ProviderHandle != "" || overridden.ContextTokens != 0 || overridden.ContextLimit != 0 || overridden.ProjectBindingRevision != 1 {
		t.Fatalf("override did not reset runtime: %+v", overridden)
	}

	if stored, err := db.UpdateSessionProviderHandleForProjectRevision(ctx, created.ID, "stale", 0); err != nil || stored {
		t.Fatalf("stale handle update = stored %v err %v, want fenced", stored, err)
	}
	if stored, err := db.UpdateSessionContextForProjectRevision(ctx, created.ID, 150, 200, 0); err != nil || stored {
		t.Fatalf("stale context update = stored %v err %v, want fenced", stored, err)
	}

	restored, err := db.UpdateSessionProject(ctx, created.ID, created.InheritedProjectID, false)
	if err != nil {
		t.Fatalf("clear override: %v", err)
	}
	if restored.ProjectID != "alpha" || restored.ProjectOverridden || restored.ProjectBindingRevision != 2 {
		t.Fatalf("restored session = %+v", restored)
	}
}

func TestUpdateSessionContextRoundTrips(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// New sessions start un-observed (0/0) so the ring stays hidden until a turn.
	if created.ContextTokens != 0 || created.ContextLimit != 0 {
		t.Fatalf("new session context = %d/%d, want 0/0", created.ContextTokens, created.ContextLimit)
	}
	if err := db.UpdateSessionContext(ctx, created.ID, 81000, 200000); err != nil {
		t.Fatalf("update context: %v", err)
	}
	got, err := db.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ContextTokens != 81000 || got.ContextLimit != 200000 {
		t.Fatalf("stored context = %d/%d, want 81000/200000", got.ContextTokens, got.ContextLimit)
	}
}

func TestAddSessionUsageAccumulatesAndRollsUpPerGoal(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := db.CreateGoal(ctx, Goal{Title: "ship it", LeadAgent: "jared"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	mk := func() Session {
		s, err := db.CreateSession(ctx, Session{AgentName: "jared", Provider: "claude", PermissionMode: "approve", Origin: OriginGoal, GoalID: goal.ID})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		if s.Usage.Total() != 0 {
			t.Fatalf("new session usage = %d, want 0", s.Usage.Total())
		}
		return s
	}
	s1 := mk()
	s2 := mk()

	// Two turns on s1 accumulate.
	if _, err := db.AddSessionUsage(ctx, s1.ID, SessionUsage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 5, CacheWriteTokens: 1}); err != nil {
		t.Fatalf("add usage 1: %v", err)
	}
	total, err := db.AddSessionUsage(ctx, s1.ID, SessionUsage{InputTokens: 200, OutputTokens: 30})
	if err != nil {
		t.Fatalf("add usage 2: %v", err)
	}
	if total.InputTokens != 300 || total.OutputTokens != 50 || total.Total() != 356 {
		t.Fatalf("accumulated total = %+v (sum %d), want input 300 output 50 sum 356", total, total.Total())
	}
	if _, err := db.AddSessionUsage(ctx, s2.ID, SessionUsage{InputTokens: 44}); err != nil {
		t.Fatalf("add usage s2: %v", err)
	}

	// A zero delta is a no-op that returns current totals.
	if got, err := db.AddSessionUsage(ctx, s1.ID, SessionUsage{}); err != nil || got.Total() != 356 {
		t.Fatalf("zero-delta = %+v err=%v, want unchanged 356", got, err)
	}

	// GetSession reflects the persisted totals.
	if got, err := db.GetSession(ctx, s1.ID); err != nil || got.Usage.Total() != 356 {
		t.Fatalf("get session usage = %+v err=%v", got.Usage, err)
	}

	// Goal roll-up sums both sessions into one claude group.
	groups, err := db.SumGoalUsage(ctx, goal.ID)
	if err != nil {
		t.Fatalf("sum goal usage: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Provider != "claude" || groups[0].Usage.Total() != 400 {
		t.Fatalf("group = %+v, want claude total 400", groups[0])
	}

	// A missing session errors; a goal with no usage returns no groups.
	if _, err := db.AddSessionUsage(ctx, "nope", SessionUsage{InputTokens: 1}); err == nil {
		t.Error("expected error adding usage to missing session")
	}
	empty, err := db.CreateGoal(ctx, Goal{Title: "empty", LeadAgent: "jared"})
	if err != nil {
		t.Fatalf("create empty goal: %v", err)
	}
	if groups, err := db.SumGoalUsage(ctx, empty.ID); err != nil || len(groups) != 0 {
		t.Fatalf("empty goal groups = %d err=%v, want 0", len(groups), err)
	}
}

func TestAppendMessagesStoresMessageKind(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var column string
	if err := db.db.QueryRowContext(ctx, `SELECT name FROM pragma_table_info('messages') WHERE name = 'kind'`).Scan(&column); err != nil {
		if err == sql.ErrNoRows {
			t.Fatal("messages.kind column was not migrated")
		}
		t.Fatalf("inspect messages.kind: %v", err)
	}

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	inserted, err := db.AppendMessages(ctx, created.ID, []Message{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Kind: KindReasoning, Content: "thinking privately"},
		{Role: RoleAssistant, Kind: KindNarration, Content: "let me check the config"},
		{Role: RoleAssistant, Kind: KindError, Content: "boom"},
	})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}
	wantKinds := []MessageKind{KindMessage, KindReasoning, KindNarration, KindError}
	for i, want := range wantKinds {
		if inserted[i].Kind != want {
			t.Fatalf("inserted[%d] kind = %q, want %q", i, inserted[i].Kind, want)
		}
	}
	history, err := db.ListMessages(ctx, created.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(history) != len(wantKinds) {
		t.Fatalf("history length = %d, want %d: %+v", len(history), len(wantKinds), history)
	}
	for i, want := range wantKinds {
		if history[i].Kind != want {
			t.Fatalf("history[%d] kind = %q, want %q", i, history[i].Kind, want)
		}
	}
}

func TestAppendMessagesWaitsForConcurrentGoalWriter(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := db.CreateGoal(ctx, Goal{Title: "Ship", LeadAgent: "jared"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginRoadmap,
		GoalID:         goal.ID,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	locker, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open locker connection: %v", err)
	}
	defer locker.Close()
	if _, err := locker.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin goal writer: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = locker.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if _, err := locker.ExecContext(ctx, `INSERT INTO goal_events
		(goal_id, session_id, kind, body, payload_json)
		VALUES (?, ?, ?, ?, '{}')`, goal.ID, sess.ID, GoalEventToolUse, "working"); err != nil {
		t.Fatalf("append uncommitted goal event: %v", err)
	}

	type appendResult struct {
		messages []Message
		err      error
	}
	result := make(chan appendResult, 1)
	go func() {
		messages, err := db.AppendMessages(ctx, sess.ID, []Message{{Role: RoleUser, Content: "hello"}})
		result <- appendResult{messages: messages, err: err}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for db.db.Stats().InUse < 2 {
		select {
		case got := <-result:
			t.Fatalf("append returned before waiting for goal writer: %v", got.err)
		case <-time.After(time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("append did not acquire a second database connection")
		}
	}
	select {
	case got := <-result:
		t.Fatalf("append returned while goal writer held the lock: %v", got.err)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := locker.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatalf("commit goal writer: %v", err)
	}
	locked = false

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("append after goal writer committed: %v", got.err)
		}
		if len(got.messages) != 1 || got.messages[0].Seq != 1 {
			t.Fatalf("appended messages = %+v, want one message at seq 1", got.messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("append did not finish after goal writer committed")
	}

	history, err := db.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(history) != 1 || history[0].Seq != 1 || history[0].Content != "hello" {
		t.Fatalf("history = %+v, want exactly the appended message", history)
	}
}

func TestSetSessionArchivedRoundTrips(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	created, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// A new session is live: nothing archives it until a run finishes or the
	// user says so.
	if created.ArchivedAt != "" {
		t.Fatalf("new session archived at %q, want empty", created.ArchivedAt)
	}

	archived, err := db.SetSessionArchived(ctx, created.ID, "2026-08-17T10:00:00Z")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archived.ArchivedAt != "2026-08-17T10:00:00Z" {
		t.Fatalf("archived at = %q, want 2026-08-17T10:00:00Z", archived.ArchivedAt)
	}
	// The marker has to survive the list query too — that is what the sidebar reads.
	listed, err := db.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(listed) != 1 || listed[0].ArchivedAt != "2026-08-17T10:00:00Z" {
		t.Fatalf("listed sessions = %+v, want one archived", listed)
	}

	revived, err := db.SetSessionArchived(ctx, created.ID, "")
	if err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if revived.ArchivedAt != "" {
		t.Fatalf("unarchived at = %q, want empty", revived.ArchivedAt)
	}

	if _, err := db.SetSessionArchived(ctx, "missing", "2026-08-17T10:00:00Z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive missing session err = %v, want ErrNotFound", err)
	}
}

// newAnswerTestSession opens a store with one session ready to take messages.
func newAnswerTestSession(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := db.CreateSession(ctx, Session{
		AgentName:      "jared",
		Provider:       "claude",
		PermissionMode: "approve",
		Origin:         OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return db, sess.ID
}

// TestLatestTurnAnswerReturnsTheCurrentTurnsAnswer checks the query picks the answer
// belonging to the turn that just ended, past the narration and reasoning around it.
func TestLatestTurnAnswerReturnsTheCurrentTurnsAnswer(t *testing.T) {
	ctx := context.Background()
	db, sessionID := newAnswerTestSession(t)

	if _, err := db.AppendMessages(ctx, sessionID, []Message{
		{Role: RoleUser, Content: "first ask"},
		{Role: RoleAssistant, Content: "the first answer"},
		{Role: RoleUser, Content: "second ask"},
		{Role: RoleAssistant, Kind: KindReasoning, Content: "thinking privately"},
		{Role: RoleAssistant, Kind: KindNarration, Content: "let me check the config"},
		{Role: RoleAssistant, Content: "the second answer"},
	}); err != nil {
		t.Fatalf("append messages: %v", err)
	}

	answer, err := db.LatestTurnAnswer(ctx, sessionID)
	if err != nil {
		t.Fatalf("latest turn answer: %v", err)
	}
	if answer != "the second answer" {
		t.Errorf("answer = %q, want the latest turn's answer", answer)
	}
}

// TestLatestTurnAnswerIgnoresAnEarlierTurn is the reason the query is bounded by the
// last user message. A turn that only thought has no answer of its own, and reporting
// the previous turn's answer would describe work that already finished.
func TestLatestTurnAnswerIgnoresAnEarlierTurn(t *testing.T) {
	ctx := context.Background()
	db, sessionID := newAnswerTestSession(t)

	if _, err := db.AppendMessages(ctx, sessionID, []Message{
		{Role: RoleUser, Content: "first ask"},
		{Role: RoleAssistant, Content: "the first answer"},
		{Role: RoleUser, Content: "second ask"},
		{Role: RoleAssistant, Kind: KindReasoning, Content: "thinking privately"},
	}); err != nil {
		t.Fatalf("append messages: %v", err)
	}

	if _, err := db.LatestTurnAnswer(ctx, sessionID); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound for a turn that produced no answer", err)
	}
}

// TestLatestTurnAnswerSkipsNonAnswerKinds covers a turn whose only rows are narration
// and a durable error: neither is the agent's answer.
func TestLatestTurnAnswerSkipsNonAnswerKinds(t *testing.T) {
	ctx := context.Background()
	db, sessionID := newAnswerTestSession(t)

	if _, err := db.AppendMessages(ctx, sessionID, []Message{
		{Role: RoleUser, Content: "ask"},
		{Role: RoleAssistant, Kind: KindNarration, Content: "working on it"},
		{Role: RoleAssistant, Kind: KindError, Content: "boom"},
	}); err != nil {
		t.Fatalf("append messages: %v", err)
	}

	if _, err := db.LatestTurnAnswer(ctx, sessionID); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// TestLatestTurnAnswerForAnUnknownSession checks a deleted or bogus session is a
// not-found rather than an empty success, so callers can tell the two apart.
func TestLatestTurnAnswerForAnUnknownSession(t *testing.T) {
	db, _ := newAnswerTestSession(t)
	if _, err := db.LatestTurnAnswer(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
