package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestPlanSessionSubmitRejectApproveFlow(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "planner", Provider: config.ProviderCodex, PermissionMode: config.PermissionYolo}); err != nil {
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
	if session.PlanState != store.PlanPendingSubmission || !session.PlanExplicit {
		t.Fatalf("session should start gated, got state=%q explicit=%v", session.PlanState, session.PlanExplicit)
	}
	if session.PermissionMode != config.PermissionYolo {
		t.Fatalf("stored build permission should remain yolo, got %q", session.PermissionMode)
	}

	planPath := filepath.Join(c.paths.ProjectsDir, "demo", "plans", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	planMarkdown := validStructuredPlanMarkdown("Demo")
	submitted, err := c.SubmitPlan(ctx, SubmitPlanRequest{
		SessionID: session.ID,
		FilePath:  planPath,
		Markdown:  planMarkdown,
	})
	if err != nil {
		t.Fatalf("submit plan: %v", err)
	}
	if submitted.PlanState != store.PlanAwaitingApproval || submitted.PlanInfo.FilePath != planPath {
		t.Fatalf("bad submitted plan state: %+v", submitted)
	}
	history, err := c.store.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(history) != 1 || history[0].Role != store.RoleAssistant || !strings.Contains(history[0].Content, "# Plan: Demo") {
		t.Fatalf("submit should append canonical assistant plan message, got %+v", history)
	}

	rejected, err := c.RejectPlan(ctx, session.ID)
	if err != nil {
		t.Fatalf("reject plan: %v", err)
	}
	if rejected.PlanState != store.PlanNone || rejected.PlanInfo.Markdown != "" {
		t.Fatalf("reject should clear plan state/info, got %+v", rejected)
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Fatalf("reject should delete validated plan file, stat err=%v", err)
	}

	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatalf("rewrite plan: %v", err)
	}
	if _, err := c.store.UpdateSessionPlanState(ctx, session.ID, store.PlanPendingSubmission, true, store.PlanInfo{}); err != nil {
		t.Fatalf("reset plan state: %v", err)
	}
	if _, err := c.SubmitPlan(ctx, SubmitPlanRequest{SessionID: session.ID, FilePath: planPath, Markdown: validStructuredPlanMarkdown("Demo v2")}); err != nil {
		t.Fatalf("resubmit plan: %v", err)
	}
	approved, err := c.ApprovePlan(ctx, session.ID)
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	if approved.Session.PlanState != store.PlanNone || approved.Session.PlanExplicit {
		t.Fatalf("approve should clear gate, got %+v", approved.Session)
	}
	if approved.Session.ProviderHandle != "" {
		t.Fatalf("yolo approval should clear provider handle, got %q", approved.Session.ProviderHandle)
	}
	if !strings.Contains(approved.NextMessage, "Proceed with implementation") {
		t.Fatalf("approve should return continuation message, got %q", approved.NextMessage)
	}
}

func TestSubmitPlanUnassignedSessionRoutesToUnassignedDir(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "planner", Provider: config.ProviderCodex, PermissionMode: config.PermissionYolo}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	// No project on this session — it is unassigned.
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName:                      "planner",
		Origin:                         store.OriginWeb,
		CreatePlanBeforeImplementation: true,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if strings.TrimSpace(session.ProjectID) != "" {
		t.Fatalf("session should be unassigned, got project %q", session.ProjectID)
	}

	// A plan under <ProjectsDir>/unassigned/plans is accepted.
	planPath := filepath.Join(c.paths.ProjectsDir, "unassigned", "plans", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	submitted, err := c.SubmitPlan(ctx, SubmitPlanRequest{
		SessionID: session.ID,
		FilePath:  planPath,
		Markdown:  validStructuredPlanMarkdown("Unassigned"),
	})
	if err != nil {
		t.Fatalf("submit plan: %v", err)
	}
	if submitted.PlanInfo.FilePath != planPath {
		t.Fatalf("submitted plan path = %q, want %q", submitted.PlanInfo.FilePath, planPath)
	}

	// A plan outside the unassigned plans dir (e.g. an invented project dir) is rejected.
	strayPath := filepath.Join(c.paths.ProjectsDir, "made-up", "plans", "plan.md")
	if err := os.MkdirAll(filepath.Dir(strayPath), 0o755); err != nil {
		t.Fatalf("mkdir stray plans: %v", err)
	}
	if err := os.WriteFile(strayPath, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatalf("write stray plan: %v", err)
	}
	if _, err := c.store.UpdateSessionPlanState(ctx, session.ID, store.PlanPendingSubmission, true, store.PlanInfo{}); err != nil {
		t.Fatalf("reset plan state: %v", err)
	}
	if _, err := c.SubmitPlan(ctx, SubmitPlanRequest{
		SessionID: session.ID,
		FilePath:  strayPath,
		Markdown:  validStructuredPlanMarkdown("Stray"),
	}); err == nil {
		t.Fatal("expected plan outside unassigned plans dir to be rejected")
	}
}

