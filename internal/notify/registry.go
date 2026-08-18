package notify

import (
	"github.com/Podiom/Podiom/internal/store"
)

// Category groups notification types into the buckets the preference UI shows.
// Users think in categories ("goals", "schedules"), not in event types.
type Category string

const (
	// CategoryAgent covers a live session needing the user: questions,
	// permissions, and failures.
	CategoryAgent Category = "agent_interaction"
	// CategoryGoals covers autonomous goal activity.
	CategoryGoals Category = "goals"
	// CategorySchedules covers scheduled runs.
	CategorySchedules Category = "schedules"
	// CategoryTasks covers roadmap task activity.
	CategoryTasks Category = "tasks"
	// CategorySystem covers daemon-level warnings.
	CategorySystem Category = "system"
)

// ActionID is a stable identifier for an operation the user can perform straight
// from a notification. The mobile apps map these onto native action groups, so
// the set is deliberately closed: Podiom names an action, the client decides how
// to render it, and no server response can ever describe new behaviour to run.
type ActionID string

const (
	// ActionOpen navigates to the resource. It never writes to the domain.
	ActionOpen ActionID = "open"
	// ActionAllow grants a pending session permission request.
	ActionAllow ActionID = "allow"
	// ActionDeny refuses a permission request or an access request.
	ActionDeny ActionID = "deny"
	// ActionApprove grants a pending goal access request.
	ActionApprove ActionID = "approve"
	// ActionDone completes a goal action item.
	ActionDone ActionID = "done"
	// ActionBlocked marks a goal action item as one the user cannot do.
	ActionBlocked ActionID = "blocked"
	// ActionReview opens a proposed goal completion for review.
	ActionReview ActionID = "review"
	// ActionMarkDone accepts a proposed goal completion.
	ActionMarkDone ActionID = "mark_done"
)

// ActionAnswerPrefix builds the action id for picking one predefined answer to a
// question: "answer:0" selects the first option. Indexing the option list rather
// than carrying its text is what keeps the action vocabulary closed — the client
// sends a position, never a server-supplied string to act on.
const ActionAnswerPrefix = "answer:"

// ResourceKind names the domain object a notification is about. It drives both
// which actions a notification can offer and which notifications a domain change
// resolves.
type ResourceKind string

const (
	// ResourceNone is for notifications with no actionable object behind them.
	ResourceNone ResourceKind = ""
	// ResourceGoalActionItem is a goal_action_items row.
	ResourceGoalActionItem ResourceKind = "goal_action_item"
	// ResourceAccessRequest is an access_requests row.
	ResourceAccessRequest ResourceKind = "access_request"
	// ResourceAgentQuestion is a deferred agent_questions row.
	ResourceAgentQuestion ResourceKind = "agent_question"
	// ResourceSessionQuestion is a live session's in-memory user-input request.
	// Distinct from ResourceAgentQuestion because it lives in the broker, not the
	// database, so its pending state is checked differently.
	ResourceSessionQuestion ResourceKind = "session_question"
	// ResourcePermissionRequest is a live session's in-memory permission request.
	ResourcePermissionRequest ResourceKind = "permission_request"
	// ResourceFallbackRequest is a live session waiting on a rate-limit fallback
	// choice.
	ResourceFallbackRequest ResourceKind = "fallback_request"
	// ResourceAuthRequest is a live session whose provider account is signed out.
	ResourceAuthRequest ResourceKind = "auth_request"
	// ResourceGoalRateLimit is a goal_rate_limit_blocks row.
	ResourceGoalRateLimit ResourceKind = "goal_rate_limit"
	// ResourceGoalCompletion is a goal awaiting the user's completion verdict.
	ResourceGoalCompletion ResourceKind = "goal_completion"
	// ResourceGoalRun is a goal_runs row.
	ResourceGoalRun ResourceKind = "goal_run"
	// ResourceScheduleRun is a schedule_runs row.
	ResourceScheduleRun ResourceKind = "schedule_run"
	// ResourceTask is a roadmap task.
	ResourceTask ResourceKind = "task"
	// ResourceGoal is a goal, for informational timeline notifications.
	ResourceGoal ResourceKind = "goal"
	// ResourceSession is a session, for failures with nothing to decide.
	ResourceSession ResourceKind = "session"
)

