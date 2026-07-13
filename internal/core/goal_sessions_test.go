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

	if _, err := c.StartGoalPlanning(ctx, goal.ID); err != nil {
		t.Fatalf("start planning: %v", err)
	}
	if len(fake.Requests) == 0 || !strings.Contains(fake.Requests[len(fake.Requests)-1].Message, "Bias toward staged rollout.") {
		t.Fatalf("planning prompt did not include feedback: requests=%+v", fake.Requests)
	}

	if _, err := c.RunGoalReview(ctx, goal.ID); err != nil {
		t.Fatalf("run review: %v", err)
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
