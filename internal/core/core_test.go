package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

func TestCreateAgentScaffoldsDirectory(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	paths := c.AgentPaths(agent.Name)
	if _, err := os.Stat(paths.Workspace); err != nil {
		t.Fatalf("workspace not scaffolded: %v", err)
	}
	soul, err := os.ReadFile(paths.Soul)
	if err != nil {
		t.Fatalf("SOUL.md not scaffolded: %v", err)
	}
	if !strings.Contains(string(soul), "Name: writer") {
		t.Fatalf("SOUL.md did not include agent name:\n%s", soul)
	}
	if _, err := os.Stat(paths.Agents); !os.IsNotExist(err) {
		t.Fatalf("per-agent AGENTS.md should be left for the user, stat err=%v", err)
	}
	// MEMORY.md is scaffolded empty beside SOUL.md (MEM1/MEM2).
	mem, err := os.ReadFile(paths.Memory)
	if err != nil {
		t.Fatalf("MEMORY.md not scaffolded: %v", err)
	}
	if len(mem) != 0 {
		t.Fatalf("MEMORY.md should be created empty, got %q", mem)
	}
}

func TestMemoryComposesAsFourthLayerWithinBudget(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "keeper", Provider: config.ProviderClaude})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	paths := c.AgentPaths(agent.Name)
	if err := os.WriteFile(paths.Soul, []byte("soul layer\n"), 0o644); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}

	// A memory well over the injection budget so truncation is exercised.
	var sb strings.Builder
	for i := 0; i < memoryBudgetLines+100; i++ {
		fmt.Fprintf(&sb, "- memory line %d\n", i)
	}
	if err := c.WriteAgentMemory(agent.Name, sb.String()); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	// Claude: the payload @-imports a truncated snapshot, not MEMORY.md directly.
	claudePayload, err := c.composer.Compose(ctx, agent, DeliveryClaudeImport)
	if err != nil {
		t.Fatalf("compose claude: %v", err)
	}
	snapPath := filepath.Join(paths.Workspace, ".podiom-memory.md")
	if !strings.Contains(string(claudePayload.Bytes), "@"+snapPath) {
		t.Fatalf("claude payload should import the memory snapshot:\n%s", claudePayload.Bytes)
	}
	if strings.Contains(string(claudePayload.Bytes), "@"+paths.Memory+"\n") {
		t.Fatalf("claude payload must not import MEMORY.md directly (would bypass budget)")
	}
	snap, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read memory snapshot: %v", err)
	}
	if got := strings.Count(string(snap), "memory line "); got != memoryBudgetLines {
		t.Fatalf("snapshot should carry exactly %d memory lines, got %d", memoryBudgetLines, got)
	}
	if !strings.Contains(string(snap), "truncated to fit injection budget") {
		t.Fatalf("snapshot should note the truncation:\n%s", snap)
	}

	// Codex: the truncated memory is inlined last, after SOUL.md.
	agent.Provider = config.ProviderCodex
	codexPayload, err := c.composer.Compose(ctx, agent, DeliveryCodexBundle)
	if err != nil {
		t.Fatalf("compose codex: %v", err)
	}
	got := string(codexPayload.Bytes)
	if soulIdx, memIdx := strings.Index(got, "soul layer"), strings.Index(got, "MEMORY.md"); soulIdx == -1 || memIdx == -1 || soulIdx > memIdx {
		t.Fatalf("codex bundle should carry memory after SOUL.md:\n%s", got)
	}
	if n := strings.Count(got, "memory line "); n != memoryBudgetLines {
		t.Fatalf("codex bundle should carry exactly %d memory lines, got %d", memoryBudgetLines, n)
	}

	// An empty memory contributes nothing (MEM2).
	if err := c.ClearAgentMemory(agent.Name); err != nil {
		t.Fatalf("clear memory: %v", err)
	}
	agent.Provider = config.ProviderClaude
	emptyPayload, err := c.composer.Compose(ctx, agent, DeliveryClaudeImport)
	if err != nil {
		t.Fatalf("recompose claude: %v", err)
	}
	if strings.Contains(string(emptyPayload.Bytes), ".podiom-memory.md") {
		t.Fatalf("empty memory should not be composed in:\n%s", emptyPayload.Bytes)
	}
}

