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

// Clone copies remote into dir. dir must not already be a repository.
func (r *Runner) Clone(ctx context.Context, remote, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, err := r.capture(ctx, "", "clone", remote, dir)
	return err
}

// Fetch updates remote refs.
func (r *Runner) Fetch(ctx context.Context, dir string) error {
	_, err := r.capture(ctx, dir, "fetch", "--all", "--prune")
	return err
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

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// PublicKey returns the user's SSH public key when one of the conventional
// files exists, so Settings can show what to paste into GitHub. Private keys
// are never read.
func PublicKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
		raw, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err == nil && len(bytes.TrimSpace(raw)) > 0 {
			return strings.TrimSpace(string(raw))
		}
	}
	return ""
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
