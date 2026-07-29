package git

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

func testRunner(t *testing.T) *Runner {
	t.Helper()
	runner, err := New(podiomexec.Discovery{})
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	return runner
}

// newRepo creates a repository with one commit, using per-command identity so
// the test never depends on (or touches) the host's global git config.
func newRepo(t *testing.T, r *Runner) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if err := r.Init(ctx, dir, "main"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.py"), []byte("def add(a,b):\n    return a+b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.capture(ctx, dir, "add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := r.capture(ctx, dir,
		"-c", "user.name=Podiom Test", "-c", "user.email=test@example.invalid",
		"commit", "-m", "init"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

func TestInitCreatesRepoOnTheDeclaredBranch(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	dir := t.TempDir()

	if IsRepo(dir) {
		t.Fatal("fresh temp dir reported as a repo")
	}
	if err := r.Init(ctx, dir, "trunk"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !IsRepo(dir) {
		t.Fatal("init did not produce a repo")
	}
	branch, err := r.CurrentBranch(ctx, dir)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	// Before the first commit HEAD is unborn — the state a just-materialized
	// project is in — and the branch name must still be reported.
	if branch != "trunk" {
		t.Fatalf("initial branch: got %q want trunk", branch)
	}

	// Re-initialising an existing repo is a no-op, not an error: materializing a
	// project twice must be safe.
	if err := r.Init(ctx, dir, "trunk"); err != nil {
		t.Fatalf("second init should be a no-op: %v", err)
	}
}

func TestBranchLifecycle(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	dir := newRepo(t, r)

	if err := r.CreateBranch(ctx, dir, "fix/widget", "main"); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	branch, err := r.CurrentBranch(ctx, dir)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	if branch != "fix/widget" {
		t.Fatalf("branch: got %q want fix/widget", branch)
	}

	// Starting the same work twice must be idempotent — the branching policy is
	// applied by a tool the agent may call more than once.
	if err := r.Checkout(ctx, dir, "main"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}
	if err := r.CreateBranch(ctx, dir, "fix/widget", "main"); err != nil {
		t.Fatalf("re-creating an existing branch should check it out: %v", err)
	}
	if branch, _ = r.CurrentBranch(ctx, dir); branch != "fix/widget" {
		t.Fatalf("branch after re-create: got %q want fix/widget", branch)
	}
	if !r.BranchExists(ctx, dir, "main") {
		t.Fatal("base branch went missing")
	}
	if r.BranchExists(ctx, dir, "never-made") {
		t.Fatal("unknown branch reported as existing")
	}
}

func TestStatusPorcelainReportsWorkingTree(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	dir := newRepo(t, r)

	clean, err := r.StatusPorcelain(ctx, dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.TrimSpace(clean) != "" {
		t.Fatalf("fresh repo should be clean, got %q", clean)
	}

	if err := os.WriteFile(filepath.Join(dir, "calc.py"), []byte("def add(a,b):\n    return a+b\n\ndef subtract(a,b):\n    return a-b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := r.StatusPorcelain(ctx, dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(dirty, "calc.py") {
		t.Fatalf("modified file not reported: %q", dirty)
	}
}

// A missing config key is "not set", not a failure — Check depends on this to
// report a missing identity rather than erroring.
func TestConfigGetMissingKeyIsEmpty(t *testing.T) {
	r := testRunner(t)
	value, _ := r.ConfigGet(context.Background(), "podiom.definitely-not-set")
	if value != "" {
		t.Fatalf("unset key returned %q", value)
	}
}

// CanReach must fail fast on an unreachable remote rather than blocking on a
// credential prompt: without GIT_TERMINAL_PROMPT=0 this hangs forever.
func TestCanReachFailsFastWithoutPrompting(t *testing.T) {
	r := testRunner(t)
	err := r.CanReach(context.Background(), filepath.Join(t.TempDir(), "nope.git"))
	if err == nil {
		t.Fatal("unreachable remote reported as reachable")
	}
}

func TestCheckReportsIdentityState(t *testing.T) {
	status := Check(context.Background(), podiomexec.Discovery{})
	if !status.Found {
		t.Skip("git not available")
	}
	if status.Version == "" {
		t.Fatal("git found but no version reported")
	}
	// Ready is exactly "installed and has a commit identity".
	hasIdentity := status.UserName != "" && status.UserEmail != ""
	if status.Ready != hasIdentity {
		t.Fatalf("Ready=%v but identity present=%v", status.Ready, hasIdentity)
	}
	if !status.Ready && status.Hint == "" {
		t.Fatal("a not-ready status must say what is missing")
	}
}

// A key under $HOME is Podiom's first choice, ahead of the passwd home. The
// empty case is deliberately not asserted here: the developer machine running
// this test usually has a real key in the passwd home, and finding it is the
// correct behaviour.
func TestPublicKeyPrefersTheHomeSSHDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir reads USERPROFILE on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI0000000000000000000000000000000000000000000 test@example.invalid"
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte(key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PublicKey(); got != key {
		t.Fatalf("public key: got %q want %q", got, key)
	}
}

// OpenSSH ignores $HOME and expands ~ from the passwd entry, so on the Home
// Assistant add-on (HOME=/data/home, root's passwd home /root) ssh-keygen
// writes a key Podiom would otherwise never see. Both homes must be searched.
func TestSSHDirsIncludesTheOpenSSHHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir reads USERPROFILE on Windows")
	}
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skip("no passwd home available")
	}
	t.Setenv("HOME", t.TempDir())

	want := filepath.Join(u.HomeDir, ".ssh")
	for _, dir := range sshDirs() {
		if dir == want {
			return
		}
	}
	t.Fatalf("sshDirs() = %v, missing the OpenSSH home %q", sshDirs(), want)
}

// Podiom must never invent git credentials from the GitHub App token it holds
// for repo listing. This is a source-level guard on that boundary.
func TestNoTokenBasedCredentialPlumbing(t *testing.T) {
	raw, err := os.ReadFile("git.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"x-access-token", "GIT_ASKPASS", "credential.helper", "token.json"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("git package references %q; credentials must stay the user's own", forbidden)
		}
	}
}
