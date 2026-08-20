package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestStartTaskCreatesRoadmapSessionWithProvenance(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{ProjectID: "mission-control", Title: "Add dark mode", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if sess.Origin != store.OriginRoadmap || sess.TaskID != task.ID {
		t.Fatalf("session provenance wrong: %+v", sess)
	}
	if sess.ProjectID != "mission-control" || sess.InheritedProjectID != "mission-control" {
		t.Fatalf("session project binding = project %q inherited %q, want mission-control", sess.ProjectID, sess.InheritedProjectID)
	}

	moved, err := c.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if moved.Status != store.TaskInProgress {
		t.Fatalf("task should be in_progress, got %q", moved.Status)
	}

	// The session is discoverable from the task for "Open in chat".
	found, ok, err := c.TaskSession(ctx, task.ID)
	if err != nil || !ok || found.ID != sess.ID {
		t.Fatalf("task session lookup failed: ok=%v err=%v", ok, err)
	}

	req := fake.StartRequests[0]
	projectRoot := filepath.Join(c.paths.ProjectsDir, "mission-control")
	agentWorkspace := c.AgentPaths("jared").Workspace
	if req.WorkspaceDir != projectRoot {
		t.Fatalf("workspace dir = %q, want %q", req.WorkspaceDir, projectRoot)
	}
	wantDirs := []string{agentWorkspace, c.paths.ProjectsDir}
	if !reflect.DeepEqual(req.ExtraWorkspaceDirs, wantDirs) {
		t.Fatalf("start extra workspace dirs = %#v, want %#v", req.ExtraWorkspaceDirs, wantDirs)
	}
}

// TestStartTaskUnattendedPersistsTaskPrompt guards the contract an agent start
// relies on: an unattended start seeds the session with the task prompt, so the
// task actually runs and the chat is not empty.
func TestStartTaskUnattendedPersistsTaskPrompt(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"on it"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{
		ProjectID:     "mission-control",
		Title:         "Add dark mode",
		Body:          "Follow the existing theme tokens.",
		AssignedAgent: "jared",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID, Unattended: true})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	history, err := c.History(ctx, sess.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("unattended start left the session with no history")
	}
	if history[0].Role != store.RoleUser {
		t.Fatalf("first message role = %q, want %q", history[0].Role, store.RoleUser)
	}
	// Exact match: a title-only prompt would silently drop the task body.
	if history[0].Content != TaskPrompt(task) {
		t.Fatalf("first message = %q, want %q", history[0].Content, TaskPrompt(task))
	}
}

