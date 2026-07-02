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
