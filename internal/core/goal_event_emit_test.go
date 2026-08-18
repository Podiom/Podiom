package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// TestEveryGoalEventAppendGoesThroughTheWrapper is the guard the whole goal
// notification design rests on.
//
// Goal notifications come from one subscription to the timeline, which only works
// if every append in core routes through appendGoalEvent. A call that reaches
// c.store.AppendGoalEvent directly persists the event but silently produces no
// live update and no notification — a failure with no symptom at the call site,
// which is exactly the kind that survives review.
func TestEveryGoalEventAppendGoesThroughTheWrapper(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the core package directory")
	}
	dir := filepath.Dir(thisFile)
	sources, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}

	// Only the wrapper itself may call the store directly.
	const wrapper = "goal_event_emit.go"
	for _, path := range sources {
		name := filepath.Base(path)
		if name == wrapper || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, "store.AppendGoalEvent") {
				continue
			}
			t.Errorf("%s:%d calls the store's AppendGoalEvent directly; use c.appendGoalEvent so the "+
				"event reaches the live broadcast and the notification engine", name, i+1)
		}
	}
}

// TestGoalEventsReachTheObserver drives the real core operations that append
// timeline entries and checks each one is observed. The wrapper guard above proves
// the call sites route correctly; this proves the routing actually fires.
func TestGoalEventsReachTheObserver(t *testing.T) {
	c, cleanup := newTestCore(t)
	defer cleanup()
	ctx := context.Background()

	var seen struct {
		sync.Mutex
		kinds []store.GoalEventKind
	}
	c.SetGoalEventHandler(func(ev store.GoalEvent) {
		seen.Lock()
		seen.kinds = append(seen.kinds, ev.Kind)
		seen.Unlock()
	})
	observed := func(kind store.GoalEventKind) bool {
		seen.Lock()
		defer seen.Unlock()
		for _, k := range seen.kinds {
			if k == kind {
				return true
			}
		}
		return false
	}

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "alice", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Release Podiom 1.0", LeadAgent: "alice"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if !observed(store.GoalEventCreated) {
		t.Error("goal creation did not reach the observer")
	}

	if _, err := c.RecordGoalProgress(ctx, RecordGoalProgressRequest{
		GoalID: goal.ID, Body: "Cut the release branch",
	}); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	if !observed(store.GoalEventProgress) {
		t.Error("recording progress did not reach the observer")
	}

	if _, err := c.AddGoalFeedback(ctx, goal.ID, "Prioritise the changelog"); err != nil {
		t.Fatalf("add feedback: %v", err)
	}
	if !observed(store.GoalEventUserFeedback) {
		t.Error("user feedback did not reach the observer")
	}

	if _, err := c.ProposeGoalCompletion(ctx, goal.ID, "", "Everything shipped"); err != nil {
		t.Fatalf("propose completion: %v", err)
	}
	if !observed(store.GoalEventCompletionProposed) {
		t.Error("a completion proposal did not reach the observer")
	}

	if _, err := c.TransitionGoal(ctx, goal.ID, store.GoalDone, "confirmed"); err != nil {
		t.Fatalf("transition goal: %v", err)
	}
	if !observed(store.GoalEventStatusChange) {
		t.Error("a status change did not reach the observer")
	}
}
