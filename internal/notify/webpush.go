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
type webPushPayload struct {
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	SessionID string          `json:"session_id"`
	GoalID    string          `json:"goal_id,omitempty"`
	Kind      string          `json:"kind"`
	Approval  *ApprovalAction `json:"approval,omitempty"`
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

// Name identifies the channel in dispatcher logs.
func (c *WebPushChannel) Name() string { return webPushKind }

// Send encrypts and delivers n to every stored Web Push subscription.
func (c *WebPushChannel) Send(ctx context.Context, n Notification) error {
	subs, err := c.store.ListPushSubscriptions(ctx, webPushKind)
	if err != nil {
		return fmt.Errorf("list web push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}
	payload, err := json.Marshal(webPushPayloadForNotification(n))
	if err != nil {
		return fmt.Errorf("encode web push payload: %w", err)
	}

	var firstErr error
	for _, sub := range subs {
		if err := c.sendOne(ctx, sub, payload); err != nil {
			c.log.Warn("web push delivery failed", "endpoint", sub.Endpoint, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func webPushPayloadForNotification(n Notification) webPushPayload {
	return webPushPayload{
		Title:     n.Title,
		Body:      n.Body,
		SessionID: n.SessionID,
		GoalID:    n.GoalID,
		Kind:      n.Kind,
		Approval:  n.Approval,
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
