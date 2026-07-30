package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
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

	// Pending rate-limit recovery suppresses automatic review until resolved.
	if err := db.SetGoalNextReview(ctx, created.ID, past); err != nil {
		t.Fatalf("set next review: %v", err)
	}
	if _, err := db.CreateGoalRateLimitBlock(ctx, GoalRateLimitBlock{
		GoalID:    created.ID,
		SessionID: "blocked-session",
		Phase:     GoalRateLimitReview,
		Provider:  config.ProviderClaude,
		Error:     "rate limited on claude/default; no fallback available",
	}); err != nil {
		t.Fatalf("create rate-limit block: %v", err)
	}
	if due, _ = db.ListDueGoalReviews(ctx, now); len(due) != 0 {
		t.Fatalf("pending rate-limit block should suppress due review, got %+v", due)
	}
	if _, err := db.ResolveGoalRateLimitBlock(ctx, "missing", config.ProviderCodex, "", "gpt-5", "medium"); err == nil {
		t.Fatalf("resolve missing should fail")
	}
	pending, err := db.PendingGoalRateLimit(ctx, created.ID)
	if err != nil {
		t.Fatalf("pending rate limit: %v", err)
	}
	if _, err := db.ResolveGoalRateLimitBlock(ctx, pending.ID, config.ProviderCodex, "", "gpt-5", "medium"); err != nil {
		t.Fatalf("resolve pending rate limit: %v", err)
	}
	if due, _ = db.ListDueGoalReviews(ctx, now); len(due) != 1 {
		t.Fatalf("resolved rate-limit block should allow due review, got %+v", due)
	}

	// Paused goals never fire even when overdue.
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

// The stated next step must survive an unrelated full-row UpdateGoal — a cadence
// edit or lead handoff built from a stale read must not erase the agent's intent,
// which is why the columns live outside UpdateGoal's SET clause.
func TestGoalNextStepSurvivesUpdateAndClears(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	created, err := db.CreateGoal(ctx, Goal{Title: "Grow the newsletter", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if created.NextStep != "" || created.NextStepWhy != "" || created.NextStepAt != "" {
		t.Fatalf("new goal should have no next step, got %+v", created)
	}

	stated := time.Now().UTC().Format(time.RFC3339)
	if err := db.SetGoalNextStep(ctx, created.ID, "Post the launch thread on r/selfhosted",
		"Organic signups stalled and Reddit is the cheapest channel left untried.", stated); err != nil {
		t.Fatalf("set next step: %v", err)
	}
	got, err := db.GetGoal(ctx, created.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if got.NextStep != "Post the launch thread on r/selfhosted" {
		t.Fatalf("next step = %q", got.NextStep)
	}
	if got.NextStepWhy == "" || got.NextStepAt != stated {
		t.Fatalf("next step why/at did not round-trip: %+v", got)
	}

	// A full-row write from a read taken BEFORE the next step was stated.
	stale := created
	stale.ReviewEvery = "12h"
	updated, err := db.UpdateGoal(ctx, stale)
	if err != nil {
		t.Fatalf("update goal: %v", err)
	}
	if updated.NextStep != "Post the launch thread on r/selfhosted" {
		t.Fatalf("UpdateGoal clobbered the next step: %q", updated.NextStep)
	}
	if updated.ReviewEvery != "12h" {
		t.Fatalf("cadence edit lost: %q", updated.ReviewEvery)
	}

	if err := db.SetGoalNextStep(ctx, created.ID, "", "", ""); err != nil {
		t.Fatalf("clear next step: %v", err)
	}
	cleared, err := db.GetGoal(ctx, created.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if cleared.NextStep != "" || cleared.NextStepWhy != "" || cleared.NextStepAt != "" {
		t.Fatalf("next step not cleared: %+v", cleared)
	}

	if err := db.SetGoalNextStep(ctx, "missing", "x", "y", stated); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set on missing goal err = %v, want ErrNotFound", err)
	}
}

func TestGoalEventsAppendOnlyAndPagination(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "g", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	for _, kind := range []GoalEventKind{GoalEventCreated, GoalEventPlanningStarted, GoalEventProgress, GoalEventRateLimited, GoalEventRateLimitResolved} {
		if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: kind, Body: "b"}); err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}

	events, err := db.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 5 || events[0].Kind != GoalEventRateLimitResolved {
		t.Fatalf("events = %+v, want 5 newest-first", events)
	}

	var feedbackMigration, editableFeedbackMigration string
	for _, m := range migrations {
		if m.version == 20 {
			feedbackMigration = m.sql
		}
		if m.version == 23 {
			editableFeedbackMigration = m.sql
		}
	}
	if feedbackMigration == "" {
		t.Fatal("missing v20 feedback migration")
	}
	if editableFeedbackMigration == "" {
		t.Fatal("missing v23 editable feedback migration")
	}
	if _, err := db.db.ExecContext(ctx, feedbackMigration); err != nil {
		t.Fatalf("replay v20 feedback migration: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, editableFeedbackMigration); err != nil {
		t.Fatalf("replay v23 editable feedback migration: %v", err)
	}
	// v24 adds run provenance after the historical table-rebuild migrations.
	// Replaying v20 above intentionally recreates that older shape, so restore
	// the later column before exercising the current store queries.
	if _, err := db.db.ExecContext(ctx, `ALTER TABLE goal_events ADD COLUMN run_id TEXT`); err != nil {
		t.Fatalf("restore v24 run_id after replay: %v", err)
	}
	events, err = db.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events after v20 replay: %v", err)
	}
	if len(events) != 5 || events[0].Kind != GoalEventRateLimitResolved {
		t.Fatalf("events after v20 replay = %+v, want existing rows preserved", events)
	}
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventUserFeedback, Body: "nudge strategy"}); err != nil {
		t.Fatalf("append feedback: %v", err)
	}
	feedback, err := db.ListGoalEventsByKind(ctx, goal.ID, GoalEventUserFeedback, 10)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(feedback) != 1 || feedback[0].Body != "nudge strategy" {
		t.Fatalf("feedback events = %+v", feedback)
	}

	// Cursor pagination: entries strictly older than `before`.
	page, err := db.ListGoalEvents(ctx, goal.ID, 1, feedback[0].ID)
	if err != nil {
		t.Fatalf("list events page: %v", err)
	}
	if len(page) != 1 || page[0].Kind != GoalEventRateLimitResolved {
		t.Fatalf("page = %+v, want the rate-limit-resolved event", page)
	}

	updated, err := db.UpdateUnreadGoalFeedback(ctx, goal.ID, feedback[0].ID, "nudge launch")
	if err != nil {
		t.Fatalf("update unread feedback: %v", err)
	}
	if updated.Body != "nudge launch" {
		t.Fatalf("updated feedback body = %q", updated.Body)
	}

	if _, err := db.db.ExecContext(ctx, `UPDATE goal_events SET body = 'tampered' WHERE id = ?`, page[0].ID); err == nil {
		t.Fatalf("non-feedback goal event UPDATE should be rejected by trigger")
	}
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventReviewStarted}); err != nil {
		t.Fatalf("append review started: %v", err)
	}
	if _, err := db.UpdateUnreadGoalFeedback(ctx, goal.ID, feedback[0].ID, "too late"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update read feedback err = %v, want ErrNotFound", err)
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

func TestGoalToolUseEventsAndContextFilter(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "g", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	// The 'tool_use' kind round-trips (v21 CHECK constraint accepts it).
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventProgress, Body: "moved"}); err != nil {
		t.Fatalf("append progress: %v", err)
	}
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventToolUse, Body: "`Bash` — ls"}); err != nil {
		t.Fatalf("append tool_use: %v", err)
	}

	all, err := db.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("list events = %+v (err %v), want 2", all, err)
	}
	// The context view (used for review prompts) excludes tool_use.
	ctxEvents, err := db.ListGoalContextEvents(ctx, goal.ID, 50)
	if err != nil {
		t.Fatalf("list context events: %v", err)
	}
	if len(ctxEvents) != 1 || ctxEvents[0].Kind != GoalEventProgress {
		t.Fatalf("context events = %+v, want only the progress entry", ctxEvents)
	}
}

