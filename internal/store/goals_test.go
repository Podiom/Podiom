package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openGoalStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGoalCRUDAndDueReviews(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	created, err := db.CreateGoal(ctx, Goal{
		Title:           "Ship the docs site",
		Description:     "Stand up docs.example.dev",
		SuccessCriteria: "Site live, every endpoint covered",
		Metrics: []GoalMetric{
			{Name: "Endpoints documented", Target: 52, Current: 38},
			{Name: "Guides published", Target: 10, Current: 7},
		},
		ReviewEvery: "24h",
		LeadAgent:   "atlas",
		ProjectID:   "docs",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if created.Status != GoalActive {
		t.Fatalf("default status = %q, want active", created.Status)
	}
	if len(created.Metrics) != 2 || created.Metrics[0].Target != 52 {
		t.Fatalf("metrics did not round-trip: %+v", created.Metrics)
	}
	if created.NextReviewAt != "" {
		t.Fatalf("next_review_at should be empty as given, got %q", created.NextReviewAt)
	}

	// Due-review pickup: only active goals with an arrived next_review_at fire.
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if err := db.SetGoalNextReview(ctx, created.ID, past); err != nil {
		t.Fatalf("set next review: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	due, err := db.ListDueGoalReviews(ctx, now)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 || due[0].ID != created.ID {
		t.Fatalf("due = %+v, want the created goal", due)
	}

	// Future review time → not due.
	if err := db.SetGoalNextReview(ctx, created.ID, future); err != nil {
		t.Fatalf("set next review: %v", err)
	}
	if due, _ = db.ListDueGoalReviews(ctx, now); len(due) != 0 {
		t.Fatalf("future review should not be due, got %+v", due)
	}

	// Paused goals never fire even when overdue.
	if err := db.SetGoalNextReview(ctx, created.ID, past); err != nil {
		t.Fatalf("set next review: %v", err)
	}
	paused := created
	paused.Status = GoalPaused
	paused.NextReviewAt = past
	if _, err := db.UpdateGoal(ctx, paused); err != nil {
		t.Fatalf("pause goal: %v", err)
	}
	if due, _ = db.ListDueGoalReviews(ctx, now); len(due) != 0 {
		t.Fatalf("paused goal should not be due, got %+v", due)
	}

	// Status filter on list.
	goals, err := db.ListGoals(ctx, string(GoalPaused))
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	if len(goals) != 1 {
		t.Fatalf("paused list = %d goals, want 1", len(goals))
	}

	if err := db.DeleteGoal(ctx, created.ID); err != nil {
		t.Fatalf("delete goal: %v", err)
	}
	if _, err := db.GetGoal(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted goal err = %v, want ErrNotFound", err)
	}
}

func TestGoalEventsAppendOnlyAndPagination(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "g", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	for _, kind := range []GoalEventKind{GoalEventCreated, GoalEventPlanningStarted, GoalEventProgress} {
		if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: kind, Body: "b"}); err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}

	events, err := db.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 || events[0].Kind != GoalEventProgress {
		t.Fatalf("events = %+v, want 3 newest-first", events)
	}

	// Cursor pagination: entries strictly older than `before`.
	page, err := db.ListGoalEvents(ctx, goal.ID, 1, events[0].ID)
	if err != nil {
		t.Fatalf("list events page: %v", err)
	}
	if len(page) != 1 || page[0].Kind != GoalEventPlanningStarted {
		t.Fatalf("page = %+v, want the planning event", page)
	}

	// Append-only: UPDATE is rejected at the schema level.
	if _, err := db.db.ExecContext(ctx, `UPDATE goal_events SET body = 'tampered' WHERE id = ?`, events[0].ID); err == nil {
		t.Fatalf("goal_events UPDATE should be rejected by trigger")
	}

	// Metric application is transactional with the event append.
	ev := GoalEvent{GoalID: goal.ID, Kind: GoalEventMetricUpdate, Payload: `{"updates":[{"name":"m","from":0,"to":5}]}`}
	if _, err := db.AppendGoalEventWithMetrics(ctx, ev, []GoalMetric{{Name: "m", Target: 10, Current: 5}}); err != nil {
		t.Fatalf("append with metrics: %v", err)
	}
	got, err := db.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if len(got.Metrics) != 1 || got.Metrics[0].Current != 5 {
		t.Fatalf("metrics after event = %+v, want m current 5", got.Metrics)
	}

	// Cascade: deleting the goal removes its timeline.
	if err := db.DeleteGoal(ctx, goal.ID); err != nil {
		t.Fatalf("delete goal: %v", err)
	}
	if events, _ = db.ListGoalEvents(ctx, goal.ID, 0, 0); len(events) != 0 {
		t.Fatalf("timeline should cascade on goal delete, got %+v", events)
	}
}

func TestAccessRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "g", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	req, err := db.CreateAccessRequest(ctx, AccessRequest{
		GoalID:    goal.ID,
		AgentName: "atlas",
		Kind:      AccessMCPServer,
		Payload:   `{"server_name":"netlify"}`,
		Reason:    "need deploy access",
	})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if req.Status != AccessPending {
		t.Fatalf("default status = %q, want pending", req.Status)
	}

	// Deny path with a note relayed to the agent.
	denied, err := db.DecideAccessRequest(ctx, req.ID, AccessDenied, "not yet")
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	if denied.Status != AccessDenied || denied.DecisionNote != "not yet" || denied.DecidedAt == "" {
		t.Fatalf("denied = %+v", denied)
	}

	// Double-decide is rejected.
	if _, err := db.DecideAccessRequest(ctx, req.ID, AccessApproved, ""); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("double decide err = %v, want ErrAlreadyDecided", err)
	}

	// Approve → executed flow, and the failed-retry loop.
	req2, err := db.CreateAccessRequest(ctx, AccessRequest{GoalID: goal.ID, AgentName: "atlas", Kind: AccessSkill})
	if err != nil {
		t.Fatalf("create request 2: %v", err)
	}
	// Executed can only follow approved.
	if _, err := db.MarkAccessRequestExecuted(ctx, req2.ID, ""); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("execute pending err = %v, want ErrAlreadyDecided", err)
	}
	if _, err := db.DecideAccessRequest(ctx, req2.ID, AccessApproved, "go ahead"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	failed, err := db.MarkAccessRequestExecuted(ctx, req2.ID, "marketplace 500")
	if err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if failed.Status != AccessFailed || failed.ExecutionError != "marketplace 500" {
		t.Fatalf("failed = %+v", failed)
	}
	// Failed stays retryable: approving again clears the error.
	retried, err := db.DecideAccessRequest(ctx, req2.ID, AccessApproved, "retry")
	if err != nil {
		t.Fatalf("retry approve: %v", err)
	}
	if retried.ExecutionError != "" {
		t.Fatalf("retry should clear execution_error, got %q", retried.ExecutionError)
	}
	executed, err := db.MarkAccessRequestExecuted(ctx, req2.ID, "")
	if err != nil {
		t.Fatalf("mark executed: %v", err)
	}
	if executed.Status != AccessExecuted || executed.ExecutedAt == "" {
		t.Fatalf("executed = %+v", executed)
	}

	// Filters.
	pending, err := db.ListAccessRequests(ctx, goal.ID, string(AccessPending))
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v, want none", pending)
	}
	all, err := db.ListAccessRequests(ctx, goal.ID, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all = %d requests, want 2", len(all))
	}
}

