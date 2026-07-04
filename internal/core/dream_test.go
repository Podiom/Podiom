package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// dreamTestCore builds a core wired to a fake adapter the test can script and
// inspect, plus a helper to seed dreamable sessions.
func dreamTestCore(t *testing.T) (*Core, *adapter.Fake, func()) {
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

// seedExchange creates a session for the agent with a real user+assistant
// exchange, making it dreamable.
func seedExchange(t *testing.T, c *Core, agent, name, userMsg, assistantMsg string) store.Session {
	t.Helper()
	ctx := context.Background()
	sess, err := c.store.CreateSession(ctx, store.Session{
		AgentName: agent, Name: name, Provider: config.ProviderClaude, PermissionMode: config.PermissionApprove, Origin: store.OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := c.store.AppendMessages(ctx, sess.ID, []store.Message{
		{Role: store.RoleUser, Content: userMsg},
		{Role: store.RoleAssistant, Content: assistantMsg},
	}); err != nil {
		t.Fatalf("append messages: %v", err)
	}
	return sess
}

func TestDreamConsolidatesMemoryAndMarksSessions(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := dreamTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := c.WriteAgentMemory("jared", "# Memory — Jared\n\n## What we've settled\n- Conventional commits.\n"); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	s1 := seedExchange(t, c, "jared", "Auth migration", "Move auth to the new session store.", "On it — I'll flag schema drift.")
	s2 := seedExchange(t, c, "jared", "Flaky test", "Stabilise the e2e suite.", "Patched the login fixture.")

	fake.Responses = []string{`{"memory":"# Memory — Jared\n\n## What we've settled\n- Conventional commits.\n- Tests green before done.","note":"The PR-note habit stayed with me.","kept":1,"merged":1,"pruned":0,"new_items":[{"section":"What we've settled","text":"Tests green before done."}]}`}

	res, err := c.DreamAgent(ctx, "jared", DreamOptions{Trigger: store.DreamManual})
	if err != nil {
		t.Fatalf("dream: %v", err)
	}
	if res.NoOp {
		t.Fatal("expected a real dream, got no-op")
	}
	if res.Dream.Status != store.DreamSuccess {
		t.Fatalf("expected success, got %q (%s)", res.Dream.Status, res.Dream.Error)
	}
	if res.Dream.SessionCount != 2 || res.Dream.Kept != 1 || res.Dream.Merged != 1 {
		t.Fatalf("unexpected journal stats: %+v", res.Dream)
	}
	if len(res.Dream.NewItems) != 1 || res.Dream.NewItems[0].Text != "Tests green before done." {
		t.Fatalf("unexpected new items: %+v", res.Dream.NewItems)
	}

	mem, err := c.ReadAgentMemory("jared")
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	if !strings.Contains(mem, "Tests green before done.") {
		t.Fatalf("memory not updated:\n%s", mem)
	}

	// The prompt must carry the current memory (as authoritative base) and the
	// session transcripts.
	if len(fake.Requests) == 0 {
		t.Fatal("no turn requests recorded")
	}
	prompt := fake.Requests[len(fake.Requests)-1].Message
	for _, want := range []string{"Conventional commits.", "Move auth to the new session store.", "Patched the login fixture.", "authoritative"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if eff := fake.Requests[len(fake.Requests)-1].Settings.Effort; eff != "low" {
		t.Fatalf("dream should run at low effort, got %q", eff)
	}

	// Both sessions are now marked dreamed, so a second dream is a no-op.
	for _, id := range []string{s1.ID, s2.ID} {
		got, err := c.store.GetSession(ctx, id)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if got.DreamedAt == "" {
			t.Fatalf("session %q not marked dreamed", id)
		}
	}
	res2, err := c.DreamAgent(ctx, "jared", DreamOptions{Trigger: store.DreamManual})
	if err != nil {
		t.Fatalf("second dream: %v", err)
	}
	if !res2.NoOp {
		t.Fatal("expected second dream to be a no-op")
	}
}

func TestDreamNoOpWhenNothingToDream(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := dreamTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "quiet", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := c.WriteAgentMemory("quiet", "# Memory\n\n- keep me\n"); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	res, err := c.DreamAgent(ctx, "quiet", DreamOptions{Trigger: store.DreamNightly})
	if err != nil {
		t.Fatalf("dream: %v", err)
	}
	if !res.NoOp {
		t.Fatal("expected no-op with no sessions")
	}
	// No dream row is written for a no-op.
	dreams, err := c.store.ListDreams(ctx, "quiet", 0)
	if err != nil {
		t.Fatalf("list dreams: %v", err)
	}
	if len(dreams) != 0 {
		t.Fatalf("expected no dream rows, got %d", len(dreams))
	}
	// Memory unchanged.
	mem, _ := c.ReadAgentMemory("quiet")
	if !strings.Contains(mem, "keep me") {
		t.Fatalf("memory should be untouched:\n%s", mem)
	}
}

func TestDreamUserOnlySessionNotDreamable(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := dreamTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "solo", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.store.CreateSession(ctx, store.Session{
		AgentName: "solo", Provider: config.ProviderClaude, PermissionMode: config.PermissionApprove, Origin: store.OriginWeb,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := c.store.AppendMessages(ctx, sess.ID, []store.Message{{Role: store.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	n, err := c.store.CountUndreamedSessions(ctx, "solo")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("a user-only session should not be dreamable, got %d", n)
	}
}

func TestDreamFailureLeavesMemoryAndSessionsUntouched(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := dreamTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "risky", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	const base = "# Memory — Risky\n\n## Keep\n- do not lose this\n"
	if err := c.WriteAgentMemory("risky", base); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	sess := seedExchange(t, c, "risky", "S", "user", "assistant")

	// Garbage (non-JSON) reply → parse failure.
	fake.Responses = []string{"not json at all"}

	res, err := c.DreamAgent(ctx, "risky", DreamOptions{Trigger: store.DreamManual})
	if err == nil {
		t.Fatal("expected an error from a garbage reply")
	}
	if res.Dream.Status != store.DreamErrored {
		t.Fatalf("expected errored dream row, got %q", res.Dream.Status)
	}
	// Memory must be exactly the base — untouched.
	mem, _ := c.ReadAgentMemory("risky")
	if mem != base {
		t.Fatalf("memory should be untouched on failure:\n%s", mem)
	}
	// Session must remain un-dreamed for retry.
	got, _ := c.store.GetSession(ctx, sess.ID)
	if got.DreamedAt != "" {
		t.Fatal("session must not be marked dreamed after a failed dream")
	}
}

func TestDreamRateLimitAborts(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := dreamTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "limited", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := c.WriteAgentMemory("limited", "# Memory\n\n- base\n"); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	sess := seedExchange(t, c, "limited", "S", "user", "assistant")
	fake.RateLimitedTurns = 1

	_, err := c.DreamAgent(ctx, "limited", DreamOptions{Trigger: store.DreamNightly})
	if err == nil {
		t.Fatal("expected an error when rate-limited")
	}
	got, _ := c.store.GetSession(ctx, sess.ID)
	if got.DreamedAt != "" {
		t.Fatal("rate-limited dream must not mark sessions dreamed")
	}
}

func TestDreamDueMatrix(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := dreamTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "due", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Use an absolute past timestamp; near midnight, formatting "now - 2h" as
	// HH:MM can resolve to a time later today instead of a time already passed.
	dueAt := time.Now().Add(-2 * time.Hour)

	// No sessions yet → not due even though the time has passed.
	if c.dreamDue(ctx, "due", dueAt) {
		t.Fatal("should not be due with no pending sessions")
	}

	seedExchange(t, c, "due", "S", "user", "assistant")
	if !c.dreamDue(ctx, "due", dueAt) {
		t.Fatal("should be due: time passed and a session is pending")
	}

	// A dream time in the future → not due yet. Use an absolute future timestamp
	// rather than formatting through todaysDreamTime; near midnight, "now + 2h"
	// can become an HH:MM that already passed earlier today.
	futureAt := time.Now().Add(2 * time.Hour)
	if c.dreamDue(ctx, "due", futureAt) {
		t.Fatal("should not be due before the dream time")
	}
}