func TestInstructionCompositionPayloads(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "builder", Provider: config.ProviderClaude})
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

	claudePayload, err := c.composer.Compose(ctx, agent, DeliveryClaudeImport)
	if err != nil {
		t.Fatalf("compose claude: %v", err)
	}
	wantClaude := "# Podiom generated Claude context for builder\n\n" +
		"@" + c.paths.BaseAgents + "\n" +
		"@" + paths.Agents + "\n" +
		"@" + paths.Soul + "\n"
	if string(claudePayload.Bytes) != wantClaude {
		t.Fatalf("unexpected claude payload:\n%s", claudePayload.Bytes)
	}
	if claudePayload.Path != filepath.Join(paths.Workspace, "CLAUDE.md") {
		t.Fatalf("unexpected claude payload path %q", claudePayload.Path)
	}

	agent.Provider = config.ProviderCodex
	codexPayload, err := c.composer.Compose(ctx, agent, DeliveryCodexBundle)
	if err != nil {
		t.Fatalf("compose codex: %v", err)
	}
	got := string(codexPayload.Bytes)
	baseIdx := strings.Index(got, "base layer")
	agentIdx := strings.Index(got, "agent layer")
	soulIdx := strings.Index(got, "soul layer")
	if baseIdx == -1 || agentIdx == -1 || soulIdx == -1 {
		t.Fatalf("codex payload missing layers:\n%s", got)
	}
	if !(baseIdx < agentIdx && agentIdx < soulIdx) {
		t.Fatalf("codex payload order is wrong:\n%s", got)
	}
	if codexPayload.Path != filepath.Join(paths.Workspace, "AGENTS.md") {
		t.Fatalf("unexpected codex payload path %q", codexPayload.Path)
	}
}

func TestAppendTurnHistorySurvivesReopen(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	fake := adapter.NewFake()
	fake.Responses = []string{"assistant one"}
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "analyst", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "analyst", Origin: store.OriginCLI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.ProviderHandle == "" {
		t.Fatalf("expected provider handle to be stored")
	}
	written, err := c.AppendTurn(ctx, session.ID, "hello")
	if err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("expected user and assistant messages, got %d", len(written))
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	history, err := reopened.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list reopened history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(history))
	}
	if history[0].Seq != 1 || history[0].Role != store.RoleUser || history[0].Content != "hello" {
		t.Fatalf("bad first message: %+v", history[0])
	}
	if history[1].Seq != 2 || history[1].Role != store.RoleAssistant || history[1].Content != "assistant one" {
		t.Fatalf("bad second message: %+v", history[1])
	}
}

