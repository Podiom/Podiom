package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func newGitProject(t *testing.T, c *Core, git *projects.Git) store.Session {
	t.Helper()
	ctx := context.Background()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "dev", Provider: "claude"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App", Git: git}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "dev", Origin: store.OriginWeb, ProjectID: "app",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session
}

// A project that declares local git gets a working copy created in place, and
// the agent is told the policy in one line rather than having to infer it.
func TestGitEnabledProjectMaterializesALocalRepo(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newGitProject(t, c, &projects.Git{Enabled: true, DefaultBranch: "main"})

	projectCtx, err := c.SessionProjectContext(ctx, session.ID)
	if err != nil {
		t.Fatalf("project context: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectCtx.CodeDir, ".git")); err != nil {
		t.Fatalf("git-enabled project has no repository: %v", err)
	}
	if !projectCtx.SourceCtl.Ready {
		t.Fatalf("source control not ready: %s", projectCtx.SourceCtl.Reason)
	}
	if projectCtx.SourceCtl.Branch != "main" {
		t.Fatalf("branch: got %q want main", projectCtx.SourceCtl.Branch)
	}
}

// A project without source control gets a plain directory and an agent that is
// told not to run git — the posture is a real choice, not a missing setting.
func TestProjectWithoutGitStaysAPlainDirectory(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newGitProject(t, c, nil)

	projectCtx, err := c.SessionProjectContext(ctx, session.ID)
	if err != nil {
		t.Fatalf("project context: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectCtx.CodeDir, ".git")); !os.IsNotExist(err) {
		t.Fatal("project without git should not get a repository")
	}
	if line := gitPromptLine(projectCtx.SourceCtl); !strings.Contains(line, "does not use git") {
		t.Fatalf("prompt line should tell the agent not to run git: %q", line)
	}

	if _, err := c.StartWork(ctx, session.ID, "bugfix", "anything"); err == nil {
		t.Fatal("start work should refuse on a project without source control")
	}
}

// The branching policy is executable: Podiom performs the checkout, so an agent
// cannot skip it the way it could skip a prompt instruction.
func TestStartWorkAppliesTheBranchingPolicy(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newGitProject(t, c, &projects.Git{
		Enabled: true, DefaultBranch: "main", Branching: projects.BranchingPerTask,
	})

	projectCtx, err := c.SessionProjectContext(ctx, session.ID)
	if err != nil {
		t.Fatalf("project context: %v", err)
	}
	if !projectCtx.SourceCtl.Ready {
		t.Skipf("git not ready in this environment: %s", projectCtx.SourceCtl.Reason)
	}

	result, err := c.StartWork(ctx, session.ID, "bugfix", "Widget Crash")
	if err != nil {
		t.Fatalf("start work: %v", err)
	}
	if result.Branch != "fix/widget-crash" {
		t.Fatalf("branch: got %q want fix/widget-crash", result.Branch)
	}
	if !result.Created {
		t.Fatal("first call should have created the branch")
	}

	// Calling it again for the same work is safe — the agent may well do so.
	again, err := c.StartWork(ctx, session.ID, "bugfix", "Widget Crash")
	if err != nil {
		t.Fatalf("second start work: %v", err)
	}
	if again.Branch != result.Branch || again.Created {
		t.Fatalf("repeat call should be idempotent: %#v", again)
	}
}

// Under a direct policy the answer is the default branch, so the same agent
// call does the right thing on a project that does not want feature branches.
func TestStartWorkHonoursDirectPolicy(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	session := newGitProject(t, c, &projects.Git{
		Enabled: true, DefaultBranch: "main", Branching: projects.BranchingDirect,
	})

	projectCtx, err := c.SessionProjectContext(ctx, session.ID)
	if err != nil {
		t.Fatalf("project context: %v", err)
	}
	if !projectCtx.SourceCtl.Ready {
		t.Skipf("git not ready in this environment: %s", projectCtx.SourceCtl.Reason)
	}
	result, err := c.StartWork(ctx, session.ID, "bugfix", "Widget Crash")
	if err != nil {
		t.Fatalf("start work: %v", err)
	}
	if result.Branch != "main" {
		t.Fatalf("direct policy must stay on main, got %q", result.Branch)
	}
}

// When git is declared but unusable, the session still works — the agent is
// told to ask once and then carry on without source control.
func TestUnreadyGitDegradesRatherThanFailing(t *testing.T) {
	line := gitPromptLine(ProjectGitState{
		Declared: &projects.Git{Enabled: true},
		Ready:    false,
		Reason:   "git is not installed on this machine.",
	})
	for _, want := range []string{"ask the user once", "uncommitted", "do not ask again"} {
		if !strings.Contains(strings.ToLower(line), want) {
			t.Fatalf("degradation instruction missing %q: %q", want, line)
		}
	}
}
