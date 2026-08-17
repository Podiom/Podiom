package core

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// newScheduledTestCore builds a core backed by a fake adapter and returns the
// fake so tests can script permission behavior.
func newScheduledTestCore(t *testing.T) (*Core, *adapter.Fake, func()) {
	t.Helper()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	fake := adapter.NewFake()
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	return c, fake, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func TestRunScheduledCreatesProvenancedSession(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"did the thing"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	sess, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "morning-calendar",
		RunID:        "run-1",
		AgentName:    "jared",
		Provider:     config.ProviderCodex,
		Model:        "gpt-5.1",
		Effort:       "medium",
		Task:         "Summarise the calendar.",
	})
	if err != nil {
		t.Fatalf("run scheduled: %v", err)
	}
	if sess.Origin != store.OriginSchedule {
		t.Fatalf("origin = %q, want schedule", sess.Origin)
	}
	if sess.ScheduleID != "morning-calendar" || sess.RunID != "run-1" {
		t.Fatalf("missing schedule/run linkage: %+v", sess)
	}
	if sess.Provider != config.ProviderCodex || sess.Model != "gpt-5.1" || sess.Effort != "medium" {
		t.Fatalf("scheduled run target = %+v", sess)
	}

	history, err := c.History(ctx, sess.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || history[0].Role != store.RoleUser || history[1].Content != "did the thing" {
		t.Fatalf("unexpected scheduled history: %+v", history)
	}

	// The session is discoverable by schedule for "revisit and continue manually".
	bySchedule, err := c.store.ListSessionsBySchedule(ctx, "morning-calendar")
	if err != nil {
		t.Fatalf("list by schedule: %v", err)
	}
	if len(bySchedule) != 1 || bySchedule[0].ID != sess.ID {
		t.Fatalf("schedule session not linked for revisit: %+v", bySchedule)
	}
}

func TestRunScheduledPreapprovedDeniesUnlistedTool(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.PermissionTool = "Bash"

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "router", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "nightly",
		RunID:        "run-deny",
		AgentName:    "router",
		Task:         "Try to run a command.",
		// preapproved with empty allow-list: deny all side effects.
	}); err != nil {
		t.Fatalf("run scheduled: %v", err)
	}

	decisions := fake.RecordedDecisions()
	if len(decisions) == 0 {
		t.Fatal("expected a permission decision to be recorded")
	}
	if decisions[0].Behavior != "deny" {
		t.Fatalf("preapproved empty allow-list should deny, got %q", decisions[0].Behavior)
	}
}

func TestRunScheduledPreapprovedAllowsListedTool(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.PermissionTool = "Bash"

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "router", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "nightly",
		RunID:        "run-allow",
		AgentName:    "router",
		AllowedTools: []string{"Bash"},
		Task:         "Run an allow-listed command.",
	}); err != nil {
		t.Fatalf("run scheduled: %v", err)
	}

	decisions := fake.RecordedDecisions()
	if len(decisions) == 0 {
		t.Fatal("expected a permission decision to be recorded")
	}
	if decisions[0].Behavior != "allow" {
		t.Fatalf("allow-listed tool should be allowed, got %q", decisions[0].Behavior)
	}
}

func TestAllowListRelayDecisions(t *testing.T) {
	relay := NewAllowListRelay([]string{"Read", "LS"})
	allow, _ := relay.RequestPermission(context.Background(), adapter.PermissionRequest{ToolName: "Read"}, 0)
	if allow.Behavior != "allow" {
		t.Fatalf("Read should be allowed, got %q", allow.Behavior)
	}
	deny, _ := relay.RequestPermission(context.Background(), adapter.PermissionRequest{ToolName: "Bash"}, 0)
	if deny.Behavior != "deny" {
		t.Fatalf("Bash should be denied, got %q", deny.Behavior)
	}

	empty := NewAllowListRelay(nil)
	d, _ := empty.RequestPermission(context.Background(), adapter.PermissionRequest{ToolName: "Read"}, 0)
	if d.Behavior != "deny" {
		t.Fatalf("empty allow-list should deny all, got %q", d.Behavior)
	}
}

func TestRunScheduledArchivesFinishedSession(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"did the thing"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "morning-calendar",
		RunID:        "run-1",
		AgentName:    "jared",
		Task:         "Summarise the calendar.",
	})
	if err != nil {
		t.Fatalf("run scheduled: %v", err)
	}
	// A schedule session is one-shot, so the run ending is the whole lifecycle.
	// The returned session carries the stamp, not just the stored row.
	if sess.ArchivedAt == "" {
		t.Fatalf("returned session archived at = %q, want a stamp", sess.ArchivedAt)
	}
	stored, err := c.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.ArchivedAt == "" {
		t.Fatalf("stored session archived at = %q, want a stamp", stored.ArchivedAt)
	}
}

func TestRunScheduledArchivesEvenWhenTheTurnErrors(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.SendTurnError = errors.New("provider exploded")

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "morning-calendar",
		RunID:        "run-1",
		AgentName:    "jared",
		Task:         "Summarise the calendar.",
	})
	// The run is reported as failed, but a failed run is as finished as a
	// successful one — it must not linger in the live session list.
	if err == nil {
		t.Fatal("run scheduled err = nil, want the turn error")
	}
	stored, err := c.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.ArchivedAt == "" {
		t.Fatalf("failed run archived at = %q, want a stamp", stored.ArchivedAt)
	}
}

func TestUserTurnUnarchivesSession(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"ran", "answered"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "morning-calendar",
		RunID:        "run-1",
		AgentName:    "jared",
		Task:         "Summarise the calendar.",
	})
	if err != nil {
		t.Fatalf("run scheduled: %v", err)
	}
	if sess.ArchivedAt == "" {
		t.Fatal("precondition: scheduled session should be archived")
	}

	// Picking the conversation back up brings it out of the archive.
	events, err := c.StreamTurn(ctx, sess.ID, "actually, what about tomorrow?", TurnOptions{})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	for range events {
	}
	stored, err := c.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.ArchivedAt != "" {
		t.Fatalf("archived at after user turn = %q, want empty", stored.ArchivedAt)
	}
}

func TestUnattendedTurnLeavesArchivedSessionArchived(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"ran", "ran again"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.RunScheduled(ctx, ScheduledRunRequest{
		ScheduleName: "morning-calendar",
		RunID:        "run-1",
		AgentName:    "jared",
		Task:         "Summarise the calendar.",
	})
	if err != nil {
		t.Fatalf("run scheduled: %v", err)
	}

	// Unattended traffic is exactly what the archive exists to keep out of the
	// way, so it must not revive a session the way a user's turn does.
	events, err := c.StreamTurn(ctx, sess.ID, "another unattended pass", TurnOptions{Unattended: true})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	for range events {
	}
	stored, err := c.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.ArchivedAt == "" {
		t.Fatalf("archived at after unattended turn = %q, want the stamp to survive", stored.ArchivedAt)
	}
}