// Delivery channel names. These are the values stored in notification
// preferences, so they are part of Podiom's persisted state and must stay stable.
//
// AllChannels is deliberately broader than the channels a given daemon has
// running: a preference is written for every known channel, so unchecking a type
// keeps it unchecked when a channel is added later rather than silently reverting
// to the registry default.
const (
	// ChannelWebPush is the browser Web Push channel. The value matches the
	// push_subscriptions kind it has always used, so existing rows keep working.
	ChannelWebPush = "webpush"
	// ChannelNativePush is the iOS/Android channel delivered via the Push Relay.
	ChannelNativePush = "native_push"
)

// AllChannels is every channel a preference can be recorded for.
func AllChannels() []string {
	return []string{ChannelWebPush, ChannelNativePush}
}

// Title is the category's user-facing group heading.
func (c Category) Title() string {
	switch c {
	case CategoryAgent:
		return "Agent interaction"
	case CategoryGoals:
		return "Goals"
	case CategorySchedules:
		return "Schedules"
	case CategoryTasks:
		return "Tasks"
	case CategorySystem:
		return "System"
	}
	return string(c)
}

// Action sets. These become the APNs category on iOS, which is the only thing that makes
// action buttons appear, and the Android app groups its actions the same way.
//
// The relay does not validate the value — whatever Podiom sends becomes the category
// verbatim — so this is a closed set that the iOS app must register to match. A category
// the app does not know arrives with no buttons, silently. That is why these are declared
// here rather than derived from the action ids: derivation is a guess about a vocabulary
// Podiom owns, and it goes wrong the first time an action set is added.
const (
	ActionSetSessionPermission = "session_permission"
	ActionSetAccessRequest     = "access_request"
	ActionSetGoalActionItem    = "goal_action_item"
	ActionSetGoalCompletion    = "goal_completion"
	ActionSetQuestion          = "question"
)

// ActionSets is every action set Podiom emits, for the apps to register against.
func ActionSets() []string {
	return []string{
		ActionSetSessionPermission,
		ActionSetAccessRequest,
		ActionSetGoalActionItem,
		ActionSetGoalCompletion,
		ActionSetQuestion,
	}
}

// Navigation targets. These are logical tokens, not URLs: the web and mobile
// clients own the mapping from token plus ids to a route. Storing a route here
// would let a frontend rename break notifications that are already on someone's
// phone.
const (
	NavSession           = "session"
	NavSessionPermission = "session_permission"
	NavGoal              = "goal"
	NavGoalTimeline      = "goal_timeline"
	NavGoalActionItem    = "goal_action_item"
	NavGoalQuestion      = "goal_question"
	NavGoalAccess        = "goal_access_request"
	NavGoalCompletion    = "goal_completion"
	NavGoalRecovery      = "goal_recovery"
	NavScheduleRun       = "schedule_run"
	NavTask              = "task"
)