// Podiom's own plan prompt is the fallback contract, used only by providers
// that cannot plan natively. Both shipped providers do, so it is exercised
// directly rather than through a turn.
func TestFallbackPlanPromptTargetsTheSessionPlansDir(t *testing.T) {
	c, cleanup := newTestCore(t)
	defer cleanup()

	unassigned := c.planModePrompt(store.Session{PlanState: store.PlanPendingSubmission}, projectExecutionContext{})
	if !strings.Contains(unassigned, filepath.Join(c.paths.ProjectsDir, "unassigned", "plans")) {
		t.Fatalf("unassigned prompt should point at unassigned/plans:\n%s", unassigned)
	}
	if strings.Contains(unassigned, "<project>") {
		t.Fatalf("unassigned prompt still contains a placeholder:\n%s", unassigned)
	}

	projectDir := filepath.Join(c.paths.ProjectsDir, "demo")
	bound := c.planModePrompt(store.Session{PlanState: store.PlanPendingSubmission}, projectExecutionContext{ProjectDir: projectDir})
	for _, want := range []string{
		"Podiom plan mode is active for this session.",
		"# Plan: <short title>",
		"## Risks And Rollback",
		filepath.Join(projectDir, "plans"),
	} {
		if !strings.Contains(bound, want) {
			t.Fatalf("fallback plan prompt missing %q:\n%s", want, bound)
		}
	}
}

// A provider that plans natively is asked to do so, and must NOT receive
// Podiom's plan prompt: the provider's own plan contract is already in its
// system prompt, so re-prepending Podiom's to every user message would only
// cost tokens and compete with it.
func TestNativePlanModeReplacesTheInjectedPrompt(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()

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
	if _, err := c.AppendTurn(ctx, session.ID, "Build a dashboard"); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake requests = %d, want 1", len(fake.Requests))
	}
	req := fake.Requests[0]
	if !req.Settings.PlanMode {
		t.Fatal("native provider was not asked to run its own plan mode")
	}
	if strings.Contains(req.Message, "Podiom plan mode is active") {
		t.Fatalf("native plan mode must not inject Podiom's prompt:\n%s", req.Message)
	}
	if !strings.Contains(req.Message, "Build a dashboard") {
		t.Fatalf("user message missing:\n%s", req.Message)
	}
}

// Feedback keeps the session awaiting approval and re-runs the turn still in
// plan mode, so the provider revises with its own workflow. The revision
// instruction must not mention Podiom's headings or submit tool for a native
// provider — neither applies to the plan it produced.
func TestPlanFeedbackRevisesInPlanMode(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()

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
	if _, err := c.CaptureNativePlan(ctx, session.ID, adapter.PlanProposal{Markdown: "# Native plan\n\nDo the work."}); err != nil {
		t.Fatalf("capture plan: %v", err)
	}
	decision, err := c.FeedbackPlan(ctx, session.ID, "Add a migration step.")
	if err != nil {
		t.Fatalf("feedback plan: %v", err)
	}
	if strings.Contains(decision.NextMessage, "podiom_submit_plan") ||
		strings.Contains(decision.NextMessage, "structured Markdown headings") {
		t.Fatalf("native revision must not cite the fallback contract: %q", decision.NextMessage)
	}
	if !strings.Contains(decision.NextMessage, "Add a migration step.") {
		t.Fatalf("feedback text missing: %q", decision.NextMessage)
	}
	if decision.Session.PlanState != store.PlanAwaitingApproval {
		t.Fatalf("feedback must keep the session awaiting approval, got %q", decision.Session.PlanState)
	}
	if _, err := c.AppendTurn(ctx, session.ID, decision.NextMessage); err != nil {
		t.Fatalf("append feedback turn: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake requests = %d, want 1", len(fake.Requests))
	}
	if !fake.Requests[0].Settings.PlanMode {
		t.Fatal("revision turn must still run in plan mode")
	}
}

