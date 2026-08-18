package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
