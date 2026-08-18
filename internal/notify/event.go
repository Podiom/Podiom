package notify

// ResourceRef names one domain object, for resolving notifications about it.
type ResourceRef struct {
	Kind ResourceKind
	ID   string
}

// Event is what a producer publishes: the fact that something happened, plus the
// ids needed to describe and navigate to it.
//
// Producers deliberately do not supply a title or body. Rendering lives in
// render.go so the wording for a notification type exists in one place rather
// than at every call site that can trigger it, and so behaviour is never derived
// from presentation text.
type Event struct {
	// Type is a registry type constant. An unregistered type is a producer bug
	// and is dropped with a warning.
	Type string

	// Scope ids. Each is set where it applies and left empty otherwise; they let
	// the client route and let the engine look up display names.
	SessionID    string
	GoalID       string
	ScheduleName string
	TaskID       string
	AgentName    string

	// Resource names the actionable domain object, when there is one. Together
	// with the type it is also the deduplication key: a producer firing twice for
	// the same still-open request must not stack up notifications.
	Resource   ResourceKind
	ResourceID string

	// Detail is the one piece of variable text a type needs — an error message, an
	// action item's title, a metric summary. Rendering decides how to use it and
	// truncates it, so a large tool output or transcript can never reach a push
	// payload through this field.
	Detail string

	// Approval carries a pending permission request so the existing Web Push
	// approve action keeps working.
	Approval *ApprovalAction

	// Resolves marks domain objects as handled instead of creating a notification.
	// An event with Resolves set records no new row.
	Resolves []ResourceRef
}
