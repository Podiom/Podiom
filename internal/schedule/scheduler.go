package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/core"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/store"
	cron "github.com/robfig/cron/v3"
)

// resyncInterval controls how often the scheduler rescans the schedules
// directory so newly dropped, edited, or removed files take effect without a
// daemon restart (R7.2).
const resyncInterval = 15 * time.Second

// recentRunLimit bounds how many runs List reports per schedule.
const recentRunLimit = 7

// Options configures a Scheduler.
type Options struct {
	Dir    string
	Core   *core.Core
	Store  *store.Store
	Logger *slog.Logger
}

// Scheduler scans schedule files, registers cron jobs inside podiomd, and runs
// each fired job as a normal Podiom session (R7.1 / R7.3a).
type Scheduler struct {
	dir   string
	core  *core.Core
	store *store.Store
	log   *slog.Logger
	cron  *cron.Cron

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	jobs      map[string]*job
	parseErrs map[string]error
}

type job struct {
	spec    string
	entryID cron.EntryID
}

// New constructs a Scheduler. Call Start to begin firing.
func New(opts Options) *Scheduler {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		dir:       opts.Dir,
		core:      opts.Core,
		store:     opts.Store,
		log:       log,
		cron:      cron.New(),
		ctx:       ctx,
		cancel:    cancel,
		jobs:      map[string]*job{},
		parseErrs: map[string]error{},
	}
}

// Start performs an initial scan, starts the cron loop, and begins periodic
// resyncing of the schedules directory.
func (s *Scheduler) Start() {
	s.log.Info("scheduler started", "event", "schedule", "dir", s.dir)
	s.Sync()
	s.cron.Start()
	go s.resyncLoop()
}

// Stop cancels in-flight runs and stops the cron loop.
func (s *Scheduler) Stop() {
	s.log.Info("scheduler stopped", "event", "schedule", "dir", s.dir)
	s.cancel()
	s.cron.Stop()
}

func (s *Scheduler) resyncLoop() {
	ticker := time.NewTicker(resyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.Sync()
			s.pickupDueTasks(s.ctx)
			s.pickupDueGoalReviews(s.ctx)
		}
	}
}

// pickupDueTasks starts any backlog task whose scheduled pickup time has passed,
// running it unattended under the preapproved policy. Starting a task moves it to
// in_progress, so it is not picked up twice.
func (s *Scheduler) pickupDueTasks(ctx context.Context) {
	started := time.Now()
	s.log.Info("due task check started", "event", "schedule")
	now := time.Now().UTC().Format(time.RFC3339)
	due, err := s.store.ListDueTasks(ctx, now)
	if err != nil {
		s.log.Warn("due task check failed", "event", "schedule", podiomlog.ErrorAttr(err), podiomlog.DurationMS("duration_ms", time.Since(started)))
		return
	}
	for _, task := range due {
		s.log.Info("due task found", "event", "schedule", "task", task.ID, "project", task.ProjectID, "agent", task.AssignedAgent, "pickup_at", task.PickupAt)
		s.log.Info("task pickup started", "event", "schedule", "task", task.ID, "project", task.ProjectID, "agent", task.AssignedAgent, "unattended", true)
		sess, err := s.core.StartTask(ctx, core.StartTaskRequest{TaskID: task.ID, Unattended: true})
		if err != nil {
			s.log.Warn("task pickup failed", "event", "schedule", "task", task.ID, "project", task.ProjectID, "agent", task.AssignedAgent, podiomlog.ErrorAttr(err))
			continue
		}
		s.log.Info("task picked up", "event", "schedule", "task", task.ID, "project", task.ProjectID, "agent", task.AssignedAgent, "session", sess.ID, "unattended", true)
	}
	s.log.Info("due task check finished", "event", "schedule", "due_tasks", len(due), podiomlog.DurationMS("duration_ms", time.Since(started)))
}

// pickupDueGoalReviews fires an unattended review session for every active goal
// whose next_review_at has arrived. The clock is advanced BEFORE the review
// runs, so a long or crashed review can neither double-fire nor stall the
// cadence; pausing or closing a goal stops reviews atomically because the due
// query filters on live status.
func (s *Scheduler) pickupDueGoalReviews(ctx context.Context) {
	started := time.Now()
	now := time.Now().UTC().Format(time.RFC3339)
	due, err := s.store.ListDueGoalReviews(ctx, now)
	if err != nil {
		s.log.Warn("due goal check failed", "event", "goal", podiomlog.ErrorAttr(err), podiomlog.DurationMS("duration_ms", time.Since(started)))
		return
	}
	for _, goal := range due {
		s.log.Info("due goal review found", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent, "next_review_at", goal.NextReviewAt)
		if err := s.core.AdvanceGoalReviewClock(ctx, goal.ID); err != nil {
			s.log.Warn("goal review clock advance failed", "event", "goal", "goal", goal.ID, podiomlog.ErrorAttr(err))
			continue
		}
		sess, err := s.core.RunGoalReview(ctx, goal.ID)
		if err != nil {
			s.log.Warn("goal review failed", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent, podiomlog.ErrorAttr(err))
			continue
		}
		s.log.Info("goal review finished", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent, "session", sess.ID)
	}
	if len(due) > 0 {
		s.log.Info("due goal check finished", "event", "goal", "due_goals", len(due), podiomlog.DurationMS("duration_ms", time.Since(started)))
	}
}

