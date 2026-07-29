package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestTaskDescribeEndpointsReturnBody(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := srv.core.CreateTask(ctx, store.Task{
		ProjectID:     "mission-control",
		Title:         "Add settings",
		Body:          "Add a settings page.",
		AssignedAgent: "writer",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "new draft",
			path: "/api/tasks/describe",
			body: `{"agent":"writer","project_id":"mission-control","title":"Draft task","body":"Draft this.","assigned_agent":"writer"}`,
		},
		{
			name: "existing task",
			path: "/api/tasks/" + task.ID + "/describe",
			body: `{"agent":"writer"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
			rr := httptest.NewRecorder()
			srv.handleTask(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			var res struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(rr.Body).Decode(&res); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if res.Body == "" {
				t.Fatalf("empty body response")
			}
		})
	}
}

func TestProjectInstructionsEndpointReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/mission-control/instructions", nil)
	rr := httptest.NewRecorder()
	srv.handleProject(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got core.ProjectInstructions
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode initial: %v", err)
	}
	if got.ProjectID != "mission-control" || got.Instructions != "" || !strings.HasSuffix(got.Path, "/projects/projects.yaml") {
		t.Fatalf("initial instructions = %+v", got)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/projects/mission-control/instructions", bytes.NewBufferString(`{"instructions":"project layer\n"}`))
	rr = httptest.NewRecorder()
	srv.handleProject(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if got.Instructions != "project layer\n" {
		t.Fatalf("saved instructions = %q", got.Instructions)
	}
	project, err := srv.core.GetProject(ctx, "mission-control")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if project.Instructions != "project layer\n" {
		t.Fatalf("ledger instructions = %q", project.Instructions)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects/missing/instructions", nil)
	rr = httptest.NewRecorder()
	srv.handleProject(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("missing project should fail, body=%s", rr.Body.String())
	}
}
func TestProjectGitPatchPersistsToLedger(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	const remote = "git@github.com:acme/mission-control.git"
	if _, err := srv.core.CreateProject(ctx, projects.Project{
		ID:   "mission-control",
		Name: "Mission Control",
		Git:  &projects.Git{Enabled: true, Remote: remote},
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/projects/mission-control", bytes.NewBufferString(`{
		"git": {
			"enabled": false,
			"remote": "git@github.com:acme/mission-control.git",
			"default_branch": "trunk",
			"branching": "branch-per-task",
			"branch_prefixes": {
				"feature": "feat/",
				"spike": "spike/"
			},
			"commit": "auto"
		}
	}`))
	rr := httptest.NewRecorder()
	srv.handleProject(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", rr.Code, rr.Body.String())
	}

	var response projects.Project
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Git == nil || response.Git.Remote != remote || response.Git.DefaultBranch != "trunk" {
		t.Fatalf("response git = %#v", response.Git)
	}

	// Read through a fresh ledger to prove the PATCH reached projects.yaml,
	// rather than only updating the response object.
	persisted, err := projects.New(paths.ProjectsDir).Get("mission-control")
	if err != nil {
		t.Fatalf("read persisted project: %v", err)
	}
	if persisted.Git == nil {
		t.Fatal("persisted git block is missing")
	}
	if persisted.Git.Enabled ||
		persisted.Git.Remote != remote ||
		persisted.Git.DefaultBranch != "trunk" ||
		persisted.Git.Branching != projects.BranchingPerTask ||
		persisted.Git.Commit != projects.CommitAuto ||
		persisted.Git.BranchPrefixes["feature"] != "feat/" ||
		persisted.Git.BranchPrefixes["spike"] != "spike/" {
		t.Fatalf("persisted git block = %#v", persisted.Git)
	}
}