func TestFinalAssistantPersistSurvivesCanceledTurnContext(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "analyst", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "analyst", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	turnCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.appendFinalMessages(turnCtx, session.ID, []store.Message{
		{Role: store.RoleAssistant, Content: "finished after the socket closed"},
	}); err != nil {
		t.Fatalf("append final message with canceled context: %v", err)
	}

	history, err := c.History(ctx, session.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || history[0].Role != store.RoleAssistant || history[0].Content != "finished after the socket closed" {
		t.Fatalf("final assistant message was not persisted: %+v", history)
	}
}

func TestSlashCommandsUpdateSessionSettingsAndMetadata(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "operator", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "operator", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := c.HandleSlashCommand(ctx, session.ID, "/model claude-sonnet")
	if err != nil {
		t.Fatalf("model slash: %v", err)
	}
	if !result.Handled || result.Session.Model != "claude-sonnet" {
		t.Fatalf("model slash did not update session: %+v", result)
	}
	result, err = c.HandleSlashCommand(ctx, session.ID, "/effort high")
	if err != nil {
		t.Fatalf("effort slash: %v", err)
	}
	if result.Session.Effort != "high" {
		t.Fatalf("effort slash did not update session: %+v", result.Session)
	}
	result, err = c.HandleSlashCommand(ctx, session.ID, "/permission yolo")
	if err != nil {
		t.Fatalf("permission slash: %v", err)
	}
	if result.Session.PermissionMode != config.PermissionYolo {
		t.Fatalf("permission slash did not update session: %+v", result.Session)
	}
	// Switching to yolo must carry an explicit whole-machine opt-in warning (R8.31).
	if !strings.Contains(strings.ToLower(result.Notice), "whole-machine") {
		t.Fatalf("yolo slash missing opt-in warning: %q", result.Notice)
	}
	result, err = c.HandleSlashCommand(ctx, session.ID, "/name Launch Plan")
	if err != nil {
		t.Fatalf("name slash: %v", err)
	}
	if result.Session.Name != "Launch Plan" || result.Session.AutoNamed {
		t.Fatalf("name slash did not update metadata: %+v", result.Session)
	}
}

func TestUpdateSessionSettingsDirectly(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "operator", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "operator", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	updated, err := c.UpdateSessionSettings(ctx, session.ID, "", "", config.PermissionYolo)
	if err != nil {
		t.Fatalf("update permission: %v", err)
	}
	if updated.ID != session.ID {
		t.Fatalf("settings update changed session id: got %q want %q", updated.ID, session.ID)
	}
	if updated.PermissionMode != config.PermissionYolo {
		t.Fatalf("permission was not updated: %+v", updated)
	}
	if updated.Model != session.Model || updated.Effort != session.Effort {
		t.Fatalf("empty direct settings should preserve model/effort: before=%+v after=%+v", session, updated)
	}
}

func TestCreateSessionCanOverrideAgentProvider(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	c, err := New(Options{
		Paths:   paths,
		Store:   db,
		Adapter: adapter.NewFake(),
		Profiles: []config.Profile{
			{Name: "claude-work", Provider: config.ProviderClaude, ConfigDir: "/tmp/claude-work"},
			{Name: "codex-main", Provider: config.ProviderCodex, HomeDir: "/tmp/codex-main"},
		},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name:     "portable",
		Provider: config.ProviderClaude,
		Profile:  "claude-work",
		Model:    "sonnet",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	bareCodex, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "portable",
		Origin:    store.OriginWeb,
		Provider:  config.ProviderCodex,
	})
	if err != nil {
		t.Fatalf("create bare codex session: %v", err)
	}
	if bareCodex.Provider != config.ProviderCodex || bareCodex.Profile != "" {
		t.Fatalf("bare provider override did not select codex default: %+v", bareCodex)
	}
	if bareCodex.Model != "" {
		t.Fatalf("cross-provider session should not inherit claude model, got %q", bareCodex.Model)
	}

	profileCodex, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "portable",
		Origin:    store.OriginWeb,
		Profile:   "codex-main",
	})
	if err != nil {
		t.Fatalf("create profiled codex session: %v", err)
	}
	if profileCodex.Provider != config.ProviderCodex || profileCodex.Profile != "codex-main" {
		t.Fatalf("profile override did not infer codex target: %+v", profileCodex)
	}

	if _, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "portable",
		Origin:    store.OriginWeb,
		Provider:  config.ProviderClaude,
		Profile:   "codex-main",
	}); err == nil {
		t.Fatal("expected mismatched provider/profile to fail")
	}
}