// Sync reconciles registered cron jobs with the current contents of the
// schedules directory: new or changed enabled files are (re)registered, and
// disabled or removed files are unregistered so they no longer fire (R7.2a).
func (s *Scheduler) Sync() {
	started := time.Now()
	s.log.Info("schedule scan started", "event", "schedule", "dir", s.dir)
	schedules, parseErrs, err := ScanDir(s.dir)
	if err != nil {
		s.log.Warn("schedule scan failed", "event", "schedule", "dir", s.dir, podiomlog.ErrorAttr(err), podiomlog.DurationMS("duration_ms", time.Since(started)))
		return
	}

	desired := make(map[string]Schedule, len(schedules))
	for _, sc := range schedules {
		desired[sc.Name] = sc
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.parseErrs = parseErrs
	registered, removed, unchanged := 0, 0, 0

	// Drop jobs that are gone, now disabled, or whose timing changed.
	for name, j := range s.jobs {
		sc, ok := desired[name]
		if !ok || !sc.Enabled || sc.CronSpec() != j.spec {
			s.cron.Remove(j.entryID)
			delete(s.jobs, name)
			removed++
			s.log.Info("schedule job removed", "event", "schedule", "schedule", name)
		}
	}

	// Register new or re-enabled jobs.
	for name, sc := range desired {
		if !sc.Enabled {
			continue
		}
		if _, ok := s.jobs[name]; ok {
			unchanged++
			continue
		}
		spec := sc.CronSpec()
		jobName := name
		entryID, err := s.cron.AddFunc(spec, func() { s.fire(jobName) })
		if err != nil {
			s.parseErrs[name] = fmt.Errorf("invalid schedule timing %q: %w", spec, err)
			s.log.Warn("schedule job registration failed", "event", "schedule", "schedule", name, "spec", spec, podiomlog.ErrorAttr(err))
			continue
		}
		s.jobs[name] = &job{spec: spec, entryID: entryID}
		registered++
		s.log.Info("schedule job registered", "event", "schedule", "schedule", name, "spec", spec, "agent", sc.Agent)
	}
	for name, perr := range parseErrs {
		s.log.Warn("schedule parse failed", "event", "schedule", "schedule", name, podiomlog.ErrorAttr(perr))
	}
	s.log.Info("schedule scan finished",
		"event", "schedule",
		"files", len(schedules)+len(parseErrs),
		"enabled", enabledScheduleCount(schedules),
		"parse_errors", len(parseErrs),
		"registered", registered,
		"removed", removed,
		"unchanged", unchanged,
		"jobs", len(s.jobs),
		podiomlog.DurationMS("duration_ms", time.Since(started)),
	)
}

// Status is a schedule's current state for the CLI/API: its frontmatter, whether
// it is registered, its next fire time, any parse error, and recent runs.
type Status struct {
	Name          string              `json:"name"`
	Path          string              `json:"path"`
	Agent         string              `json:"agent"`
	Model         string              `json:"model"`
	Effort        string              `json:"effort"`
	Cron          string              `json:"cron"`
	Every         string              `json:"every"`
	RunPermission RunPermission       `json:"run_permission"`
	AllowedTools  []string            `json:"allowed_tools"`
	Enabled       bool                `json:"enabled"`
	NextRun       *time.Time          `json:"next_run,omitempty"`
	ParseError    string              `json:"parse_error,omitempty"`
	Runs          []store.ScheduleRun `json:"runs"`
}

// List returns the current state of every schedule file, newest-run-aware and
// sorted by name. It resyncs first so freshly dropped files appear.
func (s *Scheduler) List(ctx context.Context) ([]Status, error) {
	s.Sync()
	schedules, parseErrs, err := ScanDir(s.dir)
	if err != nil {
		return nil, err
	}

	var out []Status
	for _, sc := range schedules {
		status := Status{
			Name:          sc.Name,
			Path:          sc.Path,
			Agent:         sc.Agent,
			Model:         sc.Model,
			Effort:        sc.Effort,
			Cron:          sc.Cron,
			Every:         sc.Every,
			RunPermission: sc.RunPermission,
			AllowedTools:  sc.AllowedTools,
			Enabled:       sc.Enabled,
		}
		if next := s.nextRun(sc.Name); next != nil {
			status.NextRun = next
		}
		runs, err := s.store.ListScheduleRuns(ctx, sc.Name, recentRunLimit)
		if err != nil {
			return nil, err
		}
		status.Runs = runs
		out = append(out, status)
	}
	for name, perr := range parseErrs {
		out = append(out, Status{Name: name, ParseError: perr.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Scheduler) nextRun(name string) *time.Time {
	s.mu.Lock()
	j, ok := s.jobs[name]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	entry := s.cron.Entry(j.entryID)
	if entry.Next.IsZero() {
		return nil
	}
	next := entry.Next
	return &next
}

// RunNow triggers a manual run of a schedule immediately. A disabled schedule
// can still be run manually; only automatic firing is suppressed when disabled.
func (s *Scheduler) RunNow(ctx context.Context, name string) (store.ScheduleRun, error) {
	s.log.Info("schedule manual run requested", "event", "schedule", "schedule", name, "trigger", store.TriggerManual)
	s.Sync()
	if _, err := os.Stat(s.pathFor(name)); err != nil {
		s.log.Warn("schedule manual run failed", "event", "schedule", "schedule", name, "trigger", store.TriggerManual, podiomlog.ErrorAttr(err))
		return store.ScheduleRun{}, fmt.Errorf("schedule %q not found", name)
	}
	return s.run(ctx, name, store.TriggerManual)
}

// fire is the cron callback. It runs the job in the scheduler's lifetime context
// so a daemon shutdown cancels it.
func (s *Scheduler) fire(name string) {
	s.log.Info("schedule cron triggered", "event", "schedule", "schedule", name, "trigger", store.TriggerCron)
	if _, err := s.run(s.ctx, name, store.TriggerCron); err != nil {
		s.log.Warn("scheduled run failed", "event", "schedule", "schedule", name, "trigger", store.TriggerCron, podiomlog.ErrorAttr(err))
	}
}

// run executes one scheduled run end to end: re-parse the file (honoring the
// latest edits and the enabled switch), record a run, execute it as a normal
// Podiom session, and persist the terminal status.
func (s *Scheduler) run(ctx context.Context, name string, trigger store.RunTrigger) (store.ScheduleRun, error) {
	started := time.Now()
	sched, err := Parse(s.pathFor(name))
	if err != nil {
		s.log.Warn("scheduled run failed", "event", "schedule", "schedule", name, "trigger", trigger, "stage", "parse", podiomlog.ErrorAttr(err))
		return store.ScheduleRun{}, err
	}
	// Defensive: a disabled file must never fire automatically even if a cron
	// entry briefly outlives a resync (R7.2a).
	if trigger == store.TriggerCron && !sched.Enabled {
		s.log.Info("schedule cron skipped disabled", "event", "schedule", "schedule", name, "trigger", trigger)
		return store.ScheduleRun{}, nil
	}

	run, err := s.store.CreateScheduleRun(ctx, store.ScheduleRun{
		ScheduleName: name,
		Trigger:      trigger,
		Status:       store.RunRunning,
	})
	if err != nil {
		s.log.Warn("scheduled run failed", "event", "schedule", "schedule", name, "trigger", trigger, "stage", "create_run", podiomlog.ErrorAttr(err), podiomlog.DurationMS("duration_ms", time.Since(started)))
		return store.ScheduleRun{}, err
	}
	permission := "preapproved"
	if sched.RunPermission == PermissionYolo {
		permission = "yolo"
	}
	s.log.Info("scheduled run started",
		"event", "schedule",
		"schedule", name,
		"run", run.ID,
		"trigger", trigger,
		"agent", sched.Agent,
		"permission", permission,
		"allowed_tools", len(sched.AllowedTools),
	)

	sess, runErr := s.core.RunScheduled(ctx, core.ScheduledRunRequest{
		ScheduleName: name,
		RunID:        run.ID,
		AgentName:    sched.Agent,
		Model:        sched.Model,
		Effort:       sched.Effort,
		Yolo:         sched.RunPermission == PermissionYolo,
		AllowedTools: sched.AllowedTools,
		Task:         sched.Body,
	})

	status := store.RunSuccess
	errMsg := ""
	if runErr != nil {
		status = store.RunError
		errMsg = runErr.Error()
	}
	finished, ferr := s.store.FinishScheduleRun(ctx, run.ID, sess.ID, status, errMsg)
	if ferr != nil {
		s.log.Warn("scheduled run failed", "event", "schedule", "schedule", name, "run", run.ID, "trigger", trigger, "stage", "finish_run", "session", sess.ID, podiomlog.ErrorAttr(ferr), podiomlog.DurationMS("duration_ms", time.Since(started)))
		return run, ferr
	}
	s.log.Info("scheduled run finished",
		"event", "schedule", "schedule", name, "run", run.ID, "trigger", trigger, "status", status, "session", sess.ID, podiomlog.DurationMS("duration_ms", time.Since(started)))
	return finished, runErr
}

// Delete removes a schedule: it deletes the markdown file, resyncs so the cron
// entry is unregistered (Sync drops jobs whose file is gone, R7.2a), and clears
// the schedule's run history. The sessions those runs produced are preserved.
func (s *Scheduler) Delete(ctx context.Context, name string) error {
	started := time.Now()
	s.log.Info("schedule delete requested", "event", "schedule", "schedule", name)
	path := s.pathFor(name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			s.log.Warn("schedule delete failed", "event", "schedule", "schedule", name, podiomlog.ErrorAttr(err))
			return fmt.Errorf("schedule %q not found", name)
		}
		s.log.Warn("schedule delete failed", "event", "schedule", "schedule", name, podiomlog.ErrorAttr(err))
		return err
	}
	if err := os.Remove(path); err != nil {
		s.log.Warn("schedule delete failed", "event", "schedule", "schedule", name, podiomlog.ErrorAttr(err))
		return fmt.Errorf("delete schedule %q: %w", name, err)
	}
	s.Sync()
	if err := s.store.DeleteScheduleRuns(ctx, name); err != nil {
		s.log.Warn("schedule delete failed", "event", "schedule", "schedule", name, "stage", "delete_runs", podiomlog.ErrorAttr(err))
		return err
	}
	s.log.Info("schedule deleted", "event", "schedule", "schedule", name, podiomlog.DurationMS("duration_ms", time.Since(started)))
	return nil
}

func (s *Scheduler) pathFor(name string) string {
	return filepath.Join(s.dir, name+".md")
}

// CreateParams describes a new schedule file to author.
type CreateParams struct {
	Name          string
	Agent         string
	Model         string
	Effort        string
	Cron          string
	Every         string
	RunPermission RunPermission
	AllowedTools  []string
	Body          string
}

// Create authors a new schedule markdown file under the schedules directory,
// validates it by parsing it back, registers it, and returns its status. It
// errors if a schedule with the same name already exists.
func (s *Scheduler) Create(ctx context.Context, p CreateParams) (Status, error) {
	started := time.Now()
	name := Slug(p.Name)
	if name == "" {
		return Status{}, fmt.Errorf("schedule name is required")
	}
	s.log.Info("schedule create requested", "event", "schedule", "schedule", name, "agent", p.Agent, "permission", p.RunPermission, "allowed_tools", len(p.AllowedTools))
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "create_dir", podiomlog.ErrorAttr(err))
		return Status{}, fmt.Errorf("create schedules dir: %w", err)
	}
	path := s.pathFor(name)
	if _, err := os.Stat(path); err == nil {
		s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "exists")
		return Status{}, fmt.Errorf("schedule %q already exists", name)
	}

	if p.RunPermission == "" {
		p.RunPermission = PermissionPreapproved
	}
	content := Render(p)
	// Validate before committing the file to disk so we never leave an invalid
	// schedule lying around.
	if _, err := parseBytes(path, []byte(content)); err != nil {
		s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "parse", podiomlog.ErrorAttr(err))
		return Status{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "write", podiomlog.ErrorAttr(err))
		return Status{}, fmt.Errorf("write schedule %q: %w", name, err)
	}

	s.Sync()
	statuses, err := s.List(ctx)
	if err != nil {
		return Status{}, err
	}
	for _, st := range statuses {
		if st.Name == name {
			s.log.Info("schedule created", "event", "schedule", "schedule", name, "agent", st.Agent, "enabled", st.Enabled, "permission", st.RunPermission, "allowed_tools", len(st.AllowedTools), podiomlog.DurationMS("duration_ms", time.Since(started)))
			return st, nil
		}
	}
	s.log.Info("schedule created", "event", "schedule", "schedule", name, podiomlog.DurationMS("duration_ms", time.Since(started)))
	return Status{Name: name, Path: path}, nil
}

func enabledScheduleCount(schedules []Schedule) int {
	count := 0
	for _, sc := range schedules {
		if sc.Enabled {
			count++
		}
	}
	return count
}
