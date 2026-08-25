// Package git runs the host's git binary on behalf of Podiom.
//
// Two boundaries define it. First, everything is an argv array — never a shell
// string — so a branch or commit message can never be interpreted as a command,
// following the same rule as internal/tools ("what the user approves is exactly
// what runs"). Second, Podiom never manufactures credentials: git authenticates
// with whatever the user has already configured (an SSH key, a credential
// helper), and the GitHub App token Podiom holds for repo listing and archive
// downloads is deliberately not reused here. Podiom's job is to detect what is
// configured, help the user configure it, and then get out of the way.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

// outputTail caps captured output folded into errors, mirroring internal/tools.
const outputTail = 2000

// probeTimeout bounds the quick informational commands (version, config reads).
const probeTimeout = 5 * time.Second

// Runner executes git commands. The zero value is unusable — use New.
type Runner struct {
	bin string
}

// New discovers the git binary. GIT_BIN overrides it, for free, via the shared
// discovery rules.
func New(d podiomexec.Discovery) (*Runner, error) {
	found, err := d.Find("git")
	if err != nil {
		return nil, err
	}
	return &Runner{bin: found.Path}, nil
}

// Bin returns the resolved git path.
func (r *Runner) Bin() string { return r.bin }

// Status describes how ready the host is to do source control. It is shaped
// like providercheck.Status on purpose: the "missing / misconfigured / ready"
// rendering is the same problem, and a git card in Settings should look like
// the provider doctor.
type Status struct {
	Found     bool   `json:"found"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
	// SSHKey is the public key Podiom would offer for GitHub, when one exists.
	SSHKey string `json:"ssh_key,omitempty"`
	// Ready means git can make a commit: it is installed and has an identity.
	// Remote access is per-repository and is checked separately with CanReach.
	Ready bool   `json:"ready"`
	Hint  string `json:"hint,omitempty"`
	Error string `json:"error,omitempty"`
}

// InstallHint tells the user how to get git, per OS. On the Home Assistant
// image git is preinstalled, so this is for desktop installs.
func InstallHint() string {
	return "Install git from https://git-scm.com/downloads (macOS: xcode-select --install or brew install git; Debian/Ubuntu: apt install git)"
}

// Check reports whether the host can do source control.
func Check(ctx context.Context, d podiomexec.Discovery) Status {
	runner, err := New(d)
	if err != nil {
		return Status{Hint: InstallHint(), Error: err.Error()}
	}
	status := Status{Found: true, Path: runner.bin}
	if version, err := runner.capture(ctx, "", "--version"); err == nil {
		status.Version = strings.TrimSpace(version)
	}
	status.UserName, _ = runner.ConfigGet(ctx, "user.name")
	status.UserEmail, _ = runner.ConfigGet(ctx, "user.email")
	status.SSHKey = PublicKey()
	switch {
	case status.UserName == "" || status.UserEmail == "":
		status.Hint = "Set a commit identity so the agent's commits are attributable."
	default:
		status.Ready = true
	}
	return status
}

// ConfigGet reads a global git config value. A missing key is not an error —
// git exits non-zero for it — so the caller gets an empty string.
func (r *Runner) ConfigGet(ctx context.Context, key string) (string, error) {
	out, err := r.capture(ctx, "", "config", "--global", "--get", key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ConfigSet writes a global git config value.
func (r *Runner) ConfigSet(ctx context.Context, key, value string) error {
	_, err := r.capture(ctx, "", "config", "--global", key, value)
	return err
}

// Init creates a repository in dir if one is not already there, and makes the
// first branch match the project's declared default so later pushes line up.
func (r *Runner) Init(ctx context.Context, dir, defaultBranch string) error {
	if IsRepo(dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	branch := strings.TrimSpace(defaultBranch)
	if branch == "" {
		branch = "main"
	}
	_, err := r.capture(ctx, dir, "init", "--initial-branch="+branch)
	return err
}

// ValidateRemote rejects remotes git would read as something other than a place
// to fetch from. Podiom passes argv arrays, so a remote can never be
// reinterpreted as a shell string — but git itself treats a leading "-" as one
// of its own options and "<helper>::<arg>" as a remote helper it will execute,
// which is the same class of problem one level down. Callers must treat an empty
// remote as "local repository" before calling; here it is an error.
func ValidateRemote(remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return fmt.Errorf("a remote URL is required")
	}
	if strings.HasPrefix(remote, "-") {
		return fmt.Errorf("a remote URL cannot start with %q — git would read it as one of its own options", "-")
	}
	if strings.Contains(remote, "::") {
		return fmt.Errorf("%q looks like a git remote helper, which git would execute; use an https:// or ssh:// URL", remote)
	}
	for _, ch := range remote {
		if ch < 0x20 || ch == 0x7f || ch == ' ' {
			return fmt.Errorf("a remote URL cannot contain spaces or control characters")
		}
	}
	// An absolute path is a local repository — a bare repo on disk or a mount.
	if filepath.IsAbs(remote) {
		return nil
	}
	if scheme, rest, ok := strings.Cut(remote, "://"); ok {
		switch scheme {
		case "https", "http", "ssh", "git", "file":
		default:
			return fmt.Errorf("git cannot fetch over %q; use https, ssh, git or file", scheme)
		}
		host, _, _ := strings.Cut(rest, "/")
		if _, after, found := strings.Cut(host, "@"); found {
			host = after
		}
		if host == "" && scheme != "file" {
			return fmt.Errorf("%q has no host", remote)
		}
		if strings.HasPrefix(host, "-") {
			return fmt.Errorf("%q has a host git would read as an option", remote)
		}
		return nil
	}
	// Otherwise the scp-like form: [user@]host:path.
	host, path, ok := strings.Cut(remote, ":")
	if !ok {
		return fmt.Errorf("%q is not a git remote; expected an https:// or ssh:// URL, or host:path", remote)
	}
	if _, after, found := strings.Cut(host, "@"); found {
		host = after
	}
	if host == "" || strings.Contains(host, "/") || strings.HasPrefix(host, "-") {
		return fmt.Errorf("%q is not a git remote; expected an https:// or ssh:// URL, or host:path", remote)
	}
	if path == "" {
		return fmt.Errorf("%q names a host but no repository", remote)
	}
	return nil
}

// Clone copies remote into dir. dir must not already be a repository.
func (r *Runner) Clone(ctx context.Context, remote, dir string) error {
	// The backstop rather than the only check: this is reached with remotes an
	// agent wrote straight into projects.yaml, not just with UI input.
	if err := ValidateRemote(remote); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	ctx, cancel := longCommandContext(ctx)
	defer cancel()
	_, err := r.captureEnv(ctx, "", []string{"GIT_TERMINAL_PROMPT=0"}, "clone", remote, dir)
	return err
}

// Fetch updates remote refs.
func (r *Runner) Fetch(ctx context.Context, dir string) error {
	ctx, cancel := longCommandContext(ctx)
	defer cancel()
	_, err := r.captureEnv(ctx, dir, []string{"GIT_TERMINAL_PROMPT=0"}, "fetch", "--all", "--prune")
	return err
}

// RepositoryRoot returns the top-level working-tree directory containing dir.
// Unlike checking for dir/.git, this supports linked worktrees and callers
// operating in a subdirectory of the checkout.
func (r *Runner) RepositoryRoot(ctx context.Context, dir string) (string, error) {
	out, err := r.capture(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(out)), nil
}

// RemoteNames lists configured remotes in git's stable name order.
func (r *Runner) RemoteNames(ctx context.Context, dir string) ([]string, error) {
	out, err := r.capture(ctx, dir, "remote")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// PreferredRemote chooses origin, or the sole configured remote. Multiple
// non-origin remotes are intentionally ambiguous and yield no selection.
func (r *Runner) PreferredRemote(ctx context.Context, dir string) (string, error) {
	names, err := r.RemoteNames(ctx, dir)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		if name == "origin" {
			return name, nil
		}
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", nil
}

// RemoteURL returns one remote's fetch URL.
func (r *Runner) RemoteURL(ctx context.Context, dir, name string) (string, error) {
	out, err := r.capture(ctx, dir, "remote", "get-url", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SetRemote creates or updates a named remote without touching credentials.
func (r *Runner) SetRemote(ctx context.Context, dir, name, remote string) error {
	name, remote = strings.TrimSpace(name), strings.TrimSpace(remote)
	if name == "" || remote == "" {
		return fmt.Errorf("remote name and URL are required")
	}
	if err := ValidateRemote(remote); err != nil {
		return err
	}
	if _, err := r.RemoteURL(ctx, dir, name); err == nil {
		_, err = r.capture(ctx, dir, "remote", "set-url", name, remote)
		return err
	}
	_, err := r.capture(ctx, dir, "remote", "add", name, remote)
	return err
}

// RemoteDefaultBranch reads <remote>/HEAD when the server advertises it.
func (r *Runner) RemoteDefaultBranch(ctx context.Context, dir, remote string) (string, error) {
	out, err := r.capture(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(out)
	return strings.TrimPrefix(value, remote+"/"), nil
}

// CheckoutDefault checks out an existing local branch or creates it tracking
// the matching remote branch. It never resets an existing branch.
func (r *Runner) CheckoutDefault(ctx context.Context, dir, remote, branch string) error {
	if r.BranchExists(ctx, dir, branch) {
		return r.Checkout(ctx, dir, branch)
	}
	if remote == "" || !r.remoteBranchExists(ctx, dir, remote, branch) {
		return fmt.Errorf("default branch %q is not available locally or on a remote", branch)
	}
	_, err := r.capture(ctx, dir, "checkout", "-b", branch, "--track", remote+"/"+branch)
	return err
}

// FastForwardUpstream advances the checked-out branch without creating a merge
// commit. A missing upstream and a diverged branch both fail without rewriting
// local history.
func (r *Runner) FastForwardUpstream(ctx context.Context, dir string) error {
	upstream, err := r.capture(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil || strings.TrimSpace(upstream) == "" {
		return fmt.Errorf("checked-out branch has no upstream")
	}
	_, err = r.capture(ctx, dir, "merge", "--ff-only", strings.TrimSpace(upstream))
	return err
}

func (r *Runner) remoteBranchExists(ctx context.Context, dir, remote, branch string) bool {
	_, err := r.capture(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return err == nil
}

// CurrentBranch returns the checked-out branch, or "" when HEAD is detached.
//
// `branch --show-current` is used rather than `rev-parse --abbrev-ref HEAD`
// because a freshly initialised repository has an unborn HEAD: rev-parse fails
// outright there, while this still reports the branch the first commit will
// land on — which is exactly the state a just-materialized project is in.
func (r *Runner) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := r.capture(ctx, dir, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// StatusPorcelain returns the working-tree status in a stable machine format.
func (r *Runner) StatusPorcelain(ctx context.Context, dir string) (string, error) {
	return r.capture(ctx, dir, "status", "--porcelain")
}

// BranchExists reports whether a local branch is present.
func (r *Runner) BranchExists(ctx context.Context, dir, branch string) bool {
	_, err := r.capture(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Checkout switches to an existing branch.
func (r *Runner) Checkout(ctx context.Context, dir, branch string) error {
	_, err := r.capture(ctx, dir, "checkout", branch)
	return err
}

// CreateBranch creates a branch from base and checks it out. When the branch
// already exists it is simply checked out, so starting work twice on the same
// task is idempotent rather than an error.
func (r *Runner) CreateBranch(ctx context.Context, dir, branch, base string) error {
	if r.BranchExists(ctx, dir, branch) {
		return r.Checkout(ctx, dir, branch)
	}
	args := []string{"checkout", "-b", branch}
	if base = strings.TrimSpace(base); base != "" && r.BranchExists(ctx, dir, base) {
		args = append(args, base)
	}
	_, err := r.capture(ctx, dir, args...)
	return err
}

// CanReach reports whether the host's credentials can reach a remote.
//
// GIT_TERMINAL_PROMPT=0 is what makes this a check rather than a hang: without
// it git would sit waiting for a username on a terminal nobody is watching.
func (r *Runner) CanReach(ctx context.Context, remote string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := r.captureEnv(ctx, "", []string{"GIT_TERMINAL_PROMPT=0"}, "ls-remote", "--exit-code", "-h", remote)
	return err
}

func longCommandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, 2*time.Minute)
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// PublicKey returns the user's SSH public key when one of the conventional
// files exists, so Settings can show what to paste into GitHub. Private keys
// are never read.
func PublicKey() string {
	for _, dir := range sshDirs() {
		for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && len(bytes.TrimSpace(raw)) > 0 {
				return strings.TrimSpace(string(raw))
			}
		}
	}
	return ""
}

// sshDirs returns the .ssh directories a key may live in. $HOME is Podiom's
// (and git's) idea of home; OpenSSH deliberately ignores $HOME and expands ~
// from the passwd entry. The Home Assistant image aligns both values at
// /data/home for its podiom user. Check both for standalone installs where
// they may still differ, so the key is found wherever ssh put it.
func sshDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".ssh"))
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if dir := filepath.Join(u.HomeDir, ".ssh"); len(dirs) == 0 || dir != dirs[0] {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (r *Runner) capture(ctx context.Context, dir string, args ...string) (string, error) {
	return r.captureEnv(ctx, dir, nil, args...)
}

// captureEnv runs one git command and folds its output into any error. Podiom
// never passes a shell string: args is an argv array, so branch names and
// commit messages cannot be reinterpreted as commands.
func (r *Runner) captureEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, probeTimeout)
		defer cancel()
	}
	cmd := podiomexec.Command(ctx, r.bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := out.String()
	if ctx.Err() != nil {
		return text, fmt.Errorf("git %s timed out: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		return text, fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, tail(text))
	}
	return text, nil
}

func tail(s string) string {
	if len(s) <= outputTail {
		return s
	}
	return "…" + s[len(s)-outputTail:]
}
