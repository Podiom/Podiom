package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func runGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", dir, "add", name)
	runGit(t, "-C", dir, "-c", "user.name=Podiom Test", "-c", "user.email=test@example.invalid", "commit", "-m", message)
}

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

	// Provide a temporary git identity so the repo is reported as ready in any
	// environment, including CI runners that have no global git config.
	tmpHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpHome, ".gitconfig"),
		[]byte("[user]\n\tname = Test User\n\temail = test@example.com\n"), 0o644); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}
	t.Setenv("HOME", tmpHome)

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

func TestProjectAdoptsRepositoryAndTracksRemoteChanges(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	created, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"})
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", filepath.Join(c.paths.ProjectsDir, created.Path), "init", "--initial-branch=trunk")
	runGit(t, "-C", filepath.Join(c.paths.ProjectsDir, created.Path), "remote", "add", "origin", "git@example.test:one/app.git")

	adopted, err := c.GetProject(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Git == nil || !adopted.Git.Enabled || adopted.Git.Remote != "git@example.test:one/app.git" || adopted.Git.DefaultBranch != "trunk" {
		t.Fatalf("adopted git = %#v", adopted.Git)
	}
	if adopted.GitState == nil || !adopted.GitState.Detected {
		t.Fatalf("runtime state = %#v", adopted.GitState)
	}
	runGit(t, "-C", filepath.Join(c.paths.ProjectsDir, created.Path), "remote", "set-url", "origin", "git@example.test:two/app.git")
	changed, err := c.GetProject(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Git.Remote != "git@example.test:two/app.git" {
		t.Fatalf("changed remote = %q", changed.Git.Remote)
	}
	runGit(t, "-C", filepath.Join(c.paths.ProjectsDir, created.Path), "remote", "remove", "origin")
	changed, err = c.GetProject(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Git.Remote != "" {
		t.Fatalf("removed remote remained configured as %q", changed.Git.Remote)
	}
	disabled := *changed.Git
	disabled.Enabled = false
	ignored, err := c.UpdateProject(ctx, created.ID, projects.ProjectPatch{Git: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if ignored.Git.Enabled || ignored.GitState == nil || !strings.Contains(ignored.GitState.Warning, "explicitly disabled") {
		t.Fatalf("disabled repository state = %#v / %#v", ignored.Git, ignored.GitState)
	}
}

func TestPullOnSessionStartUpdatesDefaultBranch(t *testing.T) {
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	runGit(t, "init", "--bare", origin)
	runGit(t, "init", "--initial-branch=main", seed)
	commitFile(t, seed, "version.txt", "one\n", "one")
	runGit(t, "-C", seed, "remote", "add", "origin", origin)
	runGit(t, "-C", seed, "push", "-u", "origin", "main")
	runGit(t, "-C", origin, "symbolic-ref", "HEAD", "refs/heads/main")

	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "dev", Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App", Git: &projects.Git{
		Enabled: true, Remote: origin, DefaultBranch: "main", PullOnSessionStart: true,
	}}); err != nil {
		t.Fatal(err)
	}
	first, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "dev", Origin: store.OriginWeb, ProjectID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceControlWarning != "" {
		t.Fatalf("first startup warning = %q", first.SourceControlWarning)
	}
	commitFile(t, seed, "version.txt", "two\n", "two")
	runGit(t, "-C", seed, "push", "origin", "main")
	checkout := filepath.Join(c.paths.ProjectsDir, "app")
	runGit(t, "-C", checkout, "checkout", "-b", "feature/work")

	second, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "dev", Origin: store.OriginWeb, ProjectID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if second.SourceControlWarning != "" {
		t.Fatalf("second startup warning = %q", second.SourceControlWarning)
	}
	if branch := runGit(t, "-C", checkout, "branch", "--show-current"); branch != "main" {
		t.Fatalf("startup branch = %q", branch)
	}
	raw, err := os.ReadFile(filepath.Join(checkout, "version.txt"))
	if err != nil || string(raw) != "two\n" {
		t.Fatalf("updated file = %q, %v", raw, err)
	}
}

func TestPullOnSessionStartDirtyTreeContinuesWithWarning(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "dev", Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App", Git: &projects.Git{
		Enabled: true, DefaultBranch: "main", PullOnSessionStart: true,
	}}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(c.paths.ProjectsDir, "app")
	runGit(t, "-C", root, "init", "--initial-branch=main")
	commitFile(t, root, "dirty.txt", "clean\n", "initial")
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "dev", Origin: store.OriginWeb, ProjectID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(session.SourceControlWarning, "uncommitted changes") {
		t.Fatalf("warning = %q", session.SourceControlWarning)
	}
	persisted, err := c.GetSession(ctx, session.ID)
	if err != nil || persisted.SourceControlWarning != session.SourceControlWarning {
		t.Fatalf("persisted warning = %q, %v", persisted.SourceControlWarning, err)
	}
}

