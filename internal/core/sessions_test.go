package core

import (
	"context"
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
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake})
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
	c, err := New(Options{Paths: paths, Store: db, Adapter: fake, DaemonAddr: "127.0.0.1:8787"})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	return c, fake, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
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
