package core

import (
	"context"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
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