func TestGitHubCloneRecordsCheckoutAndRemote(t *testing.T) {
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	runGit(t, "init", "--bare", origin)
	runGit(t, "init", "--initial-branch=main", seed)
	commitFile(t, seed, "README.md", "hello\n", "initial")
	runGit(t, "-C", seed, "remote", "add", "origin", origin)
	runGit(t, "-C", seed, "push", "-u", "origin", "main")
	runGit(t, "-C", origin, "symbolic-ref", "HEAD", "refs/heads/main")

	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	repo := projects.CheckoutRepo("acme", "app", "https://github.example/acme/app", "main", "main")
	cloned, err := c.CloneGitHubProject(ctx, "app", repo, []string{origin})
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Git == nil || !cloned.Git.Enabled || cloned.Git.Remote != origin {
		t.Fatalf("git = %#v", cloned.Git)
	}
	if cloned.Repo == nil || cloned.Repo.Mode != "git" || cloned.Repo.SourceKind != "clone" {
		t.Fatalf("repo = %#v", cloned.Repo)
	}
	if cloned.GitState == nil || !cloned.GitState.Detected {
		t.Fatalf("git state = %#v", cloned.GitState)
	}
	if _, err := os.Stat(filepath.Join(c.paths.ProjectsDir, "app", "repo", "README.md")); err != nil {
		t.Fatalf("cloned file: %v", err)
	}
}

func TestSnapshotFallbackPullContinuesWithWarning(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "dev", Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	repo := projects.SnapshotRepo("acme", "app", "https://github.example/acme/app", "main", "main")
	if _, err := c.CreateProject(ctx, projects.Project{
		ID: "app", Name: "App", Repo: &repo,
		Git: &projects.Git{Enabled: true, Remote: "https://github.example/acme/app.git", DefaultBranch: "main", PullOnSessionStart: true},
	}); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(c.paths.ProjectsDir, "app", "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("snapshot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "dev", Origin: store.OriginWeb, ProjectID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(session.SourceControlWarning), "snapshot") {
		t.Fatalf("warning = %q", session.SourceControlWarning)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("snapshot fallback was converted unexpectedly: %v", err)
	}
}

// seedOrigin builds a bare repository whose HEAD is master, so a test can tell
// the branch the clone actually landed on apart from the "main" default the
// caller guessed.
func seedOrigin(t *testing.T) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	runGit(t, "init", "--bare", origin)
	runGit(t, "init", "--initial-branch=master", seed)
	commitFile(t, seed, "README.md", "from the remote\n", "initial")
	runGit(t, "-C", seed, "remote", "add", "origin", origin)
	runGit(t, "-C", seed, "push", "-u", "origin", "master")
	runGit(t, "-C", origin, "symbolic-ref", "HEAD", "refs/heads/master")
	return origin
}

// Enabling git with a remote on an empty project clones it. This is the whole
// point of the feature: before, it ran git init and the remote's content never
// arrived.
func TestEnableGitClonesTheRemoteIntoAnEmptyProject(t *testing.T) {
	ctx := context.Background()
	origin := seedOrigin(t)
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	updated, err := c.ConfigureProjectGit(ctx, "app", projects.Git{
		Enabled: true, Remote: origin, DefaultBranch: "main",
	}, false)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if updated.Git == nil || !updated.Git.Enabled || updated.Git.Remote != origin {
		t.Fatalf("git = %#v", updated.Git)
	}
	// The clone knows the remote's HEAD; the caller's "main" was only a guess.
	if updated.Git.DefaultBranch != "master" {
		t.Fatalf("default branch = %q, want master", updated.Git.DefaultBranch)
	}
	if updated.GitState == nil || !updated.GitState.Detected {
		t.Fatalf("git state = %#v", updated.GitState)
	}
	if _, err := os.Stat(filepath.Join(c.paths.ProjectsDir, "app", "README.md")); err != nil {
		t.Fatalf("cloned file: %v", err)
	}
}

