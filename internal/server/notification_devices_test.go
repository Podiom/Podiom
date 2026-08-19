package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

// registerDeviceAPI registers a device through the HTTP surface.
func registerDeviceAPI(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.handleNotificationDevices(rr,
		httptest.NewRequest(http.MethodPost, "/api/notification-devices", bytes.NewReader([]byte(body))))
	return rr
}

// TestDeviceRegistrationNeverEchoesThePushToken is the guarantee that matters here.
// Anything holding the token can reach the device, and this list is read by the
// dashboard, so the token must not come back out of the API at all.
func TestDeviceRegistrationNeverEchoesThePushToken(t *testing.T) {
	srv, _, _ := newNotifyTestServer(t)
	const token = "super-secret-fcm-token"

	rr := registerDeviceAPI(t, srv, `{"device_id":"device-1","platform":"ios","label":"iPhone","push_token":"`+token+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), token) {
		t.Errorf("the registration response echoed the push token:\n%s", rr.Body.String())
	}

	list := httptest.NewRecorder()
	srv.handleNotificationDevices(list, httptest.NewRequest(http.MethodGet, "/api/notification-devices", nil))
	if strings.Contains(list.Body.String(), token) {
		t.Errorf("the device list contains the push token:\n%s", list.Body.String())
	}
}

// TestDeviceRegistrationReportsTheInstallation checks a registering app learns which
// Podiom it is talking to, so an app connected to several can label them and knows
// where to send its notification actions.
//
// The server is built through New with the option set, rather than by assigning the
// field, because the bug this guards against is the option not being wired at all —
// which a test that pokes the field directly cannot see.
func TestDeviceRegistrationReportsTheInstallation(t *testing.T) {
	srv, _, _ := newNotifyTestServerWithOptions(t, func(opts *Options) {
		opts.InstallationID = "install-abc"
	})

	rr := registerDeviceAPI(t, srv, `{"device_id":"device-1","platform":"android","push_token":"token-a"}`)
	var got notificationDeviceRegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.InstallationID != "install-abc" {
		t.Errorf("InstallationID = %q, want install-abc", got.InstallationID)
	}
	if got.Device.ID != "device-1" || !got.Device.Enabled {
		t.Errorf("device = %+v, want device-1 enabled", got.Device)
	}
}

