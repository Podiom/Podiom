package store

import "github.com/Podiom/Podiom/internal/config"

// SessionOrigin records where a session was created. It is provenance and is
// immutable after creation.
type SessionOrigin string

const (
	// OriginWeb marks a session created from the web UI.
	OriginWeb SessionOrigin = "web"
	// OriginCLI marks a session created from the CLI.
	OriginCLI SessionOrigin = "cli"
	// OriginOnboarding marks the first-run birth session that helps shape an
	// agent's SOUL.md.
	OriginOnboarding SessionOrigin = "onboarding"
	// OriginSchedule marks a session created by a scheduled run.
	OriginSchedule SessionOrigin = "schedule"
	// OriginRoadmap marks a session created from a roadmap task.
	OriginRoadmap SessionOrigin = "roadmap"
	// OriginGoal marks a session created by a goal's planning or review loop.
	OriginGoal SessionOrigin = "goal"
)

// PlanState records whether a session is mechanically gated for plan mode.
type PlanState string

const (
	// PlanNone means the session is not gated by plan mode.
	PlanNone PlanState = "none"
	// PlanPendingSubmission means plan mode is explicit and the agent still
	// needs to submit the first plan.
	PlanPendingSubmission PlanState = "pending_submission"
	// PlanAwaitingApproval means a submitted plan is waiting on the user.
	PlanAwaitingApproval PlanState = "awaiting_approval"
)

// PlanInfo is the displayable plan artifact attached to a session.
type PlanInfo struct {
	FilePath    string `json:"file_path"`
	Markdown    string `json:"markdown"`
	SubmittedAt string `json:"submitted_at"`
	UpdatedAt   string `json:"updated_at"`
}

// MessageRole identifies the speaker for a canonical history entry.
type MessageRole string

const (
	// RoleUser is a user-authored history entry.
	RoleUser MessageRole = "user"
	// RoleAssistant is an assistant-authored history entry.
	RoleAssistant MessageRole = "assistant"
)

// MessageKind identifies whether a canonical history entry is conversation
// content or a Podiom diagnostic entry rendered only for humans.
type MessageKind string

const (
	// KindMessage is normal user/assistant conversation content.
	KindMessage MessageKind = "message"
	// KindError is a durable, session-scoped error shown in the chat history.
	KindError MessageKind = "error"
	// KindReasoning is provider reasoning/thinking text. It is persisted for
	// future display but hidden from today's chat and excluded from replay.
	KindReasoning MessageKind = "reasoning"
)

// Agent is Podiom's durable definition of a named colleague.
//
// MCPConfig is treated as sensitive (it may embed server commands, local URLs,
// tokens, or credentials) and is never serialized to clients or logs — the
// `json:"-"` tag redacts it at every JSON boundary, REST and WebSocket alike
// (R8.29). It is read/written only through the store's column mapping.
type Agent struct {
	Name           string
	Provider       config.Provider
	Profile        string
	Model          string
	Effort         string
	PermissionMode config.PermissionMode
	Fallback       []string
	MCPServers     []string `json:"-"`
	MCPConfig      string   `json:"-"`
	// AvatarUpdatedAt versions the agent's uploaded profile picture (empty = none).
	// The bytes live on disk (agents/<name>/avatar.png); this stamp drives
	// client-side cache-busting and tells the UI a picture exists.
	AvatarUpdatedAt string
	CreatedAt       string
	UpdatedAt       string
}

// Session is Podiom's durable conversation unit and current provider settings.
type Session struct {
	ID             string
	AgentName      string
	Name           string
	Description    string
	AutoNamed      bool
	Provider       config.Provider
	Profile        string
	Model          string
	Effort         string
	PermissionMode config.PermissionMode
	Origin         SessionOrigin
	ScheduleID     string
	RunID          string
	TaskID         string
	GoalID         string
	RollingSummary string
	ProviderHandle string
	CreatedAt      string
	UpdatedAt      string
	ProjectID      string
	PlanState      PlanState
	PlanExplicit   bool
	PlanInfo       PlanInfo
	// DreamedAt is set once a session has been consolidated into the agent's
	// MEMORY.md. Empty means the session is un-dreamed (pending consolidation).
	DreamedAt string
	// ContextTokens is the last request's prompt size in tokens; ContextLimit is
	// the model's context window. Both are refreshed each turn from the provider
	// stream and drive the composer's context-window ring. 0 means un-observed.
	ContextTokens int64
	ContextLimit  int64
	// Usage* are cumulative billed-token totals accumulated across every turn of
	// the session (each turn's provider usage is added). Unlike ContextTokens
	// (a snapshot), these only grow. SessionUsage.Total() is the billable metric.
	Usage SessionUsage
}