// TestGoalsMigrationPreservesSessions guards the v14 sessions-table rebuild: a
// fully-populated pre-v14 session (every column through v13) must survive the
// migration byte-for-byte, and its message history must survive the DROP TABLE
// (which is why migrations run with foreign_keys OFF).
func TestGoalsMigrationPreservesSessions(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	if _, err := db.CreateAgent(ctx, Agent{Name: "atlas", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	want := Session{
		AgentName:      "atlas",
		Name:           "Docs work",
		Description:    "desc",
		AutoNamed:      true,
		Provider:       "claude",
		Profile:        "default",
		Model:          "claude-fable-5",
		Effort:         "high",
		PermissionMode: "approve",
		Origin:         OriginRoadmap,
		ScheduleID:     "sched",
		RunID:          "run",
		TaskID:         "task",
		ProjectID:      "proj",
		RollingSummary: "summary",
		ProviderHandle: "handle",
		PlanState:      PlanAwaitingApproval,
		PlanExplicit:   true,
		PlanInfo: PlanInfo{
			FilePath: "/p.md", Markdown: "# p", SubmittedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z",
		},
		ContextTokens: 1234,
		ContextLimit:  200000,
	}
	created, err := db.CreateSession(ctx, want)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.AppendMessages(ctx, created.ID, []Message{{Role: RoleUser, Kind: KindMessage, Content: "hi"}}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	// Replay the v14 rebuild against live data: it must not error and must not
	// orphan or cascade-delete children. (Fresh DBs apply v14 before data
	// exists, so exercise the rebuild explicitly here.)
	conn, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatalf("fk off: %v", err)
	}
	var rebuild string
	var postRebuild []string
	for _, m := range migrations {
		if m.version == 14 {
			rebuild = m.sql
		}
		// Migrations after v14 that ALTER the sessions table must be re-applied
		// after this isolated v14 replay so GetSession (which selects their columns)
		// still works — mirroring the real forward order where they run after v14.
		if m.version == 16 {
			postRebuild = append(postRebuild, m.sql)
		}
	}
	// Strip the CREATE TABLE/INDEX parts that already exist; re-run only the
	// sessions rebuild portion by dropping goals tables first.
	if _, err := conn.ExecContext(ctx, `DROP TABLE access_requests; DROP TABLE goal_events; DROP TABLE goals;`); err != nil {
		t.Fatalf("drop goals tables: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `DROP INDEX idx_sessions_agent_name; DROP INDEX idx_sessions_origin;
		DROP INDEX idx_sessions_schedule_id; DROP INDEX idx_sessions_task_id; DROP INDEX idx_sessions_project_id;
		DROP INDEX idx_sessions_dreamed; DROP INDEX idx_sessions_plan_state; DROP INDEX idx_sessions_goal_id;`); err != nil {
		t.Fatalf("drop session indexes: %v", err)
	}
	if _, err := conn.ExecContext(ctx, rebuild); err != nil {
		t.Fatalf("replay v14 rebuild: %v", err)
	}
	for _, sql := range postRebuild {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatalf("re-apply post-v14 sessions migration: %v", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("fk on: %v", err)
	}

	got, err := db.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get session after rebuild: %v", err)
	}
	// GoalID is new and empty; everything else must match the pre-rebuild row.
	created.GoalID = ""
	if got != created {
		t.Fatalf("session after rebuild = %+v, want %+v", got, created)
	}
	msgs, err := db.ListMessages(ctx, created.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages after rebuild = %d, want 1 (rebuild cascade-deleted history?)", len(msgs))
	}

	// The rebuilt table accepts the new origin and goal linkage.
	goalSess, err := db.CreateSession(ctx, Session{
		AgentName: "atlas", Provider: "claude", PermissionMode: "approve",
		Origin: OriginGoal, GoalID: "goal-1",
	})
	if err != nil {
		t.Fatalf("create goal session: %v", err)
	}
	if goalSess.Origin != OriginGoal || goalSess.GoalID != "goal-1" {
		t.Fatalf("goal session = %+v", goalSess)
	}
}
