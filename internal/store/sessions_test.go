package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

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