// SessionUsage is a session's cumulative billed-token total, broken out by class.
type SessionUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Total sums all token classes into the billable metric.
func (u SessionUsage) Total() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// Add returns u with a per-turn delta added to every class.
func (u SessionUsage) Add(d SessionUsage) SessionUsage {
	return SessionUsage{
		InputTokens:      u.InputTokens + d.InputTokens,
		OutputTokens:     u.OutputTokens + d.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + d.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + d.CacheWriteTokens,
	}
}

// Message is one ordered entry in a session's canonical history.
type Message struct {
	ID        int64
	SessionID string
	Seq       int
	Role      MessageRole
	Kind      MessageKind
	Content   string
	CreatedAt string
}

// DreamTrigger records what caused a memory-consolidation dream.
type DreamTrigger string

const (
	// DreamNightly marks a dream fired by the built-in nightly runner (including
	// downtime catch-up runs — the trigger records who asked, not why it was late).
	DreamNightly DreamTrigger = "nightly"
	// DreamManual marks a dream started on demand (CLI or UI "Dream now").
	DreamManual DreamTrigger = "manual"
)

// DreamStatus is the lifecycle state of a dream.
type DreamStatus string

const (
	// DreamRunning marks an in-flight dream.
	DreamRunning DreamStatus = "running"
	// DreamSuccess marks a dream that consolidated memory without error.
	DreamSuccess DreamStatus = "success"
	// DreamErrored marks a dream that failed; MEMORY.md and the source sessions
	// are left untouched so the work is retried on the next cycle.
	DreamErrored DreamStatus = "error"
)

// DreamNewItem is one memory line the dream added, tagged with the section it
// belongs to. It powers the UI's NEW badges and "since" dates without polluting
// MEMORY.md, which stays clean user-editable markdown.
type DreamNewItem struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// Dream is one nightly (or on-demand) memory-consolidation run. It doubles as
// the "dream journal" entry the UI renders and as the record of when memory last
// grew for an agent. A no-op dream (no un-dreamed sessions) is never persisted.
type Dream struct {
	ID           string
	AgentName    string
	RanAt        string
	FinishedAt   string
	Trigger      DreamTrigger
	Status       DreamStatus
	Error        string
	SessionCount int
	Kept         int
	Merged       int
	Pruned       int
	Note         string
	NewItems     []DreamNewItem
}

// RunTrigger records what caused a scheduled run.
type RunTrigger string

const (
	// TriggerCron marks a run fired by the embedded cron scheduler.
	TriggerCron RunTrigger = "cron"
	// TriggerManual marks a run started by an explicit "Run now".
	TriggerManual RunTrigger = "manual"
)

// RunStatus is the lifecycle state of a scheduled run.
type RunStatus string

const (
	// RunRunning marks an in-flight scheduled run.
	RunRunning RunStatus = "running"
	// RunSuccess marks a scheduled run that completed without error.
	RunSuccess RunStatus = "success"
	// RunError marks a scheduled run that failed.
	RunError RunStatus = "error"
)

// ScheduleRun records one execution of a schedule. It links the schedule to the
// durable session it produced (R7.9 / R4.12) so the run can be revisited and
// continued manually.
type ScheduleRun struct {
	ID           string
	ScheduleName string
	SessionID    string
	Trigger      RunTrigger
	Status       RunStatus
	Error        string
	StartedAt    string
	FinishedAt   string
}

// PushSubscription is a registered destination for OS/native notifications. It
// is delivery-technology-neutral: Kind names the channel ('webpush' today;
// 'apns'/'fcm' later) and Payload carries the kind-specific credentials as JSON
// (for webpush: {"p256dh":..,"auth":..}). Endpoint is the stable identity used
// to upsert and to prune dead subscriptions.
type PushSubscription struct {
	ID        string
	Kind      string
	Endpoint  string
	Payload   string
	CreatedAt string
}

// TaskStatus is a roadmap task's kanban column.
type TaskStatus string

const (
	// TaskBacklog is unstarted work.
	TaskBacklog TaskStatus = "backlog"
	// TaskInProgress is work an agent has been started on.
	TaskInProgress TaskStatus = "in_progress"
	// TaskReview is work awaiting review.
	TaskReview TaskStatus = "review"
	// TaskDone is completed work.
	TaskDone TaskStatus = "done"
)