// Notification types. Every producer references one of these constants; nothing
// outside this package writes a type literal, so the registry stays the only
// place that knows what notification kinds exist.
const (
	TypeSessionQuestion           = "session.question"
	TypeSessionPermissionRequired = "session.permission_required"
	TypeSessionActionRequired     = "session.action_required"
	TypeSessionExecutionFailed    = "session.execution_failed"

	TypeScheduleStarted   = "schedule.started"
	TypeScheduleSucceeded = "schedule.succeeded"
	TypeScheduleFailed    = "schedule.failed"
	TypeScheduleQuestion  = "schedule.question"

	TypeGoalRunStarted         = "goal.run_started"
	TypeGoalRunSucceeded       = "goal.run_succeeded"
	TypeGoalRunFailed          = "goal.run_failed"
	TypeGoalProgress           = "goal.progress"
	TypeGoalMetricChanged      = "goal.metric_changed"
	TypeGoalPlanChanged        = "goal.plan_changed"
	TypeGoalQuestion           = "goal.question"
	TypeGoalActionRequested    = "goal.action_requested"
	TypeGoalAccessRequested    = "goal.access_requested"
	TypeGoalCompletionProposed = "goal.completion_proposed"
	TypeGoalRateLimited        = "goal.rate_limited"
	TypeGoalStatusChanged      = "goal.status_changed"

	TypeTaskStarted        = "task.started"
	TypeTaskCompleted      = "task.completed"
	TypeTaskReviewRequired = "task.review_required"
	TypeTaskFailed         = "task.failed"

	TypeSystemWarning = "system.warning"
)

// Legacy Web Push `kind` values. The service worker keys behaviour off these
// strings — it shows an Approve button only for "permission" — so the six types
// that existed before the notification engine keep emitting their original kind.
// Types without one fall back to their notification type.
const (
	legacyKindPermission     = "permission"
	legacyKindQuestion       = "question"
	legacyKindGoalAccess     = "goal_access_request"
	legacyKindGoalReview     = "goal_review"
	legacyKindGoalRateLimit  = "goal_rate_limit"
	legacyKindGoalActionItem = "goal_action_item"
)

// Info is everything Podiom knows about one notification type.
//
// Adding a notification type means adding one entry here: producers reference
// Type, the preference API renders Category and Label, the engine reads
// Importance and DefaultOn, clients route NavTarget, and action derivation
// narrows Actions against live domain state.
type Info struct {
	// Type is the stable machine-readable identifier. It is part of Podiom's API
	// and should not change after release.
	Type string
	// Category is the preference group this type appears under.
	Category Category
	// Label is the preference row's user-facing wording.
	Label string
	// Importance is the delivery weight channels map to platform capabilities.
	Importance store.NotificationImportance
	// DefaultOn decides whether this type notifies externally with no explicit
	// user choice. Events that block progress default on; high-frequency
	// informational ones default off.
	DefaultOn bool
	// Resource names the domain object, if any, that this type is about.
	Resource ResourceKind
	// NavTarget is the logical view a tap should open.
	NavTarget string
	// Actions is the candidate action set. Live domain state narrows it: a
	// resolved object offers navigation only.
	Actions []ActionID
	// ActionSet names the native action group this type's buttons belong to. Empty means
	// the notification arrives and opens but shows no buttons, which is the right answer
	// for anything with nothing to decide.
	ActionSet string
	// Dedupe collapses repeats about the same still-open resource into one
	// notification. It is set for types where a repeat carries no new information —
	// one pending request is one ask, and a schedule file that still will not parse
	// is still the same problem — and left unset for a stream of news, where two
	// progress updates are two things that happened.
	Dedupe bool
	// LegacyKind is the Web Push `kind` this type must keep emitting for service
	// worker compatibility. Empty means "use Type".
	LegacyKind string
	// Producer documents the single choke point that emits this type, so the
	// mapping between domain code and notification stays findable.
	Producer string
}

// Actionable reports whether this type can offer more than navigation. It is what
// the engine records on the row so the Notification Center can style unresolved
// asks differently without re-deriving actions for every row in the list.
func (i Info) Actionable() bool {
	return len(i.Actions) > 1
}