func TestGoalRateLimitBlocksCRUDAndIdempotency(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "g", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	first, err := db.CreateGoalRateLimitBlock(ctx, GoalRateLimitBlock{
		GoalID:    goal.ID,
		SessionID: "s1",
		Phase:     GoalRateLimitPlanning,
		Provider:  config.ProviderClaude,
		Profile:   "work",
		Model:     "sonnet",
		Effort:    "medium",
		Error:     "rate limit reached",
	})
	if err != nil {
		t.Fatalf("create block: %v", err)
	}
	second, err := db.CreateGoalRateLimitBlock(ctx, GoalRateLimitBlock{
		GoalID:    goal.ID,
		SessionID: "s1",
		Phase:     GoalRateLimitPlanning,
		Provider:  config.ProviderClaude,
		Error:     "rate limit reached again",
	})
	if err != nil {
		t.Fatalf("create duplicate block: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate session should return existing block: first=%s second=%s", first.ID, second.ID)
	}
	pending, err := db.ListPendingGoalRateLimits(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatalf("pending blocks = %+v, want first block", pending)
	}
	resolved, err := db.ResolveGoalRateLimitBlock(ctx, first.ID, config.ProviderCodex, "main", "gpt-5", "high")
	if err != nil {
		t.Fatalf("resolve block: %v", err)
	}
	if resolved.Status != GoalRateLimitResolved || resolved.ResolvedProvider != config.ProviderCodex || resolved.ResolvedModel != "gpt-5" {
		t.Fatalf("resolved block did not persist target: %+v", resolved)
	}
	if pending, err = db.ListPendingGoalRateLimits(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending after resolve = %+v err=%v, want none", pending, err)
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
