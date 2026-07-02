package server

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestWebSocketSendTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
	fake := adapter.NewFake()
	fake.Responses = []string{"assistant from ws"}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: fake})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := coreSvc.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc})
	ts := httptest.NewServer(srv.httpSrv.Handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type:           "send_turn",
		RequestID:      "req-1",
		AgentName:      "webber",
		Message:        "hello",
		Model:          "opus",
		Effort:         "high",
		ProjectID:      "mission-control",
		PermissionMode: config.PermissionYolo,
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}

	var sawSession, sawAssistant, sawDone, sawFinalState bool
	for i := 0; i < 12; i++ {
		var msg ServerMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read ws: %v", err)
		}
		switch msg.Type {
		case "state":
			if sawDone {
				sawFinalState = true
			}
		case "session":
			sawSession = msg.Session != nil &&
				msg.Session.Origin == store.OriginWeb &&
				msg.Session.Model == "opus" &&
				msg.Session.Effort == "high" &&
				msg.Session.PermissionMode == config.PermissionYolo &&
				msg.Session.ProjectID == "mission-control"
		case "assistant":
			sawAssistant = msg.Delta == "assistant from ws"
		case "done":
			sawDone = true
		}
		if sawSession && sawAssistant && sawDone && sawFinalState {
			return
		}
	}
	t.Fatalf("missing expected events: session=%v assistant=%v done=%v final_state=%v", sawSession, sawAssistant, sawDone, sawFinalState)
}

func TestWebSocketActiveTurnSurvivesReconnectWithPermission(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.PermissionTool = "Bash"
	fake.Responses = []string{"approved after reconnect"}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	conn1 := dialWSTest(t, wsURL)
	if err := wsjson.Write(ctx, conn1, ClientMessage{
		Type:      "send_turn",
		RequestID: "req-1",
		AgentName: "webber",
		Message:   "needs a tool",
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}

	var sessionID string
	perm := readWSTestUntil(t, conn1, "permission request", func(msg ServerMessage) bool {
		if msg.Type == "session" && msg.Session != nil {
			sessionID = msg.Session.ID
		}
		return msg.Type == "permission_request" && msg.Request != nil
	})
	if sessionID == "" {
		t.Fatal("session id was not announced before permission request")
	}
	if err := conn1.Close(websocket.StatusNormalClosure, "route changed"); err != nil {
		t.Fatalf("close first socket: %v", err)
	}

	conn2 := dialWSTest(t, wsURL)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn2, ClientMessage{Type: "attach_session", RequestID: "attach-1", SessionID: sessionID}); err != nil {
		t.Fatalf("write attach: %v", err)
	}
	state := readWSTestUntil(t, conn2, "reattached turn state", func(msg ServerMessage) bool {
		return msg.Type == "turn_state" && msg.TurnState != nil && msg.TurnState.SessionID == sessionID && msg.TurnState.PendingPermission != nil
	})
	if state.TurnState.PendingPermission.ID != perm.Request.ID {
		t.Fatalf("reattached permission id = %q, want %q", state.TurnState.PendingPermission.ID, perm.Request.ID)
	}
	if err := wsjson.Write(ctx, conn2, ClientMessage{
		Type:      "permission_decision",
		RequestID: perm.Request.ID,
		Decision:  &adapter.PermissionDecision{Behavior: "allow"},
	}); err != nil {
		t.Fatalf("write decision: %v", err)
	}
	readWSTestUntil(t, conn2, "done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sessionID
	})

	history, err := coreSvc.History(ctx, sessionID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || history[1].Role != store.RoleAssistant || history[1].Content != "approved after reconnect" {
		t.Fatalf("history was not completed after reconnect: %+v", history)
	}
}

