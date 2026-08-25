package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	podiomexec "github.com/Podiom/Podiom/internal/exec"
	podiomgit "github.com/Podiom/Podiom/internal/git"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func (c *Core) projectGitLock(projectID string) *sync.Mutex {
	lock, _ := c.projectGitLocks.LoadOrStore(projectID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (c *Core) projectCodeDir(proj projects.Project) string {
	root := filepath.Join(c.paths.ProjectsDir, proj.Path)
	if proj.Repo != nil {
		return filepath.Join(root, "repo")
	}
	return root
}

// inspectProjectGit reads the checkout without materializing or changing it.
func (c *Core) inspectProjectGit(ctx context.Context, proj projects.Project) projects.GitState {
	state := projects.GitState{}
	dir := c.projectCodeDir(proj)
	runner, err := c.gitRunner()
	if err != nil {
		if podiomgit.IsRepo(dir) {
			state.Detected = true
			state.Root = dir
		}
		state.Warning = "git is not installed on this machine"
		return state
	}
	root, err := runner.RepositoryRoot(ctx, dir)
	if err != nil {
		if proj.Git != nil && proj.Git.Enabled {
			state.Warning = "Git is configured, but no repository is present in the project workspace."
		}
		return state
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, proj.Path)
	compareProjectDir, compareRoot := projectDir, root
	if resolved, resolveErr := filepath.EvalSymlinks(projectDir); resolveErr == nil {
		compareProjectDir = resolved
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		compareRoot = resolved
	}
	rel, err := filepath.Rel(compareProjectDir, compareRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		state.Warning = "The detected repository root is outside the project directory."
		return state
	}
	state.Detected = true
	state.Root = root
	state.Branch, _ = runner.CurrentBranch(ctx, dir)
	remoteNames, _ := runner.RemoteNames(ctx, dir)
	remoteName, _ := runner.PreferredRemote(ctx, dir)
	if remoteName != "" {
		state.Remote, _ = runner.RemoteURL(ctx, dir, remoteName)
	}
	if remoteName == "" && len(remoteNames) > 1 {
		state.RemoteAmbiguous = true
	}
	host := podiomgit.Check(ctx, podiomexec.Discovery{})
	state.Ready = host.Ready
	if !host.Ready {
		state.Warning = firstLine(firstNonEmpty(host.Hint, host.Error, "git is not fully set up"))
	}
	if state.RemoteAmbiguous {
		state.Warning = "Multiple Git remotes are configured and none is named origin; Podiom cannot choose one automatically."
	}
	if proj.Git != nil && !proj.Git.Enabled {
		state.Ready = false
		state.Warning = "A Git repository is present, but source control is explicitly disabled for this project."
	}
	return state
}

// reconcileProjectGit adopts repositories created by users or agents and keeps
// the persisted remote aligned with the checkout. An explicit disabled block is
// an override; only an undeclared project is automatically enabled.
func (c *Core) reconcileProjectGit(ctx context.Context, projectID string) (projects.Project, error) {
	lock := c.projectGitLock(projectID)
	lock.Lock()
	defer lock.Unlock()
	return c.reconcileProjectGitLocked(ctx, projectID)
}

func (c *Core) reconcileProjectGitLocked(ctx context.Context, projectID string) (projects.Project, error) {
	proj, err := c.ledger.Get(projectID)
	if err != nil {
		return projects.Project{}, err
	}
	state := c.inspectProjectGit(ctx, proj)
	patch := projects.ProjectPatch{}
	changed := false
	if state.Detected {
		switch {
		case proj.Git == nil:
			branch := c.detectedDefaultBranch(ctx, proj, state)
			patch.Git = &projects.Git{Enabled: true, Remote: state.Remote, DefaultBranch: branch}
			changed = true
		case proj.Git.Enabled && !state.RemoteAmbiguous && proj.Git.Remote != state.Remote:
			next := *proj.Git
			next.Remote = state.Remote
			patch.Git = &next
			changed = true
		}
		if proj.Repo != nil && proj.Repo.Mode == "snapshot" {
			next := *proj.Repo
			next.Mode = "git"
			next.SourceKind = "checkout"
			patch.Repo = &next
			changed = true
		}
	}
	if changed {
		proj, err = c.ledger.Update(projectID, patch)
		if err != nil {
			return projects.Project{}, err
		}
		state = c.inspectProjectGit(ctx, proj)
	}
	proj.GitState = &state
	return proj, nil
}

func (c *Core) detectedDefaultBranch(ctx context.Context, proj projects.Project, state projects.GitState) string {
	runner, err := c.gitRunner()
	if err == nil && state.Detected {
		remote, _ := runner.PreferredRemote(ctx, c.projectCodeDir(proj))
		if remote != "" {
			if branch, err := runner.RemoteDefaultBranch(ctx, c.projectCodeDir(proj), remote); err == nil && branch != "" {
				return branch
			}
		}
	}
	if state.Branch != "" {
		return state.Branch
	}
	return "main"
}

// ErrGitConfirmationRequired is returned when enabling a remote would replace
// files that are already in the project's code directory. The caller re-sends
// the request with force once the user has agreed.
var ErrGitConfirmationRequired = errors.New("project code directory has existing files; confirmation required")

// ConfigureProjectGit persists a project's source-control policy and brings the
// working copy into line with it in one locked step.
//
// Three outcomes, decided by what is on disk rather than by what the caller
// believes: an existing checkout is adopted and its origin repointed, a remote
// with no checkout is cloned, and neither means git init. Cloning over existing
// files is destructive, so it needs force — Podiom moves them into
// .podiom-backups rather than deleting them, and the clone is staged next to the
// target so a failed clone leaves the project untouched.
//
// Disabling never removes .git; a checkout stays exactly as it is.
func (c *Core) ConfigureProjectGit(ctx context.Context, projectID string, requested projects.Git, force bool) (projects.Project, error) {
	lock := c.projectGitLock(projectID)
	lock.Lock()
	defer lock.Unlock()
	proj, err := c.ledger.Get(projectID)
	if err != nil {
		return projects.Project{}, err
	}
	requested.Remote = strings.TrimSpace(requested.Remote)
	if requested.Remote != "" {
		if err := podiomgit.ValidateRemote(requested.Remote); err != nil {
			return projects.Project{}, err
		}
	}

	cloned := false
	if requested.Enabled {
		state := c.inspectProjectGit(ctx, proj)
		runner, err := c.gitRunner()
		if err != nil {
			return projects.Project{}, err
		}
		dir := c.projectCodeDir(proj)
		switch {
		case state.Detected:
			// Repoint rather than re-clone: discarding a real history is a
			// bigger decision than a checkbox, and reconcileProjectGit would
			// revert an edited remote that was never written to the checkout.
			if requested.Remote == "" {
				requested.Remote = state.Remote
			} else if requested.Remote != state.Remote {
				if state.RemoteAmbiguous {
					return projects.Project{}, fmt.Errorf("this checkout has several remotes and none is named origin; set one in the checkout first")
				}
				if err := runner.SetRemote(ctx, dir, "origin", requested.Remote); err != nil {
					return projects.Project{}, err
				}
			}
			if requested.DefaultBranch == "" {
				requested.DefaultBranch = c.detectedDefaultBranch(ctx, proj, state)
			}
		case requested.Remote != "":
			if !force && dirHasEntries(dir) {
				return projects.Project{}, ErrGitConfirmationRequired
			}
			branch, backup, err := c.cloneProjectRemote(ctx, proj, requested.Remote, dir)
			if err != nil {
				return projects.Project{}, err
			}
			// The clone knows the remote's default branch; the request only guessed.
			requested.DefaultBranch = branch
			c.log.Info("project cloned", "event", "project", "project", proj.ID,
				"remote", requested.Remote, "path", dir, "backup", backup)
			cloned = true
		default:
			if err := runner.Init(ctx, dir, requested.DefaultBranch); err != nil {
				return projects.Project{}, err
			}
			c.log.Info("project repository initialised", "event", "project", "project", proj.ID, "path", dir)
		}
	}

	patch := projects.ProjectPatch{Git: &requested}
	if cloned && proj.Repo != nil {
		repo := *proj.Repo
		repo.Mode = "git"
		repo.SourceKind = "clone"
		repo.SyncedAt = time.Now().UTC().Format(time.RFC3339)
		patch.Repo = &repo
	}
	// c.ledger.Update, not c.UpdateProject: that one reconciles, which takes
	// this same non-reentrant lock.
	updated, err := c.ledger.Update(projectID, patch)
	if err != nil {
		return projects.Project{}, err
	}
	state := c.inspectProjectGit(ctx, updated)
	updated.GitState = &state
	return updated, nil
}

// dirHasEntries reports whether a directory holds anything. A directory that is
// not there yet counts as empty, which is the common case for <project>/repo.
func dirHasEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// cloneProjectRemote clones remote into a staging directory beside the target
// and moves it into place, so a clone that fails against a bad URL or missing
// credentials leaves the project exactly as it was. It returns the branch the
// clone landed on and the backup directory anything displaced was moved to.
func (c *Core) cloneProjectRemote(ctx context.Context, proj projects.Project, remote, target string) (string, string, error) {
	runner, err := c.gitRunner()
	if err != nil {
		return "", "", err
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, proj.Path)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return "", "", err
	}
	stage, err := os.MkdirTemp(projectDir, ".podiom-clone-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	checkout := filepath.Join(stage, "checkout")
	if err := runner.Clone(ctx, remote, checkout); err != nil {
		return "", "", err
	}
	// A fresh clone sits on whatever the remote advertises as HEAD. An empty
	// remote advertises nothing, so fall back rather than persisting "".
	branch, _ := runner.CurrentBranch(ctx, checkout)
	branch = firstNonEmpty(branch, "main")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", "", err
	}
	backup, err := replaceDirContents(target, checkout, filepath.Join(projectDir, ".podiom-backups"))
	if err != nil {
		return "", "", err
	}
	return branch, backup, nil
}

// replaceDirContents moves everything already in root aside into a timestamped
// backup, then moves the staged checkout in. It returns the backup directory, or
// "" when root was empty.
//
// The staging directory is a sibling inside the project, so it is skipped along
// with the backup root: for a project with no connected repo the code directory
// is the project directory itself, and moving the clone we are about to install
// into the backup would lose it.
func replaceDirContents(root, stage, backupRoot string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	backup := ""
	for _, entry := range entries {
		if entry.Name() == ".podiom-backups" || strings.HasPrefix(entry.Name(), ".podiom-clone-") {
			continue
		}
		if backup == "" {
			backup = filepath.Join(backupRoot, time.Now().UTC().Format("20060102T150405Z"))
			if err := os.MkdirAll(backup, 0o755); err != nil {
				return "", err
			}
		}
		if err := os.Rename(filepath.Join(root, entry.Name()), filepath.Join(backup, entry.Name())); err != nil {
			return "", err
		}
	}
	staged, err := os.ReadDir(stage)
	if err != nil {
		return "", err
	}
	for _, entry := range staged {
		if err := os.Rename(filepath.Join(stage, entry.Name()), filepath.Join(root, entry.Name())); err != nil {
			return "", err
		}
	}
	return backup, nil
}

// CloneGitHubProject tries the user's ordinary Git credentials against each
// clean remote URL and records a real checkout on success. Callers retain the
// archive fallback; no GitHub App token crosses into this path.
func (c *Core) CloneGitHubProject(ctx context.Context, projectID string, repo projects.Repo, remotes []string) (projects.Project, error) {
	lock := c.projectGitLock(projectID)
	lock.Lock()
	defer lock.Unlock()
	proj, err := c.ledger.Get(projectID)
	if err != nil {
		return projects.Project{}, err
	}
	if len(remotes) == 0 {
		return projects.Project{}, fmt.Errorf("repository has no clone URL")
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, proj.Path)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return projects.Project{}, err
	}
	stage, err := os.MkdirTemp(projectDir, ".podiom-clone-*")
	if err != nil {
		return projects.Project{}, err
	}
	defer os.RemoveAll(stage)
	checkout := filepath.Join(stage, "checkout")
	runner, err := c.gitRunner()
	if err != nil {
		return projects.Project{}, err
	}
	var remote string
	var cloneErr error
	for _, candidate := range remotes {
		_ = os.RemoveAll(checkout)
		if err := runner.Clone(ctx, candidate, checkout); err != nil {
			cloneErr = err
			continue
		}
		remote = candidate
		break
	}
	if remote == "" {
		return projects.Project{}, cloneErr
	}
	target := filepath.Join(projectDir, "repo")
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return projects.Project{}, fmt.Errorf("project source directory is not empty")
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return projects.Project{}, err
	}
	if err := os.Rename(checkout, target); err != nil {
		return projects.Project{}, err
	}
	repo.Mode = "git"
	repo.SourceKind = "clone"
	repo.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	updated, err := c.ledger.Update(projectID, projects.ProjectPatch{
		Repo: &repo,
		Git:  &projects.Git{Enabled: true, Remote: remote, DefaultBranch: repo.DefaultBranch},
	})
	if err != nil {
		return projects.Project{}, err
	}
	state := c.inspectProjectGit(ctx, updated)
	updated.GitState = &state
	return updated, nil
}

