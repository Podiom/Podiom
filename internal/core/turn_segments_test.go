package core

import (
	"context"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// segmentFixture is a session ready to have adapter events fed through
// consumeAdapterEvents directly. Driving the consumer rather than a whole turn is
// what makes the *mid-turn* writes observable: a turn only returns once its
// channel closes, by which point the ordering under test is already history.
type segmentFixture struct {
	core      *Core
	sessionID string
}

func newSegmentFixture(t *testing.T) (segmentFixture, func()) {
	t.Helper()
	ctx := context.Background()
	c, _, cleanup := newTestCoreAdapter(t)
	c.noBg = true
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "seg", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "seg", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return segmentFixture{core: c, sessionID: session.ID}, cleanup
}

// consume feeds events through consumeAdapterEvents, draining the outbound turn
// stream concurrently the way StreamTurn's consumer does, and returns the turn's
// unflushed tail plus every message the consumer persisted along the way.
func (f segmentFixture) consume(t *testing.T, events ...adapter.Event) (turnOutput, []store.Message) {
	t.Helper()
	ctx := context.Background()
	in := make(chan adapter.Event, len(events))
	for _, event := range events {
		in <- event
	}
	close(in)

	out := make(chan TurnEvent, 64)
	var stored []store.Message
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for event := range out {
			if event.Kind == "message_stored" && event.Message != nil {
				stored = append(stored, *event.Message)
			}
		}
	}()
	output, rateLimited, ok := f.core.consumeAdapterEvents(ctx, out, f.sessionID, "", "", config.ProviderClaude, "", "", in)
	close(out)
	<-drained
	if rateLimited || !ok {
		t.Fatalf("consume ended early: rateLimited=%v ok=%v", rateLimited, ok)
	}
	return output, stored
}

