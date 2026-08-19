package server

import (
	"encoding/json"
	"net/http"
	"strings"

	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/google/uuid"
)

// notificationDeviceRegisterRequest is what a mobile app sends to register itself.
type notificationDeviceRegisterRequest struct {
	// DeviceID is generated and kept by the app, so a token refresh updates the
	// existing registration rather than creating a second one for the same phone.
	DeviceID string `json:"device_id"`
	Platform string `json:"platform"`
	Label    string `json:"label,omitempty"`
	// PushToken is sensitive routing information. It is accepted here and never
	// returned anywhere.
	PushToken  string `json:"push_token"`
	AppVersion string `json:"app_version,omitempty"`
}

// notificationDeviceView is a registration as the API reports it.
//
// The push token is deliberately absent. Anything holding it can reach the device,
// and a registration list is read by the dashboard, so there is no version of
// echoing it back that is worth the risk.
type notificationDeviceView struct {
	ID         string `json:"id"`
	Platform   string `json:"platform"`
	Label      string `json:"label"`
	AppVersion string `json:"app_version"`
	// Enabled is the user's mute; Status is delivery health. They answer different
	// questions and a device can be enabled and invalid at once, so both are reported.
	Enabled    bool   `json:"enabled"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	LastSeenAt string `json:"last_seen_at"`
}

// notificationDeviceRegisterResponse confirms a registration and names the
// installation, so an app connected to several Podioms can label this one and knows
// which installation to send its notification actions back to.
type notificationDeviceRegisterResponse struct {
	Device         notificationDeviceView `json:"device"`
	InstallationID string                 `json:"installation_id"`
}

// handleNotificationDevices lists and registers native push devices.
func (s *Server) handleNotificationDevices(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	db := s.core.Store()
	switch r.Method {
	case http.MethodGet:
		devices, err := db.ListNotificationDevices(r.Context(), false)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		views := make([]notificationDeviceView, 0, len(devices))
		for _, device := range devices {
			views = append(views, notificationDeviceViewOf(device))
		}
		writeJSON(w, map[string]any{"devices": views, "installation_id": s.installationID}, nil)
	case http.MethodPost:
		var req notificationDeviceRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		platform := strings.TrimSpace(req.Platform)
		if platform != "ios" && platform != "android" {
			http.Error(w, "platform must be ios or android", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.PushToken) == "" {
			http.Error(w, "push_token is required", http.StatusBadRequest)
			return
		}
		device, err := db.UpsertNotificationDevice(r.Context(), store.NotificationDevice{
			ID:         strings.TrimSpace(req.DeviceID),
			Platform:   platform,
			Label:      strings.TrimSpace(req.Label),
			PushToken:  strings.TrimSpace(req.PushToken),
			AppVersion: strings.TrimSpace(req.AppVersion),
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.mirrorDeviceToRelay(r, device)
		writeJSON(w, notificationDeviceRegisterResponse{
			Device:         notificationDeviceViewOf(device),
			InstallationID: s.installationID,
		}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleNotificationDevice mutes, unmutes, or removes one device at
// /api/notification-devices/{id}[/{action}].
func (s *Server) handleNotificationDevice(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/notification-devices/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "device id is required", http.StatusBadRequest)
		return
	}
	db := s.core.Store()

	if action == "" && r.Method == http.MethodDelete {
		if err := db.DeleteNotificationDevice(r.Context(), id); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.forgetDeviceAtRelay(r, id)
		writeJSON(w, map[string]string{"deleted": id}, nil)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch action {
	// Enabling and disabling is registration state — whether this phone receives
	// anything — and is separate from notification preferences, which decide which
	// events matter. Muting one device must not change what the others get.
	case "enable", "disable":
		device, err := db.SetNotificationDeviceEnabled(r.Context(), id, action == "enable")
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, notificationDeviceViewOf(device), nil)
	default:
		http.Error(w, "unknown device action", http.StatusNotFound)
	}
}

// mirrorDeviceToRelay gives the relay the routing record it needs to resolve this device
// id when a notification is pushed to it.
//
// Best effort on purpose. The local registration has already succeeded, and a device the
// relay does not know about heals itself: the push comes back `unknown_device`, the row is
// marked invalid, and the app re-registers on its next launch. Failing the request instead
// would make a relay outage look like a broken app.
func (s *Server) mirrorDeviceToRelay(r *http.Request, device store.NotificationDevice) {
	if s.registrar == nil {
		return
	}
	if err := s.registrar.RegisterDevice(r.Context(), device); err != nil {
		s.log.Warn("mirror device to relay failed", "event", "notification",
			"device", device.ID, podiomlog.ErrorAttr(err))
	}
}

// forgetDeviceAtRelay removes the relay's routing record for a device.
//
// Also best effort: a record left behind can only be pushed to by this installation, and
// the next push to it answers `unknown_device` locally rather than reaching anyone.
func (s *Server) forgetDeviceAtRelay(r *http.Request, deviceID string) {
	if s.registrar == nil {
		return
	}
	if err := s.registrar.RemoveDevice(r.Context(), deviceID); err != nil {
		s.log.Warn("remove device from relay failed", "event", "notification",
			"device", deviceID, podiomlog.ErrorAttr(err))
	}
}

// notificationDeviceViewOf drops the push token on its way out.
func notificationDeviceViewOf(d store.NotificationDevice) notificationDeviceView {
	return notificationDeviceView{
		ID:         d.ID,
		Platform:   d.Platform,
		Label:      d.Label,
		AppVersion: d.AppVersion,
		Enabled:    d.Enabled,
		Status:     d.Status,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
		LastSeenAt: d.LastSeenAt,
	}
}

// notificationTestResult is one device's outcome from a test push.
type notificationTestResult struct {
	DeviceID string `json:"device_id"`
	Label    string `json:"label"`
	Platform string `json:"platform"`
	// Status is "accepted" when the relay took the message for this device, and
	// "failed" otherwise. It is not a claim that anyone saw the notification.
	Status string `json:"status"`
	// Error is the relay's reason, verbatim, when Status is not "accepted". This is
	// the whole point of the endpoint: a push that no device accepts is otherwise
	// indistinguishable from one that worked.
	Error string `json:"error,omitempty"`
}

// handleNotificationTestPush sends a real push to every registered device and reports
// what the relay said about each one.
//
// It deliberately bypasses notify.Engine. The engine filters by per-type preference and
// dedupes, both of which are right for real notifications and wrong for a test the user
// just asked for: a muted preference would make the button do nothing and say nothing.
func (s *Server) handleNotificationTestPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.nativePush == nil {
		http.Error(w, "native push is not configured on this installation", http.StatusServiceUnavailable)
		return
	}

	devices, err := s.core.Store().ListNotificationDevices(r.Context(), true)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}

	results, err := s.nativePush.Send(r.Context(), notify.Envelope{
		// A fresh id every time. The relay uses the notification id as its idempotency
		// key with a 24h TTL, so a fixed one would replay the first response and send
		// nothing to FCM — a test button that reports success without testing anything.
		ID:             uuid.NewString(),
		InstallationID: s.installationID,
		// No type: a test is not one of the registry's notification kinds, and the
		// client only reads the type for routing, which this deliberately has none of.
		// Time-sensitive, so a phone in Focus still shows it. A test the user is
		// watching for is worth interrupting; being held silently would read as a
		// delivery failure.
		Importance: string(store.NotificationImportant),
		Title:      "Podiom test notification",
		Body:       "If you can see this, push notifications are working.",
		// No action set and no nav target: tap-to-open, no buttons. There is no stored
		// notification behind this, so there is nothing for a button to act on.
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}

	labels := make(map[string]store.NotificationDevice, len(devices))
	for _, device := range devices {
		labels[device.ID] = device
	}
	views := make([]notificationTestResult, 0, len(results))
	for _, result := range results {
		view := notificationTestResult{DeviceID: result.Destination, Status: "accepted"}
		if device, ok := labels[result.Destination]; ok {
			view.Label = device.Label
			view.Platform = device.Platform
		}
		if result.Err != nil {
			view.Status = "failed"
			view.Error = result.Err.Error()
		}
		views = append(views, view)
	}
	// An empty list means no device was eligible — none registered, all muted, or all
	// marked invalid. Reported as itself rather than as a successful send.
	writeJSON(w, map[string]any{"results": views}, nil)
}