// SyncProjectGit performs the Projects-page Sync action for a real checkout.
// It has the same no-rewrite guarantees as session startup, but returns an
// error to the explicit caller instead of degrading to a warning.
func (c *Core) SyncProjectGit(ctx context.Context, projectID string) (projects.Project, error) {
	lock := c.projectGitLock(projectID)
	lock.Lock()
	defer lock.Unlock()
	proj, err := c.ledger.Get(projectID)
	if err != nil {
		return projects.Project{}, err
	}
	state := c.inspectProjectGit(ctx, proj)
	if !state.Detected {
		return projects.Project{}, fmt.Errorf("project is not a Git checkout")
	}
	runner, err := c.gitRunner()
	if err != nil {
		return projects.Project{}, err
	}
	dir := c.projectCodeDir(proj)
	dirty, err := runner.StatusPorcelain(ctx, dir)
	if err != nil {
		return projects.Project{}, err
	}
	if strings.TrimSpace(dirty) != "" {
		return projects.Project{}, fmt.Errorf("working tree has uncommitted changes")
	}
	remote, err := runner.PreferredRemote(ctx, dir)
	if err != nil || remote == "" {
		return projects.Project{}, fmt.Errorf("repository has no unambiguous remote")
	}
	if err := runner.Fetch(ctx, dir); err != nil {
		return projects.Project{}, err
	}
	branch := firstNonEmpty(proj.Git.DefaultBranch, state.Branch, "main")
	if err := runner.CheckoutDefault(ctx, dir, remote, branch); err != nil {
		return projects.Project{}, err
	}
	if err := runner.FastForwardUpstream(ctx, dir); err != nil {
		return projects.Project{}, err
	}
	if proj.Repo != nil {
		repo := *proj.Repo
		repo.SyncedAt = time.Now().UTC().Format(time.RFC3339)
		proj, err = c.ledger.Update(projectID, projects.ProjectPatch{Repo: &repo})
		if err != nil {
			return projects.Project{}, err
		}
	}
	state = c.inspectProjectGit(ctx, proj)
	proj.GitState = &state
	return proj, nil
}

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

	// Initialize or clone the working copy first. A commit identity is not
	// required for git init or git clone — only for git commit — so we do this
	// before the identity check so that the repository exists even when the
	// user has not yet configured one.
	_, repoErr := runner.RepositoryRoot(ctx, root)
	if repoErr != nil {
		switch {
		case proj.Git.Remote != "":
			if entries, readErr := os.ReadDir(root); readErr == nil && len(entries) > 0 {
				state.Reason = "the project contains a source snapshot, not a Git checkout; create or clone a repository there to enable Git operations."
				return state
			}
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

	status := podiomgit.Check(ctx, podiomexec.Discovery{})
	if !status.Ready {
		state.Reason = status.Hint
		if state.Reason == "" {
			state.Reason = "git is not fully set up."
		}
		return state
	}

	state.Branch, _ = runner.CurrentBranch(ctx, root)
	state.Ready = true
	return state
}

// prepareNewSessionGit applies the optional startup update once, before the
// provider is started. Every failure is converted into a durable warning; a
// session must never be blocked merely because source control is unavailable.
func (c *Core) prepareNewSessionGit(ctx context.Context, sess store.Session) (store.Session, error) {
	if strings.TrimSpace(sess.ProjectID) == "" {
		return sess, nil
	}
	proj, err := c.reconcileProjectGit(ctx, sess.ProjectID)
	if err != nil || proj.Git == nil || !proj.Git.Enabled || !proj.Git.PullOnSessionStart {
		return sess, nil
	}
	lock := c.projectGitLock(proj.ID)
	lock.Lock()
	defer lock.Unlock()

	warning := c.updateProjectDefaultBranch(ctx, proj, sess.ID)
	if warning == "" {
		return sess, nil
	}
	updated, err := c.store.UpdateSessionSourceControlWarning(ctx, sess.ID, warning)
	if err != nil {
		return sess, err
	}
	c.log.Warn("project startup update skipped", "event", "project", "project", proj.ID, "session", sess.ID, "warning", warning)
	return updated, nil
}

func (c *Core) updateProjectDefaultBranch(ctx context.Context, proj projects.Project, newSessionID string) string {
	root := c.projectCodeDir(proj)
	materializeReason := ""
	if proj.Git != nil && proj.Git.Enabled {
		if proj.Git.Remote == "" {
			_ = os.MkdirAll(root, 0o755)
		}
		materialized := c.materializeProjectGit(ctx, proj, root)
		materializeReason = materialized.Reason
	}
	state := c.inspectProjectGit(ctx, proj)
	if !state.Detected {
		if proj.Repo != nil && proj.Repo.Mode == "snapshot" {
			return "Could not pull the default branch because this GitHub source is a snapshot fallback, not a Git checkout."
		}
		if materializeReason != "" {
			return "Could not pull the default branch: " + strings.TrimSuffix(materializeReason, ".") + "."
		}
		return "Could not pull the default branch because no Git repository is present in the project workspace."
	}
	if c.projectHasActiveTurn(ctx, proj.ID, newSessionID) {
		return "Skipped the startup pull because another session is actively using this project's shared checkout."
	}
	runner, err := c.gitRunner()
	if err != nil {
		return "Could not pull the default branch: " + firstLine(err.Error())
	}
	dir := c.projectCodeDir(proj)
	dirty, err := runner.StatusPorcelain(ctx, dir)
	if err != nil {
		return "Could not inspect the working tree before pulling: " + firstLine(err.Error())
	}
	if strings.TrimSpace(dirty) != "" {
		return "Skipped the startup pull because the project's shared working tree has uncommitted changes."
	}
	remote, err := runner.PreferredRemote(ctx, dir)
	if err != nil || remote == "" {
		return "Could not pull the default branch because the repository has no unambiguous remote."
	}
	if err := runner.Fetch(ctx, dir); err != nil {
		return "Could not fetch remote changes: " + firstLine(err.Error())
	}
	branch := strings.TrimSpace(proj.Git.DefaultBranch)
	if branch == "" {
		branch = c.detectedDefaultBranch(ctx, proj, state)
	}
	if err := runner.CheckoutDefault(ctx, dir, remote, branch); err != nil {
		return "Could not check out the default branch " + branch + ": " + firstLine(err.Error())
	}
	if err := runner.FastForwardUpstream(ctx, dir); err != nil {
		return "Could not fast-forward the default branch " + branch + ": " + firstLine(err.Error())
	}
	c.log.Info("project default branch updated", "event", "project", "project", proj.ID, "session", newSessionID, "branch", branch, "remote", remote)
	return ""
}

func (c *Core) projectHasActiveTurn(ctx context.Context, projectID, excludeSessionID string) bool {
	if c.activeTurn == nil {
		return false
	}
	sessions, err := c.store.ListSessions(ctx)
	if err != nil {
		return false
	}
	for _, session := range sessions {
		if session.ID != excludeSessionID && session.ProjectID == projectID && c.activeTurn(session.ID) {
			return true
		}
	}
	return false
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
