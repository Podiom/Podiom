package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

func TestPermissionBrokerDecision(t *testing.T) {
	b := newPermissionBroker()
	requests, unsubscribe := b.subscribe("turn-1")
	defer unsubscribe()

	done := make(chan adapter.PermissionDecision, 1)
	go func() {
		decision, _ := b.RequestPermission(context.Background(), adapter.PermissionRequest{
			ID:       "req-1",
			TurnID:   "turn-1",
			ToolName: "Bash",
		}, time.Second)
		done <- decision
	}()

	req := <-requests
	if req.ID != "req-1" || req.ToolName != "Bash" {
		t.Fatalf("bad relayed request: %+v", req)
	}
	if !b.decide("req-1", adapter.PermissionDecision{Behavior: "allow"}) {
		t.Fatalf("decision was not accepted")
	}
	decision := <-done
	if decision.Behavior != "allow" {
		t.Fatalf("bad decision: %+v", decision)
	}
}

func TestPermissionBrokerTimeoutAutoDenies(t *testing.T) {
	var buf bytes.Buffer
	b := newPermissionBroker(slog.New(slog.NewTextHandler(&buf, nil)))
	decision, err := b.RequestPermission(context.Background(), adapter.PermissionRequest{
		ID:     "req-1",
		TurnID: "turn-1",
	}, time.Nanosecond)
	if err != errPermissionTimeout {
		t.Fatalf("expected timeout, got %v", err)
	}
	if decision.Behavior != "deny" {
		t.Fatalf("expected auto deny, got %+v", decision)
	}
	logs := buf.String()
	for _, want := range []string{`event=permission`, `msg="permission timed out"`, `decision=deny`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestUserInputBrokerDecision(t *testing.T) {
	var buf bytes.Buffer
	b := newUserInputBroker(slog.New(slog.NewTextHandler(&buf, nil)))
	requests, unsubscribe := b.subscribe("turn-1")
	defer unsubscribe()

	done := make(chan adapter.UserInputDecision, 1)
	go func() {
		decision, _ := b.RequestUserInput(context.Background(), adapter.UserInputRequest{
			ID:     "input-1",
			TurnID: "turn-1",
			Questions: []adapter.UserInputQuestion{{
				ID:       "intent",
				Question: "Pick one",
			}},
		}, time.Second)
		done <- decision
	}()

	req := <-requests
	if req.ID != "input-1" || req.Questions[0].ID != "intent" {
		t.Fatalf("bad relayed request: %+v", req)
	}
	decision := adapter.UserInputDecision{Answers: map[string][]string{"intent": []string{"Draft"}}}
	if !b.decide("input-1", decision) {
		t.Fatalf("decision was not accepted")
	}
	got := <-done
	if got.Answers["intent"][0] != "Draft" {
		t.Fatalf("bad decision: %+v", got)
	}
	logs := buf.String()
	for _, want := range []string{`event=user_input`, `msg="user input requested"`, `msg="user input answered"`, `answer_keys=1`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestHandleUserInputRequestRelaysBlockingQuestion(t *testing.T) {
	b := newUserInputBroker()
	s := &Server{input: b}
	requests, unsubscribe := b.subscribe("turn-1")
	defer unsubscribe()

	body := `{"id":"claude-question","provider":"claude","item_id":"toolu-question","questions":[{"question":"Pick one","header":"Choice","multiSelect":false}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/user-input-requests/turn-1?timeout=1s", strings.NewReader(body))
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handleUserInputRequest(rr, req)
		close(done)
	}()

	input := <-requests
	if input.ID != "claude-question" || input.TurnID != "turn-1" || input.EndsTurn {
		t.Fatalf("bad relayed request: %+v", input)
	}
	if len(input.Questions) != 1 || input.Questions[0].ID != "q1" {
		t.Fatalf("questions were not normalized: %+v", input.Questions)
	}
	if !b.decide(input.ID, adapter.UserInputDecision{Answers: map[string][]string{"q1": {"A"}}}) {
		t.Fatal("answer was not accepted")
	}
	<-done
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var decision adapter.UserInputDecision
	if err := json.Unmarshal(rr.Body.Bytes(), &decision); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := decision.Answers["q1"]; len(got) != 1 || got[0] != "A" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestHandleUserInputRequestTimeoutReturnsEmptyDecision(t *testing.T) {
	s := &Server{input: newUserInputBroker()}
	body := `{"provider":"claude","questions":[{"question":"Pick one"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/user-input-requests/missing-turn?timeout=1ns", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleUserInputRequest(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"answers":{}`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleUserInputRequestRejectsMalformedQuestion(t *testing.T) {
	s := &Server{input: newUserInputBroker()}
	for _, body := range []string{
		`{"provider":"claude","questions":[]}`,
		`{"provider":"claude","questions":[{"question":"  "}]}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/user-input-requests/turn-1", strings.NewReader(body))
		rr := httptest.NewRecorder()
		s.handleUserInputRequest(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, response = %s", body, rr.Code, rr.Body.String())
		}
	}
}

func TestActiveTurnRestoresBlockingUserInputOnReconnect(t *testing.T) {
	hub := newActiveTurnHub()
	if _, err := hub.start("session-1", "turn-1", "request-1", nil, func() {}); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	input := &adapter.UserInputRequest{
		ID:       "claude-question",
		TurnID:   "turn-1",
		EndsTurn: false,
		Questions: []adapter.UserInputQuestion{{
			ID:       "q1",
			Question: "Pick one",
		}},
	}
	hub.recordUserInput("session-1", input)

	state, ok := hub.attach("session-1", nil)
	if !ok || state.PendingUserInput == nil {
		t.Fatalf("pending question was not restored: %+v", state)
	}
	if state.PendingPermission != nil || state.PendingUserInput.ID != input.ID || state.PendingUserInput.EndsTurn {
		t.Fatalf("bad restored interaction state: %+v", state)
	}
}

func TestUserInputBrokerMetadata(t *testing.T) {
	b := newUserInputBroker()
	b.attach("input-1", "session-1", true)
	meta := b.popMeta("input-1")
	if meta.sessionID != "session-1" || !meta.restoreRoadmap {
		t.Fatalf("bad metadata: %+v", meta)
	}
	if empty := b.popMeta("input-1"); empty.sessionID != "" || empty.restoreRoadmap {
		t.Fatalf("metadata should be removed after pop: %+v", empty)
	}
}

func TestPermissionBrokerMetadata(t *testing.T) {
	b := newPermissionBroker()
	b.attach("perm-1", "session-1", true)
	b.attach("perm-1", "session-1", false)
	meta := b.popMeta("perm-1")
	if meta.sessionID != "session-1" || !meta.restoreRoadmap {
		t.Fatalf("bad metadata: %+v", meta)
	}
	if empty := b.popMeta("perm-1"); empty.sessionID != "" || empty.restoreRoadmap {
		t.Fatalf("metadata should be removed after pop: %+v", empty)
	}
}

func TestHTTPPermissionRequestUsesPlanGateBeforeBroker(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake(), DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "dinesh", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{
		AgentName:                      "dinesh",
		Origin:                         store.OriginWeb,
		CreatePlanBeforeImplementation: true,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	srv.broker.attachTurn("turn-1", sess.ID)
	defer srv.broker.detachTurn("turn-1")
	requests, unsubscribe := srv.broker.subscribe("turn-1")
	defer unsubscribe()

	req := httptest.NewRequest(http.MethodPost, "/api/permissions/turn-1", strings.NewReader(`{"id":"perm-1","tool_name":"Bash"}`))
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.handlePermissionRequest(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var decision adapter.PermissionDecision
	if err := json.NewDecoder(rr.Body).Decode(&decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if decision.Behavior != "deny" || decision.Message != core.PlanGateMessage {
		t.Fatalf("decision = %+v, want plan gate deny", decision)
	}
	select {
	case delivered := <-requests:
		t.Fatalf("plan-gated request should not reach broker/UI: %+v", delivered)
	default:
	}
}

func TestHTTPPermissionRequestUsesInterviewGateBeforeBroker(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake(), DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "interviewer", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{AgentName: "interviewer", Origin: store.OriginInterview})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	srv.broker.attachTurn("turn-1", sess.ID)
	defer srv.broker.detachTurn("turn-1")
	requests, unsubscribe := srv.broker.subscribe("turn-1")
	defer unsubscribe()

	for _, tc := range []struct {
		tool string
		want string
	}{
		{"mcp__podiom_interview__podiom_ask_profile_question", "allow"},
		{"Bash", "deny"},
	} {
		body := fmt.Sprintf(`{"id":"perm-%s","tool_name":%q}`, tc.want, tc.tool)
		req := httptest.NewRequest(http.MethodPost, "/api/permissions/turn-1", strings.NewReader(body)).WithContext(ctx)
		rr := httptest.NewRecorder()
		srv.handlePermissionRequest(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", tc.tool, rr.Code, rr.Body.String())
		}
		var decision adapter.PermissionDecision
		if err := json.NewDecoder(rr.Body).Decode(&decision); err != nil {
			t.Fatalf("decode %s decision: %v", tc.tool, err)
		}
		if decision.Behavior != tc.want {
			t.Fatalf("%s decision = %+v, want %s", tc.tool, decision, tc.want)
		}
	}
	select {
	case delivered := <-requests:
		t.Fatalf("interview-gated request should not reach broker/UI: %+v", delivered)
	default:
	}
}
