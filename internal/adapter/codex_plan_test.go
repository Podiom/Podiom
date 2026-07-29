package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

// testCodexClient returns a client with discovery pre-populated, so the pure
// mode-building logic can be exercised without a live app-server.
func testCodexClient() *codexClient {
	return &codexClient{
		log: slog.Default(),
		meta: codexMeta{
			presets: []codexCollabPreset{
				{Name: "Plan", Mode: "plan", ReasoningEffort: "medium"},
				{Name: "Default", Mode: "default"},
			},
			presetsDone:  true,
			modelDone:    true,
			defaultModel: "gpt-5.5",
		},
	}
}

func TestCodexPlanProposal(t *testing.T) {
	planItem := json.RawMessage(`{"item":{"id":"turn-1-plan","type":"plan","text":"# Plan\n\n## Summary\nDo it."}}`)

	t.Run("completed plan item is captured", func(t *testing.T) {
		got := codexPlanProposal("item/completed", planItem)
		if got == nil {
			t.Fatal("completed plan item was not captured")
		}
		if !strings.Contains(got.Markdown, "## Summary") {
			t.Fatalf("markdown not captured: %q", got.Markdown)
		}
		// Codex writes no file; Podiom keeps the canonical copy itself.
		if got.FilePath != "" {
			t.Fatalf("Codex plans have no provider file, got %q", got.FilePath)
		}
	})

	t.Run("only the completed item is authoritative", func(t *testing.T) {
		// item/plan/delta text may differ from the final item, per the schema,
		// so a started item must not be captured.
		if got := codexPlanProposal("item/started", planItem); got != nil {
			t.Fatalf("started plan item should not be captured: %#v", got)
		}
	})

	t.Run("other item types are ignored", func(t *testing.T) {
		msg := json.RawMessage(`{"item":{"id":"i1","type":"agentMessage","text":"here is a plan"}}`)
		if got := codexPlanProposal("item/completed", msg); got != nil {
			t.Fatalf("agentMessage captured as a plan: %#v", got)
		}
	})

	t.Run("turn/plan/updated is not a plan", func(t *testing.T) {
		// The update_plan progress checklist shares the word but is a different
		// feature; conflating them would submit a todo list for approval.
		msg := json.RawMessage(`{"plan":[{"step":"one","status":"pending"}]}`)
		if got := codexPlanProposal("turn/plan/updated", msg); got != nil {
			t.Fatalf("progress checklist captured as a plan: %#v", got)
		}
	})

	t.Run("empty plan text is not a plan", func(t *testing.T) {
		msg := json.RawMessage(`{"item":{"id":"i1","type":"plan","text":"  "}}`)
		if got := codexPlanProposal("item/completed", msg); got != nil {
			t.Fatalf("empty plan captured: %#v", got)
		}
	})
}

// TestCodexCollaborationModeRidesEveryTurn: the setting is sticky on the thread,
// so an implementation turn must say "default" explicitly or the thread would
// keep planning after the user approved.
func TestCodexCollaborationModeRidesEveryTurn(t *testing.T) {
	c := testCodexClient()
	ctx := context.Background()

	plan := c.collaborationMode(ctx, TurnSettings{PlanMode: true, Model: "gpt-5.5"})
	if plan == nil || plan["mode"] != "plan" {
		t.Fatalf("plan turn mode: %#v", plan)
	}
	normal := c.collaborationMode(ctx, TurnSettings{PlanMode: false, Model: "gpt-5.5"})
	if normal == nil || normal["mode"] != "default" {
		t.Fatalf("non-plan turn must explicitly request default: %#v", normal)
	}
}

// TestCodexCollaborationModeSettings pins the two fields most easily got wrong:
// model is required, and developer_instructions must be null to select Codex's
// built-in plan contract (which does not clear the thread's own instructions).
func TestCodexCollaborationModeSettings(t *testing.T) {
	c := testCodexClient()
	mode := c.collaborationMode(context.Background(), TurnSettings{PlanMode: true, Model: "gpt-5.5"})
	settings, ok := mode["settings"].(map[string]any)
	if !ok {
		t.Fatalf("no settings: %#v", mode)
	}
	if settings["model"] != "gpt-5.5" {
		t.Fatalf("model must be supplied: %#v", settings)
	}
	if got, present := settings["developer_instructions"]; !present || got != nil {
		t.Fatalf("developer_instructions must be null to select the built-ins: %#v", settings)
	}
	if settings["reasoning_effort"] != "medium" {
		t.Fatalf("preset effort should be used when the session sets none: %#v", settings)
	}

	// With no session model, the preset's model or the discovered default fills in.
	mode = c.collaborationMode(context.Background(), TurnSettings{PlanMode: true})
	settings, _ = mode["settings"].(map[string]any)
	if settings["model"] != "gpt-5.5" {
		t.Fatalf("model should fall back to the discovered default: %#v", settings)
	}
}

// TestCodexCollaborationModeDegradesWithoutPresets: a server that cannot list
// modes (older Codex, or the experimental capability lost) must leave the field
// off rather than send a malformed one.
func TestCodexCollaborationModeDegradesWithoutPresets(t *testing.T) {
	c := &codexClient{log: slog.Default(), meta: codexMeta{presetsDone: true, modelDone: true}}
	if got := c.collaborationMode(context.Background(), TurnSettings{PlanMode: true}); got != nil {
		t.Fatalf("expected no collaboration mode without presets, got %#v", got)
	}
}

// TestCodexPlanModePinsReadOnlySandbox: plan mode is behavioral orchestration,
// not a sandbox boundary, so Podiom enforces non-mutation rather than trusting
// the instruction — including when the session itself is in yolo.
func TestCodexPlanModePinsReadOnlySandbox(t *testing.T) {
	for _, mode := range []config.PermissionMode{config.PermissionApprove, config.PermissionAuto, config.PermissionYolo} {
		params := codexTurnStartParams("thread-1", "plan it", nil, TurnSettings{
			PermissionMode: mode,
			PlanMode:       true,
			WorkspaceDir:   "/tmp/project/repo",
		}, nil)
		policy, ok := params["sandboxPolicy"].(map[string]any)
		if !ok || policy["type"] != "readOnly" {
			t.Fatalf("%s plan turn must pin readOnly, got %#v", mode, params["sandboxPolicy"])
		}
	}
}
