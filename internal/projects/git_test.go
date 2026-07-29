package projects

import "testing"

func TestGitBranchForAppliesThePolicy(t *testing.T) {
	direct := &Git{Enabled: true, Branching: BranchingDirect, DefaultBranch: "main"}
	if got := direct.BranchFor("bugfix", "widget crash"); got != "main" {
		t.Fatalf("direct policy must stay on the default branch, got %q", got)
	}

	perTask := &Git{Enabled: true, Branching: BranchingPerTask, DefaultBranch: "main"}
	if got := perTask.BranchFor("bugfix", "Widget Crash!"); got != "fix/widget-crash" {
		t.Fatalf("bugfix branch: got %q want fix/widget-crash", got)
	}
	if got := perTask.BranchFor("feature", "Add Search"); got != "feature/add-search" {
		t.Fatalf("feature branch: got %q want feature/add-search", got)
	}
	// An unknown kind still gets a branch rather than landing on main.
	if got := perTask.BranchFor("spike", "try things"); got != "work/try-things" {
		t.Fatalf("unknown kind: got %q want work/try-things", got)
	}
	// With nothing usable to name it, the default branch is the safe answer.
	if got := perTask.BranchFor("bugfix", "!!!"); got != "main" {
		t.Fatalf("unnameable slug: got %q want main", got)
	}

	// A project without source control has no branch at all.
	if got := (&Git{Enabled: false}).BranchFor("bugfix", "x"); got != "" {
		t.Fatalf("disabled project returned branch %q", got)
	}
	var absent *Git
	if got := absent.BranchFor("bugfix", "x"); got != "" {
		t.Fatalf("undeclared git returned branch %q", got)
	}
}

func TestNormalizeGitFillsDefaults(t *testing.T) {
	got := normalizeGit(&Git{Enabled: true})
	if got.DefaultBranch != "main" || got.Branching != BranchingDirect || got.Commit != CommitAsk {
		t.Fatalf("defaults not applied: %#v", got)
	}
	if got.BranchPrefixes["bugfix"] != "fix/" {
		t.Fatalf("default prefixes missing: %#v", got.BranchPrefixes)
	}
	// A disabled block keeps a remote the user configured, so toggling git back
	// on does not silently lose it.
	kept := normalizeGit(&Git{Enabled: false, Remote: "git@github.com:me/app.git"})
	if kept.Remote != "git@github.com:me/app.git" {
		t.Fatalf("disabled block dropped the remote: %#v", kept)
	}
	if normalizeGit(nil) != nil {
		t.Fatal("undeclared git must stay undeclared")
	}
}

// Project has a hand-written UnmarshalYAML that enumerates every field, so a
// new field is silently dropped on read unless it is added there too — which is
// exactly what happened to the git block the first time. This guards the round
// trip rather than the field list.
func TestGitBlockSurvivesTheLedgerRoundTrip(t *testing.T) {
	l := New(t.TempDir())
	if _, err := l.Create(Project{
		ID:   "app",
		Name: "App",
		Git: &Git{
			Enabled:       true,
			Remote:        "git@github.com:me/app.git",
			DefaultBranch: "trunk",
			Branching:     BranchingPerTask,
			Commit:        CommitAuto,
		},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := l.Get("app")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Git == nil {
		t.Fatal("git block lost on the ledger round trip")
	}
	if got.Git.Remote != "git@github.com:me/app.git" ||
		got.Git.DefaultBranch != "trunk" ||
		got.Git.Branching != BranchingPerTask ||
		got.Git.Commit != CommitAuto {
		t.Fatalf("git block altered on the round trip: %#v", got.Git)
	}

	// And a project written before the block existed reads back as undeclared,
	// not as a half-populated policy.
	if _, err := l.Create(Project{ID: "legacy", Name: "Legacy"}); err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	legacy, err := l.Get("legacy")
	if err != nil {
		t.Fatalf("get legacy: %v", err)
	}
	if legacy.Git != nil {
		t.Fatalf("project without a git block should read back undeclared, got %#v", legacy.Git)
	}
}