func TestProfileSlashSwitchesTargetAndClearsHandle(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	c, err := New(Options{
		Paths:   paths,
		Store:   db,
		Adapter: adapter.NewFake(),
		Profiles: []config.Profile{
			{Name: "work", Provider: config.ProviderClaude, ConfigDir: "/tmp/claude-work"},
			{Name: "codex-main", Provider: config.ProviderCodex, HomeDir: "/tmp/codex-main"},
		},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "switcher", Provider: config.ProviderClaude, Profile: "work"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "switcher", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.ProviderHandle == "" {
		t.Fatal("expected initial fake handle")
	}

	result, err := c.HandleSlashCommand(ctx, session.ID, "/profile codex-main")
	if err != nil {
		t.Fatalf("profile slash: %v", err)
	}
	if result.Session.Provider != config.ProviderCodex || result.Session.Profile != "codex-main" {
		t.Fatalf("profile slash did not switch provider/profile: %+v", result.Session)
	}
	if result.Session.ProviderHandle != "" {
		t.Fatalf("profile switch should clear provider handle, got %q", result.Session.ProviderHandle)
	}
}

func TestRateLimitFallbackSwitchesProviderWithReplayHistory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	fake := adapter.NewFake()
	fake.RateLimitedTurns = 1
	fake.Responses = []string{"fallback ok"}
	c, err := New(Options{
		Paths:                 paths,
		Store:                 db,
		Adapter:               fake,
		DisableBackgroundWork: true,
		Profiles: []config.Profile{
			{Name: "work", Provider: config.ProviderClaude, ConfigDir: "/tmp/claude-work"},
			{Name: "codex-main", Provider: config.ProviderCodex, HomeDir: "/tmp/codex-main"},
		},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name:     "fallbacker",
		Provider: config.ProviderClaude,
		Profile:  "work",
		Fallback: []string{"work", "codex-main"},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "fallbacker", Origin: store.OriginCLI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.UpdateSessionMetadata(ctx, session.ID, "Manual", "", false); err != nil {
		t.Fatalf("avoid auto-name: %v", err)
	}
	if _, err := db.AppendMessages(ctx, session.ID, []store.Message{
		{Role: store.RoleUser, Content: "remember alpha"},
		{Role: store.RoleAssistant, Content: "alpha remembered"},
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	written, err := c.AppendTurn(ctx, session.ID, "continue after limit")
	if err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if got := written[len(written)-1].Content; got != "fallback ok" {
		t.Fatalf("unexpected assistant after fallback %q", got)
	}
	updated, err := c.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("get updated session: %v", err)
	}
	if updated.Provider != config.ProviderCodex || updated.Profile != "codex-main" {
		t.Fatalf("fallback did not switch target: %+v", updated)
	}
	if len(fake.Requests) < 2 {
		t.Fatalf("expected original and fallback requests, got %d", len(fake.Requests))
	}
	if fake.Requests[0].Handle.Provider != config.ProviderClaude {
		t.Fatalf("first request should use claude: %+v", fake.Requests[0].Handle)
	}
	if fake.Requests[1].Handle.Provider != config.ProviderCodex || fake.Requests[1].Settings.Profile != "codex-main" {
		t.Fatalf("fallback request should use codex profile: %+v", fake.Requests[1])
	}
	if len(fake.Requests[1].History) != 2 || fake.Requests[1].History[0].Content != "remember alpha" {
		t.Fatalf("fallback request did not replay prior history: %+v", fake.Requests[1].History)
	}
}

func TestCreateAgentValidatesFallbackTargets(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	c, err := New(Options{
		Paths:    paths,
		Store:    db,
		Adapter:  adapter.NewFake(),
		Profiles: []config.Profile{{Name: "work", Provider: config.ProviderClaude, ConfigDir: "/tmp/claude-work"}},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	// A profile name plus a bare provider token are both valid fallback entries.
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name:     "ok",
		Provider: config.ProviderClaude,
		Fallback: []string{"work", "codex"},
	}); err != nil {
		t.Fatalf("expected valid fallback chain to be accepted, got %v", err)
	}

	// An unknown fallback profile must be rejected at create time, not deferred
	// to a rate-limit event.
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name:     "bad",
		Provider: config.ProviderClaude,
		Fallback: []string{"ghost"},
	}); err == nil {
		t.Fatal("expected unknown fallback profile to be rejected, got nil")
	}

	// The agent's own profile must match its provider.
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{
		Name:     "mismatch",
		Provider: config.ProviderCodex,
		Profile:  "work",
	}); err == nil {
		t.Fatal("expected provider/profile mismatch to be rejected, got nil")
	}
}

func TestReplayHistoryUsesRollingSummaryAndRecentMessages(t *testing.T) {
	history := make([]store.Message, 20)
	for i := range history {
		history[i] = store.Message{Role: store.RoleUser, Content: "message"}
	}
	history[19].Content = "latest"
	sess := store.Session{RollingSummary: "older summary"}

	got := replayHistory(sess, history)
	if len(got) != recentReplayMessages+1 {
		t.Fatalf("expected summary plus recent messages, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "older summary") {
		t.Fatalf("summary message missing rolling summary: %+v", got[0])
	}
	if got[len(got)-1].Content != "latest" {
		t.Fatalf("recent tail not preserved: %+v", got[len(got)-1])
	}
}

func TestAutoNameSessionUsesModelJSON(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	fake := adapter.NewFake()
	fake.Responses = []string{`{"name":"Release Checklist","description":"Coordinate final checks before release."}`}
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "namer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "namer", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.AppendMessages(ctx, session.ID, []store.Message{
		{Role: store.RoleUser, Content: "Prepare the release checklist."},
		{Role: store.RoleAssistant, Content: "I will collect the final checks."},
	}); err != nil {
		t.Fatalf("append messages: %v", err)
	}
	updated, err := c.AutoNameSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("auto name: %v", err)
	}
	if updated.Name != "Release Checklist" || !updated.AutoNamed {
		t.Fatalf("unexpected auto-name result: %+v", updated)
	}
}

