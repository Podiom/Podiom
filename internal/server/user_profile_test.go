package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestUserProfileGetPutDeleteRoundTrip(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	// GET before any profile exists.
	getReq := httptest.NewRequest(http.MethodGet, "/api/user-profile", nil)
	getRR := httptest.NewRecorder()
	srv.handleUserProfile(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", getRR.Code, getRR.Body.String())
	}
	var info userProfileInfo
	if err := json.Unmarshal(getRR.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode profile info: %v", err)
	}
	if info.Exists || info.Profile != "" {
		t.Fatalf("fresh profile info should be empty: %+v", info)
	}

	// PUT stores a cleaned profile.
	putReq := httptest.NewRequest(http.MethodPut, "/api/user-profile",
		bytes.NewBufferString(`{"profile":"# About the user\n\n- likes brevity\n"}`))
	putRR := httptest.NewRecorder()
	srv.handleUserProfile(putRR, putReq)
	if putRR.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", putRR.Code, putRR.Body.String())
	}
	if err := json.Unmarshal(putRR.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode PUT reply: %v", err)
	}
	if !info.Exists || info.Profile != "# About the user\n\n- likes brevity\n" {
		t.Fatalf("unexpected PUT reply: %+v", info)
	}
	stored, err := srv.core.ReadUserProfile()
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if stored != "# About the user\n\n- likes brevity\n" {
		t.Fatalf("stored profile = %q", stored)
	}

	// A blank PUT is rejected instead of saving a heading-only profile.
	blankReq := httptest.NewRequest(http.MethodPut, "/api/user-profile",
		bytes.NewBufferString(`{"profile":"   "}`))
	blankRR := httptest.NewRecorder()
	srv.handleUserProfile(blankRR, blankReq)
	if blankRR.Code != http.StatusBadRequest {
		t.Fatalf("blank PUT status = %d, want 400; body=%s", blankRR.Code, blankRR.Body.String())
	}

	// DELETE removes it.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/user-profile", nil)
	delRR := httptest.NewRecorder()
	srv.handleUserProfile(delRR, delReq)
	if delRR.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", delRR.Code, delRR.Body.String())
	}
	if cleared, _ := srv.core.ReadUserProfile(); cleared != "" {
		t.Fatalf("profile should be cleared, got %q", cleared)
	}
}

func TestWebSocketStartInterview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.Responses = []string{"# About the user\n\ndraft profile"}
	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "greeter", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type:      "start_interview",
		RequestID: "req-interview",
		AgentName: "greeter",
		Model:     "opus",
		Effort:    "high",
	}); err != nil {
		t.Fatalf("write start_interview: %v", err)
	}

	var sessionID string
	var sawAssistant, sawDone bool
	for i := 0; i < 15 && !(sessionID != "" && sawAssistant && sawDone); i++ {
		var msg ServerMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read ws: %v", err)
		}
		switch msg.Type {
		case "session":
			if msg.Session == nil {
				t.Fatalf("session message without session")
			}
			if msg.Session.Origin != store.OriginOnboarding {
				t.Fatalf("interview session origin = %q, want %q", msg.Session.Origin, store.OriginOnboarding)
			}
			if msg.Session.PermissionMode != config.PermissionApprove {
				t.Fatalf("interview permission mode = %q, want approve", msg.Session.PermissionMode)
			}
			sessionID = msg.Session.ID
		case "assistant":
			sawAssistant = msg.Delta == "# About the user\n\ndraft profile"
		case "done":
			sawDone = true
		}
	}
	if sessionID == "" || !sawAssistant || !sawDone {
		t.Fatalf("missing events: session=%q assistant=%v done=%v", sessionID, sawAssistant, sawDone)
	}

	// The opening turn carries the interview prompt as its user message.
	history, err := coreSvc.History(ctx, sessionID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var sawPrompt bool
	for _, m := range history {
		if m.Role == "user" && bytes.Contains([]byte(m.Content), []byte("USER.md")) {
			sawPrompt = true
		}
	}
	if !sawPrompt {
		t.Fatalf("interview prompt not found in session history")
	}
}
