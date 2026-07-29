package core

import (
	"context"
	"fmt"
	"strings"

	podiomexec "github.com/Podiom/Podiom/internal/exec"
	podiomgit "github.com/Podiom/Podiom/internal/git"
	"github.com/Podiom/Podiom/internal/projects"
)

// ProjectGitState is what Podiom knows about a project's source control right
// now: the declared policy plus whether the host can actually act on it.
type ProjectGitState struct {
	// Declared is the project's policy. Nil when the project has no source
	// control, which is a valid choice rather than a missing setting.
	Declared *projects.Git `json:"declared,omitempty"`
	// Ready means git is installed, has a commit identity, and the working copy
	// exists. When false, Reason says what is missing in one sentence.
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
	// Branch is the checked-out branch, when there is a working copy.
	Branch string `json:"branch,omitempty"`
}

// gitRunner resolves the host's git binary, or reports why it cannot.
func (c *Core) gitRunner() (*podiomgit.Runner, error) {
	return podiomgit.New(podiomexec.Discovery{})
}

// GitStatus reports the host's git readiness, for the Settings card and the
// setup flow.
func (c *Core) GitStatus(ctx context.Context) podiomgit.Status {
	return podiomgit.Check(ctx, podiomexec.Discovery{})
}

// SetGitIdentity writes the commit identity Podiom's agents will use. The
// values are the user's own; Podiom only saves them.
func (c *Core) SetGitIdentity(ctx context.Context, name, email string) error {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" || email == "" {
		return fmt.Errorf("git identity needs both a name and an email")
	}
	runner, err := c.gitRunner()
	if err != nil {
		return err
	}
	if err := runner.ConfigSet(ctx, "user.name", name); err != nil {
		return err
	}
	return runner.ConfigSet(ctx, "user.email", email)
}

// materializeProjectGit brings a git-enabled project's working copy into
// existence, and reports what the agent may rely on.
//
// It is deliberately non-fatal: a session on a project whose git is not set up
// still opens and still works, it just cannot do source control. That is the
// degradation the agent is told about, rather than a failed session.
func (c *Core) materializeProjectGit(ctx context.Context, proj projects.Project, root string) ProjectGitState {
	if proj.Git == nil || !proj.Git.Enabled {
		return ProjectGitState{Declared: proj.Git}
	}
	state := ProjectGitState{Declared: proj.Git}

	runner, err := c.gitRunner()
	if err != nil {
		state.Reason = "git is not installed on this machine."
		return state
	}
	status := podiomgit.Check(ctx, podiomexec.Discovery{})
	if !status.Ready {
		state.Reason = status.Hint
		if state.Reason == "" {
			state.Reason = "git is not fully set up."
		}
		return state
	}

	if !podiomgit.IsRepo(root) {
		switch {
		case proj.Git.Remote != "":
			// Cloning into a directory Podiom already created would fail, so the
			// caller must not have pre-created it for remote-backed projects.
			if err := runner.Clone(ctx, proj.Git.Remote, root); err != nil {
				state.Reason = "could not clone " + proj.Git.Remote + ": " + firstLine(err.Error())
				return state
			}
			c.log.Info("project cloned", "event", "project", "project", proj.ID, "remote", proj.Git.Remote, "path", root)
		default:
			if err := runner.Init(ctx, root, proj.Git.DefaultBranch); err != nil {
				state.Reason = "could not create a local repository: " + firstLine(err.Error())
				return state
			}
			c.log.Info("project repository initialised", "event", "project", "project", proj.ID, "path", root)
		}
	}

	state.Branch, _ = runner.CurrentBranch(ctx, root)
	state.Ready = true
	return state
}

