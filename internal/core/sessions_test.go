package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

// newTestCoreAdapter mirrors newTestCore but exposes the fake adapter so tests
// can inspect the StartRequest delivered to a provider on session start.
func newTestCoreAdapter(t *testing.T) (*Core, *adapter.Fake, func()) {
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
	fake := adapter.NewFake()
	// Disable post-turn background goroutines (auto-name, rolling summary): they
	// keep writing to the store after the test body returns and race t.TempDir's
	// RemoveAll cleanup into a "directory not empty" failure.
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	return c, fake, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func newTestCoreAdapterWithDaemon(t *testing.T) (*Core, *adapter.Fake, func()) {
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
	fake := adapter.NewFake()
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake, DaemonAddr: "127.0.0.1:8787", DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	return c, fake, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func TestUpdatedPermissionAppliesToNextTurnWithoutResettingHandle(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "operator", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "operator", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	originalHandle := session.ProviderHandle

	updated, err := c.UpdateSessionSettings(ctx, session.ID, "", "", config.PermissionYolo)
	if err != nil {
		t.Fatalf("update permission: %v", err)
	}
	if updated.ProviderHandle != originalHandle {
		t.Fatalf("permission update changed provider handle: got %q want %q", updated.ProviderHandle, originalHandle)
	}

	events, err := c.StreamTurn(ctx, session.ID, "continue with full access", TurnOptions{})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	for range events {
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake turn requests = %d, want 1", len(fake.Requests))
	}
	req := fake.Requests[0]
	if req.Settings.PermissionMode != config.PermissionYolo {
		t.Fatalf("next turn permission = %q, want yolo", req.Settings.PermissionMode)
	}
	if req.Handle.ID != originalHandle {
		t.Fatalf("next turn handle = %q, want %q", req.Handle.ID, originalHandle)
	}
	if len(fake.StartRequests) != 1 {
		t.Fatalf("permission update restarted provider: start requests = %d, want 1", len(fake.StartRequests))
	}
}

// startRequestFor returns the StartRequest the fake adapter recorded for the
// given session ID.
func startRequestFor(t *testing.T, fake *adapter.Fake, sessionID string) adapter.StartRequest {
	t.Helper()
	for _, req := range fake.StartRequests {
		if req.SessionID == sessionID {
			return req
		}
	}
	t.Fatalf("adapter received no StartRequest for session %q", sessionID)
	return adapter.StartRequest{}
}

func TestSessionStartIncludesInternalPlanMCP(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapterWithDaemon(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "builder", Provider: config.ProviderCodex})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: agent.Name,
		Origin:    store.OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := startRequestFor(t, fake, session.ID)
	if !hasMCPServer(req.MCPServers, "podiom_plan") {
		t.Fatalf("initial StartRequest missing internal plan MCP server: %+v", req.MCPServers)
	}
	if !hasMCPServer(req.MCPAllServers, "podiom_plan") {
		t.Fatalf("initial StartRequest missing plan MCP in all servers: %+v", req.MCPAllServers)
	}
	var plan *podiommcp.Server
	for i := range req.MCPServers {
		if req.MCPServers[i].Name == "podiom_plan" {
			plan = &req.MCPServers[i]
			break
		}
	}
	if plan == nil {
		t.Fatalf("plan server not found")
	}
	if got := envValue(plan.EnvVars, config.EnvHome); got != c.paths.Home {
		t.Fatalf("plan MCP %s env = %q, want %q", config.EnvHome, got, c.paths.Home)
	}
}

func TestSessionStartIncludesInternalManageMCP(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapterWithDaemon(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "manager", Provider: config.ProviderCodex})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: agent.Name,
		Origin:    store.OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := startRequestFor(t, fake, session.ID)
	if !hasMCPServer(req.MCPServers, "podiom_manage") {
		t.Fatalf("initial StartRequest missing internal manage MCP server: %+v", req.MCPServers)
	}
	if !hasMCPServer(req.MCPAllServers, "podiom_manage") {
		t.Fatalf("initial StartRequest missing manage MCP in all servers: %+v", req.MCPAllServers)
	}
	var manage *podiommcp.Server
	for i := range req.MCPServers {
		if req.MCPServers[i].Name == "podiom_manage" {
			manage = &req.MCPServers[i]
			break
		}
	}
	if manage == nil {
		t.Fatalf("manage server not found")
	}
	args := strings.Join(manage.Args, " ")
	for _, want := range []string{"manage-mcp", "--session " + session.ID, "--agent " + agent.Name} {
		if !strings.Contains(args, want) {
			t.Fatalf("manage MCP args missing %q: %v", want, manage.Args)
		}
	}
	if got := envValue(manage.EnvVars, config.EnvHome); got != c.paths.Home {
		t.Fatalf("manage MCP %s env = %q, want %q", config.EnvHome, got, c.paths.Home)
	}
}

