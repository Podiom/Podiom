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
	// OriginInterview marks a disposable USER.md interview session.
	OriginInterview SessionOrigin = "interview"
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
	// KindReasoning is provider reasoning/thinking text. It is rendered in chat
	// as a "thinking" entry, visually distinct from the turn's answer, and
	// excluded from replay.
	KindReasoning MessageKind = "reasoning"
	// KindNarration is assistant prose from before a turn's final answer — what
	// the agent said while working, split off at each tool call. Like reasoning
	// it renders as a non-answer entry and is excluded from replay.
	KindNarration MessageKind = "narration"
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
	// SourceControlWarning records a non-fatal checkout/update problem observed
	// while this session was created so both the UI and resumed agent context can
	// explain why the working copy may not be current.
	SourceControlWarning string
	PlanState            PlanState
	PlanExplicit         bool
	PlanInfo             PlanInfo
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
	ID          int64
	SessionID   string
	Seq         int
	Role        MessageRole
	Kind        MessageKind
	Content     string
	CreatedAt   string
	Attachments []Attachment
}

// Attachment is one durable photo associated with a user message. The original
// and normalized visual bytes live under PODIOM_HOME; only safe metadata crosses
// the API boundary.
type Attachment struct {
	ID         string
	SessionID  string
	MessageID  int64
	Position   int `json:"-"`
	Name       string
	MIMEType   string
	SizeBytes  int64
	Width      int
	Height     int
	CreatedAt  string
	VisualPath string `json:"-"`
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
	// LeadSessionID is the continuing planning/review conversation for the
	// current lead agent. It is replaced only for an explicit lead handoff.
	LeadSessionID string
	Status        GoalStatus
	// NextReviewAt is when the scheduler should fire the next unattended review.
	// Empty when paused/terminal or when automatic reviews are disabled.
	NextReviewAt string
	// ClosingReport is the agent-written markdown set when it proposes completion.
	ClosingReport string
	// NextStep is the agent-stated strategic move it will make before the next
	// review — an action ("Post the launch thread on r/selfhosted"), not a
	// restatement of a scheduled task. NextStepWhy is its one-sentence rationale
	// and NextStepAt when it was stated, so a stale intention is visible as such.
	// Written only through RecordGoalProgress (never a full-row UpdateGoal) and
	// cleared when the goal is proposed complete or goes terminal.
	NextStep    string
	NextStepWhy string
	NextStepAt  string
	CreatedAt   string
	UpdatedAt   string
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
	// GoalEventToolUse records one provider tool invocation (a shell command, file
	// edit, install, web fetch, or MCP call) observed during a goal-linked
	// unattended run. Because the whole goal chain runs in yolo mode, tool calls
	// never reach the permission broker — these events are the goal's audit trail
	// of what it actually did. The payload's read_only flag lets the UI collapse
	// read-only calls.
	GoalEventToolUse GoalEventKind = "tool_use"
	// GoalEventQuestionAsked records the agent asking the user a question during
	// an unattended goal run. The goal pauses its reviews until answered.
	GoalEventQuestionAsked GoalEventKind = "question_asked"
	// GoalEventQuestionAnswered records the user answering that question from the
	// goal page; the answer is fed into the next review session.
	GoalEventQuestionAnswered GoalEventKind = "question_answered"
	// GoalEventActionRequested records the agent handing a step back to the user
	// because only a human can do it. Unlike a question this does not pause
	// reviews — the agent keeps working around it.
	GoalEventActionRequested GoalEventKind = "action_requested"
	// GoalEventActionResponded records the user's verdict on an action item; it
	// reaches the agent in its next review prompt.
	GoalEventActionResponded GoalEventKind = "action_responded"
)

// GoalEvent is one timeline entry in the goal's audit trail. Entries are
// immutable except for unread user feedback body edits; rows are removed only by
// goal cascade.
// SessionID links the event to the session that produced it ("" for user
// actions from the UI), so every autonomous claim is attributable (§8).
type GoalEvent struct {
	ID        int64
	GoalID    string
	SessionID string
	RunID     string
	Kind      GoalEventKind
	// Body is human-readable markdown.
	Body string
	// Payload is kind-specific JSON (metric deltas, request id, old/new status…).
	Payload   string
	CreatedAt string
}

// GoalRunKind identifies the execution shape behind a goal activity.
type GoalRunKind string

const (
	GoalRunPlanning     GoalRunKind = "planning"
	GoalRunReview       GoalRunKind = "review"
	GoalRunTask         GoalRunKind = "task"
	GoalRunSchedule     GoalRunKind = "schedule"
	GoalRunConversation GoalRunKind = "conversation"
)

// GoalRunStatus is the durable lifecycle of one goal-linked turn.
type GoalRunStatus string

const (
	GoalRunRunning     GoalRunStatus = "running"
	GoalRunSucceeded   GoalRunStatus = "succeeded"
	GoalRunFailed      GoalRunStatus = "failed"
	GoalRunRateLimited GoalRunStatus = "rate_limited"
	GoalRunInterrupted GoalRunStatus = "interrupted"
)

