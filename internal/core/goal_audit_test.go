package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// lastTurnRequestFor returns the most recent fake turn request for a session.
func lastTurnRequestFor(t *testing.T, fake *adapter.Fake, sessionID string) adapter.TurnRequest {
	t.Helper()
	for i := len(fake.Requests) - 1; i >= 0; i-- {
		if fake.Requests[i].SessionID == sessionID {
			return fake.Requests[i]
		}
	}
	t.Fatalf("no turn request for session %q", sessionID)
	return adapter.TurnRequest{}
}

func TestGoalPlanningRunsYolo(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned"}

	// A yolo agent would trivially pass; use the default approve agent so the test
	// proves the goal chain forces yolo regardless of the agent's standing mode.
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.StartGoalPlanning(ctx, goal.ID)
	if err != nil {
		t.Fatalf("start goal planning: %v", err)
	}
	if sess.PermissionMode != config.PermissionYolo {
		t.Fatalf("goal session permission = %q, want yolo", sess.PermissionMode)
	}
	req := lastTurnRequestFor(t, fake, sess.ID)
	if req.Settings.PermissionMode != config.PermissionYolo {
		t.Fatalf("turn permission = %q, want yolo", req.Settings.PermissionMode)
	}
	if req.Relay != nil {
		t.Fatalf("yolo goal run must not attach a permission relay")
	}
}

func TestGoalRunRecordsToolUseEvents(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned"}
	fake.ToolUses = []adapter.ToolUse{
		{Provider: config.ProviderClaude, ToolUseID: "t1", Name: "Bash", Summary: "npm install left-pad", Input: json.RawMessage(`{"command":"npm install left-pad"}`)},
		{Provider: config.ProviderClaude, ToolUseID: "t2", Name: "Read", Summary: "/repo/main.go", Input: json.RawMessage(`{"file_path":"/repo/main.go"}`)},
	}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := c.StartGoalPlanning(ctx, goal.ID); err != nil {
		t.Fatalf("start goal planning: %v", err)
	}

	tools, err := c.store.ListGoalEventsByKind(ctx, goal.ID, store.GoalEventToolUse, 0)
	if err != nil {
		t.Fatalf("list tool_use events: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tool_use events = %d, want 2", len(tools))
	}
	if tools[0].RunID == "" || tools[1].RunID != tools[0].RunID {
		t.Fatalf("tool events should share one exact run id: %+v", tools)
	}
	// Newest first: Read (read-only), then Bash (side-effecting).
	var readPayload, bashPayload map[string]any
	if err := json.Unmarshal([]byte(tools[0].Payload), &readPayload); err != nil {
		t.Fatalf("unmarshal read payload: %v", err)
	}
	if err := json.Unmarshal([]byte(tools[1].Payload), &bashPayload); err != nil {
		t.Fatalf("unmarshal bash payload: %v", err)
	}
	if readPayload["read_only"] != true {
		t.Fatalf("Read should be read_only: %v", readPayload)
	}
	if bashPayload["read_only"] != false {
		t.Fatalf("Bash should not be read_only: %v", bashPayload)
	}
	if !strings.Contains(tools[1].Body, "npm install left-pad") {
		t.Fatalf("bash body should show the command: %q", tools[1].Body)
	}

	// The review context excludes tool_use so it does not flood the prompt.
	ctxEvents, err := c.store.ListGoalContextEvents(ctx, goal.ID, 50)
	if err != nil {
		t.Fatalf("list context events: %v", err)
	}
	for _, ev := range ctxEvents {
		if ev.Kind == store.GoalEventToolUse {
			t.Fatalf("context events must exclude tool_use, got %+v", ev)
		}
	}
}

func TestGoalToolUseTruncatesLargeInput(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"planned"}
	big := strings.Repeat("x", 5000)
	fake.ToolUses = []adapter.ToolUse{
		{Provider: config.ProviderClaude, Name: "Write", Summary: "/repo/big.txt", Input: json.RawMessage(`{"file_path":"/repo/big.txt","content":"` + big + `"}`)},
	}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := c.StartGoalPlanning(ctx, goal.ID); err != nil {
		t.Fatalf("start goal planning: %v", err)
	}

	tools, err := c.store.ListGoalEventsByKind(ctx, goal.ID, store.GoalEventToolUse, 0)
	if err != nil || len(tools) != 1 {
		t.Fatalf("tool_use events = %d (err %v), want 1", len(tools), err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(tools[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["input_truncated"] != true {
		t.Fatalf("large input should be marked truncated: %v", payload)
	}
	if s, _ := payload["input"].(string); len(s) > goalToolUseInputMax+10 {
		t.Fatalf("stored input length %d exceeds cap", len(s))
	}
	// The file path stays visible even though the body was elided.
	if !strings.Contains(tools[0].Body, "/repo/big.txt") {
		t.Fatalf("body should keep the file path: %q", tools[0].Body)
	}
}

func TestNonGoalSessionRecordsNoToolUseEvents(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"done"}
	fake.ToolUses = []adapter.ToolUse{{Provider: config.ProviderClaude, Name: "Bash", Summary: "ls"}}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "solo", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "solo", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	events, err := c.StreamTurn(ctx, sess.ID, "hello", TurnOptions{})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	for range events { // drain
	}
	// No panic and no goal to attach to: the tool_use event is simply dropped.
	// (A non-goal session has no goal_events table rows to assert against.)
}

func TestGoalLinkedTaskRunsYolo(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"worked"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "worker", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "worker"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{Title: "Do the thing", AssignedAgent: "worker", GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID, Unattended: true})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if sess.PermissionMode != config.PermissionYolo {
		t.Fatalf("goal-linked task session permission = %q, want yolo", sess.PermissionMode)
	}
	if sess.GoalID != goal.ID {
		t.Fatalf("goal-linked task session GoalID = %q, want %q", sess.GoalID, goal.ID)
	}
	req := lastTurnRequestFor(t, fake, sess.ID)
	if req.Relay != nil {
		t.Fatalf("yolo goal-linked task run must not attach a permission relay")
	}
	history, err := c.History(ctx, sess.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || history[0].Role != store.RoleUser || history[1].Content != "worked" {
		t.Fatalf("unexpected goal-linked task history: %+v", history)
	}
}

func TestGoalLinkedScheduleForcesYolo(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"ran"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "runner", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "runner"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	// Yolo: false, but GoalID set — RunScheduled must force yolo.
	sess, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "goal-sched",
		RunID:        "run-1",
		AgentName:    "runner",
		Task:         "do it",
		GoalID:       goal.ID,
		Yolo:         false,
	})
	if err != nil {
		t.Fatalf("run scheduled: %v", err)
	}
	if sess.PermissionMode != config.PermissionYolo {
		t.Fatalf("goal-linked schedule permission = %q, want yolo", sess.PermissionMode)
	}
	if sess.GoalID != goal.ID {
		t.Fatalf("goal-linked schedule session GoalID = %q, want %q", sess.GoalID, goal.ID)
	}
}
