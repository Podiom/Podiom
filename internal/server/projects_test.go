package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestProjectGitEndpointInitializesALocalRepo(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "plain", Name: "Plain"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/plain/git", bytes.NewBufferString(`{
		"git": {
			"enabled": true,
			"default_branch": "trunk",
			"branching": "direct",
			"commit": "ask",
			"pull_on_session_start": true
		}
	}`))
	rr := httptest.NewRecorder()
	srv.handleProject(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(paths.ProjectsDir, "plain", ".git")); err != nil {
		t.Fatalf("Git was not initialized: %v", err)
	}
	project, err := srv.core.GetProject(ctx, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if project.Git == nil || !project.Git.Enabled || !project.Git.PullOnSessionStart || project.Git.DefaultBranch != "trunk" {
		t.Fatalf("git = %#v", project.Git)
	}
	if project.GitState == nil || !project.GitState.Detected {
		t.Fatalf("git state = %#v", project.GitState)
	}
}

// Replacing a project's files is the user's call, so the endpoint refuses until
// they have said yes and answers 409 rather than an opaque error.
func TestProjectGitEndpointNeedsConfirmationBeforeReplacing(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "plain", Name: "Plain"}); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(paths.ProjectsDir, "plain")
	if err := os.WriteFile(filepath.Join(projectDir, "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origin := seedServerOrigin(t)

	body := `{"git":{"enabled":true,"remote":"` + origin + `","default_branch":"main"},"force":%s}`
	rr := httptest.NewRecorder()
	srv.handleProject(rr, httptest.NewRequest(http.MethodPost, "/api/projects/plain/git",
		bytes.NewBufferString(fmt.Sprintf(body, "false"))))
	if rr.Code != http.StatusConflict {
		t.Fatalf("unconfirmed status = %d, body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	srv.handleProject(rr, httptest.NewRequest(http.MethodPost, "/api/projects/plain/git",
		bytes.NewBufferString(fmt.Sprintf(body, "true"))))
	if rr.Code != http.StatusOK {
		t.Fatalf("confirmed status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, "README.md")); err != nil {
		t.Fatalf("cloned file: %v", err)
	}
	project, err := srv.core.GetProject(ctx, "plain")
	if err != nil {
		t.Fatal(err)
	}
	// The clone landed on the remote's HEAD, not the branch the caller guessed.
	if project.Git == nil || project.Git.DefaultBranch != "master" {
		t.Fatalf("git = %#v", project.Git)
	}
}

// PATCH records policy and nothing else. It is what the page uses for colour and
// description, so it must never be able to move a user's files.
func TestProjectPatchDoesNotTouchTheWorkingCopy(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "plain", Name: "Plain"}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.handleProject(rr, httptest.NewRequest(http.MethodPatch, "/api/projects/plain",
		bytes.NewBufferString(`{"git":{"enabled":true,"default_branch":"trunk"}}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", rr.Code, rr.Body.String())
	}
	project, err := srv.core.GetProject(ctx, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if project.Git == nil || !project.Git.Enabled {
		t.Fatalf("policy was not persisted: %#v", project.Git)
	}
	if _, err := os.Stat(filepath.Join(paths.ProjectsDir, "plain", ".git")); !os.IsNotExist(err) {
		t.Fatal("PATCH created a repository; only the git endpoint may touch the disk")
	}
}

// seedServerOrigin builds a bare repository whose HEAD is master, so a test can
// tell the cloned branch apart from the caller's guess.
func seedServerOrigin(t *testing.T) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--bare", origin)
	run("init", "--initial-branch=master", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("from the remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-C", seed, "add", "README.md")
	run("-C", seed, "-c", "user.name=Podiom Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	run("-C", seed, "remote", "add", "origin", origin)
	run("-C", seed, "push", "-u", "origin", "master")
	run("-C", origin, "symbolic-ref", "HEAD", "refs/heads/master")
	return origin
}

// startTaskTestFixture creates an agent, a project, and one task with a body, so
// the start-endpoint tests can assert on the full Title + Body prompt.
func startTaskTestFixture(t *testing.T, srv *Server, title string) store.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := srv.core.GetAgent(ctx, "writer"); err != nil {
		if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
			t.Fatalf("create project: %v", err)
		}
	}
	task, err := srv.core.CreateTask(ctx, store.Task{
		ProjectID:     "mission-control",
		Title:         title,
		Body:          "Follow the existing theme tokens.",
		AssignedAgent: "writer",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// TestTaskStartUnattendedRunsTaskPrompt is the regression test for the bug where
// an agent-initiated start (podiom_start_task) created a session and moved the
// task to in_progress but never sent the prompt, leaving an empty chat and a
// task that never ran.
func TestTaskStartUnattendedRunsTaskPrompt(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	task := startTaskTestFixture(t, srv, "Add dark mode")

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/start", bytes.NewBufferString(`{"unattended":true}`))
	rr := httptest.NewRecorder()
	srv.handleTask(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var session store.Session
	if err := json.NewDecoder(rr.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.ID == "" {
		t.Fatal("start returned no session")
	}

	// The turn runs in a background goroutine, so poll for the seeded prompt.
	want := core.TaskPrompt(task)
	deadline := time.Now().Add(5 * time.Second)
	var seeded bool
	for time.Now().Before(deadline) {
		history, err := srv.core.History(ctx, session.ID)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(history) > 0 {
			if history[0].Role != store.RoleUser || history[0].Content != want {
				t.Fatalf("first message = %q (role %q), want %q as user", history[0].Content, history[0].Role, want)
			}
			seeded = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !seeded {
		t.Fatal("unattended start never seeded the task prompt")
	}
	// Let the background goroutine's post-run bookkeeping settle before the
	// test store closes.
	time.Sleep(200 * time.Millisecond)
}

// TestTaskStartWithoutBodyStaysAttended pins the browser's contract: it POSTs
// with no body at all, which must decode to unattended=false so the web client
// keeps sending the first turn itself (and does not send it twice).
func TestTaskStartWithoutBodyStaysAttended(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	task := startTaskTestFixture(t, srv, "Add light mode")

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/start", nil)
	rr := httptest.NewRecorder()
	srv.handleTask(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var session store.Session
	if err := json.NewDecoder(rr.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.ID == "" {
		t.Fatal("start returned no session")
	}

	time.Sleep(200 * time.Millisecond)
	history, err := srv.core.History(ctx, session.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("attended start should leave history empty, got %d messages", len(history))
	}
}
