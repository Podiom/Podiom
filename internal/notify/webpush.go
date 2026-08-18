package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/Podiom/Podiom/internal/store"
)

// webPushKind is the store `kind` value for browser Web Push subscriptions.
const webPushKind = "webpush"

// WebPushStore is the subscription persistence the Web Push channel needs. The
// store's *Store satisfies it directly. Keeping it an interface lets the channel
// be unit-tested with a fake and keeps notify from importing server/core.
type WebPushStore interface {
	ListPushSubscriptions(ctx context.Context, kind string) ([]store.PushSubscription, error)
	DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) error
}

// webPushPayload is the JSON delivered to the service worker's `push` handler.
//
// The first six fields are the original shape and are load-bearing: the shipped
// service worker keys its behaviour off `kind` and reads `approval` to offer its
// approve action, so they keep their names and values.
//
// The routing fields below were added with the notification engine so a browser tap
// lands on the exact resource rather than merely the right page, the same way a
// native notification does.
type webPushPayload struct {
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	SessionID string          `json:"session_id"`
	GoalID    string          `json:"goal_id,omitempty"`
	Kind      string          `json:"kind"`
	Approval  *ApprovalAction `json:"approval,omitempty"`

	NotificationID string `json:"notification_id,omitempty"`
	Type           string `json:"type,omitempty"`
	NavTarget      string `json:"nav_target,omitempty"`
	ScheduleName   string `json:"schedule_name,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
}

// WebPushChannel delivers notifications to every registered browser Web Push
// subscription. Dead subscriptions (404/410 from the push service) are pruned as
// a side effect of sending.
type WebPushChannel struct {
	store   WebPushStore
	keys    VAPIDKeys
	subject string // VAPID "sub": a mailto: or https URL identifying this server
	log     *slog.Logger
}

// NewWebPushChannel constructs the channel. subject should be a mailto: or URL
// per the VAPID spec; a sensible default is applied when empty.
func NewWebPushChannel(st WebPushStore, keys VAPIDKeys, subject string, log *slog.Logger) *WebPushChannel {
	if log == nil {
		log = slog.Default()
	}
	if subject == "" {
		subject = "mailto:podiom@localhost"
	}
	return &WebPushChannel{store: st, keys: keys, subject: subject, log: log}
}

// Name identifies the channel in preferences and delivery rows.
func (c *WebPushChannel) Name() string { return ChannelWebPush }

// Send encrypts and delivers env to every stored Web Push subscription, and
// reports one result per subscription.
//
// Results are keyed by the subscription row id rather than its endpoint: the
// endpoint URL embeds a per-browser secret path, and delivery history is read
// back by the dashboard.
func (c *WebPushChannel) Send(ctx context.Context, env Envelope) ([]Result, error) {
	subs, err := c.store.ListPushSubscriptions(ctx, webPushKind)
	if err != nil {
		return nil, fmt.Errorf("list web push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(webPushPayloadForEnvelope(env))
	if err != nil {
		return nil, fmt.Errorf("encode web push payload: %w", err)
	}

	results := make([]Result, 0, len(subs))
	for _, sub := range subs {
		err := c.sendOne(ctx, sub, payload)
		if err != nil {
			c.log.Warn("web push delivery failed", "endpoint", sub.Endpoint, "err", err)
		}
		results = append(results, Result{Destination: sub.ID, Err: err})
	}
	return results, nil
}

func webPushPayloadForEnvelope(env Envelope) webPushPayload {
	return webPushPayload{
		Title:          env.Title,
		Body:           env.Body,
		SessionID:      env.SessionID,
		GoalID:         env.GoalID,
		Kind:           env.PushKind,
		Approval:       env.Approval,
		NotificationID: env.ID,
		Type:           env.Type,
		NavTarget:      env.NavTarget,
		ScheduleName:   env.ScheduleName,
		TaskID:         env.TaskID,
		ResourceID:     env.ResourceID,
	}
}

func (c *WebPushChannel) sendOne(ctx context.Context, row store.PushSubscription, payload []byte) error {
	// Payload is the browser PushSubscription JSON, which matches webpush.Subscription.
	var sub webpush.Subscription
	if err := json.Unmarshal([]byte(row.Payload), &sub); err != nil {
		return fmt.Errorf("decode subscription: %w", err)
	}
	if sub.Endpoint == "" {
		sub.Endpoint = row.Endpoint
	}

	resp, err := webpush.SendNotificationWithContext(ctx, payload, &sub, &webpush.Options{
		Subscriber:      c.subject,
		VAPIDPublicKey:  c.keys.Public,
		VAPIDPrivateKey: c.keys.Private,
		TTL:             60,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		// The subscription is permanently gone; prune it so we stop trying.
		if derr := c.store.DeletePushSubscriptionByEndpoint(ctx, row.Endpoint); derr != nil {
			c.log.Warn("prune dead subscription failed", "endpoint", row.Endpoint, "err", derr)
		}
		return fmt.Errorf("subscription gone (%d), pruned", resp.StatusCode)
	case resp.StatusCode >= 400:
		return fmt.Errorf("push service returned %s", resp.Status)
	default:
		return nil
	}
}
