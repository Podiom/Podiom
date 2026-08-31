package core

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

func createGoalMemoryTestRun(t *testing.T, c *Core, goal store.Goal, kind store.GoalRunKind) (store.Session, store.GoalRun) {
	t.Helper()
	sess, _, err := c.ensureGoalLeadSession(context.Background(), goal)
	if err != nil {
		t.Fatalf("ensure lead session: %v", err)
	}
	run, err := c.beginGoalRun(context.Background(), sess, kind, "")
	if err != nil {
		t.Fatalf("begin goal run: %v", err)
	}
	return sess, run
}

func TestCommitGoalMemoryPreservesOmittedItemsAndAcknowledgesFeedback(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship safely", LeadAgent: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := c.AddGoalFeedback(ctx, goal.ID, "Use a staged rollout.")
	if err != nil {
		t.Fatal(err)
	}
	sess, run := createGoalMemoryTestRun(t, c, goal, store.GoalRunPlanning)
	state := "The rollout plan is ready."
	plan := []string{"Deploy to the canary group"}
	memory, err := c.CommitGoalMemory(ctx, CommitGoalMemoryInput{
		GoalID: goal.ID, SessionID: sess.ID, BaseRevision: 0, CurrentState: &state, ActivePlan: &plan,
		Upserts:              []GoalMemoryUpsert{{ID: "decision-rollout", Kind: store.GoalMemoryDecision, Title: "Use staged rollout", Rationale: "Limits blast radius."}},
		FeedbackDispositions: []GoalFeedbackDispositionInput{{EventID: feedback.ID, Disposition: store.GoalFeedbackIncorporated, MemoryItemIDs: []string{"decision-rollout"}}},
		Outcome:              "Prepared a staged rollout plan.",
	})
	if err != nil {
		t.Fatalf("commit memory: %v", err)
	}
	if memory.Status != store.GoalMemoryReady || memory.Revision != 1 || memory.LastRunID != run.ID {
		t.Fatalf("memory = %+v", memory)
	}
	pending, err := c.store.ListPendingGoalFeedback(ctx, goal.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending feedback = %+v, err %v", pending, err)
	}
	if _, err := c.finishGoalRun(ctx, run.ID, store.GoalRunSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	secondRun, err := c.beginGoalRun(ctx, sess, store.GoalRunReview, "")
	if err != nil {
		t.Fatal(err)
	}
	memory, err = c.CommitGoalMemory(ctx, CommitGoalMemoryInput{
		GoalID: goal.ID, SessionID: sess.ID, BaseRevision: 1,
		Upserts: []GoalMemoryUpsert{{ID: "risk-capacity", Kind: store.GoalMemoryRisk, Title: "Canary capacity", Detail: "Watch saturation."}},
		Outcome: "Added the main rollout risk.",
	})
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if memory.Document.CurrentState != state || len(memory.Document.ActivePlan) != 1 || len(memory.Document.Items) != 2 {
		t.Fatalf("omitted memory was not preserved: %+v", memory.Document)
	}
	if memory.LastRunID != secondRun.ID {
		t.Fatalf("last run = %q, want %q", memory.LastRunID, secondRun.ID)
	}
}

func TestReadyGoalReviewUsesFreshContextAndCompletePendingFeedback(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Remember early choices", LeadAgent: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	sess, run := createGoalMemoryTestRun(t, c, goal, store.GoalRunPlanning)
	state := "The original architecture decision remains active."
	if _, err := c.CommitGoalMemory(ctx, CommitGoalMemoryInput{GoalID: goal.ID, SessionID: sess.ID,
		BaseRevision: 0, CurrentState: &state, Upserts: []GoalMemoryUpsert{{ID: "decision-early", Kind: store.GoalMemoryDecision, Title: "Keep the early decision"}}, Outcome: "Saved the initial plan."}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.finishGoalRun(ctx, run.ID, store.GoalRunSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.AppendUserMessage(ctx, sess.ID, "old canonical transcript that must not replay", nil); err != nil {
		t.Fatal(err)
	}
	feedback, err := c.AddGoalFeedback(ctx, goal.ID, "This full feedback must reach the fresh review verbatim.")
	if err != nil {
		t.Fatal(err)
	}
	fake.Responses = make([]string, 21)
	for i := range fake.Responses {
		fake.Responses[i] = "reviewed"
	}
	packetSize := 0
	for i := 0; i < 21; i++ {
		if _, err := c.RunGoalReview(ctx, goal.ID); err != nil {
			t.Fatalf("review %d: %v", i+1, err)
		}
		req := fake.Requests[len(fake.Requests)-1]
		if len(req.History) != 0 || req.Handle.ID != "" {
			t.Fatalf("review %d replayed state: handle %q history %d", i+1, req.Handle.ID, len(req.History))
		}
		if !strings.Contains(req.Message, state) || !strings.Contains(req.Message, feedback.Body) || !strings.Contains(req.Message, "Feedback "+fmtInt(feedback.ID)) {
			t.Fatalf("review packet missing durable context:\n%s", req.Message)
		}
		if i == 0 {
			packetSize = len(req.Message)
		} else if len(req.Message) != packetSize {
			t.Fatalf("review packet grew from %d to %d bytes by review %d", packetSize, len(req.Message), i+1)
		}
	}
}

func fmtInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func TestRepairGoalMemoryLeavesGoalPausedAndFeedbackPending(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Recover context", LeadAgent: "lead", NextStep: "Check the build"})
	if err != nil {
		t.Fatal(err)
	}
	feedback, _ := c.AddGoalFeedback(ctx, goal.ID, "Do not lose this note.")
	if err := c.blockGoalMemory(ctx, goal.ID, "validation_failed", "bad document"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TransitionGoal(ctx, goal.ID, store.GoalActive, "Resume early"); err == nil {
		t.Fatal("blocked goal resumed before memory repair")
	}
	fake.Responses = []string{"GOAL_MEMORY_VALID"}
	result, err := c.RepairGoalMemory(ctx, goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memory.Status != store.GoalMemoryReady || result.FeedbackRead != 1 {
		t.Fatalf("repair result = %+v", result)
	}
	got, _ := c.GetGoal(ctx, goal.ID)
	if got.Status != store.GoalPaused || got.NextReviewAt != "" {
		t.Fatalf("repair resumed goal: %+v", got)
	}
	pending, _ := c.store.ListPendingGoalFeedback(ctx, goal.ID)
	if len(pending) != 1 || pending[0].ID != feedback.ID {
		t.Fatalf("repair acknowledged feedback without incorporating it: %+v", pending)
	}
	if _, err := c.TransitionGoal(ctx, goal.ID, store.GoalActive, "Resume after reviewing repair"); err != nil {
		t.Fatalf("resume after repair: %v", err)
	}
}

func TestRepairGoalMemoryValidationFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Protect the old memory", LeadAgent: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.blockGoalMemory(ctx, goal.ID, "validation_failed", "bad document"); err != nil {
		t.Fatal(err)
	}
	before, err := c.GetGoalMemory(ctx, goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake.Responses = []string{"I cannot validate this draft."}
	if _, err := c.RepairGoalMemory(ctx, goal.ID); err == nil {
		t.Fatal("repair succeeded without model validation")
	}
	after, err := c.GetGoalMemory(ctx, goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != store.GoalMemoryBlocked || after.Revision != before.Revision {
		t.Fatalf("failed repair changed memory: before=%+v after=%+v", before, after)
	}
	req := fake.Requests[len(fake.Requests)-1]
	if len(req.History) != 0 || req.Handle.ID != "" || !req.Settings.Unattended || len(req.Settings.AllowedTools) != 0 {
		t.Fatalf("repair validation was not fresh and tool-denied: %+v", req)
	}
}

func TestDirectGoalChatEntersOrdinaryFeedbackInbox(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Keep chat guidance", LeadAgent: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	sess, _, err := c.ensureGoalLeadSession(ctx, goal)
	if err != nil {
		t.Fatal(err)
	}
	fake.Responses = []string{"Understood."}
	if _, err := c.AppendTurn(ctx, sess.ID, "Prefer the smaller migration first."); err != nil {
		t.Fatal(err)
	}
	pending, err := c.store.ListPendingGoalFeedback(ctx, goal.ID)
	if err != nil || len(pending) != 1 || pending[0].Body != "Prefer the smaller migration first." {
		t.Fatalf("pending direct-chat feedback = %+v, err %v", pending, err)
	}
}
