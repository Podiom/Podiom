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
	CreatedAt      string
	UpdatedAt      string
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
}

// Message is one ordered entry in a session's canonical history.
type Message struct {
	ID        int64
	SessionID string
	Seq       int
	Role      MessageRole
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

// Task is a roadmap item: a unit of work on a shared project, assignable to an
// agent and startable on demand (origin=roadmap) or at a scheduled pickup time.
// Tasks are independent in v1 — no inter-task dependencies (§2).
type Task struct {
	ID            string
	ProjectID     string
	Title         string
	Body          string
	AssignedAgent string
	Status        TaskStatus
	PlanRequired  bool
	PickupAt      string // optional RFC3339 scheduled pickup time
	CreatedAt     string
	UpdatedAt     string
}
