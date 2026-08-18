// Package notify turns Podiom domain activity into notifications and gets them to
// the user.
//
// It owns four things: the registry of every notification type Podiom produces
// (registry.go), the engine that maps domain events onto notifications, filters
// them against the user's preferences, persists them and fans them out
// (engine.go), the rendering of their human-readable text (render.go), and the
// delivery channels themselves (webpush.go, relay.go).
//
// The persisted notification is the authoritative record: external push is a way
// of telling the user about it, never the record itself. In-app delivery is the
// engine's broadcast callback, which drives the Notification Center live off the
// WebSocket; out-of-app delivery is the Channel set.
//
// The package depends on internal/store and nothing else of Podiom's. Producers
// reach in by holding an *Engine, and the two server-dependent pieces arrive
// through setters, so core and server never have to be imported here.
package notify

import (
	"context"
	"encoding/json"
)

// ApprovalAction is the minimal data a trusted same-origin client needs to allow
// a pending permission request straight from an OS notification action.
type ApprovalAction struct {
	RequestID string          `json:"request_id"`
	Input     json.RawMessage `json:"input"`
}

// Action is one operation offered on a notification, already narrowed against
// live domain state. Label is display text; ID is what the client sends back.
type Action struct {
	ID    ActionID `json:"id"`
	Label string   `json:"label"`
}

// Envelope is what a delivery channel receives: the stored notification plus the
// context a transport needs and the actions that are valid right now.
//
// Actions are computed per delivery rather than stored, because a notification's
// available operations depend on domain state that moves after it was recorded —
// an access request approved from the dashboard must stop offering Approve on a
// phone that is still showing the notification.
type Envelope struct {
	// ID is the notification id, and the handle a client sends actions against.
	ID string
	// Type is the registry type identifier.
	Type string
	// PushKind is the transport-facing kind. Types that predate the engine keep
	// their original value so the service worker's behaviour still applies.
	PushKind string
	// InstallationID names the Podiom installation that produced this, so one app
	// connected to several installations can tell them apart.
	InstallationID string
	// Importance lets a channel map onto platform notification weights.
	Importance   string
	Title        string
	Body         string
	AgentName    string
	SessionID    string
	GoalID       string
	ScheduleName string
	TaskID       string
	// ResourceKind/ResourceID name the domain object, so a tap can land on the exact
	// item rather than the page holding it.
	ResourceKind string
	ResourceID   string
	// NavTarget is the logical view a tap should open; the client owns the route.
	NavTarget string
	// ActionSet names the native action group, which becomes the APNs category. Empty
	// means tap-to-open with no buttons.
	ActionSet string
	// Actions is the set valid at send time. Empty means tap-to-open only.
	Actions []Action
	// Approval carries a pending permission request's id and input for the
	// existing Web Push approve action.
	Approval *ApprovalAction
}

// Result is one destination's outcome within a single Send.
//
// Channels report per destination rather than collapsing to one error so a
// delivery row can say what happened where: "three phones, one dead" is a
// different situation from "the transport is down", and only the former should
// prune a registration.
type Result struct {
	// Destination identifies where this attempt went — a device id or a
	// subscription row id. Never a push token or endpoint URL: those are secrets
	// and must not spread into notification history.
	Destination string
	// Err is nil when the transport accepted the payload.
	Err error
}

// Channel is one delivery technology. Implementations own their own destination
// lookup and transport, so a new one is registered on the engine without any
// producer changing.
type Channel interface {
	// Name is the channel identifier used in preferences and delivery rows.
	Name() string
	// Send delivers env to every destination this channel knows about. The error
	// is channel-level (destination lookup failed, payload could not be built);
	// per-destination outcomes are in the results. No results and no error means
	// nothing is registered — not a failure.
	Send(ctx context.Context, env Envelope) ([]Result, error)
}