// GoalRun binds a goal activity to an exact turn within a durable session.
type GoalRun struct {
	ID            string
	GoalID        string
	SessionID     string
	TurnMessageID int64
	Kind          GoalRunKind
	AgentName     string
	SourceID      string
	Status        GoalRunStatus
	Legacy        bool
	Error         string
	StartedAt     string
	FinishedAt    string
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
	RunID            string
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

// AgentQuestionOrigin is the kind of unattended run a question came from.
type AgentQuestionOrigin string

const (
	// AgentQuestionGoal is a question raised during a goal planning/review run;
	// it is surfaced and answered on the goal page and pauses goal reviews.
	AgentQuestionGoal AgentQuestionOrigin = "goal"
	// AgentQuestionSchedule is a question raised during a scheduled run; it is
	// surfaced on the Schedules page and its answer persists across runs.
	AgentQuestionSchedule AgentQuestionOrigin = "schedule"
)

// AgentQuestionStatus is the lifecycle state of a deferred agent question.
type AgentQuestionStatus string

const (
	// AgentQuestionPending means the user still needs to answer.
	AgentQuestionPending AgentQuestionStatus = "pending"
	// AgentQuestionAnswered means the user answered; the answer is available to
	// the next run.
	AgentQuestionAnswered AgentQuestionStatus = "answered"
	// AgentQuestionDismissed means the user dismissed the question without an answer.
	AgentQuestionDismissed AgentQuestionStatus = "dismissed"
)

// AgentQuestionOption is one selectable answer. Mirrors the chat
// UserInputOption JSON shape so the frontend reuses the same components.
type AgentQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AgentQuestionItem is one prompt in a deferred question. Mirrors the chat
// UserInputQuestion JSON shape (id/header/question/options/multi_select/is_secret).
type AgentQuestionItem struct {
	ID          string                `json:"id"`
	Header      string                `json:"header,omitempty"`
	Question    string                `json:"question"`
	Options     []AgentQuestionOption `json:"options,omitempty"`
	MultiSelect bool                  `json:"multi_select,omitempty"`
	IsOther     bool                  `json:"is_other,omitempty"`
	IsSecret    bool                  `json:"is_secret,omitempty"`
}

// AgentQuestion is a durable question an unattended agent asked the user
// (defer-and-resume): recorded here rather than blocking the run, answered from
// the goal/schedule page, and fed into the next run.
type AgentQuestion struct {
	ID         string
	Origin     AgentQuestionOrigin
	RefID      string // goal id, or schedule name
	SessionID  string
	Questions  []AgentQuestionItem
	Status     AgentQuestionStatus
	Answers    map[string][]string // question id → selected/freeform answers
	CreatedAt  string
	AnsweredAt string
}

// GoalActionItemStatus is the lifecycle state of an action item. The user sets
// every terminal value; the agent only ever files an item as open.
type GoalActionItemStatus string

const (
	// GoalActionOpen means the user has not responded yet. Open items do not
	// pause the goal's reviews — the agent works around them.
	GoalActionOpen GoalActionItemStatus = "open"
	// GoalActionDone means the user carried the action out.
	GoalActionDone GoalActionItemStatus = "done"
	// GoalActionBlocked means the user tried but could not, so the agent must
	// find another route.
	GoalActionBlocked GoalActionItemStatus = "blocked"
	// GoalActionDeclined means the user chose not to do it.
	GoalActionDeclined GoalActionItemStatus = "declined"
)

// GoalActionItem is a step a goal's agent handed back to the user because only
// a human can carry it out — posting from a personal account, signing something,
// making a call. It is the fourth agent→user channel, distinct from an access
// request (a capability Podiom can wire), a question (a decision, which pauses
// reviews), and next_step (the agent's own move).
//
// The exchange is instruction → verdict: the agent writes Title/Instructions/Why,
// the user answers with a Status and a free-text Response, and that response
// reaches the agent in its next planning or review prompt — the same
// store-and-replay contract as user feedback, not a live conversation.
type GoalActionItem struct {
	ID        string
	GoalID    string
	SessionID string
	RunID     string
	AgentName string
	// Title is the one-line ask, e.g. "Post the launch thread on r/selfhosted".
	Title string
	// Instructions is markdown the user can follow without further context.
	Instructions string
	// Why is one sentence on why this needs a human.
	Why         string
	Status      GoalActionItemStatus
	Response    string
	CreatedAt   string
	RespondedAt string
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
	// AccessEnvVar requests a credential/env var by NAME — never by value.
	// Acknowledge-only when approved bare, or executed when the user supplies
	// the value at approval (stored in credentials.yaml, injected into agent
	// subprocess environments).
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
	// GoalID links this task to a goal when it was created as part of that goal's
	// plan (""=standalone). Goal-linked task runs are forced yolo and their tool
	// calls are recorded on the goal's timeline.
	GoalID    string
	CreatedAt string
	UpdatedAt string
}
