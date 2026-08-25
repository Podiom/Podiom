package core

import (
	"context"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// A pinned note is a standing directive: it must render in its own binding
// section, in full, and must not also appear in the advisory recent-feedback
// stream — the whole point is that the agent can tell the two apart.
func TestPinnedFeedbackRendersAsStandingDirective(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned", "reviewed"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead", ReviewEvery: "24h"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// Long enough that the 500-char recent-feedback truncation would bite.
	directive := strings.TrimSpace("Never deploy on a Friday. " + strings.Repeat("Context that must survive intact. ", 30))
	pinnedEv, err := c.AddGoalFeedback(ctx, goal.ID, directive)
	if err != nil {
		t.Fatalf("add directive: %v", err)
	}
	if _, err := c.SetGoalFeedbackPin(ctx, goal.ID, pinnedEv.ID, true); err != nil {
		t.Fatalf("pin directive: %v", err)
	}
	if _, err := c.AddGoalFeedback(ctx, goal.ID, "Have a look at the CI failure."); err != nil {
		t.Fatalf("add ordinary feedback: %v", err)
	}

	if _, err := c.StartGoalPlanning(ctx, goal.ID); err != nil {
		t.Fatalf("start planning: %v", err)
	}
	planning := lastMessage(t, fake)
	if _, err := c.RunGoalReview(ctx, goal.ID); err != nil {
		t.Fatalf("run review: %v", err)
	}
	review := lastMessage(t, fake)

	for name, prompt := range map[string]string{"planning": planning, "review": review} {
		directives, feedback, ok := splitFeedbackSections(prompt)
		if !ok {
			t.Fatalf("%s prompt is missing the directives or feedback section:\n%s", name, prompt)
		}
		if !strings.Contains(directives, directive) {
			t.Fatalf("%s prompt truncated or dropped the directive:\n%s", name, directives)
		}
		if strings.Contains(directives, "CI failure") {
			t.Fatalf("%s prompt put ordinary feedback in the directives section:\n%s", name, directives)
		}
		if !strings.Contains(feedback, "CI failure") {
			t.Fatalf("%s prompt lost the ordinary feedback:\n%s", name, feedback)
		}
		if strings.Contains(feedback, "Never deploy on a Friday") {
			t.Fatalf("%s prompt repeated the directive in the recent-feedback stream:\n%s", name, feedback)
		}
	}
}

// Pinned text is rendered uncapped and untruncated, so both bounds have to be
// errors the user sees at the pin, never silent drops at render time.
func TestPinnedFeedbackBoundsAreEnforcedAtPinTime(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	pin := func(body string) error {
		ev, err := c.AddGoalFeedback(ctx, goal.ID, body)
		if err != nil {
			t.Fatalf("add feedback: %v", err)
		}
		_, err = c.SetGoalFeedbackPin(ctx, goal.ID, ev.ID, true)
		return err
	}

	for i := 0; i < maxPinnedGoalFeedback; i++ {
		if err := pin("directive"); err != nil {
			t.Fatalf("pin %d: %v", i, err)
		}
	}
	if err := pin("one too many"); err == nil {
		t.Fatalf("pinning past the cap of %d should fail", maxPinnedGoalFeedback)
	}

	// Re-pinning an already pinned note is a no-op and must not be refused for
	// pushing the count over the cap.
	directives, err := c.store.ListPinnedGoalFeedback(ctx, goal.ID)
	if err != nil {
		t.Fatalf("list pinned: %v", err)
	}
	if _, err := c.SetGoalFeedbackPin(ctx, goal.ID, directives[0].ID, true); err != nil {
		t.Fatalf("re-pin an already pinned note: %v", err)
	}

	// Length is checked against the text that will actually be rendered.
	if _, err := c.SetGoalFeedbackPin(ctx, goal.ID, directives[0].ID, false); err != nil {
		t.Fatalf("unpin to make room: %v", err)
	}
	if err := pin(strings.Repeat("x", maxPinnedFeedbackChars+1)); err == nil {
		t.Fatal("pinning an over-length note should fail")
	}
}

// The lead agent is told to push substantial work into tasks and schedules, so a
// directive that never reaches those runs does not actually apply to the goal.
func TestStandingDirectivesReachDelegatedRuns(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"ok", "ok", "ok", "ok"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// Before anything is pinned, a goal-linked run must be byte-identical to today.
	bare, err := c.CreateTask(ctx, store.Task{Title: "Probe", Body: "Check the thing", AssignedAgent: "lead", GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create bare task: %v", err)
	}
	if _, err := c.StartTask(ctx, StartTaskRequest{TaskID: bare.ID, Unattended: true}); err != nil {
		t.Fatalf("start bare task: %v", err)
	}
	if got := lastMessage(t, fake); got != TaskPrompt(bare) {
		t.Fatalf("goal task with no directives = %q, want the task prompt unchanged", got)
	}

	ev, err := c.AddGoalFeedback(ctx, goal.ID, "Never touch production DNS.")
	if err != nil {
		t.Fatalf("add feedback: %v", err)
	}
	if _, err := c.SetGoalFeedbackPin(ctx, goal.ID, ev.ID, true); err != nil {
		t.Fatalf("pin: %v", err)
	}

	task, err := c.CreateTask(ctx, store.Task{Title: "Migrate", Body: "Move the records", AssignedAgent: "lead", GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID, Unattended: true}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	taskPrompt := lastMessage(t, fake)
	if !strings.Contains(taskPrompt, "Never touch production DNS.") {
		t.Fatalf("goal-linked task run did not carry the directive:\n%s", taskPrompt)
	}
	if !strings.Contains(taskPrompt, goal.ID) {
		t.Fatalf("goal-linked task run did not name its goal:\n%s", taskPrompt)
	}
	if !strings.Contains(taskPrompt, "Move the records") {
		t.Fatalf("goal-linked task run lost its own task text:\n%s", taskPrompt)
	}

	if _, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "nightly",
		RunID:        "run-1",
		AgentName:    "lead",
		Task:         "Sync the records.",
		GoalID:       goal.ID,
	}); err != nil {
		t.Fatalf("run scheduled: %v", err)
	}
	schedulePrompt := lastMessage(t, fake)
	if !strings.Contains(schedulePrompt, "Never touch production DNS.") {
		t.Fatalf("goal-linked schedule run did not carry the directive:\n%s", schedulePrompt)
	}
	if !strings.Contains(schedulePrompt, "Sync the records.") {
		t.Fatalf("goal-linked schedule run lost its own task text:\n%s", schedulePrompt)
	}

	// A standalone task belongs to no goal and must be left completely alone.
	solo, err := c.CreateTask(ctx, store.Task{Title: "Unrelated", Body: "Do the other thing", AssignedAgent: "lead"})
	if err != nil {
		t.Fatalf("create standalone task: %v", err)
	}
	if _, err := c.StartTask(ctx, StartTaskRequest{TaskID: solo.ID, Unattended: true}); err != nil {
		t.Fatalf("start standalone task: %v", err)
	}
	if got := lastMessage(t, fake); got != TaskPrompt(solo) {
		t.Fatalf("standalone task prompt = %q, want it unchanged", got)
	}
}

func lastMessage(t *testing.T, fake *adapter.Fake) string {
	t.Helper()
	if len(fake.Requests) == 0 {
		t.Fatal("no provider requests recorded")
	}
	return fake.Requests[len(fake.Requests)-1].Message
}

// splitFeedbackSections carves a goal prompt into its standing-directives and
// recent-feedback sections so each can be asserted on independently.
func splitFeedbackSections(prompt string) (directives, feedback string, ok bool) {
	const directiveHead = "## Standing directives from the user"
	const feedbackHead = "## Recent user feedback (newest first)"
	di := strings.Index(prompt, directiveHead)
	fi := strings.Index(prompt, feedbackHead)
	if di < 0 || fi < 0 || fi < di {
		return "", "", false
	}
	rest := prompt[fi:]
	if end := strings.Index(rest[len(feedbackHead):], "\n## "); end >= 0 {
		rest = rest[:len(feedbackHead)+end]
	}
	return prompt[di:fi], rest, true
}
