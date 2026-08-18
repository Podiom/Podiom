package server

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

// recordingChannel captures the envelopes the engine delivers, which is how these
// tests observe what was published.
type recordingChannel struct {
	mu  sync.Mutex
	got []notify.Envelope
}

func (r *recordingChannel) Name() string { return "recorder" }

func (r *recordingChannel) Send(_ context.Context, env notify.Envelope) ([]notify.Result, error) {
	r.mu.Lock()
	r.got = append(r.got, env)
	r.mu.Unlock()
	return []notify.Result{{Destination: "recorder"}}, nil
}

func (r *recordingChannel) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.got))
	for _, env := range r.got {
		out = append(out, env.Type)
	}
	return out
}

// newNotifyTestServer builds a server whose notification engine records
// everything it would deliver.
func newNotifyTestServer(t *testing.T) (*Server, *store.Store, *recordingChannel) {
	t.Helper()
	return newNotifyTestServerWithOptions(t, nil)
}

// newNotifyTestServerWithOptions is newNotifyTestServer with a hook to adjust the
// server options, so a test can exercise how an option is wired rather than
// assigning the resulting field.
func newNotifyTestServerWithOptions(t *testing.T, adjust func(*Options)) (*Server, *store.Store, *recordingChannel) {
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
	t.Cleanup(func() { db.Close() })

	rec := &recordingChannel{}
	engine := notify.New(notify.Options{Store: db, Channels: []notify.Channel{rec}})
	t.Cleanup(engine.Close)

	coreSvc, err := core.New(core.Options{
		Paths: paths, Store: db, Adapter: adapter.NewFake(),
		DisableBackgroundWork: true, Notifications: engine,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	opts := Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths, Notifications: engine}
	if adjust != nil {
		adjust(&opts)
	}
	return New(opts), db, rec
}

// countByType returns how many notifications of a type are stored.
func countByType(t *testing.T, db *store.Store, notifType string) int {
	t.Helper()
	list, err := db.ListNotifications(context.Background(), store.NotificationFilter{Limit: 200})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	n := 0
	for _, row := range list {
		if row.Type == notifType {
			n++
		}
	}
	return n
}

// newRoadmapTaskSession sets up a project, a task, and a roadmap-origin session
// for it — the state the review transitions operate on.
func newRoadmapTaskSession(t *testing.T, srv *Server) store.Session {
	t.Helper()
	ctx := context.Background()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{
		Name: "worker", Provider: config.ProviderClaude,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := srv.core.CreateProject(ctx, projects.Project{
		ID: "podiom", Name: "Podiom", Path: filepath.Join(t.TempDir(), "repo"),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := srv.core.CreateTask(ctx, store.Task{
		ProjectID: project.ID, Title: "Write the docs", AssignedAgent: "worker",
		Status: store.TaskInProgress,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{
		AgentName: "worker", Origin: store.OriginRoadmap,
		TaskID: task.ID, ProjectID: project.ID,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess
}

// TestTransientReviewMovesDoNotNotify is the regression test for the trap in the
// task notification mapping.
//
// A roadmap task is moved into review for three transient reasons as well as the
// real one: while a submitted plan awaits approval, and while a permission or a
// question is pending. Notifying on those would tell the user work is ready to
// review while the agent is still running, then silently un-tell them when the
// task moves back. Only a finished turn means what "ready for review" says.
func TestTransientReviewMovesDoNotNotify(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	sess := newRoadmapTaskSession(t, srv)

	// A pending question, then a pending permission: both park the task in review.
	srv.markRoadmapQuestionPending(ctx, sess.ID, "req-question")
	srv.markRoadmapQuestionResolved(ctx, "req-question")
	srv.markRoadmapPermissionPending(ctx, sess.ID, "req-permission")
	srv.markRoadmapPermissionResolved(ctx, "req-permission")

	// Publish something that does notify, so once it lands the transient moves
	// ahead of it have provably been processed too.
	srv.notifications.Publish(notify.Event{
		Type: notify.TypeGoalProgress, GoalID: "goal-probe", Detail: "probe",
	})
	waitForCount(t, db, notify.TypeGoalProgress, 1)

	if got := countByType(t, db, notify.TypeTaskReviewRequired); got != 0 {
		t.Errorf("transient review moves produced %d task.review_required notifications, want 0", got)
	}
}

// TestFinishedRoadmapTurnNotifiesOnce checks the real signal does fire, and that a
// second finish does not notify again.
func TestFinishedRoadmapTurnNotifiesOnce(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	sess := newRoadmapTaskSession(t, srv)

	srv.markRoadmapSessionFinished(ctx, sess.ID)
	waitForCount(t, db, notify.TypeTaskReviewRequired, 1)

	// The task is already in review, so a repeat changes nothing and must not
	// produce a second notification.
	srv.markRoadmapSessionFinished(ctx, sess.ID)
	srv.notifications.Publish(notify.Event{
		Type: notify.TypeGoalProgress, GoalID: "goal-probe", Detail: "probe",
	})
	waitForCount(t, db, notify.TypeGoalProgress, 1)

	if got := countByType(t, db, notify.TypeTaskReviewRequired); got != 1 {
		t.Errorf("task.review_required count = %d, want 1", got)
	}
}

// waitForCount polls until a notification type reaches the expected count. The
// engine's worker is asynchronous, so tests wait on the effect.
func waitForCount(t *testing.T, db *store.Store, notifType string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countByType(t, db, notifType) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s notifications, have %d", want, notifType, countByType(t, db, notifType))
}

// seedGoalActionItem sets up a goal with a session and one open action item — the
// state an action-item notification refers to.
func seedGoalActionItem(t *testing.T, srv *Server) (store.Goal, store.GoalActionItem) {
	t.Helper()
	ctx := context.Background()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{
		Name: "lead", Provider: config.ProviderClaude,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := srv.core.CreateGoal(ctx, store.Goal{Title: "Launch Podiom", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{
		AgentName: "lead", Origin: store.OriginGoal, GoalID: goal.ID,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	res, err := srv.core.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{
		Title: "Publish the announcement",
	})
	if err != nil {
		t.Fatalf("request action: %v", err)
	}
	return goal, res.Item
}
