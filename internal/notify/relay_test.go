package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// fakeRelay stands in for the hosted relay, recording what it was asked to do and
// answering from a script.
type fakeRelay struct {
	enrollments int
	enrollErr   error

	pushes    []pushRequest
	pushKeys  []string
	pushErrs  []error // consumed one per attempt; nil means answer normally
	pushResp  pushResponse
	pushCalls int

	putDevices    map[string]putDeviceRequest
	deletedDevice []string
	deviceErr     error
}

func newFakeRelay() *fakeRelay {
	return &fakeRelay{putDevices: map[string]putDeviceRequest{}}
}

func (f *fakeRelay) Enroll(context.Context) (config.RelayEnrollment, error) {
	f.enrollments++
	if f.enrollErr != nil {
		return config.RelayEnrollment{}, f.enrollErr
	}
	return config.RelayEnrollment{InstanceID: "ins_test", Credential: "prk_ins_test.secret"}, nil
}

func (f *fakeRelay) PutDevice(_ context.Context, _, deviceID string, body putDeviceRequest) error {
	if f.deviceErr != nil {
		return f.deviceErr
	}
	f.putDevices[deviceID] = body
	return nil
}

func (f *fakeRelay) DeleteDevice(_ context.Context, _, deviceID string) error {
	if f.deviceErr != nil {
		return f.deviceErr
	}
	f.deletedDevice = append(f.deletedDevice, deviceID)
	return nil
}

func (f *fakeRelay) Push(_ context.Context, _, idempotencyKey string, req pushRequest) (pushResponse, error) {
	f.pushes = append(f.pushes, req)
	f.pushKeys = append(f.pushKeys, idempotencyKey)
	attempt := f.pushCalls
	f.pushCalls++
	if attempt < len(f.pushErrs) && f.pushErrs[attempt] != nil {
		return pushResponse{}, f.pushErrs[attempt]
	}
	return f.pushResp, nil
}

type errorReader struct{}

func (errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read failed")
}

// newRelayChannel builds a channel over the fake and a real store, with enrollment state
// in a throwaway directory.
func newRelayChannel(t *testing.T, transport relayTransport) (*RelayChannel, *store.Store, string) {
	t.Helper()
	db := newTestStore(t)
	statePath := filepath.Join(t.TempDir(), "relay.json")
	return &RelayChannel{
		transport:      transport,
		devices:        db,
		installationID: "install-1",
		statePath:      statePath,
		log:            testLogger(),
	}, db, statePath
}