// history is the session's persisted rows, which must agree with the events the
// consumer streamed.
func (f segmentFixture) history(t *testing.T) []store.Message {
	t.Helper()
	msgs, err := f.core.History(context.Background(), f.sessionID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	return msgs
}

func assistantMessage(text string) adapter.Event {
	return adapter.Event{Kind: adapter.EventAssistantMessage, Content: text}
}

func reasoningMessage(text string) adapter.Event {
	return adapter.Event{Kind: adapter.EventReasoningMessage, Content: text}
}

func toolUseEvent(name string) adapter.Event {
	return adapter.Event{Kind: adapter.EventToolUse, ToolUse: &adapter.ToolUse{Name: name}}
}

// A tool call splits the agent's prose into its own row, so what it said while
// working stops being glued onto the answer.
func TestConsumeAdapterEventsSplitsAtToolBoundary(t *testing.T) {
	f, cleanup := newSegmentFixture(t)
	defer cleanup()

	output, stored := f.consume(t,
		adapter.Event{Kind: adapter.EventAssistantDelta, Content: "let me "},
		adapter.Event{Kind: adapter.EventAssistantDelta, Content: "check the config"},
		assistantMessage("let me check the config"),
		toolUseEvent("Read"),
		reasoningMessage("the timer is module level"),
		assistantMessage("found it — moving the timer"),
		toolUseEvent("Edit"),
		assistantMessage("done, tests pass"),
	)

	if len(stored) != 3 {
		t.Fatalf("mid-turn rows = %d, want 3: %+v", len(stored), stored)
	}
	wantStored := []struct {
		kind    store.MessageKind
		content string
	}{
		{store.KindNarration, "let me check the config"},
		{store.KindReasoning, "the timer is module level"},
		{store.KindNarration, "found it — moving the timer"},
	}
	for i, want := range wantStored {
		if stored[i].Kind != want.kind || stored[i].Content != want.content {
			t.Fatalf("stored[%d] = (%q, %q), want (%q, %q)", i, stored[i].Kind, stored[i].Content, want.kind, want.content)
		}
		if stored[i].Role != store.RoleAssistant {
			t.Fatalf("stored[%d] role = %q, want assistant", i, stored[i].Role)
		}
	}
	// Rows land in arrival order, which is also seq order.
	for i := 1; i < len(stored); i++ {
		if stored[i].Seq <= stored[i-1].Seq {
			t.Fatalf("seq not increasing: %d then %d", stored[i-1].Seq, stored[i].Seq)
		}
	}
	if output.assistant != "done, tests pass" {
		t.Fatalf("tail assistant = %q, want the final answer", output.assistant)
	}
	if output.reasoning != "" {
		t.Fatalf("tail reasoning = %q, want empty (it was flushed at the tool call)", output.reasoning)
	}
	if !output.flushed {
		t.Fatal("flushed = false, want true")
	}
	if got := len(f.history(t)); got != 3 {
		t.Fatalf("persisted rows = %d, want 3 (the tail is StreamTurn's job)", got)
	}
}

// Claude's terminal `result` line repeats the last assistant block. When a tool
// call already closed that block, recording the repeat would duplicate the row.
func TestConsumeAdapterEventsDropsRepeatedTerminalAssistantMessage(t *testing.T) {
	f, cleanup := newSegmentFixture(t)
	defer cleanup()

	output, stored := f.consume(t,
		assistantMessage("here is the summary"),
		toolUseEvent("Bash"),
		assistantMessage("here is the summary"),
	)

	if len(stored) != 0 {
		t.Fatalf("mid-turn rows = %d, want 0 — the repeat is the same text: %+v", len(stored), stored)
	}
	if output.assistant != "here is the summary" {
		t.Fatalf("tail assistant = %q, want the single answer", output.assistant)
	}
	if output.flushed {
		t.Fatal("flushed = true, want false — nothing should have been written")
	}
	if got := len(f.history(t)); got != 0 {
		t.Fatalf("persisted rows = %d, want 0", got)
	}
}

// Reasoning persists the moment a tool call closes it: it can never be the
// turn's answer, so there is nothing to wait for.
func TestConsumeAdapterEventsFlushesReasoningAtToolBoundary(t *testing.T) {
	f, cleanup := newSegmentFixture(t)
	defer cleanup()

	output, stored := f.consume(t,
		reasoningMessage("first I should read it"),
		toolUseEvent("Read"),
		reasoningMessage("now I can edit"),
	)

	if len(stored) != 1 {
		t.Fatalf("mid-turn rows = %d, want 1: %+v", len(stored), stored)
	}
	if stored[0].Kind != store.KindReasoning || stored[0].Content != "first I should read it" {
		t.Fatalf("stored[0] = (%q, %q)", stored[0].Kind, stored[0].Content)
	}
	if output.reasoning != "now I can edit" {
		t.Fatalf("tail reasoning = %q", output.reasoning)
	}
	if output.assistant != "" {
		t.Fatalf("tail assistant = %q, want empty", output.assistant)
	}
}

// Deltas that arrive after a tool call start a fresh row rather than appending
// to the note the tool call ended.
func TestConsumeAdapterEventsStartsNewSegmentForDeltasAfterToolUse(t *testing.T) {
	f, cleanup := newSegmentFixture(t)
	defer cleanup()

	output, stored := f.consume(t,
		adapter.Event{Kind: adapter.EventAssistantDelta, Content: "looking now"},
		toolUseEvent("Grep"),
		adapter.Event{Kind: adapter.EventAssistantDelta, Content: "all set"},
	)

	if len(stored) != 1 || stored[0].Content != "looking now" || stored[0].Kind != store.KindNarration {
		t.Fatalf("stored = %+v, want one narration row \"looking now\"", stored)
	}
	if output.assistant != "all set" {
		t.Fatalf("tail assistant = %q, want %q", output.assistant, "all set")
	}
}

// A turn with no tool call behaves exactly as before: one reasoning row and one
// answer, both left for StreamTurn to write in a single transaction.
func TestConsumeAdapterEventsWithoutToolUseKeepsSingleTail(t *testing.T) {
	f, cleanup := newSegmentFixture(t)
	defer cleanup()

	output, stored := f.consume(t,
		reasoningMessage("thinking"),
		adapter.Event{Kind: adapter.EventAssistantDelta, Content: "part one "},
		adapter.Event{Kind: adapter.EventAssistantDelta, Content: "part two"},
		assistantMessage("part one part two"),
	)

	if len(stored) != 0 {
		t.Fatalf("mid-turn rows = %d, want 0: %+v", len(stored), stored)
	}
	if output.reasoning != "thinking" || output.assistant != "part one part two" {
		t.Fatalf("tail = (%q, %q)", output.reasoning, output.assistant)
	}
	if output.flushed {
		t.Fatal("flushed = true, want false")
	}
}

// A turn whose entire output was flushed mid-stream must still finish: it has an
// empty tail, which must not be mistaken for a turn that produced nothing.
func TestStreamTurnFinishesWhenAllOutputWasFlushed(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	c.noBg = true

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "flusher", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "flusher", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Reasoning, then a tool call that flushes it, and nothing after.
	fake.Script = []adapter.Event{
		reasoningMessage("only a working note"),
		toolUseEvent("Read"),
	}
	events, err := c.StreamTurn(ctx, session.ID, "go", TurnOptions{})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	var kinds []adapter.EventKind
	var stored []store.Message
	for event := range events {
		kinds = append(kinds, event.Kind)
		if event.Kind == "message_stored" && event.Message != nil {
			stored = append(stored, *event.Message)
		}
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != adapter.EventTurnDone {
		t.Fatalf("turn did not end with turn_done: %v", kinds)
	}
	if len(stored) != 2 {
		t.Fatalf("stored rows = %d, want user + reasoning: %+v", len(stored), stored)
	}
	if stored[1].Kind != store.KindReasoning || stored[1].Content != "only a working note" {
		t.Fatalf("stored[1] = (%q, %q)", stored[1].Kind, stored[1].Content)
	}
}

// End to end: a scripted provider turn becomes user + notes + one answer, in
// order, with only the last assistant row carrying the answer kind.
func TestAppendTurnSplitsNarrationFromFinalAnswer(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	c.noBg = true

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "worker", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "worker", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	fake.Script = []adapter.Event{
		assistantMessage("checking the auth module"),
		toolUseEvent("Read"),
		reasoningMessage("the timer sits at module level"),
		toolUseEvent("Edit"),
		assistantMessage("moved the refresh into authStore"),
	}
	written, err := c.AppendTurn(ctx, session.ID, "refactor auth")
	if err != nil {
		t.Fatalf("append turn: %v", err)
	}
	want := []struct {
		role    store.MessageRole
		kind    store.MessageKind
		content string
	}{
		{store.RoleUser, store.KindMessage, "refactor auth"},
		{store.RoleAssistant, store.KindNarration, "checking the auth module"},
		{store.RoleAssistant, store.KindReasoning, "the timer sits at module level"},
		{store.RoleAssistant, store.KindMessage, "moved the refresh into authStore"},
	}
	if len(written) != len(want) {
		t.Fatalf("wrote %d messages, want %d: %+v", len(written), len(want), written)
	}
	for i, w := range want {
		if written[i].Role != w.role || written[i].Kind != w.kind || written[i].Content != w.content {
			t.Fatalf("written[%d] = (%q, %q, %q), want (%q, %q, %q)",
				i, written[i].Role, written[i].Kind, written[i].Content, w.role, w.kind, w.content)
		}
	}

	// Working notes are the agent's scratch work, not conversation: a follow-up
	// turn must not replay them back to the provider.
	fake.Script = nil
	fake.Responses = []string{"next answer"}
	if _, err := c.AppendTurn(ctx, session.ID, "continue"); err != nil {
		t.Fatalf("append second turn: %v", err)
	}
	last := fake.Requests[len(fake.Requests)-1]
	for _, msg := range last.History {
		if msg.Kind == store.KindNarration || msg.Kind == store.KindReasoning {
			t.Fatalf("working note replayed to provider: %+v", last.History)
		}
	}
}

// Only conversation replays to a provider. The kinds that render as working
// notes or diagnostics are Podiom's own history and must stay out of it.
func TestConversationMessagesExcludesNonConversationKinds(t *testing.T) {
	history := []store.Message{
		{Role: store.RoleUser, Content: "ask"},
		{Role: store.RoleAssistant, Kind: store.KindNarration, Content: "note"},
		{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "thought"},
		{Role: store.RoleAssistant, Kind: store.KindError, Content: "boom"},
		{Role: store.RoleAssistant, Kind: store.KindMessage, Content: "answer"},
		{Role: store.RoleAssistant, Content: "answer with default kind"},
	}
	got := conversationMessages(history)
	want := []string{"ask", "answer", "answer with default kind"}
	if len(got) != len(want) {
		t.Fatalf("kept %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Fatalf("kept[%d] = %q, want %q", i, got[i].Content, w)
		}
	}
}

// The fake's default script (reasoning, tool calls, then one response) is the
// shape most existing tests rely on: a tool boundary alone must not invent rows.
func TestAppendTurnWithoutInterimProseKeepsTwoAssistantRows(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	c.noBg = true

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "plain", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "plain", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	fake.Reasoning = []string{"private chain"}
	fake.ToolUses = []adapter.ToolUse{{Name: "Read"}, {Name: "Bash"}}
	fake.Responses = []string{"visible answer"}
	written, err := c.AppendTurn(ctx, session.ID, "hello")
	if err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("wrote %d messages, want user + reasoning + answer: %+v", len(written), written)
	}
	if written[1].Kind != store.KindReasoning || written[1].Content != "private chain" {
		t.Fatalf("written[1] = (%q, %q)", written[1].Kind, written[1].Content)
	}
	if written[2].Kind != store.KindMessage || written[2].Content != "visible answer" {
		t.Fatalf("written[2] = (%q, %q)", written[2].Kind, written[2].Content)
	}
}

// A signed-out provider must reach the client as a structured auth event that
// names the exact account, and must not land in history: the provider's "run
// /login" wording is an instruction to the operator, and replaying it into
// later turns would only confuse the model.
func TestStreamTurnSurfacesAuthRequiredWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	c, fake, cleanup := newTestCoreAdapter(t)
	defer cleanup()
	c.noBg = true

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "locked", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "locked", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	fake.Script = []adapter.Event{
		{Kind: adapter.EventAuthRequired, Content: "Not logged in · Please run /login"},
	}
	events, err := c.StreamTurn(ctx, session.ID, "go", TurnOptions{})
	if err != nil {
		t.Fatalf("stream turn: %v", err)
	}
	var auth *AuthRequired
	for event := range events {
		if event.Kind == adapter.EventAuthRequired {
			auth = event.AuthRequired
		}
	}
	if auth == nil {
		t.Fatal("no auth_required event reached the client")
	}
	if auth.Provider != config.ProviderClaude || auth.Profile != "" {
		t.Fatalf("auth target = %+v, want claude with the default profile", auth)
	}
	if auth.Message != "Not logged in · Please run /login" {
		t.Fatalf("message = %q, want the provider's own wording", auth.Message)
	}

	// The turn ends here, so history must still explain the dead turn after a
	// reload — but in Podiom's words, pointing at the in-app fix rather than
	// the provider's "run /login".
	history, err := c.History(ctx, session.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var explained bool
	for _, msg := range history {
		if strings.Contains(msg.Content, "/login") {
			t.Fatalf("the provider's terminal instruction leaked into history: %+v", msg)
		}
		if msg.Kind == store.KindError && strings.Contains(msg.Content, "signed out") {
			explained = true
		}
	}
	if !explained {
		t.Fatalf("history has no error row explaining the signed-out turn: %+v", history)
	}
}