// TestStartTaskAttendedLeavesFirstTurnToCaller pins the other half of the
// contract: the web client sends the first turn itself, so an attended start
// must not send one — otherwise the browser would duplicate it.
func TestStartTaskAttendedLeavesFirstTurnToCaller(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"on it"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{
		ProjectID:     "mission-control",
		Title:         "Add dark mode",
		Body:          "Follow the existing theme tokens.",
		AssignedAgent: "jared",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	history, err := c.History(ctx, sess.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("attended start should leave history empty, got %d messages", len(history))
	}
	if len(fake.Requests) != 0 {
		t.Fatalf("attended start should not run a turn, got %d", len(fake.Requests))
	}
}

func TestProjectInstructionsApplyToProjectBoundSessions(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"ok"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, err := c.WriteProjectInstructions("mission-control", "project layer\n")
	if err != nil {
		t.Fatalf("write project instructions: %v", err)
	}

	manual, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "jared",
		Origin:    store.OriginWeb,
		ProjectID: "mission-control",
	})
	if err != nil {
		t.Fatalf("create manual project session: %v", err)
	}
	manualReq := startRequestFor(t, fake, manual.ID)
	if !strings.Contains(string(manualReq.Instructions), ".podiom-project-instructions.md") {
		t.Fatalf("manual project session missing project instruction path:\n%s", manualReq.Instructions)
	}
	snap, err := os.ReadFile(filepath.Join(c.AgentPaths("jared").Workspace, ".podiom-project-instructions.md"))
	if err != nil {
		t.Fatalf("read project instruction snapshot: %v", err)
	}
	if string(snap) != "project layer" {
		t.Fatalf("project instruction snapshot = %q", snap)
	}

	task, err := c.CreateTask(ctx, store.Task{ProjectID: "mission-control", Title: "Add dark mode", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	roadmap, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	roadmapReq := startRequestFor(t, fake, roadmap.ID)
	if !strings.Contains(string(roadmapReq.Instructions), ".podiom-project-instructions.md") {
		t.Fatalf("roadmap project session missing project instruction path:\n%s", roadmapReq.Instructions)
	}

	scheduledTask, err := c.CreateTask(ctx, store.Task{ProjectID: "mission-control", Title: "Check the logs", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create scheduled task: %v", err)
	}
	scheduledPickup, err := c.StartTask(ctx, StartTaskRequest{TaskID: scheduledTask.ID, Unattended: true})
	if err != nil {
		t.Fatalf("start scheduled task pickup: %v", err)
	}
	scheduledReq := startRequestFor(t, fake, scheduledPickup.ID)
	if !strings.Contains(string(scheduledReq.Instructions), ".podiom-project-instructions.md") {
		t.Fatalf("scheduled project pickup missing project instruction path:\n%s", scheduledReq.Instructions)
	}

	unbound, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "jared", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create unbound session: %v", err)
	}
	unboundReq := startRequestFor(t, fake, unbound.ID)
	if strings.Contains(string(unboundReq.Instructions), ".podiom-project-instructions.md") {
		t.Fatalf("unbound session should not include project instructions:\n%s", unboundReq.Instructions)
	}
}

func TestCodexProjectSessionUsesExplicitLedgerProjectInstructions(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"ok"}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	info, err := c.WriteProjectInstructions("mission-control", "project layer\n")
	if err != nil {
		t.Fatalf("write project instructions: %v", err)
	}

	sess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "jared",
		Origin:    store.OriginWeb,
		ProjectID: "mission-control",
	})
	if err != nil {
		t.Fatalf("create codex project session: %v", err)
	}
	startReq := startRequestFor(t, fake, sess.ID)
	if len(startReq.Instructions) == 0 || !strings.Contains(string(startReq.Instructions), "base layer") {
		t.Fatalf("codex project session should receive explicit base instructions:\n%s", startReq.Instructions)
	}
	if !strings.Contains(string(startReq.Instructions), "project layer") || strings.Contains(string(startReq.Instructions), info.Path) {
		t.Fatalf("codex explicit instructions should include ledger project instructions without file path:\n%s", startReq.Instructions)
	}
	if _, err := c.AppendTurn(ctx, sess.ID, "Continue"); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(fake.Requests) != 1 || len(fake.Requests[0].Settings.Instructions) == 0 {
		t.Fatalf("codex turn missing explicit instructions: %+v", fake.Requests)
	}
}

