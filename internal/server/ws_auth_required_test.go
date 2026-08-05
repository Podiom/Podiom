package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

// A turn that dies signed out must reach the browser as a structured
// auth_required message naming the exact account, so the chat can offer a
// sign-in button. Before this, the provider's raw "Not logged in · Please run
// /login" arrived as an assistant bubble — advice the user could only act on in
// a terminal they may not have.
func TestWebSocketSurfacesAuthRequiredInsteadOfRawError(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.Script = []adapter.Event{
		{Kind: adapter.EventAuthRequired, Content: "Not logged in · Please run /login"},
	}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "locked", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type:      "send_turn",
		RequestID: "req-1",
		AgentName: "locked",
		Message:   "go",
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}

	var sessionID string
	var auth *core.AuthRequired
	var assistantRows []store.Message
	for i := 0; i < 24; i++ {
		var msg ServerMessage
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := wsjson.Read(readCtx, conn, &msg)
		cancel()
		if err != nil {
			t.Fatalf("read ws: %v", err)
		}
		switch {
		case msg.Type == "session" && msg.Session != nil:
			sessionID = msg.Session.ID
		case msg.Type == "auth_required":
			auth = msg.AuthRequired
		case msg.Type == "message" && msg.Message != nil && msg.Message.Role == store.RoleAssistant:
			assistantRows = append(assistantRows, *msg.Message)
		}
		if msg.Type == "done" {
			break
		}
	}

	if auth == nil {
		t.Fatal("no auth_required message reached the browser")
	}
	if auth.Provider != config.ProviderClaude || auth.Profile != "" {
		t.Fatalf("auth target = %+v, want claude with the default profile", auth)
	}
	if auth.Message != "Not logged in · Please run /login" {
		t.Fatalf("message = %q, want the provider's own wording", auth.Message)
	}
	// The sign-in instruction must not also arrive as conversation.
	for _, row := range assistantRows {
		if strings.Contains(row.Content, "/login") {
			t.Fatalf("sign-in text leaked into the transcript: %+v", row)
		}
	}
	if sessionID == "" {
		t.Fatal("session id was never announced")
	}

	// The turn is over, so the card lives only on the live connection. History
	// carries the explanation instead — in Podiom's words, so a reload still
	// says what happened without repeating the provider's terminal advice.
	history, err := coreSvc.History(ctx, sessionID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var explained bool
	for _, row := range history {
		if row.Kind == store.KindError && strings.Contains(row.Content, "signed out") {
			explained = true
		}
	}
	if !explained {
		t.Fatalf("history has no error row explaining the signed-out turn: %+v", history)
	}
}
