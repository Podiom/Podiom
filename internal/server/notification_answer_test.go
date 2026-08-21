package server

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// TestFinishedRoadmapTurnNotifiesWithTheAgentsAnswer is the end-to-end check for the
// completion notification's body.
//
// It matters that this drives a real turn over the WebSocket rather than calling the
// producer directly: the answer exists only as a row in SQLite by the time the
// notification is published, and whether that row is committed first depends on the
// ordering between core persisting the turn's final message and emitting turn_done.
// Calling markRoadmapSessionFinished on its own would prove nothing about that.
func TestFinishedRoadmapTurnNotifiesWithTheAgentsAnswer(t *testing.T) {
	ctx := context.Background()
	srv, db, fake, wsURL, cleanup := newAnswerNotifyHarness(t)
	defer cleanup()

	// Markdown, because that is how agents actually close a turn.
	fake.Responses = []string{"**Done.** Fixed the redirect loop in `auth.go` and added a test."}

	sess := newRoadmapTaskSession(t, srv)

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type: "send_turn", RequestID: "req-1", SessionID: sess.ID, Message: "continue",
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}
	readWSTestUntil(t, conn, "roadmap done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess.ID
	})

	waitForCount(t, db, notify.TypeTaskReviewRequired, 1)
	row := onlyNotification(t, db, notify.TypeTaskReviewRequired)

	want := "Done. Fixed the redirect loop in auth.go and added a test."
	if row.Body != want {
		t.Errorf("body = %q, want the agent's closing words %q", row.Body, want)
	}
	if !strings.Contains(row.Title, "ready for review") {
		t.Errorf("title = %q, want the review wording", row.Title)
	}
}

// TestFinishedRoadmapTurnWithoutAnAnswerFallsBack checks the notification still reads
// as something when the turn ends without a closing message. The body is best-effort,
// so its absence must produce the static sentence rather than a blank line.
func TestFinishedRoadmapTurnWithoutAnAnswerFallsBack(t *testing.T) {
	ctx := context.Background()
	srv, db, fake, wsURL, cleanup := newAnswerNotifyHarness(t)
	defer cleanup()

	// A turn that only thinks: reasoning is persisted, an answer is not.
	fake.Reasoning = []string{"weighing the options"}
	fake.Responses = []string{""}

	sess := newRoadmapTaskSession(t, srv)

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type: "send_turn", RequestID: "req-1", SessionID: sess.ID, Message: "continue",
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}
	readWSTestUntil(t, conn, "roadmap done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess.ID
	})

	waitForCount(t, db, notify.TypeTaskReviewRequired, 1)
	row := onlyNotification(t, db, notify.TypeTaskReviewRequired)
	if row.Body != "The agent finished its work on this task." {
		t.Errorf("body = %q, want the static fallback", row.Body)
	}
}

// TestFinishedGoalRunNotifiesWithTheAgentsAnswer covers the second producer. It
// publishes from a deferred call inside core's own turn goroutine rather than from the
// server, so it reaches the answer row by a different route and is worth driving for
// real too. A roadmap session linked to a goal produces both notifications from one
// turn, which is also how the two read side by side in the notification center.
func TestFinishedGoalRunNotifiesWithTheAgentsAnswer(t *testing.T) {
	ctx := context.Background()
	srv, db, fake, wsURL, cleanup := newAnswerNotifyHarness(t)
	defer cleanup()

	fake.Responses = []string{"Posted the launch thread and replied to the first two comments."}

	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{
		Name: "worker", Provider: config.ProviderClaude,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := srv.core.CreateGoal(ctx, store.Goal{Title: "Launch on r/selfhosted", LeadAgent: "worker"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{
		AgentName: "worker", Origin: store.OriginRoadmap, GoalID: goal.ID,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	conn := dialWSTest(t, wsURL)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, ClientMessage{
		Type: "send_turn", RequestID: "req-1", SessionID: sess.ID, Message: "continue",
	}); err != nil {
		t.Fatalf("write send_turn: %v", err)
	}
	readWSTestUntil(t, conn, "goal run done", func(msg ServerMessage) bool {
		return msg.Type == "done" && msg.SessionID == sess.ID
	})

	waitForCount(t, db, notify.TypeGoalRunSucceeded, 1)
	row := onlyNotification(t, db, notify.TypeGoalRunSucceeded)

	want := "Posted the launch thread and replied to the first two comments."
	if row.Body != want {
		t.Errorf("body = %q, want the agent's closing words %q", row.Body, want)
	}
}

// onlyNotification returns the single stored notification of a type.
func onlyNotification(t *testing.T, db *store.Store, notifType string) store.Notification {
	t.Helper()
	list, err := db.ListNotifications(context.Background(), store.NotificationFilter{Limit: 200})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	var found []store.Notification
	for _, row := range list {
		if row.Type == notifType {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d %s notifications, want 1", len(found), notifType)
	}
	return found[0]
}

// newAnswerNotifyHarness is the WebSocket harness with a notification engine attached,
// so a real turn can be driven and the notification it produces inspected.
func newAnswerNotifyHarness(t *testing.T) (*Server, *store.Store, *adapter.Fake, string, func()) {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
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
	engine := notify.New(notify.Options{Store: db, Channels: []notify.Channel{&recordingChannel{}}})
	fake := adapter.NewFake()
	coreSvc, err := core.New(core.Options{
		Paths: paths, Store: db, Adapter: fake,
		DisableBackgroundWork: true, Notifications: engine,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Notifications: engine})
	ts := httptest.NewServer(srv.httpSrv.Handler)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	return srv, db, fake, wsURL, func() {
		ts.Close()
		engine.Close()
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}
