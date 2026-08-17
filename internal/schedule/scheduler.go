package schedule

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/config"
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
		// A webhook-only schedule has no cadence to register. It is not an error:
		// it fires when its endpoint is called, not on a clock.
		if spec == "" {
			continue
		}
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
	Name             string              `json:"name"`
	Path             string              `json:"path"`
	Agent            string              `json:"agent"`
	Provider         config.Provider     `json:"provider"`
	Profile          string              `json:"profile"`
	Model            string              `json:"model"`
	Effort           string              `json:"effort"`
	Cron             string              `json:"cron"`
	Every            string              `json:"every"`
	Webhook          bool                `json:"webhook"`
	WebhookSecret    string              `json:"webhook_secret,omitempty"`
	RunPermission    RunPermission       `json:"run_permission"`
	AllowedTools     []string            `json:"allowed_tools"`
	Enabled          bool                `json:"enabled"`
	GoalID           string              `json:"goal_id,omitempty"`
	Project          string              `json:"project,omitempty"`
	CreatedBySession string              `json:"created_by_session,omitempty"`
	CreatedByAgent   string              `json:"created_by_agent,omitempty"`
	Body             string              `json:"body"`
	NextRun          *time.Time          `json:"next_run,omitempty"`
	ParseError       string              `json:"parse_error,omitempty"`
	Runs             []store.ScheduleRun `json:"runs"`
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
			Provider:      sc.Provider,
			Profile:       sc.Profile,
			Model:         sc.Model,
			Effort:        sc.Effort,
			Cron:          sc.Cron,
			Every:         sc.Every,
			Webhook:       sc.Webhook,
			WebhookSecret: sc.WebhookSecret,
			RunPermission: sc.RunPermission,
			AllowedTools:  sc.AllowedTools,
			Enabled:       sc.Enabled,
			GoalID:        sc.GoalID,
			Project:       sc.Project,

			CreatedBySession: sc.CreatedBySession,
			CreatedByAgent:   sc.CreatedByAgent,
			Body:             sc.Body,
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