func registerDevice(t *testing.T, db *store.Store, id, token string) {
	t.Helper()
	if _, err := db.UpsertNotificationDevice(context.Background(), store.NotificationDevice{
		ID: id, Platform: "ios", PushToken: token,
	}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

// accepted builds a relay response marking every named device accepted.
func accepted(ids ...string) pushResponse {
	resp := pushResponse{Accepted: len(ids)}
	for _, id := range ids {
		resp.Results = append(resp.Results, struct {
			DeviceID string `json:"device_id"`
			Status   string `json:"status"`
		}{DeviceID: id, Status: statusAccepted})
	}
	return resp
}

func withStatus(id, status string) pushResponse {
	return pushResponse{Rejected: 1, Results: []struct {
		DeviceID string `json:"device_id"`
		Status   string `json:"status"`
	}{{DeviceID: id, Status: status}}}
}

// TestRelayPushesByDeviceIDNotToken is the security property the relay's design turns on:
// a token in a request body has no ownership record to check it against, so devices are
// named by an id the relay resolves inside the authenticated tenant.
func TestRelayPushesByDeviceIDNotToken(t *testing.T) {
	fake := newFakeRelay()
	channel, db, _ := newRelayChannel(t, fake)
	registerDevice(t, db, "phone", "super-secret-fcm-token")
	fake.pushResp = accepted("phone")

	if _, err := channel.Send(context.Background(), Envelope{ID: "not-1", Title: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(fake.pushes) != 1 {
		t.Fatalf("relay called %d times, want 1", len(fake.pushes))
	}
	req := fake.pushes[0]
	if len(req.DeviceIDs) != 1 || req.DeviceIDs[0] != "phone" {
		t.Errorf("DeviceIDs = %v, want [phone]", req.DeviceIDs)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-fcm-token") {
		t.Errorf("the push request carries a push token:\n%s", raw)
	}
}

// TestRelayRequestShape pins the wire format the relay actually accepts.
func TestRelayRequestShape(t *testing.T) {
	fake := newFakeRelay()
	channel, db, _ := newRelayChannel(t, fake)
	registerDevice(t, db, "phone", "token")
	fake.pushResp = accepted("phone")

	if _, err := channel.Send(context.Background(), Envelope{
		ID: "not-1", Type: TypeGoalActionRequested, Importance: "important",
		Title: "Alice needs your help", Body: "Publish the announcement",
		NavTarget: NavGoalActionItem, ActionSet: ActionSetGoalActionItem,
		GoalID: "goal-1", SessionID: "sess-1", ResourceID: "item-1",
		Actions: []Action{{ID: ActionDone, Label: "Done"}},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	raw, err := json.Marshal(fake.pushes[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"notification_id", "installation_id", "device_ids", "notification"} {
		if _, ok := got[field]; !ok {
			t.Errorf("request is missing %q", field)
		}
	}
	// The presentable fields are nested; the relay reads them nowhere else.
	notification, ok := got["notification"].(map[string]any)
	if !ok {
		t.Fatalf("notification is %T, want an object", got["notification"])
	}
	for _, field := range []string{"type", "importance", "title", "body", "nav_target", "action_set", "actions", "data"} {
		if _, ok := notification[field]; !ok {
			t.Errorf("notification is missing %q", field)
		}
	}
	data, ok := notification["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want an object", notification["data"])
	}
	for _, field := range []string{"goal_id", "session_id", "resource_id"} {
		if _, ok := data[field]; !ok {
			t.Errorf("data is missing %q, which the app needs to route", field)
		}
	}
}

// TestRelayDataCarriesNoReservedKey guards a rejection that would drop every push.
//
// The relay sets these keys itself and answers 400 if the caller does too, so an
// accidental addition to relayData would break delivery entirely rather than degrade it.
func TestRelayDataCarriesNoReservedKey(t *testing.T) {
	reserved := []string{
		"notification_id", "installation_id", "type", "nav_target", "action_set", "actions",
		"from", "message_type", "collapse_key", "notification",
	}
	data := relayData(Envelope{
		ID: "not-1", Type: TypeGoalActionRequested, NavTarget: NavGoalActionItem,
		ActionSet: ActionSetGoalActionItem, SessionID: "sess-1", GoalID: "goal-1",
		ScheduleName: "nightly", TaskID: "task-1", ResourceID: "item-1",
		Actions: []Action{{ID: ActionDone, Label: "Done"}},
	})
	for _, key := range reserved {
		if _, present := data[key]; present {
			t.Errorf("data carries the reserved key %q; the relay rejects the whole request", key)
		}
	}
	for key := range data {
		if strings.HasPrefix(key, "google.") || strings.HasPrefix(key, "gcm.") {
			t.Errorf("data carries the reserved key %q", key)
		}
	}
	// What it must carry, because the mobile client routes on exactly these.
	for _, key := range []string{"session_id", "goal_id", "schedule_name", "task_id", "resource_id"} {
		if _, present := data[key]; !present {
			t.Errorf("data is missing %q", key)
		}
	}
}

// TestRelayStatusHandling covers each verdict's local effect.
func TestRelayStatusHandling(t *testing.T) {
	tests := []struct {
		status      string
		wantErr     bool
		wantInvalid bool
	}{
		{statusAccepted, false, false},
		// Both of these mean unreachable as things stand, and both are revived by the app
		// registering a fresh token.
		{statusUnregistered, true, true},
		{statusUnknownDevice, true, true},
		// Transient, and the device is not implicated, so its status is left alone.
		{statusFailed, true, false},
		// A newer relay must not be able to silently disable a working phone.
		{"something_new", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			fake := newFakeRelay()
			channel, db, _ := newRelayChannel(t, fake)
			ctx := context.Background()
			registerDevice(t, db, "phone", "token")
			fake.pushResp = withStatus("phone", tc.status)

			results, err := channel.Send(ctx, Envelope{ID: "not-1", Title: "hello"})
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("len(results) = %d, want 1", len(results))
			}
			if (results[0].Err != nil) != tc.wantErr {
				t.Errorf("Err = %v, want error: %v", results[0].Err, tc.wantErr)
			}
			device, err := db.GetNotificationDevice(ctx, "phone")
			if err != nil {
				t.Fatalf("get device: %v", err)
			}
			wantStatus := store.NotificationDeviceActive
			if tc.wantInvalid {
				wantStatus = store.NotificationDeviceInvalid
			}
			if device.Status != wantStatus {
				t.Errorf("device status = %q, want %q", device.Status, wantStatus)
			}
			// The row survives either way: deleting it would lose the user's mute choice.
			if device.ID != "phone" {
				t.Error("the device row was removed")
			}
		})
	}
}

// TestInvalidDevicesAreSkipped checks a device the relay has written off is not paid for
// on every subsequent notification.
func TestInvalidDevicesAreSkipped(t *testing.T) {
	fake := newFakeRelay()
	channel, db, _ := newRelayChannel(t, fake)
	ctx := context.Background()
	registerDevice(t, db, "live", "token-live")
	registerDevice(t, db, "dead", "token-dead")
	if err := db.SetNotificationDeviceStatus(ctx, "dead", store.NotificationDeviceInvalid); err != nil {
		t.Fatalf("mark invalid: %v", err)
	}
	fake.pushResp = accepted("live")

	if _, err := channel.Send(ctx, Envelope{ID: "not-1", Title: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := fake.pushes[0].DeviceIDs; len(got) != 1 || got[0] != "live" {
		t.Errorf("DeviceIDs = %v, want [live]", got)
	}

	// Re-registering revives it, on both sides.
	registerDevice(t, db, "dead", "token-fresh")
	device, err := db.GetNotificationDevice(ctx, "dead")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if device.Status != store.NotificationDeviceActive {
		t.Errorf("status after a fresh token = %q, want active", device.Status)
	}
}

// TestMutedDeviceStaysMutedThroughATokenRotation is why the row is marked rather than
// deleted: deleting it would lose the mute, and the next registration would bring the
// device back notifying.
func TestMutedDeviceStaysMutedThroughATokenRotation(t *testing.T) {
	_, db, _ := newRelayChannel(t, newFakeRelay())
	ctx := context.Background()
	registerDevice(t, db, "phone", "token-1")
	if _, err := db.SetNotificationDeviceEnabled(ctx, "phone", false); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if err := db.SetNotificationDeviceStatus(ctx, "phone", store.NotificationDeviceInvalid); err != nil {
		t.Fatalf("mark invalid: %v", err)
	}

	registerDevice(t, db, "phone", "token-2")

	device, err := db.GetNotificationDevice(ctx, "phone")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if device.Enabled {
		t.Error("a token rotation un-muted a device the user had switched off")
	}
	if device.Status != store.NotificationDeviceActive {
		t.Errorf("status = %q, want active — a fresh token makes it reachable again", device.Status)
	}
}

// TestRelaySendsTheNotificationIDAsIdempotencyKey checks a retry cannot become a second
// buzz for one notification.
func TestRelaySendsTheNotificationIDAsIdempotencyKey(t *testing.T) {
	fake := newFakeRelay()
	channel, db, _ := newRelayChannel(t, fake)
	registerDevice(t, db, "phone", "token")
	fake.pushResp = accepted("phone")

	if _, err := channel.Send(context.Background(), Envelope{ID: "not-42", Title: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(fake.pushKeys) != 1 || fake.pushKeys[0] != "not-42" {
		t.Errorf("idempotency keys = %v, want [not-42]", fake.pushKeys)
	}
}

// TestRelayRetries covers which failures earn a second attempt. Retrying is only safe at
// all because of the idempotency key above.
func TestRelayRetries(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		attempts int
	}{
		{"rate limited", &relayStatusError{Status: http.StatusTooManyRequests, RetryAfter: time.Second}, 2},
		{"unavailable", &relayStatusError{Status: http.StatusServiceUnavailable, RetryAfter: time.Second}, 2},
		{"duplicate in flight", &relayStatusError{Status: http.StatusConflict, RetryAfter: time.Second}, 2},
		// A timeout may well have arrived; idempotency covers exactly that case.
		{"transport failure", errors.New("relay unreachable"), 2},
		// Neither of these improves by being sent again.
		{"bad request", &relayStatusError{Status: http.StatusBadRequest}, 1},
		{"unauthorized", &relayStatusError{Status: http.StatusUnauthorized}, 1},
		// An hour means the hourly quota is spent, and a replay still costs quota.
		{"quota exhausted", &relayStatusError{Status: http.StatusTooManyRequests, RetryAfter: time.Hour}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeRelay()
			channel, db, _ := newRelayChannel(t, fake)
			registerDevice(t, db, "phone", "token")
			// Fail every attempt so the count is what is being measured.
			fake.pushErrs = []error{tc.err, tc.err}

			if _, err := channel.Send(context.Background(), Envelope{ID: "not-1", Title: "hello"}); err == nil {
				t.Fatal("expected the send to fail")
			}
			if fake.pushCalls != tc.attempts {
				t.Errorf("attempts = %d, want %d", fake.pushCalls, tc.attempts)
			}
		})
	}
}

// TestRelayRetrySucceedsOnTheSecondAttempt checks a transient failure ends in delivery
// rather than a dropped push.
func TestRelayRetrySucceedsOnTheSecondAttempt(t *testing.T) {
	fake := newFakeRelay()
	channel, db, _ := newRelayChannel(t, fake)
	registerDevice(t, db, "phone", "token")
	fake.pushErrs = []error{&relayStatusError{Status: http.StatusServiceUnavailable, RetryAfter: time.Second}}
	fake.pushResp = accepted("phone")

	results, err := channel.Send(context.Background(), Envelope{ID: "not-1", Title: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if fake.pushCalls != 2 {
		t.Errorf("attempts = %d, want 2", fake.pushCalls)
	}
	if len(results) != 1 || results[0].Err != nil {
		t.Errorf("results = %+v, want one accepted", results)
	}
	// Both attempts carry the same key, which is what makes the repeat safe.
	if len(fake.pushKeys) != 2 || fake.pushKeys[0] != fake.pushKeys[1] {
		t.Errorf("idempotency keys = %v, want the same key twice", fake.pushKeys)
	}
}

// TestRelayEnrollsOnceAndPersists checks the credential is stored before use and reused
// afterwards. It cannot be read back from the relay, so losing it orphans the tenant.
func TestRelayEnrollsOnceAndPersists(t *testing.T) {
	fake := newFakeRelay()
	channel, db, statePath := newRelayChannel(t, fake)
	ctx := context.Background()
	registerDevice(t, db, "phone", "token")
	fake.pushResp = accepted("phone")

	if _, err := channel.Send(ctx, Envelope{ID: "not-1", Title: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if fake.enrollments != 1 {
		t.Errorf("enrollments = %d, want 1", fake.enrollments)
	}
	stored, err := config.LoadRelayEnrollment(statePath)
	if err != nil {
		t.Fatalf("load enrollment: %v", err)
	}
	if stored.InstanceID != "ins_test" || stored.Credential == "" {
		t.Errorf("stored enrollment = %+v, want the relay's answer persisted", stored)
	}

	// A fresh channel over the same state file must reuse it rather than enrol again:
	// registrations are capped per address and a second tenant abandons the first.
	second := &RelayChannel{transport: fake, devices: db, statePath: statePath, log: testLogger()}
	if _, err := second.Send(ctx, Envelope{ID: "not-2", Title: "hello"}); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if fake.enrollments != 1 {
		t.Errorf("enrollments after restart = %d, want still 1", fake.enrollments)
	}
}

// TestRelayRefusesToReEnrollWhenStateIsUnreadable is the safeguard that matters most
// here: treating an unreadable credential as "not enrolled" would abandon the tenant and
// every device under it, permanently, since the credential cannot be recovered.
func TestRelayRefusesToReEnrollWhenStateIsUnreadable(t *testing.T) {
	fake := newFakeRelay()
	channel, db, statePath := newRelayChannel(t, fake)
	registerDevice(t, db, "phone", "token")
	if err := os.WriteFile(statePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if _, err := channel.Send(context.Background(), Envelope{ID: "not-1", Title: "hello"}); err == nil {
		t.Fatal("expected the send to fail rather than re-enrol")
	}
	if fake.enrollments != 0 {
		t.Errorf("enrollments = %d, want 0 — an unreadable credential must not be replaced", fake.enrollments)
	}
}

func TestRelayMirrorsDeviceRegistration(t *testing.T) {
	fake := newFakeRelay()
	channel, _, _ := newRelayChannel(t, fake)
	ctx := context.Background()

	if err := channel.RegisterDevice(ctx, store.NotificationDevice{
		ID: "phone", Platform: "ios", PushToken: "token-1", Label: "My iPhone", AppVersion: "1.0",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := fake.putDevices["phone"]
	if !ok {
		t.Fatal("the device was not mirrored to the relay")
	}
	if got.FCMToken != "token-1" || got.Platform != "ios" {
		t.Errorf("mirrored %+v, want the token and platform", got)
	}
	// Label and app version stay local: the relay has no use for them, and holding them
	// would widen what a breach there exposes for no delivery benefit.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "My iPhone") || strings.Contains(string(raw), "1.0") {
		t.Errorf("device metadata reached the relay: %s", raw)
	}

	if err := channel.RemoveDevice(ctx, "phone"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fake.deletedDevice) != 1 || fake.deletedDevice[0] != "phone" {
		t.Errorf("deleted = %v, want [phone]", fake.deletedDevice)
	}
}

// TestRelayBatchesLargeFleets checks the relay's hundred-device ceiling is respected.
func TestRelayBatchesLargeFleets(t *testing.T) {
	fake := newFakeRelay()
	channel, db, _ := newRelayChannel(t, fake)
	ctx := context.Background()
	for i := range maxDevicesPerPush + 5 {
		registerDevice(t, db, "device-"+strconv.Itoa(i), "token-"+strconv.Itoa(i))
	}

	if _, err := channel.Send(ctx, Envelope{ID: "not-1", Title: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(fake.pushes) != 2 {
		t.Fatalf("relay called %d times, want 2 batches", len(fake.pushes))
	}
	for i, req := range fake.pushes {
		if len(req.DeviceIDs) > maxDevicesPerPush {
			t.Errorf("batch %d carries %d devices, over the relay's limit of %d",
				i, len(req.DeviceIDs), maxDevicesPerPush)
		}
	}
}

func TestNewRelayChannelWithoutConfigIsNil(t *testing.T) {
	db := newTestStore(t)
	statePath := filepath.Join(t.TempDir(), "relay.json")
	if channel := NewRelayChannel(db, "", statePath, "install-1", testLogger()); channel != nil {
		t.Error("a blank relay URL should yield no channel")
	}
	if channel := NewRelayChannel(nil, "https://relay.example", statePath, "install-1", testLogger()); channel != nil {
		t.Error("a missing device store should yield no channel")
	}
	if channel := NewRelayChannel(db, "https://relay.example", "", "install-1", testLogger()); channel != nil {
		t.Error("nowhere to persist the enrollment should yield no channel")
	}
}

func TestRelayChannelWithNoDevicesDoesNotCallTheRelay(t *testing.T) {
	fake := newFakeRelay()
	channel, _, _ := newRelayChannel(t, fake)

	results, err := channel.Send(context.Background(), Envelope{ID: "not-1", Title: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(results) != 0 || len(fake.pushes) != 0 || fake.enrollments != 0 {
		t.Errorf("results=%d pushes=%d enrollments=%d, want 0/0/0 — nothing to deliver to means "+
			"no reason to contact Podiom infrastructure", len(results), len(fake.pushes), fake.enrollments)
	}
}

func TestRelayErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body io.Reader
		want string
	}{
		{name: "Valid JSON", body: strings.NewReader(`{"error":{"code":"...","message":"rate limited"}}`), want: "rate limited"},
		{name: "Valid JSON with no message", body: strings.NewReader(`{"error":{"code":"..."}}`), want: `{"error":{"code":"..."}}`},
		{name: "Plan test", body: strings.NewReader(`"message":"rate limited"`), want: `"message":"rate limited"`},
		{name: "Long plan text", body: strings.NewReader(strings.Repeat("a", 5000)), want: strings.Repeat("a", 4096)},
		{name: "Empty body", body: strings.NewReader(""), want: ""},
		{name: "Invalid reader", body: errorReader{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relayErrorMessage(tt.body)
			if got != tt.want {
				t.Errorf("want: %q, got %q", tt.want, got)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "Postive integer", raw: "30", want: 30 * time.Second},
		{name: "Negtive number", raw: "-5", want: 0},
		{name: "Non-numeric", raw: "soon", want: 0},
		{name: "Decimal", raw: "5.5", want: 0},
		{name: "Zero", raw: "0", want: 0},
		{name: "Empty string", raw: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.raw)
			if got != tt.want {
				t.Errorf("for: %s, want: %v, got: %v", tt.raw, tt.want, got)
			}
		})
	}
}