// GoalStatus is a goal's lifecycle state.
type GoalStatus string

const (
	// GoalActive means the goal's autonomy loop is running; reviews fire on cadence.
	GoalActive GoalStatus = "active"
	// GoalPaused means the user suspended the goal; no reviews fire.
	GoalPaused GoalStatus = "paused"
	// GoalReview means the agent proposed completion and awaits the user's verdict.
	GoalReview GoalStatus = "review"
	// GoalDone means the user confirmed completion. Terminal but reopenable.
	GoalDone GoalStatus = "done"
	// GoalAbandoned means the user gave up on the goal. Terminal but reopenable.
	GoalAbandoned GoalStatus = "abandoned"
)

// GoalMetric is one measurable indicator on a goal. The lead agent moves
// Current over time with evidence; Target is the user-set finish line. Stored
// as JSON on the goal; history is derivable from metric_update events.
type GoalMetric struct {
	Name    string  `json:"name"`
	Target  float64 `json:"target"`
	Current float64 `json:"current"`
	Unit    string  `json:"unit,omitempty"`
}

// Goal is a user-stated outcome owned by one lead agent, which autonomously
// plans and drives the work (tasks, schedules, periodic reviews) until the
// success criteria are met. The user approves grants and completion; the goal's
// timeline (goal_events) is the audit trail.
type Goal struct {
	ID              string
	Title           string
	Description     string
	SuccessCriteria string
	Metrics         []GoalMetric
	// ReviewEvery is a Go duration string (e.g. "24h"); empty disables automatic
	// reviews. The API enforces a 15m floor.
	ReviewEvery string
	LeadAgent   string
	ProjectID   string
	Provider    config.Provider
	Profile     string
	Model       string
	Effort      string
	Status      GoalStatus
	// NextReviewAt is when the scheduler should fire the next unattended review.
	// Empty when paused/terminal or when automatic reviews are disabled.
	NextReviewAt string
	// ClosingReport is the agent-written markdown set when it proposes completion.
	ClosingReport string
	CreatedAt     string
	UpdatedAt     string
}

// GoalEventKind classifies one entry in a goal's append-only timeline.
type GoalEventKind string

const (
	// GoalEventCreated marks goal creation.
	GoalEventCreated GoalEventKind = "created"
	// GoalEventPlanningStarted marks the start of the initial planning session.
	GoalEventPlanningStarted GoalEventKind = "planning_started"
	// GoalEventReviewStarted marks the start of a periodic or manual review session.
	GoalEventReviewStarted GoalEventKind = "review_started"
	// GoalEventProgress is an agent-written progress entry with evidence.
	GoalEventProgress GoalEventKind = "progress"
	// GoalEventMetricUpdate records metric value changes (old → new in payload).
	GoalEventMetricUpdate GoalEventKind = "metric_update"
	// GoalEventPlanChange records tasks/schedules being created or adjusted.
	GoalEventPlanChange GoalEventKind = "plan_change"
	// GoalEventUserFeedback records a user-authored strategy note for future
	// goal sessions. It does not start a session by itself.
	GoalEventUserFeedback GoalEventKind = "user_feedback"
	// GoalEventAccessRequested records an access request being filed.
	GoalEventAccessRequested GoalEventKind = "access_requested"
	// GoalEventAccessDecided records the user's decision on an access request.
	GoalEventAccessDecided GoalEventKind = "access_decided"
	// GoalEventStatusChange records any goal status transition.
	GoalEventStatusChange GoalEventKind = "status_change"
	// GoalEventCompletionProposed records the agent proposing the goal is done.
	GoalEventCompletionProposed GoalEventKind = "completion_proposed"
	// GoalEventRateLimited records an unattended goal run that exhausted or
	// could not use its fallback chain and now needs the user to choose a target.
	GoalEventRateLimited GoalEventKind = "rate_limited"
	// GoalEventRateLimitResolved records the user choosing a new target so the
	// blocked goal run can continue.
	GoalEventRateLimitResolved GoalEventKind = "rate_limit_resolved"
)