func TestSubmitPlanRejectsMissingStructuredHeadings(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

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
	planPath := filepath.Join(c.paths.ProjectsDir, "demo", "plans", "plan.md")
	err = os.MkdirAll(filepath.Dir(planPath), 0o755)
	if err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	_, err = c.SubmitPlan(ctx, SubmitPlanRequest{
		SessionID: session.ID,
		FilePath:  planPath,
		Markdown:  "# Plan: Demo\n\n## Goal\nBuild it.",
	})
	if err == nil {
		t.Fatal("expected missing headings to fail")
	}
	if !strings.Contains(err.Error(), "plan markdown is missing required headings") || !strings.Contains(err.Error(), "## Context") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestPlanGateRelayAllowsReadsAndDeniesMutations(t *testing.T) {
	relay := NewPlanGateRelay()
	ctx := context.Background()

	read, err := relay.RequestPermission(ctx, adapter.PermissionRequest{
		ID:       "read",
		ToolName: "Read",
		Input:    json.RawMessage(`{"path":"README.md"}`),
	}, time.Second)
	if err != nil {
		t.Fatalf("read permission: %v", err)
	}
	if read.Behavior != "allow" {
		t.Fatalf("read should be allowed, got %+v", read)
	}

	submit, err := relay.RequestPermission(ctx, adapter.PermissionRequest{
		ID:       "submit",
		ToolName: "podiom_submit_plan",
		Input:    json.RawMessage(`{"markdown":"# Plan"}`),
	}, time.Second)
	if err != nil {
		t.Fatalf("submit permission: %v", err)
	}
	if submit.Behavior != "allow" {
		t.Fatalf("submit tool should be allowed, got %+v", submit)
	}

	write, err := relay.RequestPermission(ctx, adapter.PermissionRequest{
		ID:       "write",
		ToolName: "apply_patch",
		Input:    json.RawMessage(`{}`),
	}, time.Second)
	if err != nil {
		t.Fatalf("write permission: %v", err)
	}
	if write.Behavior != "deny" || write.Message != PlanGateMessage {
		t.Fatalf("write should be denied with plan gate message, got %+v", write)
	}
}

// TestPlanGateRelayHonorsAdapterReadOnlyClassification covers the providers
// whose tool name cannot answer the question. Codex runs every command through
// one "codex.command" tool, so judging by name alone denied `ls` and `rg` along
// with `rm`, and plan turns came back having read nothing.
func TestPlanGateRelayHonorsAdapterReadOnlyClassification(t *testing.T) {
	relay := NewPlanGateRelay()
	ctx := context.Background()

	read, err := relay.RequestPermission(ctx, adapter.PermissionRequest{
		ID:       "cmd-read",
		ToolName: "codex.command",
		ReadOnly: true,
		Input:    json.RawMessage(`{"command":"ls -la"}`),
	}, time.Second)
	if err != nil {
		t.Fatalf("read-only command: %v", err)
	}
	if read.Behavior != "allow" {
		t.Fatalf("classified read-only command should be allowed, got %+v", read)
	}

	unclassified, err := relay.RequestPermission(ctx, adapter.PermissionRequest{
		ID:       "cmd-unknown",
		ToolName: "codex.command",
		Input:    json.RawMessage(`{"command":"rm -rf ."}`),
	}, time.Second)
	if err != nil {
		t.Fatalf("unclassified command: %v", err)
	}
	if unclassified.Behavior != "deny" {
		t.Fatalf("unclassified command must stay denied, got %+v", unclassified)
	}

	fileChange, err := relay.RequestPermission(ctx, adapter.PermissionRequest{
		ID:       "file-change",
		ToolName: "codex.file_change",
		Input:    json.RawMessage(`{}`),
	}, time.Second)
	if err != nil {
		t.Fatalf("file change: %v", err)
	}
	if fileChange.Behavior != "deny" {
		t.Fatalf("file changes must stay denied in plan mode, got %+v", fileChange)
	}

	// A policy denial must not put the rest of the turn behind approval — that
	// is what turned one denied request into a turn that could run nothing.
	for _, denial := range []adapter.PermissionDecision{unclassified, fileChange} {
		if denial.StrictReview == nil || *denial.StrictReview {
			t.Fatalf("plan gate denial must clear strict review, got %+v", denial.StrictReview)
		}
	}
}

func validStructuredPlanMarkdown(title string) string {
	return strings.Join([]string{
		"# Plan: " + title,
		"",
		"## Goal",
		"Build the requested capability.",
		"",
		"## Context",
		"The session is in plan mode.",
		"",
		"## Approach",
		"Use the existing architecture.",
		"",
		"## Changes",
		"- Update the relevant subsystem.",
		"",
		"## Steps",
		"1. Inspect the code.",
		"2. Implement the change.",
		"3. Verify behavior.",
		"",
		"## Tests",
		"- Run focused tests.",
		"",
		"## Risks And Rollback",
		"Revert the touched files if needed.",
		"",
		"## Open Questions",
		"- None.",
	}, "\n")
}