// registry is the ordered list of every notification type Podiom produces.
//
// Deliberate omissions, all of which the drift test pins down:
//
//   - system.execution_failed: the requirements list it, but Podiom has no
//     system-failure concept distinct from a turn or run failure, so nothing
//     would ever emit it.
//   - goal run states `interrupted` and `rate_limited`: interrupted is applied in
//     bulk to every running row at daemon start, so mapping it would notify on
//     every restart; rate_limited already arrives as the richer
//     goal.rate_limited, which carries the block id the recovery view needs.
//   - goal events `planning_started` and `review_started`: goal.run_started
//     already covers every run kind, and emitting both would double-count.
//   - goal event `tool_use`: the goal audit trail runs to hundreds of entries per
//     run and is not something to notify about.
var registry = []Info{
	{
		Type: TypeSessionQuestion, ActionSet: ActionSetQuestion, Dedupe: true, Category: CategoryAgent, Label: "Questions",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceSessionQuestion, NavTarget: NavSession,
		Actions: []ActionID{ActionOpen}, LegacyKind: legacyKindQuestion,
		Producer: "server.activeTurnHub.recordUserInput",
	},
	{
		Type: TypeSessionPermissionRequired, ActionSet: ActionSetSessionPermission, Dedupe: true, Category: CategoryAgent, Label: "Permission requests",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourcePermissionRequest, NavTarget: NavSessionPermission,
		Actions: []ActionID{ActionOpen, ActionAllow, ActionDeny}, LegacyKind: legacyKindPermission,
		Producer: "server.activeTurnHub.recordPermission",
	},
	{
		Type: TypeSessionActionRequired, Dedupe: true, Category: CategoryAgent, Label: "Action required",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceFallbackRequest, NavTarget: NavSession,
		Actions:  []ActionID{ActionOpen},
		Producer: "server.activeTurnHub.recordFallback, recordAuthRequired",
	},
	{
		Type: TypeSessionExecutionFailed, Category: CategoryAgent, Label: "Important execution failures",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceSession, NavTarget: NavSession,
		Actions:  []ActionID{ActionOpen},
		Producer: "core.sendPersistedTurnError",
	},

	{
		Type: TypeScheduleStarted, Category: CategorySchedules, Label: "Schedule started",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceScheduleRun, NavTarget: NavScheduleRun,
		Actions:  []ActionID{ActionOpen},
		Producer: "schedule.Scheduler.execute",
	},
	{
		Type: TypeScheduleSucceeded, Category: CategorySchedules, Label: "Schedule completed successfully",
		Importance: store.NotificationNormal, DefaultOn: false,
		Resource: ResourceScheduleRun, NavTarget: NavScheduleRun,
		Actions:  []ActionID{ActionOpen},
		Producer: "schedule.Scheduler.execute",
	},
	{
		Type: TypeScheduleFailed, Category: CategorySchedules, Label: "Failures",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceScheduleRun, NavTarget: NavScheduleRun,
		Actions:  []ActionID{ActionOpen},
		Producer: "schedule.Scheduler.execute",
	},
	{
		Type: TypeScheduleQuestion, ActionSet: ActionSetQuestion, Dedupe: true, Category: CategorySchedules, Label: "Questions",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceAgentQuestion, NavTarget: NavGoalQuestion,
		Actions: []ActionID{ActionOpen}, LegacyKind: legacyKindQuestion,
		Producer: "server.handleAgentQuestions",
	},

	{
		Type: TypeGoalRunStarted, Category: CategoryGoals, Label: "Goal run started",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceGoalRun, NavTarget: NavGoalTimeline,
		Actions:  []ActionID{ActionOpen},
		Producer: "core.beginGoalRun",
	},
	{
		Type: TypeGoalRunSucceeded, Category: CategoryGoals, Label: "Goal run completed",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceGoalRun, NavTarget: NavGoalTimeline,
		Actions:  []ActionID{ActionOpen},
		Producer: "core.finishGoalRun",
	},
	{
		Type: TypeGoalRunFailed, Category: CategoryGoals, Label: "Failures and blocked goals",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceGoalRun, NavTarget: NavGoalTimeline,
		Actions:  []ActionID{ActionOpen},
		Producer: "core.finishGoalRun",
	},
	{
		Type: TypeGoalProgress, Category: CategoryGoals, Label: "Progress updates",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceGoal, NavTarget: NavGoalTimeline,
		Actions:  []ActionID{ActionOpen},
		Producer: "goal event progress",
	},
	{
		Type: TypeGoalMetricChanged, Category: CategoryGoals, Label: "Metric updates",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceGoal, NavTarget: NavGoalTimeline,
		Actions:  []ActionID{ActionOpen},
		Producer: "goal event metric_update",
	},
	{
		Type: TypeGoalPlanChanged, Category: CategoryGoals, Label: "Plan changes",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceGoal, NavTarget: NavGoalTimeline,
		Actions:  []ActionID{ActionOpen},
		Producer: "goal event plan_change",
	},
	{
		Type: TypeGoalQuestion, ActionSet: ActionSetQuestion, Dedupe: true, Category: CategoryGoals, Label: "Questions",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceAgentQuestion, NavTarget: NavGoalQuestion,
		Actions: []ActionID{ActionOpen}, LegacyKind: legacyKindQuestion,
		Producer: "goal event question_asked",
	},
	{
		Type: TypeGoalActionRequested, ActionSet: ActionSetGoalActionItem, Dedupe: true, Category: CategoryGoals, Label: "Action items",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceGoalActionItem, NavTarget: NavGoalActionItem,
		Actions: []ActionID{ActionOpen, ActionDone, ActionBlocked}, LegacyKind: legacyKindGoalActionItem,
		Producer: "goal event action_requested",
	},
	{
		Type: TypeGoalAccessRequested, ActionSet: ActionSetAccessRequest, Dedupe: true, Category: CategoryGoals, Label: "Access requests",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceAccessRequest, NavTarget: NavGoalAccess,
		Actions: []ActionID{ActionOpen, ActionApprove, ActionDeny}, LegacyKind: legacyKindGoalAccess,
		Producer: "goal event access_requested",
	},
	{
		Type: TypeGoalCompletionProposed, ActionSet: ActionSetGoalCompletion, Dedupe: true, Category: CategoryGoals, Label: "Completion proposed",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceGoalCompletion, NavTarget: NavGoalCompletion,
		Actions: []ActionID{ActionReview, ActionMarkDone}, LegacyKind: legacyKindGoalReview,
		Producer: "goal event completion_proposed",
	},
	{
		Type: TypeGoalRateLimited, Dedupe: true, Category: CategoryGoals, Label: "Rate limits",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceGoalRateLimit, NavTarget: NavGoalRecovery,
		// Recovery needs a provider, profile, model and effort chosen together,
		// which no notification action can express — so this one only navigates.
		Actions: []ActionID{ActionOpen}, LegacyKind: legacyKindGoalRateLimit,
		Producer: "goal event rate_limited",
	},
	{
		Type: TypeGoalStatusChanged, Category: CategoryGoals, Label: "Status changes",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceGoal, NavTarget: NavGoal,
		Actions:  []ActionID{ActionOpen},
		Producer: "goal event status_change",
	},

	{
		Type: TypeTaskStarted, Category: CategoryTasks, Label: "Task started",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceTask, NavTarget: NavTask,
		Actions:  []ActionID{ActionOpen},
		Producer: "core.StartTask",
	},
	{
		// The user is the one who moves a task to done, so this mostly exists to
		// keep a second device in sync. Off by default for that reason.
		Type: TypeTaskCompleted, Category: CategoryTasks, Label: "Task completed",
		Importance: store.NotificationPassive, DefaultOn: false,
		Resource: ResourceTask, NavTarget: NavTask,
		Actions:  []ActionID{ActionOpen},
		Producer: "server.handleTask",
	},
	{
		Type: TypeTaskReviewRequired, Dedupe: true, Category: CategoryTasks, Label: "Ready for review",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceTask, NavTarget: NavTask,
		Actions:  []ActionID{ActionOpen},
		Producer: "server.markRoadmapSessionFinished",
	},
	{
		// Roadmap tasks have no failed status, so this is derived: a roadmap
		// session whose turn errored while its task was still in progress.
		Type: TypeTaskFailed, Category: CategoryTasks, Label: "Task failed",
		Importance: store.NotificationImportant, DefaultOn: true,
		Resource: ResourceTask, NavTarget: NavTask,
		Actions:  []ActionID{ActionOpen},
		Producer: "core.sendPersistedTurnError",
	},

	{
		Type: TypeSystemWarning, Dedupe: true, Category: CategorySystem, Label: "Important warnings",
		Importance: store.NotificationImportant, DefaultOn: true,
		// A warning is about the schedule file that produced it, which is what
		// deduplication keys on so a resync does not re-notify every few minutes.
		Resource: ResourceScheduleRun, NavTarget: NavScheduleRun,
		Producer: "schedule.Scheduler.Sync",
	},
}