func TestDescribeProjectPromptTreatsProjectAsGeneralWork(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	fake := adapter.NewFake()
	fake.Responses = []string{"Coordinate materials, milestones, and inspections for the kitchen renovation."}
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "writer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := c.CreateProject(ctx, projects.Project{
		ID:          "kitchen-renovation",
		Name:        "Kitchen Renovation",
		Description: "Renovate the kitchen.",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := c.DescribeProject(ctx, "kitchen-renovation", "writer"); err != nil {
		t.Fatalf("describe project: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(fake.Requests))
	}
	prompt := fake.Requests[0].Message
	if strings.Contains(prompt, "description for a developer tool") {
		t.Fatalf("prompt still frames the user project as a developer tool:\n%s", prompt)
	}
	for _, want := range []string{
		"project tracked in Podiom",
		"software, writing, planning, research, physical work",
		`The project is titled "Kitchen Renovation".`,
		`Current draft to improve: "Renovate the kitchen."`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestStreamTurnUsesCurrentGlobalPermissionTimeout(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	fake := adapter.NewFake()
	fake.PermissionTool = "Bash"
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	c.SetGlobal(config.Global{
		Provider:          config.ProviderClaude,
		Effort:            "medium",
		PermissionMode:    config.PermissionApprove,
		PermissionTimeout: "5m",
	})
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "operator", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "operator", Origin: store.OriginCLI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	relay := &timeoutRecordingRelay{timeouts: make(chan time.Duration, 1)}
	events, err := c.StreamTurn(ctx, sess.ID, "run a tool", TurnOptions{PermissionRelay: relay})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	for range events {
	}
	if timeout := <-relay.timeouts; timeout != 5*time.Minute {
		t.Fatalf("permission timeout = %v, want 5m", timeout)
	}
}

type timeoutRecordingRelay struct {
	timeouts chan time.Duration
}

func (r *timeoutRecordingRelay) RequestPermission(ctx context.Context, req adapter.PermissionRequest, timeout time.Duration) (adapter.PermissionDecision, error) {
	select {
	case r.timeouts <- timeout:
	case <-ctx.Done():
		return adapter.PermissionDecision{Behavior: "deny"}, ctx.Err()
	}
	return adapter.PermissionDecision{Behavior: "allow"}, nil
}

func newTestCore(t *testing.T) (*Core, func()) {
	t.Helper()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	c, err := New(Options{Paths: paths, Store: db, Adapter: adapter.NewFake()})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	return c, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}