// GoalEvent is one append-only timeline entry — the goal's audit trail. Updates
// are rejected at the schema level; rows are removed only by goal cascade.
// SessionID links the event to the session that produced it ("" for user
// actions from the UI), so every autonomous claim is attributable (§8).
type GoalEvent struct {
	ID        int64
	GoalID    string
	SessionID string
	Kind      GoalEventKind
	// Body is human-readable markdown.
	Body string
	// Payload is kind-specific JSON (metric deltas, request id, old/new status…).
	Payload   string
	CreatedAt string
}

// GoalRateLimitStatus is the lifecycle state of a goal-level rate-limit block.
type GoalRateLimitStatus string

const (
	// GoalRateLimitPending means the user still needs to choose a recovery target.
	GoalRateLimitPending GoalRateLimitStatus = "pending"
	// GoalRateLimitResolved means the user picked a recovery target.
	GoalRateLimitResolved GoalRateLimitStatus = "resolved"
)

// GoalRateLimitPhase records which autonomous goal phase hit the provider limit.
type GoalRateLimitPhase string

const (
	// GoalRateLimitPlanning means the initial decomposition session failed.
	GoalRateLimitPlanning GoalRateLimitPhase = "planning"
	// GoalRateLimitReview means a periodic or manual review session failed.
	GoalRateLimitReview GoalRateLimitPhase = "review"
)

// GoalRateLimitBlock is a durable attention item created when an unattended
// goal run exhausts or cannot use automatic fallback after a provider rate limit.
type GoalRateLimitBlock struct {
	ID               string
	GoalID           string
	SessionID        string
	Phase            GoalRateLimitPhase
	Provider         config.Provider
	Profile          string
	Model            string
	Effort           string
	Error            string
	Status           GoalRateLimitStatus
	ResolvedProvider config.Provider
	ResolvedProfile  string
	ResolvedModel    string
	ResolvedEffort   string
	CreatedAt        string
	ResolvedAt       string
}

// AccessRequestKind is what capability the agent asked for.
type AccessRequestKind string

const (
	// AccessMCPServer requests assignment of a catalogue MCP server (automatable).
	AccessMCPServer AccessRequestKind = "mcp_server"
	// AccessSkill requests a marketplace skill install (automatable).
	AccessSkill AccessRequestKind = "skill"
	// AccessCLITool requests a host CLI tool install (acknowledge-only).
	AccessCLITool AccessRequestKind = "cli_tool"
	// AccessEnvVar requests a credential/env var by NAME — never by value
	// (acknowledge-only).
	AccessEnvVar AccessRequestKind = "env_var"
	// AccessPermissionMode requests an agent permission-mode change (automatable).
	AccessPermissionMode AccessRequestKind = "permission_mode"
)

// AccessRequestStatus is the lifecycle of an access request.
type AccessRequestStatus string

const (
	// AccessPending awaits the user's decision.
	AccessPending AccessRequestStatus = "pending"
	// AccessApproved is the user's yes. Terminal for acknowledge-only kinds;
	// automatable kinds continue to executed/failed.
	AccessApproved AccessRequestStatus = "approved"
	// AccessDenied is the user's no. Terminal.
	AccessDenied AccessRequestStatus = "denied"
	// AccessExecuted means the automatic grant ran successfully.
	AccessExecuted AccessRequestStatus = "executed"
	// AccessFailed means the automatic grant errored; the request stays
	// actionable (retryable approve).
	AccessFailed AccessRequestStatus = "failed"
)

// AccessRequest is a durable, typed capability request filed by a goal's lead
// agent. Decisions are human-only; DecisionNote is relayed back to the agent at
// its next review — it is how the user talks back.
type AccessRequest struct {
	ID        string
	GoalID    string
	AgentName string
	SessionID string
	Kind      AccessRequestKind
	// Payload is kind-specific JSON (see the goals spec §6). Never a secret value.
	Payload        string
	Reason         string
	Status         AccessRequestStatus
	DecisionNote   string
	ExecutionError string
	CreatedAt      string
	DecidedAt      string
	ExecutedAt     string
}

// Task is a roadmap item: a unit of work on a shared project, assignable to an
// agent and startable on demand (origin=roadmap) or at a scheduled pickup time.
// Tasks are independent in v1 — no inter-task dependencies (§2).
type Task struct {
	ID            string
	ProjectID     string
	Title         string
	Body          string
	AssignedAgent string
	Provider      config.Provider
	Profile       string
	Model         string
	Effort        string
	Status        TaskStatus
	PlanRequired  bool
	PickupAt      string // optional RFC3339 scheduled pickup time
	CreatedAt     string
	UpdatedAt     string
}