func TestInterviewSessionReceivesOnlyInterviewMCP(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapterWithDaemon(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "interviewer", Provider: config.ProviderCodex})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName:      agent.Name,
		Origin:         store.OriginInterview,
		PermissionMode: config.PermissionYolo,
	})
	if err != nil {
		t.Fatalf("create interview session: %v", err)
	}
	if session.PermissionMode != config.PermissionApprove {
		t.Fatalf("interview permission = %q, want approve", session.PermissionMode)
	}
	req := startRequestFor(t, fake, session.ID)
	if len(req.MCPServers) != 1 || req.MCPServers[0].Name != "podiom_interview" {
		t.Fatalf("interview MCP servers = %+v", req.MCPServers)
	}
	if len(req.MCPAllServers) != 1 || req.MCPAllServers[0].Name != "podiom_interview" {
		t.Fatalf("interview all MCP servers = %+v", req.MCPAllServers)
	}
	if len(req.NativeAgents) != 0 || req.NativeAgentName != "" {
		t.Fatalf("interview should not project native agents: %+v", req.NativeAgents)
	}
	args := strings.Join(req.MCPServers[0].Args, " ")
	for _, want := range []string{"interview-mcp", "--session " + session.ID} {
		if !strings.Contains(args, want) {
			t.Fatalf("interview MCP args missing %q: %v", want, req.MCPServers[0].Args)
		}
	}
}

func envValue(vars podiommcp.EnvVars, name string) string {
	for _, kv := range vars {
		if kv.Name == name {
			return kv.Value
		}
	}
	return ""
}

func TestStreamTurnPersistsErrorAndExcludesItFromReplay(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	c.noBg = true

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "tester", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "tester", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	fake.SendTurnError = errors.New("provider exploded")
	events, err := c.StreamTurn(ctx, session.ID, "please fail", TurnOptions{})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	var got []TurnEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 3 {
		t.Fatalf("expected user, persisted error, control error events, got %+v", got)
	}
	if got[0].Kind != "message_stored" || got[0].Message == nil || got[0].Message.Kind != store.KindMessage {
		t.Fatalf("first event should store user message: %+v", got[0])
	}
	if got[1].Kind != "message_stored" || got[1].Message == nil || got[1].Message.Kind != store.KindError {
		t.Fatalf("second event should store error message: %+v", got[1])
	}
	if got[2].Kind != "error" || got[2].Content != "provider exploded" {
		t.Fatalf("third event should be control error: %+v", got[2])
	}

	history, err := c.History(ctx, session.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || history[0].Kind != store.KindMessage || history[1].Kind != store.KindError {
		t.Fatalf("unexpected persisted history: %+v", history)
	}

	fake.SendTurnError = nil
	fake.Responses = []string{"recovered"}
	if _, err := c.AppendTurn(ctx, session.ID, "try again"); err != nil {
		t.Fatalf("append recovery turn: %v", err)
	}
	last := fake.Requests[len(fake.Requests)-1]
	for _, msg := range last.History {
		if msg.Kind == store.KindError || strings.Contains(msg.Content, "provider exploded") {
			t.Fatalf("error message was replayed to provider: %+v", last.History)
		}
	}
}

func TestPlanMCPProfileStableAcrossSessionStartAndTurn(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapterWithDaemon(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "planner", Provider: config.ProviderCodex})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName:                      agent.Name,
		Origin:                         store.OriginRoadmap,
		CreatePlanBeforeImplementation: true,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	events, err := c.StreamTurn(ctx, session.ID, "create a plan", TurnOptions{PermissionTurnID: "ws-turn-1"})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	for range events {
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake turn requests len = %d", len(fake.Requests))
	}

	startReq := startRequestFor(t, fake, session.ID)
	turnReq := fake.Requests[0]
	startProfile, unavailable := podiommcp.CodexProfile(startReq.MCPServers, startReq.MCPAllServers)
	if len(unavailable) != 0 {
		t.Fatalf("unexpected start unavailable: %+v", unavailable)
	}
	turnProfile, unavailable := podiommcp.CodexProfile(turnReq.Settings.MCPServers, turnReq.Settings.MCPAllServers)
	if len(unavailable) != 0 {
		t.Fatalf("unexpected turn unavailable: %+v", unavailable)
	}
	if startProfile != turnProfile {
		t.Fatalf("plan MCP profile changed between start and turn:\nstart:\n%s\nturn:\n%s", startProfile, turnProfile)
	}
	if strings.Contains(turnProfile, "ws-turn-1") {
		t.Fatalf("provider MCP profile should not include live turn IDs:\n%s", turnProfile)
	}
	if !strings.Contains(turnProfile, session.ID) {
		t.Fatalf("provider MCP profile should include stable session ID:\n%s", turnProfile)
	}
	if turnReq.Settings.PermissionTurnID != "ws-turn-1" {
		t.Fatalf("permission turn id = %q, want ws-turn-1", turnReq.Settings.PermissionTurnID)
	}
}