// CreatedBySession returns the names of schedules an agent session authored,
// sorted. It scans the directory directly rather than going through List: the
// reverse-provenance view needs the names, not a run history query per schedule.
func (s *Scheduler) CreatedBySession(sessionID string) ([]string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	schedules, _, err := ScanDir(s.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sc := range schedules {
		if sc.CreatedBySession == sessionID {
			names = append(names, sc.Name)
		}
	}
	sort.Strings(names)
	return names, nil
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

// ErrWebhookUnauthorized means the caller did not present the schedule's
// webhook secret — or the schedule does not exist, or has no webhook trigger.
// The three are one error on purpose: the endpoint is reachable without the
// gateway token, so it must not reveal which schedules exist.
var ErrWebhookUnauthorized = errors.New("webhook unauthorized")

// ErrWebhookDisabled means the secret was valid but the schedule is parked
// (enabled: false), so it must not fire.
var ErrWebhookDisabled = errors.New("schedule disabled")

// PrepareWebhookRun authorizes an inbound webhook call and records the run it
// will produce. It deliberately stops short of starting the session: the caller
// gets the run record to answer the HTTP request with, then hands the work to
// ExecuteWebhookRun so a slow agent run never holds the sender's connection
// open.
func (s *Scheduler) PrepareWebhookRun(ctx context.Context, name, secret string) (Schedule, store.ScheduleRun, error) {
	sched, err := Parse(s.pathFor(name))
	if err != nil {
		s.log.Warn("schedule webhook rejected", "event", "schedule", "schedule", name, "reason", "parse", podiomlog.ErrorAttr(err))
		return Schedule{}, store.ScheduleRun{}, ErrWebhookUnauthorized
	}
	if !sched.Webhook {
		s.log.Warn("schedule webhook rejected", "event", "schedule", "schedule", name, "reason", "not_a_webhook")
		return Schedule{}, store.ScheduleRun{}, ErrWebhookUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(sched.WebhookSecret)) != 1 {
		s.log.Warn("schedule webhook rejected", "event", "schedule", "schedule", name, "reason", "bad_secret")
		return Schedule{}, store.ScheduleRun{}, ErrWebhookUnauthorized
	}
	// Checked after the secret: a parked schedule is a state its owner is
	// entitled to see, and they have just proven they own it.
	if !sched.Enabled {
		s.log.Info("schedule webhook skipped disabled", "event", "schedule", "schedule", name, "trigger", store.TriggerWebhook)
		return Schedule{}, store.ScheduleRun{}, ErrWebhookDisabled
	}

	run, err := s.store.CreateScheduleRun(ctx, store.ScheduleRun{
		ScheduleName: name,
		Trigger:      store.TriggerWebhook,
		Status:       store.RunRunning,
	})
	if err != nil {
		s.log.Warn("scheduled run failed", "event", "schedule", "schedule", name, "trigger", store.TriggerWebhook, "stage", "create_run", podiomlog.ErrorAttr(err))
		return Schedule{}, store.ScheduleRun{}, err
	}
	s.log.Info("schedule webhook triggered", "event", "schedule", "schedule", name, "run", run.ID, "trigger", store.TriggerWebhook)
	return sched, run, nil
}

// ExecuteWebhookRun runs a webhook-triggered run that PrepareWebhookRun already
// authorized and recorded. It runs on the scheduler's lifetime context, not the
// request's — the HTTP response is long gone by the time the session finishes,
// and a daemon shutdown is the only thing that should cancel it.
func (s *Scheduler) ExecuteWebhookRun(sched Schedule, run store.ScheduleRun, payload string) {
	if _, err := s.execute(s.ctx, sched.Name, sched, run, payload); err != nil {
		s.log.Warn("scheduled run failed", "event", "schedule", "schedule", sched.Name, "run", run.ID, "trigger", store.TriggerWebhook, podiomlog.ErrorAttr(err))
	}
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
	return s.execute(ctx, name, sched, run, "")
}

// execute runs an already-recorded scheduled run to completion: start the
// session, drain it, and persist the terminal status. It is shared by every
// trigger — cron, manual, and webhook — so the permission policy and the
// session's provenance cannot drift between them. payload is the webhook
// request body, empty for the other triggers.
func (s *Scheduler) execute(ctx context.Context, name string, sched Schedule, run store.ScheduleRun, payload string) (store.ScheduleRun, error) {
	started := time.Now()
	trigger := run.Trigger
	// A goal-linked schedule always runs yolo as part of the goal's autonomous
	// chain, regardless of the stored run_permission.
	yolo := sched.RunPermission == PermissionYolo || sched.GoalID != ""
	permission := "preapproved"
	if yolo {
		permission = "yolo"
	}
	s.log.Info("scheduled run started",
		"event", "schedule",
		"schedule", name,
		"run", run.ID,
		"trigger", trigger,
		"agent", sched.Agent,
		"goal", sched.GoalID,
		"project", sched.Project,
		"permission", permission,
		"allowed_tools", len(sched.AllowedTools),
	)

	sess, runErr := s.core.RunScheduled(ctx, core.ScheduledRunRequest{
		ScheduleName: name,
		RunID:        run.ID,
		AgentName:    sched.Agent,
		Provider:     sched.Provider,
		Profile:      sched.Profile,
		Model:        sched.Model,
		Effort:       sched.Effort,
		Yolo:         yolo,
		AllowedTools: sched.AllowedTools,
		Task:         s.scheduleTaskPrompt(ctx, name, sched.Body, payload),
		GoalID:       sched.GoalID,
		ProjectID:    sched.Project,
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

// scheduleAnswerLimit caps how many prior answered questions a run replays.
const scheduleAnswerLimit = 20

// webhookPayloadLimit caps how much of a webhook request body reaches the
// prompt. A sender is free to POST more; the run sees the first 8KB.
const webhookPayloadLimit = 8 << 10

// scheduleTaskPrompt prepends the answers the user gave to questions earlier
// runs of this schedule asked (via podiom_ask_user), so a recurring schedule
// carries those decisions forward and does not re-ask what was already settled.
// A webhook run also gets the request body that triggered it, so the task can
// react to what fired it rather than only that it fired.
func (s *Scheduler) scheduleTaskPrompt(ctx context.Context, name, body, payload string) string {
	answered, err := s.store.ListAnsweredAgentQuestions(ctx, store.AgentQuestionSchedule, name, scheduleAnswerLimit)
	if err != nil {
		s.log.Warn("scheduled run failed to load prior answers", "event", "schedule", "schedule", name, podiomlog.ErrorAttr(err))
		return withWebhookPayload(body, payload)
	}
	if len(answered) == 0 {
		return withWebhookPayload(body, payload)
	}
	var b strings.Builder
	b.WriteString("## Previously answered questions\n\n")
	b.WriteString("The user answered these in earlier runs of this schedule — treat them as settled and act on them; do not ask again.\n\n")
	for _, q := range answered {
		for _, item := range q.Questions {
			prompt := strings.TrimSpace(item.Question)
			if prompt == "" {
				prompt = strings.TrimSpace(item.Header)
			}
			if prompt == "" {
				continue
			}
			ans := strings.Join(q.Answers[item.ID], ", ")
			if strings.TrimSpace(ans) == "" {
				ans = "(no answer)"
			}
			fmt.Fprintf(&b, "- Q: %s\n  A: %s\n", prompt, ans)
		}
	}
	b.WriteString("\n---\n\n")
	b.WriteString(withWebhookPayload(body, payload))
	return b.String()
}

// withWebhookPayload appends the request body that triggered a webhook run to
// the task, fenced so the agent can tell the payload apart from its
// instructions. Returns body unchanged when there is no payload.
func withWebhookPayload(body, payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return body
	}
	truncated := ""
	if len(payload) > webhookPayloadLimit {
		payload = payload[:webhookPayloadLimit]
		truncated = "\n…(truncated)"
	}
	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\n## Webhook payload\n\n")
	b.WriteString("This run was triggered by a webhook. The request body was:\n\n")
	b.WriteString("```\n")
	b.WriteString(payload)
	b.WriteString(truncated)
	b.WriteString("\n```\n")
	return b.String()
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
	Provider      config.Provider
	Profile       string
	Model         string
	Effort        string
	Cron          string
	Every         string
	Webhook       bool
	WebhookSecret string
	RunPermission RunPermission
	AllowedTools  []string
	Enabled       bool
	GoalID        string
	// Project binds the runs to a project. Left empty on a goal-linked schedule,
	// Create fills it in from the goal.
	Project string
	// CreatedBySession/CreatedByAgent attribute the file to the agent session
	// that authored it. Left empty for schedules a human creates.
	CreatedBySession string
	CreatedByAgent   string
	Body             string
}

// UpdateParams patches an existing schedule. Every field is a pointer: nil means
// "leave this alone", so an update never has to restate the whole file.
//
// Two fields are deliberately absent. The name is the filename, so renaming
// means delete-and-recreate. GoalID is not patchable because linking a schedule
// to a goal silently forces yolo (see Create), and an ordinary edit is the wrong
// place for a permission escalation.
type UpdateParams struct {
	Agent    *string
	Provider *config.Provider
	Profile  *string
	Model    *string
	Effort   *string
	Cron     *string
	Every    *string
	// Webhook toggles the webhook trigger. Turning it on mints a secret if the
	// schedule has none; turning it off clears the secret, so toggling off and
	// back on is how a secret is rotated.
	Webhook       *bool
	RunPermission *RunPermission
	AllowedTools  *[]string
	Enabled       *bool
	// Project rebinds the runs to another project (or "" to unbind). Patchable
	// unlike GoalID: moving a schedule between projects changes where it works,
	// not how much it is trusted.
	Project *string
	Body    *string
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
	if s.core != nil {
		if err := s.core.ValidateRunTargetForAgent(ctx, p.Agent, core.RunTarget{
			Provider: p.Provider,
			Profile:  p.Profile,
			Model:    p.Model,
			Effort:   p.Effort,
		}); err != nil {
			return Status{}, err
		}
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "create_dir", podiomlog.ErrorAttr(err))
		return Status{}, fmt.Errorf("create schedules dir: %w", err)
	}
	path := s.pathFor(name)
	if _, err := os.Stat(path); err == nil {
		s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "exists")
		return Status{}, fmt.Errorf("schedule %q already exists", name)
	}

	if p.GoalID != "" {
		// Schedules created for a goal run yolo transparently: the schedule file
		// records run_permission: yolo so the goal's autonomous posture is visible
		// on disk, not just enforced at fire time.
		p.RunPermission = PermissionYolo
		// Same reasoning for the project: record it in the file so the workspace
		// the runs will use is visible on disk. An explicit project wins, because
		// a goal's plan may legitimately put a schedule in another project.
		if strings.TrimSpace(p.Project) == "" && s.core != nil {
			p.Project = s.core.GoalProjectID(ctx, p.GoalID)
		}
	}
	if p.Project != "" && s.core != nil {
		if _, err := s.core.GetProject(ctx, p.Project); err != nil {
			s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "project", podiomlog.ErrorAttr(err))
			return Status{}, err
		}
	}
	if p.RunPermission == "" {
		p.RunPermission = PermissionPreapproved
	}
	// A webhook trigger is only ever created with a secret, so the endpoint it
	// opens is never callable without one.
	if p.Webhook && p.WebhookSecret == "" {
		secret, serr := newWebhookSecret()
		if serr != nil {
			s.log.Warn("schedule create failed", "event", "schedule", "schedule", name, "stage", "webhook_secret", podiomlog.ErrorAttr(serr))
			return Status{}, serr
		}
		p.WebhookSecret = secret
	}
	// A schedule someone deliberately created is armed; the off switch is an
	// explicit later edit (Update) rather than a step you have to remember here.
	p.Enabled = true
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

// Status returns one schedule's full state, including its body and recent runs.
func (s *Scheduler) Status(ctx context.Context, name string) (Status, error) {
	name = Slug(name)
	if name == "" {
		return Status{}, fmt.Errorf("schedule name is required")
	}
	statuses, err := s.List(ctx)
	if err != nil {
		return Status{}, err
	}
	for _, st := range statuses {
		if st.Name == name {
			return st, nil
		}
	}
	return Status{}, fmt.Errorf("schedule %q not found", name)
}

// Update patches an existing schedule file in place. It reads the current file,
// applies the supplied fields, and re-renders the whole thing — so every key the
// caller did not mention (including the creator attribution) survives untouched,
// and the file format can never drift between the create and update paths.
//
// Validation happens before the write, exactly as Create does, so a bad patch
// leaves the file on disk unchanged rather than replacing a working schedule
// with a broken one.
func (s *Scheduler) Update(ctx context.Context, name string, p UpdateParams) (Status, error) {
	started := time.Now()
	name = Slug(name)
	if name == "" {
		return Status{}, fmt.Errorf("schedule name is required")
	}
	path := s.pathFor(name)
	current, err := Parse(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Status{}, fmt.Errorf("schedule %q not found", name)
		}
		return Status{}, err
	}

	next := CreateParams{
		Name:          name,
		Agent:         current.Agent,
		Provider:      current.Provider,
		Profile:       current.Profile,
		Model:         current.Model,
		Effort:        current.Effort,
		Cron:          current.Cron,
		Every:         current.Every,
		Webhook:       current.Webhook,
		WebhookSecret: current.WebhookSecret,
		RunPermission: current.RunPermission,
		AllowedTools:  current.AllowedTools,
		Enabled:       current.Enabled,
		GoalID:        current.GoalID,
		Project:       current.Project,

		CreatedBySession: current.CreatedBySession,
		CreatedByAgent:   current.CreatedByAgent,
		Body:             current.Body,
	}

	var changed []string
	if p.Agent != nil {
		next.Agent = strings.TrimSpace(*p.Agent)
		changed = append(changed, "agent")
	}
	if p.Provider != nil {
		next.Provider = *p.Provider
		changed = append(changed, "provider")
	}
	if p.Profile != nil {
		next.Profile = strings.TrimSpace(*p.Profile)
		changed = append(changed, "profile")
	}
	if p.Model != nil {
		next.Model = strings.TrimSpace(*p.Model)
		changed = append(changed, "model")
	}
	if p.Effort != nil {
		next.Effort = strings.TrimSpace(*p.Effort)
		changed = append(changed, "effort")
	}
	// Cadence is exclusive: setting one clears the other, so a caller switching
	// from cron to every does not have to know to blank the old field.
	if p.Cron != nil {
		next.Cron = strings.TrimSpace(*p.Cron)
		if next.Cron != "" {
			next.Every = ""
		}
		changed = append(changed, "cron")
	}
	if p.Every != nil {
		next.Every = strings.TrimSpace(*p.Every)
		if next.Every != "" {
			next.Cron = ""
		}
		changed = append(changed, "every")
	}
	if p.Webhook != nil {
		next.Webhook = *p.Webhook
		switch {
		case next.Webhook && next.WebhookSecret == "":
			secret, serr := newWebhookSecret()
			if serr != nil {
				s.log.Warn("schedule update failed", "event", "schedule", "schedule", name, "stage", "webhook_secret", podiomlog.ErrorAttr(serr))
				return Status{}, serr
			}
			next.WebhookSecret = secret
		case !next.Webhook:
			// Turning the trigger off retires its secret with it, so a URL that
			// leaked while it was on cannot be revived by turning it back on.
			next.WebhookSecret = ""
		}
		changed = append(changed, "webhook")
	}
	if p.RunPermission != nil {
		next.RunPermission = *p.RunPermission
		changed = append(changed, "run_permission")
	}
	if p.AllowedTools != nil {
		next.AllowedTools = *p.AllowedTools
		changed = append(changed, "allowed_tools")
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
		changed = append(changed, "enabled")
	}
	if p.Project != nil {
		next.Project = strings.TrimSpace(*p.Project)
		if next.Project != "" && s.core != nil {
			if _, err := s.core.GetProject(ctx, next.Project); err != nil {
				s.log.Warn("schedule update failed", "event", "schedule", "schedule", name, "stage", "project", podiomlog.ErrorAttr(err))
				return Status{}, err
			}
		}
		changed = append(changed, "project")
	}
	if p.Body != nil {
		next.Body = strings.TrimSpace(*p.Body)
		changed = append(changed, "body")
	}
	if len(changed) == 0 {
		return s.Status(ctx, name)
	}

	if s.core != nil {
		if err := s.core.ValidateRunTargetForAgent(ctx, next.Agent, core.RunTarget{
			Provider: next.Provider,
			Profile:  next.Profile,
			Model:    next.Model,
			Effort:   next.Effort,
		}); err != nil {
			return Status{}, err
		}
	}

	content := Render(next)
	if _, err := parseBytes(path, []byte(content)); err != nil {
		s.log.Warn("schedule update failed", "event", "schedule", "schedule", name, "stage", "parse", podiomlog.ErrorAttr(err))
		return Status{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		s.log.Warn("schedule update failed", "event", "schedule", "schedule", name, "stage", "write", podiomlog.ErrorAttr(err))
		return Status{}, fmt.Errorf("write schedule %q: %w", name, err)
	}
	s.Sync()

	// Field names only: a schedule body can be long, and the values are on disk.
	s.log.Info("schedule updated", "event", "schedule", "schedule", name, "fields", strings.Join(changed, ","), "enabled", next.Enabled, podiomlog.DurationMS("duration_ms", time.Since(started)))
	return s.Status(ctx, name)
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
