package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
)

func TestUserProfileReadWriteDelete(t *testing.T) {
	c, cleanup := newTestCore(t)
	defer cleanup()

	// Missing file reads as empty, not an error.
	got, err := c.ReadUserProfile()
	if err != nil {
		t.Fatalf("read missing USER.md: %v", err)
	}
	if got != "" {
		t.Fatalf("missing USER.md should read empty, got %q", got)
	}

	if err := c.WriteUserProfile("  # About the user\n\nLikes short answers.  \n"); err != nil {
		t.Fatalf("write USER.md: %v", err)
	}
	got, err = c.ReadUserProfile()
	if err != nil {
		t.Fatalf("read USER.md: %v", err)
	}
	if got != "# About the user\n\nLikes short answers.\n" {
		t.Fatalf("unexpected USER.md content %q", got)
	}

	if err := c.WriteUserProfile("   \n"); err == nil {
		t.Fatalf("blank profile write should be rejected")
	}
	if err := c.WriteUserProfile(strings.Repeat("x", userProfileMaxBytes+1)); err == nil {
		t.Fatalf("oversized profile write should be rejected")
	}

	if err := c.DeleteUserProfile(); err != nil {
		t.Fatalf("delete USER.md: %v", err)
	}
	if _, err := os.Stat(c.paths.UserMD); !os.IsNotExist(err) {
		t.Fatalf("USER.md should be gone after delete, stat err=%v", err)
	}
	// Deleting again is fine.
	if err := c.DeleteUserProfile(); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}

func TestUserProfileComposesAsSecondLayer(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "companion", Provider: config.ProviderClaude})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	paths := c.AgentPaths(agent.Name)
	if err := os.WriteFile(paths.Agents, []byte("agent layer\n"), 0o644); err != nil {
		t.Fatalf("write agent AGENTS.md: %v", err)
	}
	if err := os.WriteFile(paths.Soul, []byte("soul layer\n"), 0o644); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}
	if err := c.WriteUserProfile("# About the user\n\nuser layer\n"); err != nil {
		t.Fatalf("write USER.md: %v", err)
	}

	// Claude: USER.md is @-imported directly after the base layer.
	claudePayload, err := c.composer.Compose(ctx, agent, DeliveryClaudeImport, "")
	if err != nil {
		t.Fatalf("compose claude: %v", err)
	}
	got := string(claudePayload.Bytes)
	baseIdx := strings.Index(got, "@"+c.paths.BaseAgents)
	userIdx := strings.Index(got, "@"+c.paths.UserMD)
	agentIdx := strings.Index(got, "@"+paths.Agents)
	if baseIdx == -1 || userIdx == -1 || agentIdx == -1 {
		t.Fatalf("claude payload missing layers:\n%s", got)
	}
	if !(baseIdx < userIdx && userIdx < agentIdx) {
		t.Fatalf("USER.md should sit between base and agent layers:\n%s", got)
	}

	// Codex: the profile content is inlined in the same position.
	agent.Provider = config.ProviderCodex
	codexPayload, err := c.composer.Compose(ctx, agent, DeliveryCodexBundle, "")
	if err != nil {
		t.Fatalf("compose codex: %v", err)
	}
	got = string(codexPayload.Bytes)
	baseIdx = strings.Index(got, "base layer")
	userIdx = strings.Index(got, "user layer")
	agentIdx = strings.Index(got, "agent layer")
	if baseIdx == -1 || userIdx == -1 || agentIdx == -1 {
		t.Fatalf("codex payload missing layers:\n%s", got)
	}
	if !(baseIdx < userIdx && userIdx < agentIdx) {
		t.Fatalf("USER.md should sit between base and agent layers:\n%s", got)
	}

	// Blank profile contributes nothing.
	if err := os.WriteFile(c.paths.UserMD, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("blank USER.md: %v", err)
	}
	agent.Provider = config.ProviderClaude
	blankPayload, err := c.composer.Compose(ctx, agent, DeliveryClaudeImport, "")
	if err != nil {
		t.Fatalf("recompose claude: %v", err)
	}
	if strings.Contains(string(blankPayload.Bytes), "USER.md") {
		t.Fatalf("blank USER.md should not be composed in:\n%s", blankPayload.Bytes)
	}
}