func hasMCPServer(servers []podiommcp.Server, name string) bool {
	for _, server := range servers {
		if server.Name == name {
			return true
		}
	}
	return false
}

// TestSessionStartSendsAllContextLayers verifies that every context markdown
// layer the composer produces (base AGENTS.md -> agent AGENTS.md -> SOUL.md ->
// MEMORY.md) actually reaches the adapter in the StartRequest.Instructions when
// a session is created. This guards the composition -> adapter.Start() wiring,
// not just the composer output in isolation.
func TestSessionStartSendsAllContextLayers(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
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
	if err := os.WriteFile(paths.Memory, []byte("memory layer\n"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: agent.Name,
		Origin:    store.OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := startRequestFor(t, fake, session.ID)
	instructions := string(req.Instructions)
	if strings.TrimSpace(instructions) == "" {
		t.Fatalf("StartRequest.Instructions was empty")
	}

	// Claude import mode references each source (memory via its truncated
	// snapshot). Every layer must be present so a future dropped layer fails.
	wantRefs := map[string]string{
		"base AGENTS.md":  c.paths.BaseAgents,
		"agent AGENTS.md": paths.Agents,
		"SOUL.md":         paths.Soul,
		"MEMORY.md":       ".podiom-memory.md",
	}
	for label, ref := range wantRefs {
		if !strings.Contains(instructions, ref) {
			t.Fatalf("instructions delivered to adapter missing %s (%q):\n%s", label, ref, instructions)
		}
	}

	// The generated CLAUDE.md payload is written into the agent workspace.
	if _, err := os.Stat(paths.Workspace + "/CLAUDE.md"); err != nil {
		t.Fatalf("workspace CLAUDE.md not written: %v", err)
	}
}

// TestSessionStartOmitsAbsentOptionalLayers verifies the optional layers (agent
// AGENTS.md and MEMORY.md) are skipped when absent/empty, while the required
// base + SOUL.md always reach the adapter.
func TestSessionStartOmitsAbsentOptionalLayers(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()

	agent, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "minimal", Provider: config.ProviderClaude})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	paths := c.AgentPaths(agent.Name)
	// SOUL.md is scaffolded on create; leave per-agent AGENTS.md absent and
	// MEMORY.md empty (scaffolded empty) so both optional layers are skipped.

	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: agent.Name,
		Origin:    store.OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := startRequestFor(t, fake, session.ID)
	instructions := string(req.Instructions)

	if !strings.Contains(instructions, c.paths.BaseAgents) {
		t.Fatalf("instructions missing base AGENTS.md:\n%s", instructions)
	}
	if !strings.Contains(instructions, paths.Soul) {
		t.Fatalf("instructions missing SOUL.md:\n%s", instructions)
	}
	if strings.Contains(instructions, paths.Agents) {
		t.Fatalf("absent per-agent AGENTS.md should not be referenced:\n%s", instructions)
	}
	if strings.Contains(instructions, ".podiom-memory.md") {
		t.Fatalf("empty MEMORY.md should not be referenced:\n%s", instructions)
	}
}

func TestSessionStartAndTurnSendNativeAgentProjection(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	c.noBg = true

	lead, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "Lead", Provider: config.ProviderClaude})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "Helper", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create helper: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: lead.Name, Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	startReq := startRequestFor(t, fake, session.ID)
	if startReq.NativeAgentName == "" {
		t.Fatalf("start request missing active native agent")
	}
	if len(startReq.NativeAgents) != 2 {
		t.Fatalf("start request native agent count = %d, want 2", len(startReq.NativeAgents))
	}

	if _, err := c.AppendTurn(ctx, session.ID, "hello"); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(fake.Requests) != 1 {
		t.Fatalf("fake turn requests = %d, want 1", len(fake.Requests))
	}
	turnReq := fake.Requests[0]
	if turnReq.Settings.NativeAgentName != startReq.NativeAgentName {
		t.Fatalf("turn native agent = %q, want start native agent %q", turnReq.Settings.NativeAgentName, startReq.NativeAgentName)
	}
	if len(turnReq.Settings.NativeAgents) != 2 {
		t.Fatalf("turn native agent count = %d, want 2", len(turnReq.Settings.NativeAgents))
	}
}