func TestStartTaskUsesStoredRunTarget(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{
		Title:         "Use a different engine",
		AssignedAgent: "jared",
		Provider:      config.ProviderCodex,
		Model:         "gpt-5.1",
		Effort:        "high",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if sess.Provider != config.ProviderCodex || sess.Profile != "" || sess.Model != "gpt-5.1" || sess.Effort != "high" {
		t.Fatalf("session target = %+v", sess)
	}
}

func TestCreateTaskRejectsIncompleteRunTarget(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateTask(ctx, store.Task{
		Title:         "Incomplete",
		AssignedAgent: "jared",
		Provider:      config.ProviderCodex,
	}); err == nil {
		t.Fatal("expected incomplete run target to fail")
	}
}

// An unknown project is rejected at create time rather than at start time: the
// roadmap ledger sync ignores unknown ids, so without this the task is created
// happily and can then never be started, because CreateSession refuses it.
func TestCreateTaskRejectsUnknownProject(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateTask(ctx, store.Task{
		Title:         "Somewhere else",
		AssignedAgent: "jared",
		ProjectID:     "nope",
	}); err == nil {
		t.Fatal("expected unknown project to fail")
	}
}

// A task a goal's plan created belongs to the goal's project unless the agent
// deliberately named another one, and the session it spawns has to carry that
// project all the way through — the task card and the run must agree.
func TestCreateTaskInheritsGoalProject(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	for _, id := range []string{"mission-control", "beta"} {
		if _, err := c.CreateProject(ctx, projects.Project{ID: id, Name: id}); err != nil {
			t.Fatalf("create project %q: %v", id, err)
		}
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "jared", ProjectID: "mission-control"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	inherited, err := c.CreateTask(ctx, store.Task{Title: "Do the thing", AssignedAgent: "jared", GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if inherited.ProjectID != "mission-control" {
		t.Fatalf("goal task project = %q, want mission-control", inherited.ProjectID)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: inherited.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if sess.ProjectID != "mission-control" {
		t.Fatalf("goal task session project = %q, want mission-control", sess.ProjectID)
	}

	// Explicit wins: inheritance is a default, not a force.
	explicit, err := c.CreateTask(ctx, store.Task{Title: "Elsewhere", AssignedAgent: "jared", GoalID: goal.ID, ProjectID: "beta"})
	if err != nil {
		t.Fatalf("create task with explicit project: %v", err)
	}
	if explicit.ProjectID != "beta" {
		t.Fatalf("explicit task project = %q, want beta", explicit.ProjectID)
	}
}

func TestCreateSessionStoresProjectAndRejectsUnknownProject(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "jared",
		Origin:    store.OriginWeb,
		ProjectID: "mission-control",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.ProjectID != "mission-control" {
		t.Fatalf("session project id = %q, want mission-control", sess.ProjectID)
	}

	if _, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "jared",
		Origin:    store.OriginWeb,
		ProjectID: "missing-project",
	}); err == nil {
		t.Fatal("expected unknown project id to fail")
	}
}

func TestConnectedRepoContextIsSentToRoadmapSession(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := projects.SnapshotRepo("Podiom", "Podiom", "https://github.com/Podiom/Podiom", "main", "main")
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control", Repo: &repo}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{ProjectID: "mission-control", Title: "Inspect repo", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := c.AppendTurn(ctx, sess.ID, TaskPrompt(task)); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake requests = %d, want 1", len(fake.Requests))
	}
	root := filepath.Join(c.paths.ProjectsDir, "mission-control", "repo")
	req := fake.Requests[0]
	if !strings.Contains(req.Message, "local source snapshot") || !strings.Contains(req.Message, root) {
		t.Fatalf("request missing repo context:\n%s", req.Message)
	}
	if req.Settings.WorkspaceDir != root {
		t.Fatalf("workspace dir = %q, want %q", req.Settings.WorkspaceDir, root)
	}
	projectRoot := filepath.Join(c.paths.ProjectsDir, "mission-control")
	wantDirs := []string{c.AgentPaths("jared").Workspace, c.paths.ProjectsDir, projectRoot}
	if !reflect.DeepEqual(req.Settings.ExtraWorkspaceDirs, wantDirs) {
		t.Fatalf("extra workspace dirs = %#v, want %#v", req.Settings.ExtraWorkspaceDirs, wantDirs)
	}
	if _, err := c.AppendTurn(ctx, sess.ID, "Continue with repo context"); err != nil {
		t.Fatalf("append second turn: %v", err)
	}
	if len(fake.Requests) != 2 {
		t.Fatalf("fake requests after second turn = %d, want 2", len(fake.Requests))
	}
	if !strings.Contains(fake.Requests[1].Message, "local source snapshot") || !strings.Contains(fake.Requests[1].Message, "Continue with repo context") {
		t.Fatalf("second request missing repo context:\n%s", fake.Requests[1].Message)
	}
}

func TestConnectedRepoContextIsSentToManualProjectSession(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := projects.SnapshotRepo("Podiom", "Podiom", "https://github.com/Podiom/Podiom", "main", "main")
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control", Repo: &repo}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "jared",
		Origin:    store.OriginWeb,
		ProjectID: "mission-control",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := c.AppendTurn(ctx, sess.ID, "Inspect the linked project"); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake requests = %d, want 1", len(fake.Requests))
	}
	root := filepath.Join(c.paths.ProjectsDir, "mission-control", "repo")
	req := fake.Requests[0]
	if !strings.Contains(req.Message, "Podiom project context for this session") || !strings.Contains(req.Message, root) {
		t.Fatalf("request missing project context:\n%s", req.Message)
	}
	if req.Settings.WorkspaceDir != root {
		t.Fatalf("workspace dir = %q, want %q", req.Settings.WorkspaceDir, root)
	}
	projectRoot := filepath.Join(c.paths.ProjectsDir, "mission-control")
	wantDirs := []string{c.AgentPaths("jared").Workspace, c.paths.ProjectsDir, projectRoot}
	if !reflect.DeepEqual(req.Settings.ExtraWorkspaceDirs, wantDirs) {
		t.Fatalf("extra workspace dirs = %#v, want %#v", req.Settings.ExtraWorkspaceDirs, wantDirs)
	}
}

