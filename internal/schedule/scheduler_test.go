package schedule

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

func newTestScheduler(t *testing.T) (*Scheduler, *core.Core, config.Paths, func()) {
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
	fake := adapter.NewFake()
	fake.Responses = []string{"scheduled work done"}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if _, err := coreSvc.CreateAgent(context.Background(), core.CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	s := New(Options{Dir: paths.SchedulesDir, Core: coreSvc, Store: db})
	return s, coreSvc, paths, func() {
		s.Stop()
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func TestRunNowCreatesScheduleSessionAndRunRecord(t *testing.T) {
	ctx := context.Background()
	s, c, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "morning.md", `---
agent: jared
cron: "0 7 * * *"
run_permission: preapproved
enabled: true
---
Summarise the calendar.
`)

	run, err := s.RunNow(ctx, "morning")
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if run.Status != store.RunSuccess {
		t.Fatalf("run status = %q, want success (%q)", run.Status, run.Error)
	}
	if run.Trigger != store.TriggerManual || run.SessionID == "" {
		t.Fatalf("unexpected run record: %+v", run)
	}

	sess, err := c.GetSession(ctx, run.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Origin != store.OriginSchedule || sess.ScheduleID != "morning" || sess.RunID != run.ID {
		t.Fatalf("session provenance wrong: %+v", sess)
	}

	runs, err := c.Store().ListScheduleRuns(ctx, "morning", 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one recorded run, got %d", len(runs))
	}
}

func TestSchedulerLogsSyncAndManualRun(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	s, _, paths, cleanup := newTestScheduler(t)
	defer cleanup()
	s.log = slog.New(slog.NewTextHandler(&buf, nil))

	writeSchedule(t, paths.SchedulesDir, "morning.md", `---
agent: jared
cron: "0 7 * * *"
run_permission: preapproved
enabled: true
---
Summarise the calendar.
`)

	s.Sync()
	if _, err := s.RunNow(ctx, "morning"); err != nil {
		t.Fatalf("run now: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{
		`event=schedule`,
		`msg="schedule scan started"`,
		`msg="schedule job registered"`,
		`msg="scheduled run started"`,
		`msg="scheduled run finished"`,
		`schedule=morning`,
		`trigger=manual`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestSyncRegistersEnabledNotDisabled(t *testing.T) {
	ctx := context.Background()
	s, _, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "on.md", `---
agent: jared
cron: "0 7 * * *"
enabled: true
---
do it
`)
	writeSchedule(t, paths.SchedulesDir, "off.md", `---
agent: jared
cron: "0 7 * * *"
enabled: false
---
do not
`)
	s.cron.Start() // entries only compute Next once the cron loop is running

	statuses, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]Status{}
	for _, st := range statuses {
		byName[st.Name] = st
	}
	on, off := byName["on"], byName["off"]
	if !on.Enabled || on.NextRun == nil {
		t.Fatalf("enabled schedule should be registered with a next run: %+v", on)
	}
	if off.Enabled || off.NextRun != nil {
		t.Fatalf("disabled schedule must not be registered to fire: %+v", off)
	}
}

func TestListIncludesScheduleBody(t *testing.T) {
	ctx := context.Background()
	s, _, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "morning.md", `---
agent: jared
cron: "0 7 * * *"
enabled: true
---
Summarise the calendar.
`)

	statuses, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected one schedule, got %d: %+v", len(statuses), statuses)
	}
	if statuses[0].Body != "Summarise the calendar." {
		t.Fatalf("body = %q", statuses[0].Body)
	}
}

func TestRunNowFailsForMissingSchedule(t *testing.T) {
	ctx := context.Background()
	s, _, _, cleanup := newTestScheduler(t)
	defer cleanup()
	if _, err := s.RunNow(ctx, "ghost"); err == nil {
		t.Fatal("expected error for missing schedule, got nil")
	}
}

func TestDeleteRemovesFileRegistrationAndRuns(t *testing.T) {
	ctx := context.Background()
	s, c, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "morning.md", `---
agent: jared
cron: "0 7 * * *"
run_permission: preapproved
enabled: true
---
Summarise the calendar.
`)

	// A run creates history we expect Delete to clear.
	if _, err := s.RunNow(ctx, "morning"); err != nil {
		t.Fatalf("run now: %v", err)
	}

	if err := s.Delete(ctx, "morning"); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}
	if _, err := os.Stat(paths.SchedulesDir + "/morning.md"); !os.IsNotExist(err) {
		t.Fatalf("schedule file should be gone: err = %v", err)
	}
	statuses, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, st := range statuses {
		if st.Name == "morning" {
			t.Fatalf("deleted schedule should not be listed: %+v", st)
		}
	}
	runs, err := c.Store().ListScheduleRuns(ctx, "morning", 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("run history should be cleared, got %d", len(runs))
	}

	if err := s.Delete(ctx, "ghost"); err == nil {
		t.Fatal("expected error deleting a missing schedule")
	}
}