func TestUserProfileInterviewPrompt(t *testing.T) {
	prompt := UserProfileInterviewPrompt("")
	for _, want := range []string{
		"exactly one question at a time",
		"podiom_ask_profile_question",
		"3 to 5 concrete, distinct selectable options",
		"identity_context",
		"podiom_submit_user_profile",
		"Podiom renders the final Markdown",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("interview prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "already exists") {
		t.Fatalf("fresh prompt should not mention an existing profile")
	}

	redo := UserProfileInterviewPrompt("# About the user\n\nexisting profile body")
	if !strings.Contains(redo, "existing profile body") {
		t.Fatalf("redo prompt should embed the current profile:\n%s", redo)
	}
	if !strings.Contains(redo, "refresh") {
		t.Fatalf("redo prompt should frame the interview as a refresh")
	}
}

func TestRenderUserProfileDraft(t *testing.T) {
	draft := UserProfileDraft{
		IdentityContext:   []UserProfileFact{{Label: "Name", Value: "Marcus"}},
		Communication:     []UserProfileFact{{Label: "Tone", Value: "Direct and conversational"}},
		OutputPreferences: []UserProfileFact{{Label: "Structure", Value: "Lead with the outcome"}},
		TechnicalContext:  []UserProfileFact{{Label: "Depth", Value: "Expert-level implementation detail"}},
		WorkingTogether:   []UserProfileFact{{Label: "Feedback", Value: "Challenge shaky assumptions"}},
	}
	got, err := RenderUserProfileDraft(draft)
	if err != nil {
		t.Fatalf("render draft: %v", err)
	}
	for _, want := range []string{
		"# About the user",
		"## Identity and context",
		"- **Name:** Marcus",
		"## Technical context",
		"## Working together",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered profile missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "they") {
		t.Fatalf("rendered labeled facts unexpectedly use third-person pronouns:\n%s", got)
	}

	draft.Communication = nil
	if _, err := RenderUserProfileDraft(draft); err == nil {
		t.Fatal("expected empty section to be rejected")
	}
}

func TestInterviewGateAllowsOnlyInterviewTools(t *testing.T) {
	gate := NewInterviewGateRelay()
	for _, tool := range []string{
		"mcp__podiom_interview__podiom_ask_profile_question",
		"mcp__podiom_interview__podiom_submit_user_profile",
	} {
		decision, err := gate.RequestPermission(context.Background(), adapter.PermissionRequest{ToolName: tool}, time.Second)
		if err != nil || decision.Behavior != "allow" {
			t.Fatalf("tool %q should be allowed: decision=%+v err=%v", tool, decision, err)
		}
	}
	decision, err := gate.RequestPermission(context.Background(), adapter.PermissionRequest{ToolName: "Bash"}, time.Second)
	if err != nil || decision.Behavior != "deny" {
		t.Fatalf("Bash should be denied: decision=%+v err=%v", decision, err)
	}
}

func TestCleanUserProfileMarkdown(t *testing.T) {
	fenced := "```markdown\n# About the user\n\nBody.\n```"
	if got := CleanUserProfileMarkdown(fenced); got != "# About the user\n\nBody.\n" {
		t.Fatalf("fences not stripped: %q", got)
	}
	preamble := "Here is the profile:\n\n# About the user\n\nBody.\n"
	if got := CleanUserProfileMarkdown(preamble); got != "# About the user\n\nBody.\n" {
		t.Fatalf("preamble not dropped: %q", got)
	}
	if got := CleanUserProfileMarkdown("Body only.\n"); got != "# About the user\n\nBody only.\n" {
		t.Fatalf("missing heading not injected: %q", got)
	}
	// A preamble before the opening fence must not leave the closing fence
	// dangling at the end of the sliced document.
	fencedPreamble := "Here you go:\n\n```markdown\n# About the user\n\nBody.\n```"
	if got := CleanUserProfileMarkdown(fencedPreamble); got != "# About the user\n\nBody.\n" {
		t.Fatalf("preamble+fence not cleaned: %q", got)
	}
	// Blank input stays blank rather than gaining a heading-only document.
	if got := CleanUserProfileMarkdown("   \n"); got != "\n" {
		t.Fatalf("blank input should stay blank: %q", got)
	}
}
