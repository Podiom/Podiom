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
