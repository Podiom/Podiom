package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
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