func TestWebSocketRoadmapPermissionMovesReviewRestoresAndFinishesReview(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.PermissionTool = "Bash"
	fake.ResponseDelay = 500 * time.Millisecond
	fake.Responses = []string{"approved roadmap work"}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := coreSvc.CreateTask(ctx, store.Task{Title: "Needs approval", AssignedAgent: "webber"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := coreSvc.StartTask(ctx, core.StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-1", SessionID: sess.ID, Message: "continue"}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}
	perm := readWSTestUntil(t, conn, "roadmap permission request", func(msg ServerMessage) bool {
		return msg.Type == "permission_request" && msg.Request != nil && msg.SessionID == sess.ID
	})
	waitTaskStatus(t, coreSvc, task.ID, store.TaskReview)

	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type:      "permission_decision",
		RequestID: perm.Request.ID,
		Decision:  &adapter.PermissionDecision{Behavior: "allow"},
	}); err != nil {
		t.Fatalf("write decision: %v", err)
	}
	waitTaskStatus(t, coreSvc, task.ID, store.TaskInProgress)
	readWSTestUntil(t, conn, "roadmap done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess.ID
	})
	waitTaskStatus(t, coreSvc, task.ID, store.TaskReview)
}

func TestWebSocketRoadmapCompletionMovesTaskReview(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.Responses = []string{"completed roadmap work"}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := coreSvc.CreateTask(ctx, store.Task{Title: "Finish me", AssignedAgent: "webber"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := coreSvc.StartTask(ctx, core.StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-1", SessionID: sess.ID, Message: "continue"}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}
	readWSTestUntil(t, conn, "roadmap done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess.ID
	})
	waitTaskStatus(t, coreSvc, task.ID, store.TaskReview)
}

func TestWebSocketTracksMultipleActiveSessionsIndependently(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.PermissionTool = "Bash"
	fake.Responses = []string{"first approved", "second approved"}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess1, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{AgentName: "webber", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	sess2, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{AgentName: "webber", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	permissions := map[string]*adapter.PermissionRequest{}
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-1", SessionID: sess1.ID, Message: "one"}); err != nil {
		t.Fatalf("write first send_turn: %v", err)
	}
	readWSTestUntil(t, conn, "first permission request", func(msg ServerMessage) bool {
		if msg.Type == "permission_request" && msg.Request != nil && msg.SessionID == sess1.ID {
			req := *msg.Request
			permissions[msg.SessionID] = &req
			return true
		}
		return false
	})
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-2", SessionID: sess2.ID, Message: "two"}); err != nil {
		t.Fatalf("write second send_turn: %v", err)
	}
	readWSTestUntil(t, conn, "second permission request", func(msg ServerMessage) bool {
		if msg.Type == "permission_request" && msg.Request != nil && msg.SessionID == sess2.ID {
			req := *msg.Request
			permissions[msg.SessionID] = &req
			return true
		}
		return false
	})
	if permissions[sess1.ID] == nil || permissions[sess2.ID] == nil {
		t.Fatalf("permissions were not session-scoped: %+v", permissions)
	}

	for _, sess := range []store.Session{sess1, sess2} {
		if err := wsjson.Write(ctx, conn, ClientMessage{Type: "attach_session", RequestID: "attach-" + sess.ID, SessionID: sess.ID}); err != nil {
			t.Fatalf("write attach: %v", err)
		}
		state := readWSTestUntil(t, conn, "session-specific turn state", func(msg ServerMessage) bool {
			return msg.Type == "turn_state" && msg.TurnState != nil && msg.TurnState.SessionID == sess.ID
		})
		if state.TurnState.PendingPermission == nil || state.TurnState.PendingPermission.ID != permissions[sess.ID].ID {
			t.Fatalf("bad turn state for %s: %+v", sess.ID, state.TurnState)
		}
	}

	for _, sess := range []store.Session{sess1, sess2} {
		req := permissions[sess.ID]
		if err := wsjson.Write(ctx, conn, ClientMessage{
			Type:      "permission_decision",
			RequestID: req.ID,
			Decision:  &adapter.PermissionDecision{Behavior: "allow"},
		}); err != nil {
			t.Fatalf("write decision: %v", err)
		}
		readWSTestUntil(t, conn, "done", func(msg ServerMessage) bool {
			return msg.Type == "done" && msg.SessionID == sess.ID
		})
	}
}

func TestWebSocketStopOneTurnKeepsOtherSessionRunning(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.ResponseDelay = 200 * time.Millisecond
	fake.Responses = []string{"first response", "second response"}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess1, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{AgentName: "webber", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	sess2, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{AgentName: "webber", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-1", SessionID: sess1.ID, Message: "stop me"}); err != nil {
		t.Fatalf("write first turn: %v", err)
	}
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-2", SessionID: sess2.ID, Message: "finish me"}); err != nil {
		t.Fatalf("write second turn: %v", err)
	}
	readWSTestUntil(t, conn, "first turn state", func(msg ServerMessage) bool {
		return msg.Type == "turn_state" && msg.SessionID == sess1.ID
	})
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "stop_turn", RequestID: "stop-1", SessionID: sess1.ID}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	readWSTestUntil(t, conn, "second session done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess2.ID
	})

	history1, err := coreSvc.History(ctx, sess1.ID)
	if err != nil {
		t.Fatalf("history 1: %v", err)
	}
	for _, msg := range history1 {
		if msg.Role == store.RoleAssistant {
			t.Fatalf("stopped session persisted assistant unexpectedly: %+v", history1)
		}
	}
	history2, err := coreSvc.History(ctx, sess2.ID)
	if err != nil {
		t.Fatalf("history 2: %v", err)
	}
	if len(history2) != 2 || history2[1].Role != store.RoleAssistant {
		t.Fatalf("other session did not finish: %+v", history2)
	}
}

