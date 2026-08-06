package core

import (
	"context"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestGoalPlanningUsesStoredRunTarget(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{
		Title:     "Ship it",
		LeadAgent: "lead",
		Provider:  config.ProviderCodex,
		Model:     "gpt-5.1",
		Effort:    "high",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.StartGoalPlanning(ctx, goal.ID)
	if err != nil {
		t.Fatalf("start goal planning: %v", err)
	}
	if sess.Provider != config.ProviderCodex || sess.Model != "gpt-5.1" || sess.Effort != "high" {
		t.Fatalf("goal planning target = %+v", sess)
	}
}

func TestGoalPlanningSessionUsesProjectInstructions(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, err := c.WriteProjectInstructions("mission-control", "goal project layer\n")
	if err != nil {
		t.Fatalf("write project instructions: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{
		Title:     "Ship it",
		LeadAgent: "lead",
		ProjectID: "mission-control",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.StartGoalPlanning(ctx, goal.ID)
	if err != nil {
		t.Fatalf("start goal planning: %v", err)
	}
	req := startRequestFor(t, fake, sess.ID)
	if !strings.Contains(string(req.Instructions), ".podiom-project-instructions.md") {
		t.Fatalf("goal planning session missing project instruction path:\n%s", req.Instructions)
	}
}

// The next step is the user's answer to "what will the agent do?", so it is
// written only alongside a progress entry, preserved when a later entry omits it,
// and dropped once the goal has nothing left to do.
func TestRecordGoalProgressStatesNextStep(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Grow the newsletter", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// Restating intent requires saying what moved: a next step alone is rejected.
	if _, err := c.RecordGoalProgress(ctx, RecordGoalProgressRequest{
		GoalID:   goal.ID,
		NextStep: "Post the launch thread on r/selfhosted",
	}); err == nil {
		t.Fatalf("next step without a body or metric updates should be rejected")
	}

	if _, err := c.RecordGoalProgress(ctx, RecordGoalProgressRequest{
		GoalID:      goal.ID,
		Body:        "Sent issue 4; 18 new signups.",
		NextStep:    "Post the launch thread on r/selfhosted",
		NextStepWhy: "Organic signups stalled and Reddit is the cheapest channel left untried.",
	}); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	stated, err := c.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if stated.NextStep != "Post the launch thread on r/selfhosted" {
		t.Fatalf("next step = %q", stated.NextStep)
	}
	if stated.NextStepWhy == "" || stated.NextStepAt == "" {
		t.Fatalf("next step rationale/timestamp missing: %+v", stated)
	}

	// The event payload carries it too, so the history of intentions is derivable
	// from the timeline the way metric history is.
	events, err := c.ListGoalEvents(ctx, goal.ID, 10, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var found bool
	for _, ev := range events {
		if ev.Kind == store.GoalEventProgress && strings.Contains(ev.Payload, "r/selfhosted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no progress event carried the next step payload: %+v", events)
	}

	// A later entry that omits it must not silently erase it.
	if _, err := c.RecordGoalProgress(ctx, RecordGoalProgressRequest{
		GoalID: goal.ID,
		Body:   "Drafted the thread.",
	}); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	kept, err := c.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if kept.NextStep != stated.NextStep {
		t.Fatalf("omitting next_step changed it to %q", kept.NextStep)
	}

	// Pausing keeps the intent the user paused on.
	if _, err := c.TransitionGoal(ctx, goal.ID, store.GoalPaused, "Paused by you."); err != nil {
		t.Fatalf("pause: %v", err)
	}
	paused, err := c.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if paused.NextStep != stated.NextStep {
		t.Fatalf("pausing dropped the next step: %q", paused.NextStep)
	}

	// Abandoning clears it — a finished goal has no next step.
	if _, err := c.TransitionGoal(ctx, goal.ID, store.GoalAbandoned, "Abandoned by you."); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	done, err := c.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if done.NextStep != "" || done.NextStepWhy != "" || done.NextStepAt != "" {
		t.Fatalf("abandoned goal kept a next step: %+v", done)
	}
}

// Proposing completion is a claim that nothing is left, so it drops the next step
// in the returned goal as well as in the store.
func TestProposeGoalCompletionClearsNextStep(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Grow the newsletter", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := c.RecordGoalProgress(ctx, RecordGoalProgressRequest{
		GoalID:   goal.ID,
		Body:     "Hit 1,000 subscribers.",
		NextStep: "Post the launch thread on r/selfhosted",
	}); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	proposed, err := c.ProposeGoalCompletion(ctx, goal.ID, "", "Every criterion is met: 1,000 subscribers.")
	if err != nil {
		t.Fatalf("propose completion: %v", err)
	}
	if proposed.NextStep != "" || proposed.NextStepAt != "" {
		t.Fatalf("returned goal kept a next step: %+v", proposed)
	}
	stored, err := c.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if stored.NextStep != "" {
		t.Fatalf("stored goal kept a next step: %q", stored.NextStep)
	}
}

// The review prompt shows the agent its own previous next step with its age, which
// is what lets the review duty ask "did that happen?" instead of silently drifting.
func TestGoalReviewPromptCarriesStatedNextStep(t *testing.T) {
	goal := store.Goal{
		ID:          "g1",
		Title:       "Grow the newsletter",
		NextStep:    "Post the launch thread on r/selfhosted",
		NextStepWhy: "Organic signups stalled.",
		NextStepAt:  "2026-07-29T09:00:00Z",
	}
	prompt := GoalReviewPrompt(goal, nil, nil, nil, nil, GoalActionItems{})
	for _, want := range []string{"Post the launch thread on r/selfhosted", "Organic signups stalled.", "2026-07-29T09:00:00Z", "next_step_why"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}

	bare := GoalReviewPrompt(store.Goal{ID: "g1", Title: "Grow the newsletter"}, nil, nil, nil, nil, GoalActionItems{})
	if strings.Contains(bare, "Next step you stated") {
		t.Fatalf("unstated next step should not appear in the brief:\n%s", bare)
	}
}

func TestGoalFeedbackIsUserOnlyContextForGoalRuns(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned", "reviewed"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{
		Title:       "Ship it",
		LeadAgent:   "lead",
		ReviewEvery: "24h",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := c.AddGoalFeedback(ctx, goal.ID, "   "); err == nil {
		t.Fatalf("empty feedback should fail")
	}
	before := goal.NextReviewAt
	ev, err := c.AddGoalFeedback(ctx, goal.ID, "Bias toward staged rollout.")
	if err != nil {
		t.Fatalf("add feedback: %v", err)
	}
	if ev.Kind != store.GoalEventUserFeedback || ev.SessionID != "" || ev.Body != "Bias toward staged rollout." {
		t.Fatalf("feedback event = %+v", ev)
	}
	unchanged, err := c.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if unchanged.Status != store.GoalActive || unchanged.NextReviewAt != before {
		t.Fatalf("feedback changed goal lifecycle: before next=%q after=%+v", before, unchanged)
	}

	planningSession, err := c.StartGoalPlanning(ctx, goal.ID)
	if err != nil {
		t.Fatalf("start planning: %v", err)
	}
	if len(fake.Requests) == 0 || !strings.Contains(fake.Requests[len(fake.Requests)-1].Message, "Bias toward staged rollout.") {
		t.Fatalf("planning prompt did not include feedback: requests=%+v", fake.Requests)
	}

	reviewSession, err := c.RunGoalReview(ctx, goal.ID)
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if reviewSession.ID != planningSession.ID {
		t.Fatalf("review session = %q, want continuing lead session %q", reviewSession.ID, planningSession.ID)
	}
	events, err := c.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var planningRun, reviewRun string
	for _, event := range events {
		switch event.Kind {
		case store.GoalEventPlanningStarted:
			planningRun = event.RunID
		case store.GoalEventReviewStarted:
			reviewRun = event.RunID
		}
	}
	if planningRun == "" || reviewRun == "" || planningRun == reviewRun {
		t.Fatalf("planning/review run ids = %q/%q, want distinct non-empty ids", planningRun, reviewRun)
	}
	for _, runID := range []string{planningRun, reviewRun} {
		run, sess, messages, _, err := c.GetGoalRunDetail(ctx, goal.ID, runID)
		if err != nil {
			t.Fatalf("get run %q: %v", runID, err)
		}
		if run.SessionID != planningSession.ID || sess.ID != planningSession.ID || len(messages) < 2 {
			t.Fatalf("run detail = run %+v session %+v messages %d", run, sess, len(messages))
		}
	}
	if len(fake.Requests) < 2 || !strings.Contains(fake.Requests[len(fake.Requests)-1].Message, "Recent user feedback") ||
		!strings.Contains(fake.Requests[len(fake.Requests)-1].Message, "Bias toward staged rollout.") {
		t.Fatalf("review prompt did not include feedback: %q", fake.Requests[len(fake.Requests)-1].Message)
	}
}

func TestGoalPlanningRateLimitCreatesRecoverableBlock(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.RateLimitedTurns = 1

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.StartGoalPlanning(ctx, goal.ID)
	if err == nil {
		t.Fatalf("planning should fail on exhausted rate limit")
	}

	pending, err := c.PendingGoalRateLimit(ctx, goal.ID)
	if err != nil {
		t.Fatalf("pending rate limit: %v", err)
	}
	if pending == nil {
		t.Fatal("expected pending rate-limit block")
	}
	if pending.SessionID != sess.ID || pending.Phase != store.GoalRateLimitPlanning {
		t.Fatalf("unexpected pending block: %+v session=%s", pending, sess.ID)
	}
	events, err := c.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if events[0].Kind != store.GoalEventRateLimited {
		t.Fatalf("latest event = %s, want rate_limited", events[0].Kind)
	}

	resolved, updated, err := c.ResolveGoalRateLimit(ctx, ResolveGoalRateLimitInput{
		BlockID:  pending.ID,
		Provider: config.ProviderCodex,
		Model:    "gpt-5",
		Effort:   "high",
	})
	if err != nil {
		t.Fatalf("resolve rate limit: %v", err)
	}
	if resolved.Status != store.GoalRateLimitResolved {
		t.Fatalf("resolved status = %s", resolved.Status)
	}
	if updated.Provider != config.ProviderCodex || updated.Model != "gpt-5" || updated.Effort != "high" {
		t.Fatalf("goal target not updated: %+v", updated)
	}
	if pending, err = c.PendingGoalRateLimit(ctx, goal.ID); err != nil || pending != nil {
		t.Fatalf("pending after resolve = %+v err=%v, want nil", pending, err)
	}
}

func TestReconcileGoalRateLimitsBackfillsOldSessionOnce(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Old deploy", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "lead", Origin: store.OriginGoal, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create goal session: %v", err)
	}
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:    goal.ID,
		SessionID: sess.ID,
		Kind:      store.GoalEventReviewStarted,
	}); err != nil {
		t.Fatalf("append review event: %v", err)
	}
	if _, err := c.AppendErrorMessage(ctx, sess.ID, "rate limited on claude/default; no fallback available"); err != nil {
		t.Fatalf("append error: %v", err)
	}

	created, err := c.ReconcileGoalRateLimits(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(created) != 1 || created[0].SessionID != sess.ID || created[0].Phase != store.GoalRateLimitReview {
		t.Fatalf("created blocks = %+v, want one review block for session", created)
	}
	created, err = c.ReconcileGoalRateLimits(ctx)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("second reconcile created duplicates: %+v", created)
	}
}

func TestGoalQuestionAnswerIsHumanActivityWithoutProducerSession(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Decide", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "lead", Origin: store.OriginGoal, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	asked, err := c.CreateAgentQuestion(ctx, sess.ID, []store.AgentQuestionItem{{ID: "choice", Question: "Which path?", Options: []store.AgentQuestionOption{{Label: "A"}, {Label: "B"}}}})
	if err != nil {
		t.Fatalf("ask question: %v", err)
	}
	answered, err := c.AnswerAgentQuestion(ctx, asked.Question.ID, map[string][]string{"choice": {"A"}})
	if err != nil {
		t.Fatalf("answer question: %v", err)
	}
	if answered.Event == nil || answered.Event.SessionID != "" || answered.Event.RunID != "" {
		t.Fatalf("question answer event = %+v, want human activity without producer provenance", answered.Event)
	}
}
