package server

import (
	"context"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

// A turn that narrates, works, then answers must reach the browser as several
// stored rows in order, with the working notes arriving *before* the answer —
// that ordering is what lets the client close one bubble and open the next.
func TestWebSocketStreamsWorkingNotesBeforeTheAnswer(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.Script = []adapter.Event{
		{Kind: adapter.EventAssistantMessage, Content: "checking the auth module"},
		{Kind: adapter.EventToolUse, ToolUse: &adapter.ToolUse{Name: "Read"}},
		{Kind: adapter.EventReasoningMessage, Content: "the timer sits at module level"},
		{Kind: adapter.EventToolUse, ToolUse: &adapter.ToolUse{Name: "Edit"}},
		{Kind: adapter.EventAssistantMessage, Content: "moved the refresh into authStore"},
	}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type:      "send_turn",
		RequestID: "req-1",
		AgentName: "webber",
		Message:   "refactor auth",
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}

	var sessionID string
	var assistantRows []store.Message
	for i := 0; i < 24; i++ {
		var msg ServerMessage
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := wsjson.Read(readCtx, conn, &msg)
		cancel()
		if err != nil {
			t.Fatalf("read ws: %v", err)
		}
		if msg.Type == "session" && msg.Session != nil {
			sessionID = msg.Session.ID
		}
		if msg.Type == "message" && msg.Message != nil && msg.Message.Role == store.RoleAssistant {
			assistantRows = append(assistantRows, *msg.Message)
		}
		if msg.Type == "done" {
			break
		}
	}
	if sessionID == "" {
		t.Fatal("session id was never announced")
	}

	want := []struct {
		kind    store.MessageKind
		content string
	}{
		{store.KindNarration, "checking the auth module"},
		{store.KindReasoning, "the timer sits at module level"},
		{store.KindMessage, "moved the refresh into authStore"},
	}
	if len(assistantRows) != len(want) {
		t.Fatalf("assistant rows over the socket = %d, want %d: %+v", len(assistantRows), len(want), assistantRows)
	}
	for i, w := range want {
		if assistantRows[i].Kind != w.kind || assistantRows[i].Content != w.content {
			t.Fatalf("row[%d] = (%q, %q), want (%q, %q)", i, assistantRows[i].Kind, assistantRows[i].Content, w.kind, w.content)
		}
	}

	history, err := coreSvc.History(ctx, sessionID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history = %d rows, want user + 3 assistant: %+v", len(history), history)
	}
	for i, w := range want {
		if history[i+1].Kind != w.kind || history[i+1].Content != w.content {
			t.Fatalf("history[%d] = (%q, %q), want (%q, %q)", i+1, history[i+1].Kind, history[i+1].Content, w.kind, w.content)
		}
	}
}
