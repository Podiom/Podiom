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
	submitted, err := c.SubmitPlan(ctx, SubmitPlanRequest{
		SessionID: session.ID,
		FilePath:  planPath,
		Markdown:  "# Plan\n\n- Step one",
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
	if len(history) != 1 || history[0].Role != store.RoleAssistant || !strings.Contains(history[0].Content, "# Plan") {
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
	if _, err := c.SubmitPlan(ctx, SubmitPlanRequest{SessionID: session.ID, FilePath: planPath, Markdown: "# Plan v2"}); err != nil {
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
