package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func newPlanSession(t *testing.T, c *Core) store.Session {
	t.Helper()
	ctx := context.Background()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "planner", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "demo", Name: "Demo"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName:                      "planner",
		Origin:                         store.OriginWeb,
		ProjectID:                      "demo",
		CreatePlanBeforeImplementation: true,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session
}

// A native plan is stored as the provider produced it. Podiom's structured
// heading contract governs the podiom_submit_plan fallback only — holding a
// Claude or Codex plan to it would reject valid plans.
func TestCaptureNativePlanKeepsTheProvidersShape(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newPlanSession(t, c)

	const markdown = "# Add subtract\n\n## Summary\nAdd subtract(a,b).\n\n## Test Plan\nRun the tests."
	updated, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: markdown})
	if err != nil {
		t.Fatalf("capture native plan: %v", err)
	}
	if updated.PlanState != store.PlanAwaitingApproval {
		t.Fatalf("state after capture: got %q want awaiting_approval", updated.PlanState)
	}
	if updated.PlanInfo.Markdown != markdown {
		t.Fatalf("plan markdown was altered:\n%s", updated.PlanInfo.Markdown)
	}

	// Podiom keeps its own copy, inside its own plans directory.
	planDir := filepath.Join(c.paths.ProjectsDir, "demo", "plans")
	if !strings.HasPrefix(updated.PlanInfo.FilePath, planDir) {
		t.Fatalf("canonical copy outside Podiom's plans dir: %q", updated.PlanInfo.FilePath)
	}
	raw, err := os.ReadFile(updated.PlanInfo.FilePath)
	if err != nil {
		t.Fatalf("read canonical copy: %v", err)
	}
	if !strings.Contains(string(raw), "Add subtract(a,b).") {
		t.Fatalf("canonical copy does not match the plan:\n%s", raw)
	}
}

// A revision replaces Podiom's copy in place rather than accumulating files,
// and the stored plan becomes the new one.
func TestCaptureNativePlanRevisionReplacesTheCopy(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newPlanSession(t, c)

	first, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: "# Plan\n\nFirst attempt."})
	if err != nil {
		t.Fatalf("capture first: %v", err)
	}
	second, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: "# Plan\n\nRevised after feedback."})
	if err != nil {
		t.Fatalf("capture revision: %v", err)
	}
	if first.PlanInfo.FilePath != second.PlanInfo.FilePath {
		t.Fatalf("revision started a new file: %q then %q", first.PlanInfo.FilePath, second.PlanInfo.FilePath)
	}
	raw, err := os.ReadFile(second.PlanInfo.FilePath)
	if err != nil {
		t.Fatalf("read canonical copy: %v", err)
	}
	if !strings.Contains(string(raw), "Revised after feedback") {
		t.Fatalf("canonical copy still holds the stale plan:\n%s", raw)
	}
	entries, err := os.ReadDir(filepath.Dir(second.PlanInfo.FilePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one plan file, got %d", len(entries))
	}
}

// Rejection removes Podiom's copy and clears the artifact, leaving the session
// out of plan mode.
func TestRejectNativePlanClearsPodiomsCopy(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newPlanSession(t, c)

	captured, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: "# Plan\n\nNope."})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	path := captured.PlanInfo.FilePath

	rejected, err := c.RejectPlan(ctx, session.ID)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.PlanState != store.PlanNone {
		t.Fatalf("state after reject: got %q want none", rejected.PlanState)
	}
	if rejected.PlanInfo.Markdown != "" {
		t.Fatal("rejected plan should be cleared")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Podiom's copy should be deleted on reject")
	}
}

// Approval keeps the artifact, which is what "approved" looks like on the
// session row and what the replay pinning keys off.
func TestApproveNativePlanRetainsTheArtifact(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newPlanSession(t, c)

	if _, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: "# Plan\n\nShip it."}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	decision, err := c.ApprovePlan(ctx, session.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if decision.Session.PlanState != store.PlanNone {
		t.Fatalf("approval must lift the gate, got %q", decision.Session.PlanState)
	}
	if !strings.Contains(decision.Session.PlanInfo.Markdown, "Ship it") {
		t.Fatal("approval must retain the plan so implementation can follow it")
	}
}

