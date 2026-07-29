package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
)

func writePlan(t *testing.T, dir, name, body string, modTime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestClaudePlansDirDefaultsToConfigDir pins the measured default: Claude writes
// to <CLAUDE_CONFIG_DIR>/plans, which Podiom knows because it sets that variable
// per profile.
func TestClaudePlansDirDefaultsToConfigDir(t *testing.T) {
	profile := t.TempDir()
	workspace := t.TempDir()
	if got, want := claudePlansDir(profile, workspace), filepath.Join(profile, "plans"); got != want {
		t.Fatalf("plans dir: got %q want %q", got, want)
	}
}

// TestClaudePlansDirHonorsUserSetting checks Podiom reads a plansDirectory the
// user configured rather than overriding it — both absolute and, per Claude's
// documented semantics, relative to the project root.
func TestClaudePlansDirHonorsUserSetting(t *testing.T) {
	profile := t.TempDir()
	workspace := t.TempDir()

	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(profile, "settings.json"),
		[]byte(`{"plansDirectory": "`+elsewhere+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := claudePlansDir(profile, workspace); got != elsewhere {
		t.Fatalf("absolute user plansDirectory ignored: got %q want %q", got, elsewhere)
	}

	// Project-local settings take precedence, and a relative value resolves
	// against the project root.
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "settings.json"),
		[]byte(`{"plansDirectory": "docs/plans"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := claudePlansDir(profile, workspace), filepath.Join(workspace, "docs", "plans"); got != want {
		t.Fatalf("relative user plansDirectory: got %q want %q", got, want)
	}
}

// TestClaudePlansDirIgnoresMalformedSettings: a broken settings file means "not
// configured", never a failed turn.
func TestClaudePlansDirIgnoresMalformedSettings(t *testing.T) {
	profile := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(profile, "settings.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := claudePlansDir(profile, workspace), filepath.Join(profile, "plans"); got != want {
		t.Fatalf("malformed settings should fall back: got %q want %q", got, want)
	}
}

func TestDetectPlan(t *testing.T) {
	t.Run("new file written during the turn", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "plans")
		snap := snapshotPlans(dir) // directory does not exist yet — the normal first-plan case
		writePlan(t, dir, "add-subtract-quiet-heron.md", "# Plan\n\nDo the thing.", time.Now().Add(time.Second))

		got := detectPlan(snap)
		if got == nil {
			t.Fatal("plan written during the turn was not detected")
		}
		if !strings.Contains(got.Markdown, "Do the thing") {
			t.Fatalf("wrong markdown captured: %q", got.Markdown)
		}
	})

	t.Run("revision overwriting the same filename", func(t *testing.T) {
		// The regression this guards: Claude derives the slug from the session,
		// so revising overwrites the same file. Matching on filenames alone
		// would capture the first plan and silently miss every revision.
		dir := t.TempDir()
		writePlan(t, dir, "plan.md", "# Plan\n\nFirst attempt.", time.Now().Add(-time.Hour))
		snap := snapshotPlans(dir)
		writePlan(t, dir, "plan.md", "# Plan\n\nRevised after feedback.", time.Now().Add(time.Second))

		got := detectPlan(snap)
		if got == nil {
			t.Fatal("revision overwriting the same filename was not detected")
		}
		if !strings.Contains(got.Markdown, "Revised after feedback") {
			t.Fatalf("captured the stale plan: %q", got.Markdown)
		}
	})

	t.Run("no plan written", func(t *testing.T) {
		dir := t.TempDir()
		writePlan(t, dir, "old.md", "# Plan\n\nFrom another session.", time.Now().Add(-time.Hour))
		snap := snapshotPlans(dir)

		if got := detectPlan(snap); got != nil {
			t.Fatalf("pre-existing plan misattributed to this turn: %#v", got)
		}
	})

	t.Run("newest wins when the profile is shared", func(t *testing.T) {
		dir := t.TempDir()
		snap := snapshotPlans(dir)
		writePlan(t, dir, "other-session.md", "# Plan\n\nSomeone else.", time.Now().Add(time.Second))
		writePlan(t, dir, "this-session.md", "# Plan\n\nMine, written last.", time.Now().Add(2*time.Second))

		got := detectPlan(snap)
		if got == nil || !strings.Contains(got.Markdown, "written last") {
			t.Fatalf("expected the newest plan, got %#v", got)
		}
	})

	t.Run("empty plan file is not a plan", func(t *testing.T) {
		dir := t.TempDir()
		snap := snapshotPlans(dir)
		writePlan(t, dir, "empty.md", "   \n", time.Now().Add(time.Second))

		if got := detectPlan(snap); got != nil {
			t.Fatalf("empty file treated as a plan: %#v", got)
		}
	})
}

// TestClaudePlanModeArgsAreNonIntrusive is the standing guard for the decision
// that Podiom observes rather than configures: plan mode adds --permission-mode
// plan and nothing else, and Podiom must never write a Claude settings file.
func TestClaudePlanModeArgsAreNonIntrusive(t *testing.T) {
	workspace := t.TempDir()
	c := &Claude{bin: "claude", daemonAddr: "127.0.0.1:8787", mcpCommand: "podiomd"}
	args, cleanup, _, err := c.args(TurnRequest{
		SessionID: "s1",
		Settings: TurnSettings{
			PermissionMode: config.PermissionApprove,
			PlanMode:       true,
			WorkspaceDir:   workspace,
		},
	}, true)
	defer cleanup()
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--permission-mode plan") {
		t.Fatalf("plan mode not requested: %q", got)
	}
	// Claude enforces read-only itself, so the relay would be dead weight.
	if strings.Contains(got, "--permission-prompt-tool") {
		t.Fatalf("plan mode should not wire the permission relay: %q", got)
	}
	if strings.Contains(got, "plansDirectory") {
		t.Fatalf("Podiom must not configure plansDirectory: %q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("Podiom wrote a Claude settings file; it must observe, not configure")
	}
}