func TestPlainProjectContextUsesProjectDirectory(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "dinesh", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "snake", Name: "Snake"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{ProjectID: "snake", Title: "snake", Body: "build a small snake game", AssignedAgent: "dinesh"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := c.AppendTurn(ctx, sess.ID, TaskPrompt(task)); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake requests = %d, want 1", len(fake.Requests))
	}
	projectRoot := filepath.Join(c.paths.ProjectsDir, "snake")
	req := fake.Requests[0]
	if req.Settings.WorkspaceDir != projectRoot {
		t.Fatalf("workspace dir = %q, want %q", req.Settings.WorkspaceDir, projectRoot)
	}
	if !strings.Contains(req.Message, "Create and edit durable project files under "+projectRoot) {
		t.Fatalf("request missing plain project workspace instruction:\n%s", req.Message)
	}
	wantDirs := []string{c.AgentPaths("dinesh").Workspace, c.paths.ProjectsDir}
	if !reflect.DeepEqual(req.Settings.ExtraWorkspaceDirs, wantDirs) {
		t.Fatalf("extra workspace dirs = %#v, want %#v", req.Settings.ExtraWorkspaceDirs, wantDirs)
	}
}

func TestLegacyRoadmapSessionProjectIDFallback(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{ProjectID: "mission-control", Title: "Legacy task", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	legacy, err := c.store.CreateSession(ctx, store.Session{
		AgentName:      "jared",
		Provider:       config.ProviderClaude,
		PermissionMode: config.PermissionApprove,
		Origin:         store.OriginRoadmap,
		TaskID:         task.ID,
	})
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}
	if legacy.ProjectID != "" {
		t.Fatalf("raw legacy project id = %q, want empty", legacy.ProjectID)
	}
	got, err := c.GetSession(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ProjectID != "mission-control" {
		t.Fatalf("fallback project id = %q, want mission-control", got.ProjectID)
	}
	list, err := c.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(list) == 0 || list[0].ProjectID != "mission-control" {
		t.Fatalf("listed sessions missing fallback project id: %+v", list)
	}
}

func TestStartTaskRequiresAssignedAgent(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	task, err := c.CreateTask(ctx, store.Task{Title: "unassigned work"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID}); err == nil {
		t.Fatal("expected error starting a task with no assigned agent")
	}
}

func TestDescribeTaskPromptEmbedsExpandedProjectContext(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newScheduledTestCore(t)
	defer cleanup()
	fake.Responses = []string{"Build the settings page with clear save and cancel flows."}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{
		ID:          "mission-control",
		Name:        "Mission Control",
		Description: "Operations dashboard for coordinating work.",
		Notes:       "Prefer quiet, utilitarian interfaces.",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	nearby, err := c.CreateTask(ctx, store.Task{
		ProjectID:     "mission-control",
		Title:         "Add project filters",
		Body:          "Let users filter by active projects.",
		AssignedAgent: "writer",
		Status:        store.TaskDone,
	})
	if err != nil {
		t.Fatalf("create nearby task: %v", err)
	}

	body, err := c.DescribeTask(ctx, DescribeTaskRequest{
		AgentName:     "writer",
		ProjectID:     "mission-control",
		Title:         "Add settings page",
		Body:          "Need a settings page.",
		AssignedAgent: "writer",
	})
	if err != nil {
		t.Fatalf("describe task: %v", err)
	}
	if body == "" {
		t.Fatal("expected drafted body")
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(fake.Requests))
	}
	prompt := fake.Requests[0].Message
	for _, want := range []string{
		"projects.yaml under the Podiom data directory",
		"Project context:",
		"id: mission-control",
		"Operations dashboard for coordinating work.",
		nearby.ID,
		"Add project filters",
		"Let users filter by active projects.",
		`Task title: "Add settings page"`,
		"Need a settings page.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "~/.podiom") {
		t.Fatalf("prompt should not hardcode a Unix home path:\n%s", prompt)
	}
}

func TestUpdateTaskLocksContentAfterSessionButAllowsStatus(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{Title: "Draft docs", Body: "Initial body", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.Body = "Updated before start"
	task, err = c.UpdateTask(ctx, task)
	if err != nil {
		t.Fatalf("update before session: %v", err)
	}
	if _, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID}); err != nil {
		t.Fatalf("start task: %v", err)
	}

	task.Body = "Updated after start"
	if _, err := c.UpdateTask(ctx, task); err == nil {
		t.Fatal("expected content update after session to fail")
	}

	started, err := c.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get started task: %v", err)
	}
	started.Status = store.TaskDone
	if _, err := c.UpdateTask(ctx, started); err != nil {
		t.Fatalf("status-only update after session should succeed: %v", err)
	}
}

