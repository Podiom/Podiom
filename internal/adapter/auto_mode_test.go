package adapter

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

// TestCodexAutoModeUsesWorkspaceWrite pins the auto mapping: a workspace-write
// sandbox that still asks on escalation (on-request), rather than the
// read-only/never extremes of the neighbouring modes.
func TestCodexAutoModeUsesWorkspaceWrite(t *testing.T) {
	start := codexThreadStartParams(StartRequest{
		PermissionMode: config.PermissionAuto,
		WorkspaceDir:   "/tmp/project/repo",
	})
	if start["approvalPolicy"] != "on-request" {
		t.Fatalf("auto must keep asking on escalation: %#v", start["approvalPolicy"])
	}
	if start["sandbox"] != "workspace-write" {
		t.Fatalf("auto thread sandbox: got %#v want workspace-write", start["sandbox"])
	}

	turn := codexTurnStartParams("thread-1", "hi", nil, TurnSettings{
		PermissionMode: config.PermissionAuto,
		WorkspaceDir:   "/tmp/project/repo",
	}, nil)
	policy, ok := turn["sandboxPolicy"].(map[string]any)
	if !ok || policy["type"] != "workspaceWrite" {
		t.Fatalf("auto turn sandbox policy: %#v", turn["sandboxPolicy"])
	}
	if policy["networkAccess"] != false {
		t.Fatalf("auto must not grant network access: %#v", policy)
	}
}

// TestCodexAutoModeNarrowsWritableRoots is the containment guard for auto.
//
// On Codex the writable scope is governed by runtimeWorkspaceRoots (measured
// against app-server 0.142.4), and Podiom's ExtraWorkspaceDirs deliberately
// includes the projects parent directory so agents can read the shared ledger.
// Passing that broad set to a workspace-write sandbox would let one session
// write into every project on disk. auto must therefore receive the working
// directory alone — while approve and yolo keep the broad set, where writes are
// respectively impossible and unrestricted by design.
func TestCodexAutoModeNarrowsWritableRoots(t *testing.T) {
	const (
		repo        = "/home/u/.podiom/projects/app/repo"
		projectsDir = "/home/u/.podiom/projects"
		agentWork   = "/home/u/.podiom/agents/ada/workspace"
	)
	broad := []string{agentWork, projectsDir, "/home/u/.podiom/projects/app"}

	autoRoots := codexRuntimeRoots(config.PermissionAuto, repo, broad)
	if len(autoRoots) != 1 || autoRoots[0] != repo {
		t.Fatalf("auto roots must be the working directory alone, got %#v", autoRoots)
	}
	for _, root := range autoRoots {
		if root == projectsDir {
			t.Fatal("auto granted write access to the projects parent directory")
		}
	}

	for _, mode := range []config.PermissionMode{config.PermissionApprove, config.PermissionYolo} {
		roots := codexRuntimeRoots(mode, repo, broad)
		if len(roots) != len(broad)+1 {
			t.Fatalf("%s must keep the broad root set, got %#v", mode, roots)
		}
	}

	// The declared writableRoots must agree with the runtime roots, so the wire
	// log cannot imply a wider scope than Codex actually applies.
	turn := codexTurnStartParams("thread-1", "hi", nil, TurnSettings{
		PermissionMode:     config.PermissionAuto,
		WorkspaceDir:       repo,
		ExtraWorkspaceDirs: broad,
	}, nil)
	runtime, _ := turn["runtimeWorkspaceRoots"].([]string)
	policy, _ := turn["sandboxPolicy"].(map[string]any)
	writable, _ := policy["writableRoots"].([]string)
	if len(runtime) != 1 || runtime[0] != repo {
		t.Fatalf("turn runtime roots not narrowed: %#v", runtime)
	}
	if len(writable) != len(runtime) || writable[0] != runtime[0] {
		t.Fatalf("writableRoots %#v disagrees with runtimeWorkspaceRoots %#v", writable, runtime)
	}
}

// TestClaudeAutoModeUsesAcceptEdits pins auto to acceptEdits and, critically,
// keeps the permission relay wired: only edits are automatic, every other tool
// still reaches a human.
//
// Claude's own richer "auto" mode is deliberately not used — measured against
// 2.1.220, `--permission-mode auto` is silently downgraded to "default" in
// headless -p runs (as is "manual"), so acceptEdits is the only value that
// takes effect.
func TestClaudeAutoModeUsesAcceptEdits(t *testing.T) {
	workspace := t.TempDir()
	c := &Claude{bin: "claude", daemonAddr: "127.0.0.1:8787", mcpCommand: "podiomd"}
	args, cleanup, _, err := c.args(TurnRequest{
		SessionID: "s1",
		Settings: TurnSettings{
			PermissionMode: config.PermissionAuto,
			WorkspaceDir:   workspace,
		},
	}, true)
	defer cleanup()
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--permission-mode acceptEdits") {
		t.Fatalf("auto must map to acceptEdits: %q", got)
	}
	if !strings.Contains(got, "--permission-prompt-tool mcp__podiom_permission__prompt") {
		t.Fatalf("auto must keep the relay for non-edit tools: %q", got)
	}
	if strings.Contains(got, "bypassPermissions") {
		t.Fatalf("auto must not bypass permissions: %q", got)
	}
}