// Cloning over a project that already holds files destroys nothing without the
// user's word for it, and when they give it the old files are kept.
func TestEnableGitOnANonEmptyProjectNeedsConfirmationThenBacksUpAndClones(t *testing.T) {
	ctx := context.Background()
	origin := seedOrigin(t)
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, "app")
	if err := os.WriteFile(filepath.Join(projectDir, "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	requested := projects.Git{Enabled: true, Remote: origin, DefaultBranch: "main"}
	if _, err := c.ConfigureProjectGit(ctx, "app", requested, false); !errors.Is(err, ErrGitConfirmationRequired) {
		t.Fatalf("err = %v, want ErrGitConfirmationRequired", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "notes.md")); err != nil {
		t.Fatalf("refused clone touched the project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".git")); !os.IsNotExist(err) {
		t.Fatal("refused clone left a repository behind")
	}

	if _, err := c.ConfigureProjectGit(ctx, "app", requested, true); err != nil {
		t.Fatalf("forced configure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "README.md")); err != nil {
		t.Fatalf("cloned file: %v", err)
	}
	if findBackedUp(t, projectDir, "notes.md") == "" {
		t.Fatal("notes.md was not backed up")
	}
	// The staging directory is a sibling of the target here, so a replace pass
	// that did not skip it would have moved the clone into the backup.
	for _, entry := range readDirNames(t, projectDir) {
		if strings.HasPrefix(entry, ".podiom-clone-") {
			t.Fatalf("staging directory %q left behind", entry)
		}
	}
}

// A snapshot project becomes a real checkout, and the ledger says so rather
// than continuing to claim it is an archive.
func TestEnableGitOnASnapshotProjectUpgradesTheRepoToACheckout(t *testing.T) {
	ctx := context.Background()
	origin := seedOrigin(t)
	c, cleanup := newTestCore(t)
	defer cleanup()
	repo := projects.SnapshotRepo("acme", "app", "https://github.example/acme/app", "master", "master")
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App", Repo: &repo}); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, "app")
	codeDir := filepath.Join(projectDir, "repo")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "stale.txt"), []byte("archive\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, err := c.ConfigureProjectGit(ctx, "app", projects.Git{
		Enabled: true, Remote: origin, DefaultBranch: "main",
	}, true)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if updated.Repo == nil || updated.Repo.Mode != "git" || updated.Repo.SourceKind != "clone" {
		t.Fatalf("repo = %#v", updated.Repo)
	}
	if _, err := os.Stat(filepath.Join(codeDir, "README.md")); err != nil {
		t.Fatalf("cloned file: %v", err)
	}
	// The backup belongs beside the checkout, not inside it.
	if findBackedUp(t, projectDir, "stale.txt") == "" {
		t.Fatal("stale.txt was not backed up beside the checkout")
	}
	if _, err := os.Stat(filepath.Join(codeDir, ".podiom-backups")); !os.IsNotExist(err) {
		t.Fatal("backup landed inside the new checkout")
	}
}

// An existing checkout keeps its history: the remote is repointed, not recloned.
// The follow-up read matters because reconcileProjectGit rewrites the persisted
// remote from disk, and would revert an edit that never reached the checkout.
func TestEnableGitOnADetectedRepoRepointsOriginWithoutCloning(t *testing.T) {
	ctx := context.Background()
	origin := seedOrigin(t)
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, "app")
	runGit(t, "init", "--initial-branch=main", projectDir)
	commitFile(t, projectDir, "local.txt", "kept\n", "local work")

	updated, err := c.ConfigureProjectGit(ctx, "app", projects.Git{Enabled: true, Remote: origin}, false)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if updated.Git.Remote != origin {
		t.Fatalf("remote = %q, want %q", updated.Git.Remote, origin)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "local.txt")); err != nil {
		t.Fatalf("existing history was replaced: %v", err)
	}
	if got := runGit(t, "-C", projectDir, "remote", "get-url", "origin"); got != origin {
		t.Fatalf("origin = %q, want %q", got, origin)
	}
	reread, err := c.GetProject(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if reread.Git.Remote != origin {
		t.Fatalf("remote reverted to %q on reconcile", reread.Git.Remote)
	}
}

// No remote still means a plain local repository, which is a posture in its own
// right rather than an incomplete one.
func TestEnableGitWithoutARemoteInitializesInPlace(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	updated, err := c.ConfigureProjectGit(ctx, "app", projects.Git{Enabled: true, DefaultBranch: "trunk"}, false)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if updated.Git.Remote != "" {
		t.Fatalf("remote = %q, want empty", updated.Git.Remote)
	}
	if updated.GitState == nil || !updated.GitState.Detected {
		t.Fatalf("git state = %#v", updated.GitState)
	}
	if got := runGit(t, "-C", filepath.Join(c.paths.ProjectsDir, "app"), "branch", "--show-current"); got != "trunk" {
		t.Fatalf("branch = %q, want trunk", got)
	}
}

// A remote git would read as one of its own options never reaches git, and the
// refusal happens before anything on disk moves.
func TestConfigureProjectGitRejectsAnUnsafeRemote(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()
	if _, err := c.CreateProject(ctx, projects.Project{ID: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfigureProjectGit(ctx, "app", projects.Git{
		Enabled: true, Remote: "ext::sh -c 'touch /tmp/podiom-pwned'",
	}, true); err == nil {
		t.Fatal("configure accepted a remote helper URL")
	}
	if _, err := os.Stat(filepath.Join(c.paths.ProjectsDir, "app", ".git")); !os.IsNotExist(err) {
		t.Fatal("rejected remote still created a repository")
	}
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// findBackedUp locates a file under <projectDir>/.podiom-backups/<stamp>/,
// returning "" when it is not there.
func findBackedUp(t *testing.T, projectDir, name string) string {
	t.Helper()
	root := filepath.Join(projectDir, ".podiom-backups")
	stamps, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, stamp := range stamps {
		candidate := filepath.Join(root, stamp.Name(), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
