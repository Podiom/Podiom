package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestPlanSubmitEndpointValidatesStructuredMarkdown(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake(), DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "planner", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := coreSvc.CreateProject(ctx, projects.Project{ID: "demo", Name: "Demo"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{
		AgentName:                      "planner",
		Origin:                         store.OriginWeb,
		ProjectID:                      "demo",
		CreatePlanBeforeImplementation: true,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	planPath := filepath.Join(paths.ProjectsDir, "demo", "plans", "plan.md")
	body := map[string]string{
		"file_path": planPath,
		"markdown":  "# Plan: Demo\n\n## Goal\nBuild it.",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/plans/"+session.ID+"/submit", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.handlePlan(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "plan markdown is missing required headings") {
		t.Fatalf("missing validation error body: %s", rr.Body.String())
	}

	body["markdown"] = validServerPlanMarkdown("Demo")
	raw, _ = json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "/api/plans/"+session.ID+"/submit", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()

	srv.handlePlan(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("valid status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"PlanState":"awaiting_approval"`) {
		t.Fatalf("valid response missing awaiting state: %s", rr.Body.String())
	}
}

func validServerPlanMarkdown(title string) string {
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