func TestDeviceRegistrationValidation(t *testing.T) {
	srv, _, _ := newNotifyTestServer(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"unknown platform", `{"device_id":"d","platform":"blackberry","push_token":"t"}`, http.StatusBadRequest},
		{"missing platform", `{"device_id":"d","push_token":"t"}`, http.StatusBadRequest},
		{"missing token", `{"device_id":"d","platform":"ios"}`, http.StatusBadRequest},
		{"malformed json", `{`, http.StatusBadRequest},
		{"valid", `{"device_id":"d","platform":"ios","push_token":"t"}`, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rr := registerDeviceAPI(t, srv, tc.body); rr.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// TestDeviceRegistrationWithoutAnIDStillRegisters checks the server generates the
// opaque id when an app has not stored one yet.
func TestDeviceRegistrationWithoutAnIDStillRegisters(t *testing.T) {
	srv, _, _ := newNotifyTestServer(t)

	rr := registerDeviceAPI(t, srv, `{"platform":"ios","push_token":"token-a"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got notificationDeviceRegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Device.ID == "" {
		t.Error("no device id was assigned")
	}
}

func TestDeviceEnableDisableAndDelete(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	registerDeviceAPI(t, srv, `{"device_id":"device-1","platform":"ios","push_token":"token-a"}`)

	post := func(path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleNotificationDevice(rr, httptest.NewRequest(http.MethodPost, path, nil))
		return rr
	}

	if rr := post("/api/notification-devices/device-1/disable"); rr.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	device, err := db.GetNotificationDevice(ctx, "device-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if device.Enabled {
		t.Error("device is still enabled after disable")
	}

	if rr := post("/api/notification-devices/device-1/enable"); rr.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want 200", rr.Code)
	}

	del := httptest.NewRecorder()
	srv.handleNotificationDevice(del, httptest.NewRequest(http.MethodDelete, "/api/notification-devices/device-1", nil))
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", del.Code, del.Body.String())
	}
	devices, err := db.ListNotificationDevices(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("len(devices) = %d after delete, want 0", len(devices))
	}
}

func TestDeviceEndpointsRejectBadRequests(t *testing.T) {
	srv, _, _ := newNotifyTestServer(t)
	registerDeviceAPI(t, srv, `{"device_id":"device-1","platform":"ios","push_token":"token-a"}`)

	tests := []struct {
		name    string
		method  string
		path    string
		handler func(http.ResponseWriter, *http.Request)
		want    int
	}{
		{"unknown action", http.MethodPost, "/api/notification-devices/device-1/explode", srv.handleNotificationDevice, http.StatusNotFound},
		{"missing id", http.MethodPost, "/api/notification-devices/", srv.handleNotificationDevice, http.StatusBadRequest},
		{"enable a missing device", http.MethodPost, "/api/notification-devices/nope/enable", srv.handleNotificationDevice, http.StatusNotFound},
		{"collection rejects patch", http.MethodPatch, "/api/notification-devices", srv.handleNotificationDevices, http.StatusMethodNotAllowed},
		{"item rejects get", http.MethodGet, "/api/notification-devices/device-1/enable", srv.handleNotificationDevice, http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.handler(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// testPushChannel stands in for the relay channel: it records every envelope it is
// handed and returns whatever verdicts the test configured.
type testPushChannel struct {
	got     []notify.Envelope
	results []notify.Result
	err     error
}

func (c *testPushChannel) Name() string { return "native_push" }

func (c *testPushChannel) Send(_ context.Context, env notify.Envelope) ([]notify.Result, error) {
	c.got = append(c.got, env)
	return c.results, c.err
}

// newTestPushServer builds a server whose test push reaches a fake relay channel.
func newTestPushServer(t *testing.T, channel notify.Channel) (*Server, *store.Store) {
	t.Helper()
	srv, db, _ := newNotifyTestServerWithOptions(t, func(opts *Options) {
		opts.NativePush = channel
	})
	return srv, db
}

// testPush calls the endpoint the button is wired to.
func testPush(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.handleNotificationTestPush(rr,
		httptest.NewRequest(http.MethodPost, "/api/notification-devices/test", nil))
	return rr
}

// TestTestPushUsesAFreshNotificationIDEachTime is the trap this endpoint exists to
// avoid. The relay uses the notification id as its idempotency key with a 24h TTL, so
// a fixed id makes the second press replay the first response and send nothing to FCM —
// a test button that reports success without testing anything.
func TestTestPushUsesAFreshNotificationIDEachTime(t *testing.T) {
	channel := &testPushChannel{}
	srv, _ := newTestPushServer(t, channel)

	for i := range 2 {
		if rr := testPush(t, srv); rr.Code != http.StatusOK {
			t.Fatalf("press %d: status = %d, want 200; body=%s", i+1, rr.Code, rr.Body.String())
		}
	}
	if len(channel.got) != 2 {
		t.Fatalf("channel saw %d pushes, want 2", len(channel.got))
	}
	first, second := channel.got[0].ID, channel.got[1].ID
	if first == "" || second == "" {
		t.Fatalf("notification ids must not be empty: %q, %q", first, second)
	}
	if first == second {
		t.Errorf("both presses used notification id %q; the relay would replay the first response and send nothing", first)
	}
}

// TestTestPushCarriesNoActionSet keeps the test notification inside the relay's
// vocabulary. The relay validates action sets against a closed set of five and drops
// anything else with an `unknown_action_set` warning, and there is no stored
// notification behind a test for a button to act on anyway.
func TestTestPushCarriesNoActionSet(t *testing.T) {
	channel := &testPushChannel{}
	srv, _ := newTestPushServer(t, channel)

	if rr := testPush(t, srv); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	env := channel.got[0]
	if env.ActionSet != "" {
		t.Errorf("ActionSet = %q, want empty", env.ActionSet)
	}
	if len(env.Actions) != 0 {
		t.Errorf("Actions = %v, want none", env.Actions)
	}
	if env.Title == "" || env.Body == "" {
		t.Errorf("a test notification needs a title and body; got %q / %q", env.Title, env.Body)
	}
}

// TestTestPushReportsEachDeviceVerdict is the reason the endpoint returns per-device
// results at all: a push that no device accepts must not read as a success.
func TestTestPushReportsEachDeviceVerdict(t *testing.T) {
	channel := &testPushChannel{results: []notify.Result{
		{Destination: "device-1"},
		{Destination: "device-2", Err: errors.New("device is not reachable: unregistered")},
	}}
	srv, _ := newTestPushServer(t, channel)
	registerDeviceAPI(t, srv, `{"device_id":"device-1","platform":"ios","label":"iPhone","push_token":"token-a"}`)
	registerDeviceAPI(t, srv, `{"device_id":"device-2","platform":"android","label":"Pixel","push_token":"token-b"}`)

	rr := testPush(t, srv)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Results []notificationTestResult `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results = %d, want 2; body=%s", len(got.Results), rr.Body.String())
	}

	first := got.Results[0]
	if first.Status != "accepted" || first.Error != "" {
		t.Errorf("device-1 = %+v, want accepted with no error", first)
	}
	if first.Label != "iPhone" || first.Platform != "ios" {
		t.Errorf("device-1 label/platform = %q/%q, want iPhone/ios", first.Label, first.Platform)
	}

	second := got.Results[1]
	if second.Status != "failed" {
		t.Errorf("device-2 status = %q, want failed", second.Status)
	}
	if !strings.Contains(second.Error, "unregistered") {
		t.Errorf("device-2 error = %q, want the relay's reason verbatim", second.Error)
	}
}

// TestTestPushWithNoDevicesIsNotASuccess covers the silent-success case: the relay
// channel returns no results and no error when nothing is registered, which the
// notification engine records as delivered. The endpoint must report the emptiness.
func TestTestPushWithNoDevicesIsNotASuccess(t *testing.T) {
	srv, _ := newTestPushServer(t, &testPushChannel{})

	rr := testPush(t, srv)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Results []notificationTestResult `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Results) != 0 {
		t.Errorf("results = %+v, want none when no device is registered", got.Results)
	}
}

// TestTestPushWithoutARelayIsRefused checks the option is actually wired. A server built
// without a relay must say so rather than answer 200 with an empty result set, which is
// indistinguishable from "no devices registered".
func TestTestPushWithoutARelayIsRefused(t *testing.T) {
	srv, _, _ := newNotifyTestServer(t)

	rr := testPush(t, srv)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

// TestTestPushSurfacesAChannelFailure keeps a dead relay from looking like a delivered
// push. A channel-level error is the transport failing, not a device being unreachable.
func TestTestPushSurfacesAChannelFailure(t *testing.T) {
	srv, _ := newTestPushServer(t, &testPushChannel{err: errors.New("relay unreachable")})

	rr := testPush(t, srv)
	if rr.Code == http.StatusOK {
		t.Fatalf("a failing relay answered 200: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "relay unreachable") {
		t.Errorf("body = %q, want the transport error", rr.Body.String())
	}
}

// TestTestPushNeverEchoesThePushToken holds the same line as registration: the token
// must not leave the daemon through any response.
func TestTestPushNeverEchoesThePushToken(t *testing.T) {
	const token = "super-secret-fcm-token"
	channel := &testPushChannel{results: []notify.Result{{Destination: "device-1"}}}
	srv, _ := newTestPushServer(t, channel)
	registerDeviceAPI(t, srv, `{"device_id":"device-1","platform":"ios","push_token":"`+token+`"}`)

	rr := testPush(t, srv)
	if strings.Contains(rr.Body.String(), token) {
		t.Errorf("the test push response echoed the push token:\n%s", rr.Body.String())
	}
}

// TestTestPushRejectsNonPost keeps the endpoint from being triggered by a GET, which a
// browser or crawler could issue without the user asking for it.
func TestTestPushRejectsNonPost(t *testing.T) {
	srv, _ := newTestPushServer(t, &testPushChannel{})

	rr := httptest.NewRecorder()
	srv.handleNotificationTestPush(rr,
		httptest.NewRequest(http.MethodGet, "/api/notification-devices/test", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}