func TestPickupDueGoalReviews(t *testing.T) {
	ctx := context.Background()
	s, coreSvc, _, cleanup := newTestScheduler(t)
	defer cleanup()

	goal, err := coreSvc.CreateGoal(ctx, store.Goal{Title: "Ship docs", LeadAgent: "jared", ReviewEvery: "24h"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// Not yet due: the fresh next_review_at is a day away.
	s.pickupDueGoalReviews(ctx)
	events, err := coreSvc.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, ev := range events {
		if ev.Kind == store.GoalEventReviewStarted {
			t.Fatalf("review fired before it was due")
		}
	}

	// Make it overdue and tick: exactly one review fires, and the clock has
	// advanced BEFORE the run so an immediate second tick is a no-op.
	if err := s.store.SetGoalNextReview(ctx, goal.ID, "2000-01-01T00:00:00Z"); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	s.pickupDueGoalReviews(ctx)
	s.pickupDueGoalReviews(ctx)

	events, err = coreSvc.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	reviews := 0
	for _, ev := range events {
		if ev.Kind == store.GoalEventReviewStarted {
			reviews++
			sess, err := coreSvc.GetSession(ctx, ev.SessionID)
			if err != nil {
				t.Fatalf("get review session: %v", err)
			}
			if sess.Origin != store.OriginGoal || sess.GoalID != goal.ID {
				t.Fatalf("review session = %+v, want origin goal", sess)
			}
		}
	}
	if reviews != 1 {
		t.Fatalf("review sessions = %d, want exactly 1", reviews)
	}
	after, err := coreSvc.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if after.NextReviewAt <= "2000-01-01T00:00:00Z" {
		t.Fatalf("next_review_at did not advance: %q", after.NextReviewAt)
	}

	// A paused goal never fires even when overdue.
	if _, err := coreSvc.TransitionGoal(ctx, goal.ID, store.GoalPaused, ""); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := s.store.SetGoalNextReview(ctx, goal.ID, "2000-01-01T00:00:00Z"); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	s.pickupDueGoalReviews(ctx)
	events, _ = coreSvc.ListGoalEvents(ctx, goal.ID, 0, 0)
	reviews = 0
	for _, ev := range events {
		if ev.Kind == store.GoalEventReviewStarted {
			reviews++
		}
	}
	if reviews != 1 {
		t.Fatalf("paused goal fired a review (total %d)", reviews)
	}
}

// TestUpdatePreservesUnpatchedFields pins the contract that makes Update safe to
// hand an agent: a partial patch keeps everything it did not mention — including
// the creator attribution and the body — and parking a schedule stops it firing
// without destroying it.
func TestUpdatePreservesUnpatchedFields(t *testing.T) {
	ctx := context.Background()
	s, _, _, cleanup := newTestScheduler(t)
	defer cleanup()

	created, err := s.Create(ctx, CreateParams{
		Name:             "morning",
		Agent:            "jared",
		Cron:             "0 7 * * *",
		GoalID:           "goal-1",
		CreatedBySession: "sess-1",
		CreatedByAgent:   "jared",
		Body:             "Summarise the calendar.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.Enabled {
		t.Fatalf("a freshly created schedule should be armed")
	}

	parked := false
	updated, err := s.Update(ctx, "morning", UpdateParams{Enabled: &parked})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Enabled {
		t.Errorf("enabled=false did not park the schedule")
	}
	if updated.Body != "Summarise the calendar." {
		t.Errorf("body was lost: %q", updated.Body)
	}
	if updated.CreatedBySession != "sess-1" || updated.CreatedByAgent != "jared" {
		t.Errorf("update dropped the creator attribution: %+v", updated)
	}
	if updated.GoalID != "goal-1" {
		t.Errorf("update dropped the goal link: %q", updated.GoalID)
	}
	// Parking unregisters the cron job; the file itself stays on disk.
	if updated.NextRun != nil {
		t.Errorf("a parked schedule should have no next run, got %v", updated.NextRun)
	}
	if _, err := os.Stat(updated.Path); err != nil {
		t.Errorf("parked schedule file should still exist: %v", err)
	}

	// Cadence is exclusive: switching to `every` clears `cron` without the caller
	// having to blank it.
	every := "6h"
	switched, err := s.Update(ctx, "morning", UpdateParams{Every: &every})
	if err != nil {
		t.Fatalf("update cadence: %v", err)
	}
	if switched.Every != "6h" || switched.Cron != "" {
		t.Errorf("cadence switch left both set: cron=%q every=%q", switched.Cron, switched.Every)
	}
}

// TestUpdateRejectsBadPatchWithoutTouchingTheFile keeps a broken edit from
// replacing a working schedule.
func TestUpdateRejectsBadPatchWithoutTouchingTheFile(t *testing.T) {
	ctx := context.Background()
	s, _, _, cleanup := newTestScheduler(t)
	defer cleanup()

	if _, err := s.Create(ctx, CreateParams{
		Name:  "morning",
		Agent: "jared",
		Cron:  "0 7 * * *",
		Body:  "Summarise the calendar.",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	blank := ""
	if _, err := s.Update(ctx, "morning", UpdateParams{Body: &blank}); err == nil {
		t.Fatal("expected an empty body to be rejected")
	}
	after, err := s.Status(ctx, "morning")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if after.Body != "Summarise the calendar." {
		t.Errorf("rejected patch still modified the file: %q", after.Body)
	}

	armed := true
	if _, err := s.Update(ctx, "nope", UpdateParams{Enabled: &armed}); err == nil {
		t.Error("expected an unknown schedule to error")
	}
}