func TestProjectRoadmapsSyncAndBacklogIsDropped(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	raw := []byte(`projects:
    - id: alpha
      name: Alpha
      description: Alpha project.
      path: alpha
      status: active
      stack: []
      repo: ""
      backlog: ["legacy"]
      roadmap: []
      notes: ""
    - id: beta
      name: Beta
      description: Beta project.
      path: beta
      status: active
      stack: []
      repo: ""
      roadmap: []
      notes: ""
`)
	if err := os.WriteFile(c.paths.ProjectsYAML, raw, 0o644); err != nil {
		t.Fatalf("write legacy projects.yaml: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{ProjectID: "alpha", Title: "Alpha task", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	projectsList, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if got := roadmapFor(projectsList, "alpha"); len(got) != 1 || got[0] != task.ID {
		t.Fatalf("alpha roadmap not synced: %+v", projectsList)
	}
	if got := roadmapFor(projectsList, "beta"); len(got) != 0 {
		t.Fatalf("beta roadmap should be empty: %+v", got)
	}

	task.ProjectID = "beta"
	if _, err := c.UpdateTask(ctx, task); err != nil {
		t.Fatalf("reassign task: %v", err)
	}
	projectsList, err = c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if got := roadmapFor(projectsList, "alpha"); len(got) != 0 {
		t.Fatalf("alpha roadmap should be empty after reassignment: %+v", got)
	}
	if got := roadmapFor(projectsList, "beta"); len(got) != 1 || got[0] != task.ID {
		t.Fatalf("beta roadmap not synced: %+v", got)
	}
	out, err := os.ReadFile(c.paths.ProjectsYAML)
	if err != nil {
		t.Fatalf("read projects.yaml: %v", err)
	}
	if strings.Contains(string(out), "backlog:") {
		t.Fatalf("legacy backlog field should be dropped after ledger write:\n%s", out)
	}
}

func TestRoadmapQuestionMovesTaskReviewAndRestores(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{Title: "Clarify scope", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	moved, err := c.MoveRoadmapSessionTaskForQuestion(ctx, sess.ID)
	if err != nil {
		t.Fatalf("move for question: %v", err)
	}
	if !moved {
		t.Fatal("expected in_progress task to move to review")
	}
	got, err := c.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != store.TaskReview {
		t.Fatalf("task should be review, got %q", got.Status)
	}

	if err := c.RestoreRoadmapSessionTaskAfterQuestion(ctx, sess.ID); err != nil {
		t.Fatalf("restore after question: %v", err)
	}
	got, err = c.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != store.TaskInProgress {
		t.Fatalf("task should be in_progress, got %q", got.Status)
	}
}

func roadmapFor(list []projects.Project, id string) []string {
	for _, project := range list {
		if project.ID == id {
			return project.Roadmap
		}
	}
	return nil
}

func TestRoadmapQuestionDoesNotMoveTaskAlreadyInReview(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{Title: "Already reviewing", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	task.Status = store.TaskReview
	if _, err := c.UpdateTask(ctx, task); err != nil {
		t.Fatalf("set review: %v", err)
	}

	moved, err := c.MoveRoadmapSessionTaskForQuestion(ctx, sess.ID)
	if err != nil {
		t.Fatalf("move for question: %v", err)
	}
	if moved {
		t.Fatal("task already in review should not be marked as moved")
	}
}

func TestRoadmapGenericReviewHelpersIgnoreNonRoadmapSessions(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "jared", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	moved, err := c.MoveRoadmapSessionTaskToReview(ctx, sess.ID)
	if err != nil {
		t.Fatalf("move non-roadmap session: %v", err)
	}
	if moved {
		t.Fatal("non-roadmap session should not move a task")
	}
	if err := c.RestoreRoadmapSessionTaskToInProgress(ctx, sess.ID); err != nil {
		t.Fatalf("restore non-roadmap session: %v", err)
	}
}

func TestRoadmapGenericReviewHelpersPreserveManualStatus(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := c.CreateTask(ctx, store.Task{Title: "Manual move", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	task.Status = store.TaskDone
	if _, err := c.UpdateTask(ctx, task); err != nil {
		t.Fatalf("set done: %v", err)
	}

	moved, err := c.MoveRoadmapSessionTaskToReview(ctx, sess.ID)
	if err != nil {
		t.Fatalf("move done task: %v", err)
	}
	if moved {
		t.Fatal("done task should not be marked as moved")
	}
	if err := c.RestoreRoadmapSessionTaskToInProgress(ctx, sess.ID); err != nil {
		t.Fatalf("restore done task: %v", err)
	}
	got, err := c.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != store.TaskDone {
		t.Fatalf("manual status should be preserved, got %q", got.Status)
	}
}
