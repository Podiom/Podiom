package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// attemptTimeout caps one relay call. The relay's own retry budget is six seconds
// across at most three attempts, sized to sit inside this: a relay still retrying
// after the client has given up has turned one transient failure into a duplicate.
const attemptTimeout = 10 * time.Second

// pushAttempts is the total number of tries for one notification, so one retry.
//
// Retrying at all is only safe because every push carries an idempotency key: a repeat
// is either suppressed or replays the first response, never a second notification.
const pushAttempts = 2

// retryDelayCap bounds how long a retry waits, whatever the relay asks for.
const retryDelayCap = 5 * time.Second

// retryDelayDefault applies when the relay asks for a retry without saying when.
const retryDelayDefault = time.Second

// retryAfterGiveUp is the point past which a Retry-After means "not soon".
//
// The relay asks for an hour when an installation has spent its hourly push quota, and
// a replay still costs quota, so retrying that is worse than dropping the push — the
// notification is recorded and visible in the Notification Center regardless.
const retryAfterGiveUp = 60 * time.Second

// maxDevicesPerPush is the relay's per-request device ceiling.
const maxDevicesPerPush = 100

// RelayPayload is the presentable half of a push, as the relay expects it nested under
// `notification`.
//
// It carries the minimum needed to show the notification, route a tap and render the
// available actions. There is deliberately no field for prompts, transcripts, tool
// output, environment values, file contents or the gateway token: this crosses
// infrastructure Podiom does not own, so anything sensitive stays behind the
// authenticated API the notification navigates to.
type RelayPayload struct {
	Type       string `json:"type,omitempty"`
	Importance string `json:"importance,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	// NavTarget plus the ids in Data are a logical destination, not a URL. The app owns
	// the routing, so renaming a Podiom route cannot break a notification already
	// delivered to a phone.
	NavTarget string `json:"nav_target,omitempty"`
	// ActionSet becomes the APNs category, which is the only thing that makes action
	// buttons appear on iOS. The relay does not validate it, so it must name a category
	// the app registers.
	ActionSet string   `json:"action_set,omitempty"`
	Actions   []Action `json:"actions,omitempty"`
	// Data carries routing ids through to the app. The relay reserves several key names
	// for its own use and rejects the request if any appear here, so this map is built
	// by relayData rather than assembled at call sites.
	Data map[string]string `json:"data,omitempty"`
}

// pushRequest is one call to POST /v1/push.
type pushRequest struct {
	NotificationID string       `json:"notification_id"`
	InstallationID string       `json:"installation_id,omitempty"`
	DeviceIDs      []string     `json:"device_ids"`
	Notification   RelayPayload `json:"notification"`
}

// pushResponse is the relay's per-device verdict. Every requested device gets an entry.
type pushResponse struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	Results  []struct {
		DeviceID string `json:"device_id"`
		Status   string `json:"status"`
	} `json:"results"`
}

// Per-device statuses the relay reports.
const (
	// statusAccepted means the transport took the message. It is not a claim that the
	// device received it, displayed it, or that anyone saw it.
	statusAccepted = "accepted"
	// statusUnregistered means the registration is permanently gone.
	statusUnregistered = "unregistered"
	// statusUnknownDevice means the relay holds no routing record for it — usually a
	// device registered locally whose mirror to the relay never landed.
	statusUnknownDevice = "unknown_device"
	// statusFailed means delivery did not succeed and the device is not implicated.
	statusFailed = "failed"
)

// putDeviceRequest is one call to PUT /v1/devices/{device_id}.
//
// The relay stores only what it needs to route. Labels and app versions stay in Podiom:
// the relay has no use for them, and holding them would widen what a breach there
// exposes for no delivery benefit.
type putDeviceRequest struct {
	FCMToken string `json:"fcm_token"`
	Platform string `json:"platform"`
}

// relayTransport is the seam between deciding what to deliver and talking to the relay.
// Declared as an interface so the channel is testable without a network, and so the
// hosted relay stays replaceable.
type relayTransport interface {
	// Enroll registers this installation and returns its identity. The credential is
	// returned exactly once and cannot be read back.
	Enroll(ctx context.Context) (config.RelayEnrollment, error)
	PutDevice(ctx context.Context, credential, deviceID string, body putDeviceRequest) error
	DeleteDevice(ctx context.Context, credential, deviceID string) error
	Push(ctx context.Context, credential, idempotencyKey string, req pushRequest) (pushResponse, error)
}

// DeviceStore is the device registry the relay channel needs.
type DeviceStore interface {
	ListNotificationDevices(ctx context.Context, enabledOnly bool) ([]store.NotificationDevice, error)
	SetNotificationDeviceStatus(ctx context.Context, id, status string) error
}

// RelayChannel delivers notifications to registered iOS and Android devices through the
// hosted Podiom Push Relay.
//
// The relay is a transport and nothing more. It holds no credential that can perform a
// Podiom operation, and it is not in the return path: a notification action goes from
// the app straight to the originating podiomd.
//
// Devices are addressed by the opaque id Podiom assigned them, never by push token. The
// relay resolves that id inside the authenticated tenant, which is what makes ownership
// structural — a token in a request body carries no ownership record, so there would be
// nothing to check it against.
type RelayChannel struct {
	transport      relayTransport
	devices        DeviceStore
	installationID string
	statePath      string
	log            *slog.Logger

	// mu guards enrollment. Enrolling twice would abandon the first tenant and spend
	// another of the ten registrations an address gets per hour, so the check and the
	// call are held together.
	mu         sync.Mutex
	enrollment config.RelayEnrollment
}

// NewRelayChannel builds the channel against the relay at baseURL, persisting its
// enrollment at statePath.
//
// A blank baseURL yields a nil channel, which the engine drops: an installation with no
// relay configured simply has no native push.
func NewRelayChannel(devices DeviceStore, baseURL, statePath, installationID string, log *slog.Logger) *RelayChannel {
	if baseURL == "" || devices == nil || statePath == "" {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &RelayChannel{
		transport:      &httpRelay{baseURL: baseURL, client: &http.Client{Timeout: attemptTimeout}},
		devices:        devices,
		installationID: installationID,
		statePath:      statePath,
		log:            log,
	}
}

// Name identifies the channel in preferences and delivery rows.
func (c *RelayChannel) Name() string { return ChannelNativePush }

// RegisterDevice mirrors a device registration to the relay, giving it the routing
// record it needs to resolve that device id later.
//
// Idempotent on both sides, so calling it on every registration and every token refresh
// is the intended usage.
func (c *RelayChannel) RegisterDevice(ctx context.Context, device store.NotificationDevice) error {
	credential, err := c.credential(ctx)
	if err != nil {
		return err
	}
	return c.transport.PutDevice(ctx, credential, device.ID, putDeviceRequest{
		FCMToken: device.PushToken,
		Platform: device.Platform,
	})
}

// RemoveDevice tells the relay to forget a device.
func (c *RelayChannel) RemoveDevice(ctx context.Context, deviceID string) error {
	credential, err := c.credential(ctx)
	if err != nil {
		return err
	}
	return c.transport.DeleteDevice(ctx, credential, deviceID)
}

// Send delivers env to every registered device that is enabled and reachable, and
// reports one result per device.
//
// Results are keyed by device id, never by push token: delivery history is read back by
// the dashboard, and a token is the one thing that must not spread out of the delivery
// path.
func (c *RelayChannel) Send(ctx context.Context, env Envelope) ([]Result, error) {
	devices, err := c.devices.ListNotificationDevices(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("list notification devices: %w", err)
	}
	// A device the relay has already told us is gone stays out until the app registers
	// a fresh token for it, which is what revives it on both sides.
	live := make([]store.NotificationDevice, 0, len(devices))
	for _, device := range devices {
		if device.Status == store.NotificationDeviceInvalid {
			continue
		}
		live = append(live, device)
	}
	if len(live) == 0 {
		return nil, nil
	}

	credential, err := c.credential(ctx)
	if err != nil {
		return nil, err
	}

	var results []Result
	// The relay caps a request at a hundred devices, so a larger fleet goes in batches.
	for start := 0; start < len(live); start += maxDevicesPerPush {
		end := min(start+maxDevicesPerPush, len(live))
		batch := live[start:end]
		ids := make([]string, 0, len(batch))
		for _, device := range batch {
			ids = append(ids, device.ID)
		}
		batchResults, err := c.push(ctx, credential, env, ids)
		if err != nil {
			return nil, err
		}
		results = append(results, batchResults...)
	}
	return results, nil
}

// push sends one batch and applies what the relay says about each device.
func (c *RelayChannel) push(ctx context.Context, credential string, env Envelope, ids []string) ([]Result, error) {
	req := pushRequest{
		NotificationID: env.ID,
		InstallationID: c.installationID,
		DeviceIDs:      ids,
		Notification: RelayPayload{
			Type:       env.Type,
			Importance: env.Importance,
			Title:      env.Title,
			Body:       env.Body,
			NavTarget:  env.NavTarget,
			ActionSet:  env.ActionSet,
			Actions:    env.Actions,
			Data:       relayData(env),
		},
	}

	// The notification id doubles as the idempotency key: a retry of the same
	// notification must never become a second buzz.
	resp, err := c.attempt(ctx, credential, env.ID, req)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, Result{
			Destination: item.DeviceID,
			Err:         c.applyStatus(ctx, item.DeviceID, item.Status),
		})
	}
	return results, nil
}

// applyStatus records what the relay learned about one device, and returns the error to
// store against that delivery.
func (c *RelayChannel) applyStatus(ctx context.Context, deviceID, status string) error {
	switch status {
	case statusAccepted:
		return nil

	case statusUnregistered, statusUnknownDevice:
		// Both mean this device cannot be reached as things stand: the registration is
		// gone, or the relay never received the mirror of it. Marking it rather than
		// deleting it keeps the user's mute choice and the device's label, and the row
		// comes back to life when the app registers a fresh token — which it does on
		// its next launch.
		if err := c.devices.SetNotificationDeviceStatus(ctx, deviceID, store.NotificationDeviceInvalid); err != nil {
			c.log.Warn("mark device invalid failed", "event", "notification",
				"device", deviceID, "err", err)
		}
		return fmt.Errorf("device is not reachable: %s", status)

	case statusFailed:
		// Transient, and the device is not implicated, so its status is left alone.
		return errors.New("delivery failed at the relay")

	default:
		// A status this build does not know. Recorded as a failure rather than assumed
		// to be success, and left off the device's status so a newer relay cannot
		// silently disable a working phone.
		return fmt.Errorf("unrecognised delivery status %q", status)
	}
}

// attempt performs the push, retrying once when the relay says the failure is worth
// retrying.
func (c *RelayChannel) attempt(ctx context.Context, credential, idempotencyKey string, req pushRequest) (pushResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= pushAttempts; attempt++ {
		resp, err := c.transport.Push(ctx, credential, idempotencyKey, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == pushAttempts {
			break
		}
		delay, ok := retryDelay(err)
		if !ok {
			break
		}
		select {
		case <-ctx.Done():
			return pushResponse{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return pushResponse{}, lastErr
}

// retryDelay decides whether an error is worth one more try, and how long to wait.
func retryDelay(err error) (time.Duration, bool) {
	var status *relayStatusError
	if !errors.As(err, &status) {
		// A transport failure — a timeout or a broken connection. The request may well
		// have arrived, which is exactly the case idempotency covers.
		return retryDelayDefault, true
	}
	switch status.Status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusConflict:
		// 409 is a duplicate still in flight: the relay is asking us to wait for the
		// first attempt to finish, after which we get its response.
		if status.RetryAfter > retryAfterGiveUp {
			return 0, false
		}
		delay := status.RetryAfter
		if delay <= 0 {
			delay = retryDelayDefault
		}
		return min(delay, retryDelayCap), true
	default:
		// 400 means the payload is wrong and 401 means the credential is; neither
		// improves by being sent again.
		return 0, false
	}
}

// relayData carries the routing ids the app needs to open the right resource.
//
// The relay sets notification_id, installation_id, type, nav_target, action_set and
// actions itself and rejects the whole request if any of them appear here, so this is
// built in one place rather than assembled at call sites. The keys match what the mobile
// client reads in lib/pushpayload.ts.
func relayData(env Envelope) map[string]string {
	data := map[string]string{}
	for key, value := range map[string]string{
		"session_id":    env.SessionID,
		"goal_id":       env.GoalID,
		"schedule_name": env.ScheduleName,
		"task_id":       env.TaskID,
		"resource_id":   env.ResourceID,
	} {
		if value != "" {
			data[key] = value
		}
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

// credential returns this installation's relay credential, enrolling on first use.
//
// Enrollment is lazy on purpose: an installation that never registers a phone never
// contacts Podiom infrastructure at all.
func (c *RelayChannel) credential(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enrollment.Credential != "" {
		return c.enrollment.Credential, nil
	}

	stored, err := config.LoadRelayEnrollment(c.statePath)
	if err != nil {
		// Deliberately not treated as "not enrolled". Enrolling again would abandon the
		// existing tenant and every device under it, and the credential cannot be read
		// back, so the loss would be permanent.
		return "", fmt.Errorf("relay enrollment unreadable: %w", err)
	}
	if stored.Credential != "" {
		c.enrollment = stored
		return stored.Credential, nil
	}

	fresh, err := c.transport.Enroll(ctx)
	if err != nil {
		return "", fmt.Errorf("enroll with relay: %w", err)
	}
	// Persisted before it is used: a credential spent but not stored is a tenant that
	// can never be reached again.
	if err := config.SaveRelayEnrollment(c.statePath, fresh); err != nil {
		return "", fmt.Errorf("persist relay enrollment: %w", err)
	}
	c.enrollment = fresh
	c.log.Info("enrolled with push relay", "event", "notification", "instance", fresh.InstanceID)
	return fresh.Credential, nil
}

// relayStatusError is a non-2xx answer from the relay.
type relayStatusError struct {
	Status     int
	RetryAfter time.Duration
	Message    string
}

func (e *relayStatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("relay returned %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("relay returned %d", e.Status)
}

// httpRelay talks to the relay over authenticated outbound HTTPS.
//
// Outbound only: a self-hosted Podiom needs no inbound connectivity, no Firebase project
// and no APNs certificate for native push to work.
type httpRelay struct {
	baseURL string
	client  *http.Client
}

func (h *httpRelay) Enroll(ctx context.Context) (config.RelayEnrollment, error) {
	// Registration takes no request body at all; identity is minted server-side.
	var enrollment config.RelayEnrollment
	if err := h.do(ctx, http.MethodPost, "/v1/instances", "", "", nil, &enrollment); err != nil {
		return config.RelayEnrollment{}, err
	}
	if enrollment.InstanceID == "" || enrollment.Credential == "" {
		return config.RelayEnrollment{}, errors.New("relay returned an incomplete enrollment")
	}
	return enrollment, nil
}

func (h *httpRelay) PutDevice(ctx context.Context, credential, deviceID string, body putDeviceRequest) error {
	return h.do(ctx, http.MethodPut, "/v1/devices/"+deviceID, credential, "", body, nil)
}

func (h *httpRelay) DeleteDevice(ctx context.Context, credential, deviceID string) error {
	return h.do(ctx, http.MethodDelete, "/v1/devices/"+deviceID, credential, "", nil, nil)
}

func (h *httpRelay) Push(ctx context.Context, credential, idempotencyKey string, req pushRequest) (pushResponse, error) {
	var resp pushResponse
	if err := h.do(ctx, http.MethodPost, "/v1/push", credential, idempotencyKey, req, &resp); err != nil {
		return pushResponse{}, err
	}
	return resp, nil
}

// do performs one relay call. A non-2xx answer becomes a relayStatusError carrying the
// status and any Retry-After, which is what the retry decision reads.
func (h *httpRelay) do(ctx context.Context, method, path, credential, idempotencyKey string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode relay request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build relay request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("relay unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &relayStatusError{
			Status:     resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Message:    relayErrorMessage(resp.Body),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode relay response: %w", err)
	}
	return nil
}

// relayErrorMessage extracts the relay's error text, tolerating a body that is not the
// JSON envelope — an unrouted path answers in plain text.
func relayErrorMessage(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || len(raw) == 0 {
		return ""
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	return string(bytes.TrimSpace(raw))
}

// parseRetryAfter reads the header, which the relay always sends in seconds.
func parseRetryAfter(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// DeviceRegistrar mirrors device registrations to the push relay.
//
// Declared here so the server can mirror without depending on the relay's transport, and
// so an installation with no relay configured passes nil rather than a stub.
type DeviceRegistrar interface {
	RegisterDevice(ctx context.Context, device store.NotificationDevice) error
	RemoveDevice(ctx context.Context, deviceID string) error
}