// Plan mode is togglable mid-session in both directions.
func TestSetPlanModeTransitions(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newPlanSession(t, c)

	// Created with the gate up; turning it off leaves an ordinary session.
	off, err := c.SetPlanMode(ctx, session.ID, false)
	if err != nil {
		t.Fatalf("plan off: %v", err)
	}
	if off.PlanState != store.PlanNone {
		t.Fatalf("plan off: got %q want none", off.PlanState)
	}

	// And back on mid-session — the case that was impossible before.
	on, err := c.SetPlanMode(ctx, session.ID, true)
	if err != nil {
		t.Fatalf("plan on: %v", err)
	}
	if on.PlanState != store.PlanPendingSubmission {
		t.Fatalf("plan on: got %q want pending_submission", on.PlanState)
	}

	// Toggling on again is a no-op rather than an error or a state reset.
	if _, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: "# Plan\n\nWork."}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	again, err := c.SetPlanMode(ctx, session.ID, true)
	if err != nil {
		t.Fatalf("plan on while awaiting: %v", err)
	}
	if again.PlanState != store.PlanAwaitingApproval {
		t.Fatalf("toggling on must not reset a submitted plan, got %q", again.PlanState)
	}

	// Turning it off while a plan awaits approval must clear the artifact.
	// Keeping it would look exactly like an approved plan and get pinned to
	// every later turn as though the user had accepted it.
	abandoned, err := c.SetPlanMode(ctx, session.ID, false)
	if err != nil {
		t.Fatalf("plan off while awaiting: %v", err)
	}
	if abandoned.PlanState != store.PlanNone || abandoned.PlanInfo.Markdown != "" {
		t.Fatalf("abandoned plan must not linger as approved: %#v", abandoned.PlanInfo)
	}
	if len(replayHistory(abandoned, []store.Message{{Role: store.RoleUser, Content: "hi"}})) != 1 {
		t.Fatal("abandoned plan was pinned as if approved")
	}
}

// The regression this guards is "the agent forgot the approved plan": a
// rate-limit fallback starts a fresh backing session, and a long implementation
// pushes the plan out of the recent-replay window. Either would silently drop
// the plan the user approved, so it is pinned from the session row.
func TestApprovedPlanIsPinnedIntoEveryReplay(t *testing.T) {
	approved := store.Session{
		PlanState: store.PlanNone,
		PlanInfo:  store.PlanInfo{Markdown: "# Plan\n\nStep one.", FilePath: "/plans/p.md"},
	}

	t.Run("short history", func(t *testing.T) {
		got := replayHistory(approved, []store.Message{{Role: store.RoleUser, Content: "hi"}})
		if len(got) == 0 || !strings.Contains(got[0].Content, "Step one.") {
			t.Fatalf("approved plan not pinned: %#v", got)
		}
		if !strings.Contains(got[0].Content, "/plans/p.md") {
			t.Fatal("pinned plan should carry its file path so the agent can re-read it")
		}
	})

	t.Run("history past the summarization window", func(t *testing.T) {
		summarized := approved
		summarized.RollingSummary = "Earlier work."
		var history []store.Message
		for i := 0; i < recentReplayMessages*3; i++ {
			history = append(history, store.Message{Role: store.RoleUser, Content: "filler"})
		}
		got := replayHistory(summarized, history)
		if len(got) == 0 || !strings.Contains(got[0].Content, "Step one.") {
			t.Fatal("approved plan dropped once history exceeded the replay window")
		}
	})

	t.Run("not pinned while still awaiting approval", func(t *testing.T) {
		pending := approved
		pending.PlanState = store.PlanAwaitingApproval
		got := replayHistory(pending, []store.Message{{Role: store.RoleUser, Content: "hi"}})
		if len(got) != 1 {
			t.Fatalf("unapproved plan must not be pinned: %#v", got)
		}
	})

	t.Run("nothing pinned without a plan", func(t *testing.T) {
		got := replayHistory(store.Session{PlanState: store.PlanNone}, []store.Message{{Role: store.RoleUser, Content: "hi"}})
		if len(got) != 1 {
			t.Fatalf("unexpected pinning: %#v", got)
		}
	})
}

// Migration 28 dropped the permission_mode CHECK constraint, making Go the only
// guard. These are the paths a bad value can reach the database through.
func TestInvalidPermissionModeIsRejected(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name: "bogus", Provider: config.ProviderClaude, PermissionMode: "nonsense",
	}); err == nil {
		t.Fatal("agent creation accepted an unknown permission mode")
	}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "ok", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "ok", Origin: store.OriginWeb, PermissionMode: "nonsense",
	}); err == nil {
		t.Fatal("session creation accepted an unknown permission mode")
	}

	// The new mode must still pass.
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name: "autoagent", Provider: config.ProviderClaude, PermissionMode: config.PermissionAuto,
	}); err != nil {
		t.Fatalf("auto must be accepted: %v", err)
	}
}
