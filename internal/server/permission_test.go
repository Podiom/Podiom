package server

import (
	"bytes"
	"context"
	"encoding/json"
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