// SessionProjectContext is the pull-based project view the agent asks for.
//
// The detail lives here rather than in the per-turn prompt so it costs tokens
// once, when the agent wants it, instead of on every message.
type SessionProjectContext struct {
	ProjectID   string          `json:"project_id"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Status      string          `json:"status,omitempty"`
	Stack       []string        `json:"stack,omitempty"`
	Notes       string          `json:"notes,omitempty"`
	ProjectDir  string          `json:"project_dir,omitempty"`
	CodeDir     string          `json:"code_dir,omitempty"`
	SourceCtl   ProjectGitState `json:"source_control"`
}

// SessionProjectContext resolves the project bound to a session. The session id
// comes from the MCP helper's own launch arguments, so an agent cannot ask
// about a project other than the one it is working in.
func (c *Core) SessionProjectContext(ctx context.Context, sessionID string) (SessionProjectContext, error) {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return SessionProjectContext{}, err
	}
	projectCtx, err := c.sessionProjectExecutionContext(ctx, sess)
	if err != nil {
		return SessionProjectContext{}, err
	}
	if projectCtx.ProjectDir == "" {
		return SessionProjectContext{}, fmt.Errorf("this session is not bound to a project")
	}
	proj, err := c.ledger.Get(strings.TrimSpace(sess.ProjectID))
	if err != nil {
		return SessionProjectContext{}, err
	}
	return SessionProjectContext{
		ProjectID:   proj.ID,
		Name:        proj.Name,
		Description: proj.Description,
		Status:      proj.Status,
		Stack:       proj.Stack,
		Notes:       proj.Notes,
		ProjectDir:  projectCtx.ProjectDir,
		CodeDir:     projectCtx.Root,
		SourceCtl:   projectCtx.Git,
	}, nil
}

// StartWorkResult reports the branch a piece of work belongs on.
type StartWorkResult struct {
	Branch  string `json:"branch"`
	Created bool   `json:"created"`
	Message string `json:"message"`
}

// StartWork applies the project's branching policy and returns the branch the
// agent is now on.
//
// This is what makes the policy real. Left as prompt text, "put each fix on its
// own branch" is a rule the agent can quietly skip; performed by Podiom, the
// checkout has either happened or it has not. Calling it twice for the same
// work is safe.
func (c *Core) StartWork(ctx context.Context, sessionID, kind, slug string) (StartWorkResult, error) {
	projectCtx, err := c.SessionProjectContext(ctx, sessionID)
	if err != nil {
		return StartWorkResult{}, err
	}
	state := projectCtx.SourceCtl
	if state.Declared == nil || !state.Declared.Enabled {
		return StartWorkResult{}, fmt.Errorf("project %q does not use source control", projectCtx.ProjectID)
	}
	if !state.Ready {
		return StartWorkResult{}, fmt.Errorf("git is not ready for this project: %s", state.Reason)
	}
	branch := state.Declared.BranchFor(kind, slug)
	if branch == "" {
		return StartWorkResult{}, fmt.Errorf("could not derive a branch name")
	}
	runner, err := c.gitRunner()
	if err != nil {
		return StartWorkResult{}, err
	}
	current, _ := runner.CurrentBranch(ctx, projectCtx.CodeDir)
	if current == branch {
		return StartWorkResult{Branch: branch, Message: "Already on " + branch + "."}, nil
	}
	existed := runner.BranchExists(ctx, projectCtx.CodeDir, branch)
	if err := runner.CreateBranch(ctx, projectCtx.CodeDir, branch, state.Declared.DefaultBranch); err != nil {
		return StartWorkResult{}, err
	}
	c.log.Info("work branch checked out", "event", "project", "project", projectCtx.ProjectID, "branch", branch, "created", !existed)
	message := "Switched to existing branch " + branch + "."
	if !existed {
		message = "Created and switched to " + branch + "."
	}
	return StartWorkResult{Branch: branch, Created: !existed, Message: message}, nil
}

// gitPromptLine is the one-line source-control anchor carried on every turn.
//
// The detail lives behind podiom_project_context so it is pulled rather than
// re-sent each turn, but the policy itself stays in the prompt: an agent that
// never calls the tool must still not commit to the default branch when the
// project said otherwise.
func gitPromptLine(state ProjectGitState) string {
	if state.Declared == nil || !state.Declared.Enabled {
		return "Source control: this project does not use git — do not run git commands."
	}
	if !state.Ready {
		return "Source control: this project uses git, but it is not set up on this machine (" +
			strings.TrimSuffix(state.Reason, ".") + "). Ask the user once whether to set it up in Settings → Git; " +
			"if they decline, do the work and say plainly that the changes are uncommitted, and do not ask again."
	}
	line := "Source control: git is ready"
	if state.Branch != "" {
		line += " (on branch " + state.Branch + ")"
	}
	if state.Declared.Branching == projects.BranchingPerTask {
		line += ". This project puts each feature or fix on its own branch — call podiom_start_work before editing, and let it create the branch"
	} else {
		line += ". This project commits directly to " + state.Declared.DefaultBranch
	}
	if state.Declared.Commit == projects.CommitAuto {
		return line + ". You may commit completed work yourself."
	}
	return line + ". Commit only when the user asks."
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}