// byType indexes the registry for lookup. Built once at init because every
// published notification consults it, including the ones that turn out to have no
// type at all.
var byType = func() map[string]Info {
	m := make(map[string]Info, len(registry))
	for _, info := range registry {
		m[info.Type] = info
	}
	return m
}()

// Lookup returns the registry entry for a notification type.
func Lookup(notifType string) (Info, bool) {
	info, ok := byType[notifType]
	return info, ok
}

// All returns every registered notification type in preference-UI order.
func All() []Info {
	out := make([]Info, len(registry))
	copy(out, registry)
	return out
}

// Categories is the display order of the preference groups.
func Categories() []Category {
	return []Category{CategoryAgent, CategoryGoals, CategorySchedules, CategoryTasks, CategorySystem}
}

// PushKind is the `kind` value a delivery payload carries. Types that predate the
// notification engine keep their original string so the service worker's existing
// behaviour still applies to them.
func (i Info) PushKind() string {
	if i.LegacyKind != "" {
		return i.LegacyKind
	}
	return i.Type
}

// goalEventTypes maps a goal timeline event onto the notification it produces.
// Kinds absent from this map produce no notification — see the registry comment
// for which ones and why.
var goalEventTypes = map[store.GoalEventKind]string{
	store.GoalEventProgress:           TypeGoalProgress,
	store.GoalEventMetricUpdate:       TypeGoalMetricChanged,
	store.GoalEventPlanChange:         TypeGoalPlanChanged,
	store.GoalEventStatusChange:       TypeGoalStatusChanged,
	store.GoalEventQuestionAsked:      TypeGoalQuestion,
	store.GoalEventActionRequested:    TypeGoalActionRequested,
	store.GoalEventAccessRequested:    TypeGoalAccessRequested,
	store.GoalEventCompletionProposed: TypeGoalCompletionProposed,
	store.GoalEventRateLimited:        TypeGoalRateLimited,
}

// goalEventResolves maps a goal timeline event onto the resource kind whose
// notifications it resolves. Answering a question or deciding an access request
// in any surface clears the notification that asked for it.
//
// status_change appears here as well as in goalEventTypes: any transition settles
// a pending completion proposal, because the user has now given the verdict the
// proposal was waiting for. Resolving is harmless when no proposal is outstanding.
var goalEventResolves = map[store.GoalEventKind]ResourceKind{
	store.GoalEventQuestionAnswered:  ResourceAgentQuestion,
	store.GoalEventActionResponded:   ResourceGoalActionItem,
	store.GoalEventAccessDecided:     ResourceAccessRequest,
	store.GoalEventRateLimitResolved: ResourceGoalRateLimit,
	store.GoalEventStatusChange:      ResourceGoalCompletion,
}

// GoalEventType returns the notification type a goal event produces, if any.
func GoalEventType(kind store.GoalEventKind) (string, bool) {
	t, ok := goalEventTypes[kind]
	return t, ok
}

// GoalEventResolves returns the resource kind a goal event resolves, if any.
func GoalEventResolves(kind store.GoalEventKind) (ResourceKind, bool) {
	r, ok := goalEventResolves[kind]
	return r, ok
}
