// Package notify delivers "an agent needs you" signals to out-of-app
// destinations: the OS notification tray via Web Push today, and — behind the
// same Channel seam — native APNs/FCM push later. It is deliberately decoupled
// from core/server: callers hand it a Notification and a set of Channels, and
// each Channel owns its own transport and subscription storage.
//
// In-app signalling (toasts, red dots) is NOT this package's job; that is
// driven live off the WebSocket. notify is only for reaching a user who is not
// currently looking at the dashboard.
package notify

import (
	"context"
	"encoding/json"
	"log/slog"
)

// Notification is a provider-neutral "attention required" event. Kind is the
// underlying trigger ("permission", "question", "goal_access_request", or
// "goal_review"); Title/Body are the rendered human strings a Channel presents.
// GoalID is set for goal-scoped kinds so a notification tap can deep-link to
// the goal instead of a session.
type Notification struct {
	SessionID string
	AgentName string
	GoalID    string
	Title     string
	Body      string
	Kind      string // "permission" | "question" | "goal_access_request" | "goal_review"
	Approval  *ApprovalAction
}

// ApprovalAction is the minimal data a trusted same-origin client needs to
// allow a pending permission request from an OS notification action.
type ApprovalAction struct {
	RequestID string          `json:"request_id"`
	Input     json.RawMessage `json:"input"`
}

// Channel is one delivery technology. Implementations are responsible for their
// own subscription lookup and transport. New destinations (APNs, FCM) implement
// this interface and are registered on the Dispatcher without touching callers.
type Channel interface {
	Name() string
	Send(ctx context.Context, n Notification) error
}

// Dispatcher fans a Notification out to every registered Channel. Delivery is
// best-effort: a failing channel is logged, never propagated, so one dead
// transport can't suppress the others.
type Dispatcher struct {
	channels []Channel
	log      *slog.Logger
}

// NewDispatcher builds a Dispatcher over the given channels. Nil channels are
// dropped so callers can pass optional channels inline.
func NewDispatcher(log *slog.Logger, channels ...Channel) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	live := make([]Channel, 0, len(channels))
	for _, ch := range channels {
		if ch != nil {
			live = append(live, ch)
		}
	}
	return &Dispatcher{channels: live, log: log}
}

// Notify delivers to every channel synchronously. Callers that must not block
// (e.g. a turn hot path) should invoke this in a goroutine.
func (d *Dispatcher) Notify(ctx context.Context, n Notification) {
	for _, ch := range d.channels {
		if err := ch.Send(ctx, n); err != nil {
			d.log.Warn("notification channel failed", "channel", ch.Name(), "kind", n.Kind, "err", err)
		}
	}
}
