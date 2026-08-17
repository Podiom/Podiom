package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
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
			if msg.Session.Origin != store.OriginInterview {
				t.Fatalf("interview session origin = %q, want %q", msg.Session.Origin, store.OriginInterview)
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
	// Assistant Markdown is never accepted as an interview draft. Reattaching
	// after the premature turn asks the server for one controlled recovery.
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "attach_session", SessionID: sessionID}); err != nil {
		t.Fatalf("reattach interview: %v", err)
	}
	state := readWSTestUntil(t, conn, "interview recovery state", func(msg ServerMessage) bool {
		return msg.Type == "interview_state" && msg.Interview != nil
	})
	if state.Interview.Status == "draft" || state.Interview.Draft != "" {
		t.Fatalf("plain assistant Markdown became a draft: %+v", state.Interview)
	}
	// The recovery state is written before its turn goroutine starts. Wait until
	// that turn is registered before stopping it, then reattach until the hub no
	// longer reports it as active. Otherwise TempDir cleanup can race a late
	// recovery turn that is still writing session files.
	readWSTestUntil(t, conn, "interview recovery turn", func(msg ServerMessage) bool {
		return msg.Type == "turn_state" && msg.SessionID == sessionID && msg.TurnState != nil && msg.TurnState.Status == turnStatusRunning
	})
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "stop_turn", SessionID: sessionID}); err != nil {
		t.Fatalf("stop interview recovery: %v", err)
	}
	// attach_session only re-runs interview recovery once the hub has stopped
	// reporting the turn as active; while it is still attached it just echoes the
	// current "recovering" state. So poll until the stop has actually landed
	// rather than for a fixed number of round-trips — on a loaded machine the
	// teardown easily outlasts however fast this loop can spin.
	const requestID = "wait-interview-idle"
	lastStatus := "<none>"
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := wsjson.Write(ctx, conn, ClientMessage{Type: "attach_session", RequestID: requestID, SessionID: sessionID}); err != nil {
			t.Fatalf("check interview recovery stopped: %v", err)
		}
		settled := readWSTestUntil(t, conn, "stopped interview recovery", func(msg ServerMessage) bool {
			return msg.Type == "interview_state" && msg.RequestID == requestID && msg.Interview != nil
		})
		if settled.Interview.Status == "failed" {
			return
		}
		lastStatus = settled.Interview.Status
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("interview recovery turn did not stop: last status = %q", lastStatus)
}

func TestWebSocketInterviewBridgeProducesStructuredDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.ResponseDelay = 5 * time.Second
	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "interviewer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "start_interview", RequestID: "start", AgentName: "interviewer"}); err != nil {
		t.Fatalf("start interview: %v", err)
	}
	sessionMsg := readWSTestUntil(t, conn, "interview session", func(msg ServerMessage) bool {
		return msg.Type == "session" && msg.Session != nil && msg.Session.Origin == store.OriginInterview
	})
	sessionID := sessionMsg.Session.ID
	httpBase := "http" + strings.TrimPrefix(strings.TrimSuffix(wsURL, "/api/ws"), "ws")

	for _, topic := range core.RequiredInterviewTopics {
		body, _ := json.Marshal(map[string]any{
			"topic":    topic,
			"header":   "Preference",
			"question": "Which option fits best?",
			"options": []map[string]string{
				{"label": "One", "description": "First choice."},
				{"label": "Two", "description": "Second choice."},
				{"label": "Three", "description": "Third choice."},
			},
		})
		result := make(chan *http.Response, 1)
		errCh := make(chan error, 1)
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpBase+"/api/interviews/"+sessionID+"/questions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			result <- resp
		}()
		question := readWSTestUntil(t, conn, "interview question", func(msg ServerMessage) bool {
			return msg.Type == "user_input_request" && msg.Input != nil
		})
		if len(question.Input.Questions) != 1 || !question.Input.Questions[0].IsOther {
			t.Fatalf("unexpected relayed question: %+v", question.Input)
		}
		if err := wsjson.Write(ctx, conn, ClientMessage{
			Type:      "user_input_decision",
			RequestID: question.Input.ID,
			Input:     &adapter.UserInputDecision{Answers: map[string][]string{"answer": {"One"}}},
		}); err != nil {
			t.Fatalf("answer question: %v", err)
		}
		select {
		case err := <-errCh:
			t.Fatalf("question bridge: %v", err)
		case resp := <-result:
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("question status = %d", resp.StatusCode)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	draftBody, _ := json.Marshal(validProfileDraft())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpBase+"/api/interviews/"+sessionID+"/draft", bytes.NewReader(draftBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("submit draft: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("draft status = %d", resp.StatusCode)
	}
	draftMsg := readWSTestUntil(t, conn, "interview draft", func(msg ServerMessage) bool {
		return msg.Type == "interview_state" && msg.Interview != nil && msg.Interview.Status == "draft"
	})
	if !strings.Contains(draftMsg.Interview.Draft, "**Name:** Marcus") {
		t.Fatalf("unexpected draft: %s", draftMsg.Interview.Draft)
	}
	reconnected := dialWSTest(t, wsURL)
	defer reconnected.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, reconnected, ClientMessage{Type: "attach_session", SessionID: sessionID}); err != nil {
		t.Fatalf("reattach completed interview: %v", err)
	}
	replayed := readWSTestUntil(t, reconnected, "replayed interview draft", func(msg ServerMessage) bool {
		return msg.Type == "interview_state" && msg.Interview != nil && msg.Interview.Status == "draft"
	})
	if replayed.Interview.Draft != draftMsg.Interview.Draft {
		t.Fatalf("replayed draft changed:\n%s\nwant:\n%s", replayed.Interview.Draft, draftMsg.Interview.Draft)
	}
	_ = wsjson.Write(ctx, conn, ClientMessage{Type: "stop_turn", SessionID: sessionID})
}
