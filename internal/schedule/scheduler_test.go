package schedule

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/projects"
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
	if _, err := c.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	writeSchedule(t, paths.SchedulesDir, "morning.md", `---
agent: jared
cron: "0 7 * * *"
run_permission: preapproved
enabled: true
project: mission-control
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
	if sess.ProjectID != "mission-control" || sess.InheritedProjectID != "mission-control" {
		t.Fatalf("session project binding = project %q inherited %q, want mission-control", sess.ProjectID, sess.InheritedProjectID)
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

// webhookSchedule is the file a webhook test drives: no cadence, so the only
// way it can ever run is through its endpoint.
const webhookSchedule = `---
agent: jared
webhook: true
webhook_secret: s3cr3t
enabled: true
---
React to the push.
`

// TestSyncSkipsWebhookOnlySchedule pins that a schedule with no cadence is
// neither registered to fire on a clock nor reported as broken. Registering it
// would mean handing robfig/cron an empty spec, which errors and would surface
// as a parse error on a perfectly valid file.
func TestSyncSkipsWebhookOnlySchedule(t *testing.T) {
	ctx := context.Background()
	s, _, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "on-push.md", webhookSchedule)
	s.cron.Start()

	statuses, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected one schedule, got %d: %+v", len(statuses), statuses)
	}
	st := statuses[0]
	if st.ParseError != "" {
		t.Fatalf("webhook-only schedule should be valid, got parse error %q", st.ParseError)
	}
	if st.NextRun != nil {
		t.Fatalf("webhook-only schedule has no cadence, so no next run: %+v", st.NextRun)
	}
	if !st.Webhook || st.WebhookSecret != "s3cr3t" {
		t.Fatalf("status should carry the webhook trigger: %+v", st)
	}
}

// TestWebhookRunRecordsTriggerAndPayload covers the happy path end to end: the
// secret authorizes, the run is recorded as webhook-triggered, and the request
// body reaches the agent's prompt so the run can react to what fired it.
func TestWebhookRunRecordsTriggerAndPayload(t *testing.T) {
	ctx := context.Background()
	s, c, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "on-push.md", webhookSchedule)

	sched, run, err := s.PrepareWebhookRun(ctx, "on-push", "s3cr3t")
	if err != nil {
		t.Fatalf("prepare webhook run: %v", err)
	}
	if run.Trigger != store.TriggerWebhook || run.Status != store.RunRunning {
		t.Fatalf("unexpected prepared run: %+v", run)
	}
	s.ExecuteWebhookRun(sched, run, `{"event":"push","ref":"main"}`)

	runs, err := c.Store().ListScheduleRuns(ctx, "on-push", 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one recorded run, got %d", len(runs))
	}
	finished := runs[0]
	if finished.Status != store.RunSuccess {
		t.Fatalf("run status = %q, want success (%q)", finished.Status, finished.Error)
	}
	if finished.Trigger != store.TriggerWebhook || finished.SessionID == "" {
		t.Fatalf("unexpected finished run: %+v", finished)
	}

	messages, err := c.Store().ListMessages(ctx, finished.SessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("expected the run to have prompted the agent")
	}
	prompt := messages[0].Content
	for _, want := range []string{"React to the push.", "## Webhook payload", `{"event":"push","ref":"main"}`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// TestPrepareWebhookRunRejections pins that every way of failing authorization
// looks the same to the caller and leaves no run behind. The endpoint is
// reachable without the gateway token, so a rejection must not double as a
// probe for which schedules exist.
func TestPrepareWebhookRunRejections(t *testing.T) {
	ctx := context.Background()
	s, c, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "on-push.md", webhookSchedule)
	writeSchedule(t, paths.SchedulesDir, "clock-only.md", `---
agent: jared
cron: "0 7 * * *"
enabled: true
---
Tick.
`)

	cases := []struct {
		name     string
		schedule string
		secret   string
	}{
		{"wrong secret", "on-push", "guess"},
		{"empty secret", "on-push", ""},
		{"unknown schedule", "ghost", "s3cr3t"},
		{"schedule has no webhook trigger", "clock-only", "s3cr3t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.PrepareWebhookRun(ctx, tc.schedule, tc.secret); !errors.Is(err, ErrWebhookUnauthorized) {
				t.Fatalf("err = %v, want ErrWebhookUnauthorized", err)
			}
			runs, err := c.Store().ListScheduleRuns(ctx, tc.schedule, 10)
			if err != nil {
				t.Fatalf("list runs: %v", err)
			}
			if len(runs) != 0 {
				t.Fatalf("a rejected webhook must not record a run, got %+v", runs)
			}
		})
	}
}

// TestPrepareWebhookRunRejectsDisabled pins that parking a schedule stops its
// webhook too — otherwise enabled: false would only be half an off switch.
func TestPrepareWebhookRunRejectsDisabled(t *testing.T) {
	ctx := context.Background()
	s, _, paths, cleanup := newTestScheduler(t)
	defer cleanup()

	writeSchedule(t, paths.SchedulesDir, "on-push.md", `---
agent: jared
webhook: true
webhook_secret: s3cr3t
enabled: false
---
React to the push.
`)

	if _, _, err := s.PrepareWebhookRun(ctx, "on-push", "s3cr3t"); !errors.Is(err, ErrWebhookDisabled) {
		t.Fatalf("err = %v, want ErrWebhookDisabled", err)
	}
}

// TestUpdateWebhookMintsAndRetiresSecret pins the rotation story: there is no
// separate rotate call, so toggling the trigger off and back on must produce a
// different secret rather than restoring the old one.
func TestUpdateWebhookMintsAndRetiresSecret(t *testing.T) {
	ctx := context.Background()
	s, _, _, cleanup := newTestScheduler(t)
	defer cleanup()

	created, err := s.Create(ctx, CreateParams{Name: "on-push", Agent: "jared", Webhook: true, Body: "React."})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.Webhook || created.WebhookSecret == "" {
		t.Fatalf("create should mint a secret: %+v", created)
	}
	first := created.WebhookSecret

	off := false
	parked, err := s.Update(ctx, "on-push", UpdateParams{Webhook: &off, Cron: strPtr("0 7 * * *")})
	if err != nil {
		t.Fatalf("update off: %v", err)
	}
	if parked.Webhook || parked.WebhookSecret != "" {
		t.Fatalf("turning the trigger off should retire its secret: %+v", parked)
	}

	on := true
	back, err := s.Update(ctx, "on-push", UpdateParams{Webhook: &on})
	if err != nil {
		t.Fatalf("update on: %v", err)
	}
	if back.WebhookSecret == "" || back.WebhookSecret == first {
		t.Fatalf("re-enabling should mint a fresh secret, got %q (was %q)", back.WebhookSecret, first)
	}
}

func strPtr(s string) *string { return &s }

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

// A schedule created for a goal records the goal's project in the file, for the
// same reason it records run_permission: yolo — the workspace its runs will use
// should be visible on disk, not only derived at fire time.
func TestCreateStampsGoalProject(t *testing.T) {
	ctx := context.Background()
	s, coreSvc, _, cleanup := newTestScheduler(t)
	defer cleanup()

	for _, id := range []string{"mission-control", "beta"} {
		if _, err := coreSvc.CreateProject(ctx, projects.Project{ID: id, Name: id}); err != nil {
			t.Fatalf("create project %q: %v", id, err)
		}
	}
	goal, err := coreSvc.CreateGoal(ctx, store.Goal{Title: "Ship it", LeadAgent: "jared", ProjectID: "mission-control"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	inherited, err := s.Create(ctx, CreateParams{
		Name:   "goal-sched",
		Agent:  "jared",
		Cron:   "0 7 * * *",
		GoalID: goal.ID,
		Body:   "Do the recurring thing.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if inherited.Project != "mission-control" {
		t.Fatalf("goal schedule project = %q, want mission-control", inherited.Project)
	}
	onDisk, err := os.ReadFile(inherited.Path)
	if err != nil {
		t.Fatalf("read schedule file: %v", err)
	}
	if !strings.Contains(string(onDisk), "project: mission-control") {
		t.Fatalf("goal's project is not visible on disk:\n%s", onDisk)
	}

	// Explicit wins: a goal's plan may put one schedule in another project.
	explicit, err := s.Create(ctx, CreateParams{
		Name:    "goal-sched-elsewhere",
		Agent:   "jared",
		Cron:    "0 8 * * *",
		GoalID:  goal.ID,
		Project: "beta",
		Body:    "Do the other thing.",
	})
	if err != nil {
		t.Fatalf("create with explicit project: %v", err)
	}
	if explicit.Project != "beta" {
		t.Fatalf("explicit schedule project = %q, want beta", explicit.Project)
	}

	// An unknown project fails at create time rather than producing a schedule
	// whose runs would silently land in no project at all.
	if _, err := s.Create(ctx, CreateParams{
		Name:    "bad-project",
		Agent:   "jared",
		Cron:    "0 9 * * *",
		Project: "nope",
		Body:    "Should not exist.",
	}); err == nil {
		t.Fatal("expected an unknown project to be rejected")
	}
}

// TestUpdatePreservesUnpatchedFields pins the contract that makes Update safe to
// hand an agent: a partial patch keeps everything it did not mention — including
// the creator attribution and the body — and parking a schedule stops it firing
// without destroying it.
func TestUpdatePreservesUnpatchedFields(t *testing.T) {
	ctx := context.Background()
	s, coreSvc, _, cleanup := newTestScheduler(t)
	defer cleanup()

	if _, err := coreSvc.CreateProject(ctx, projects.Project{ID: "mission-control", Name: "Mission Control"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	created, err := s.Create(ctx, CreateParams{
		Name:             "morning",
		Agent:            "jared",
		Cron:             "0 7 * * *",
		GoalID:           "goal-1",
		Project:          "mission-control",
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
	if updated.Project != "mission-control" {
		t.Errorf("update dropped the project binding: %q", updated.Project)
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