func TestWebSocketRejectsSecondTurnInSameSession(t *testing.T) {
	ctx := context.Background()
	coreSvc, fake, wsURL, cleanup := newWSTestHarness(t)
	defer cleanup()
	fake.PermissionTool = "Bash"
	fake.Responses = []string{"first completed"}

	if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{Name: "webber", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := coreSvc.CreateSession(ctx, core.CreateSessionRequest{AgentName: "webber", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-1", SessionID: sess.ID, Message: "first"}); err != nil {
		t.Fatalf("write first turn: %v", err)
	}
	permission := readWSTestUntil(t, conn, "permission request", func(msg ServerMessage) bool {
		return msg.Type == "permission_request" && msg.SessionID == sess.ID && msg.Request != nil
	})
	if err := wsjson.Write(ctx, conn, ClientMessage{Type: "send_turn", RequestID: "req-2", SessionID: sess.ID, Message: "second"}); err != nil {
		t.Fatalf("write second turn: %v", err)
	}
	got := readWSTestUntil(t, conn, "second turn error", func(msg ServerMessage) bool {
		return msg.Type == "error" && msg.RequestID == "req-2"
	})
	if !strings.Contains(got.Error, "active turn") {
		t.Fatalf("unexpected second-turn error: %q", got.Error)
	}
	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type:      "permission_decision",
		RequestID: permission.Request.ID,
		Decision:  &adapter.PermissionDecision{Behavior: "allow"},
	}); err != nil {
		t.Fatalf("write permission decision: %v", err)
	}
	readWSTestUntil(t, conn, "first turn done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess.ID
	})
}

func newWSTestHarness(t *testing.T) (*core.Core, *adapter.Fake, string, func()) {
	t.Helper()
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
	fake := adapter.NewFake()
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc})
	ts := httptest.NewServer(srv.httpSrv.Handler)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	return coreSvc, fake, wsURL, func() {
		ts.Close()
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func dialWSTest(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn
}

func readWSTestUntil(t *testing.T, conn *websocket.Conn, desc string, pred func(ServerMessage) bool) ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var msg ServerMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read ws waiting for %s: %v", desc, err)
		}
		if pred(msg) {
			return msg
		}
	}
}

func waitTaskStatus(t *testing.T, coreSvc *core.Core, taskID string, want store.TaskStatus) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	var got store.Task
	var err error
	for time.Now().Before(deadline) {
		got, err = coreSvc.GetTask(ctx, taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if got.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task status = %q, want %q", got.Status, want)
}
