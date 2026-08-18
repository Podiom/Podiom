package store

import (
	"context"
	"errors"
	"testing"
)

func TestUpsertNotificationDeviceIsIdempotent(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	first, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "device-1", Platform: "ios", Label: "iPhone", PushToken: "token-a", AppVersion: "1.0",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !first.Enabled {
		t.Error("a newly registered device should be enabled")
	}

	// The app re-registers on every launch and on every token refresh; that must
	// update the same row rather than accumulate one per call.
	second, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "device-1", Platform: "ios", Label: "iPhone 17", PushToken: "token-b", AppVersion: "1.1",
	})
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("id changed from %q to %q", first.ID, second.ID)
	}
	if second.PushToken != "token-b" {
		t.Errorf("PushToken = %q, want token-b", second.PushToken)
	}
	if second.Label != "iPhone 17" || second.AppVersion != "1.1" {
		t.Errorf("metadata not refreshed: %+v", second)
	}

	devices, err := db.ListNotificationDevices(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("len(devices) = %d, want 1 — a token refresh created a duplicate registration", len(devices))
	}
}

// TestRegisteringDeviceDoesNotUnmuteIt checks re-registration leaves the enabled
// flag alone. Otherwise the app's next launch would silently undo the user having
// switched that device off.
func TestRegisteringDeviceDoesNotUnmuteIt(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	if _, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "device-1", Platform: "android", PushToken: "token-a",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := db.SetNotificationDeviceEnabled(ctx, "device-1", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	again, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "device-1", Platform: "android", PushToken: "token-b",
	})
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if again.Enabled {
		t.Error("re-registering un-muted a device the user had switched off")
	}
}

// TestTokenStealReleasesTheOtherDevice covers the push service reassigning a token
// to a different install, which happens after a reinstall or a device restore.
// Leaving both rows would deliver every notification to that phone twice.
func TestTokenStealReleasesTheOtherDevice(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	if _, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "old-install", Platform: "ios", PushToken: "shared-token",
	}); err != nil {
		t.Fatalf("register old: %v", err)
	}
	if _, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "new-install", Platform: "ios", PushToken: "shared-token",
	}); err != nil {
		t.Fatalf("register new: %v", err)
	}

	devices, err := db.ListNotificationDevices(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].ID != "new-install" {
		t.Errorf("surviving device = %q, want new-install", devices[0].ID)
	}
}

func TestListNotificationDevicesEnabledOnly(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
			ID: id, Platform: "android", PushToken: "token-" + id,
		}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
	if _, err := db.SetNotificationDeviceEnabled(ctx, "b", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	enabled, err := db.ListNotificationDevices(ctx, true)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 2 {
		t.Errorf("len(enabled) = %d, want 2", len(enabled))
	}
	all, err := db.ListNotificationDevices(ctx, false)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len(all) = %d, want 3", len(all))
	}
}

func TestNotificationDeviceValidationAndDeletion(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	// A registration without a token has nowhere to deliver, so it is not a
	// registration at all.
	if _, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "device-1", Platform: "ios",
	}); err == nil {
		t.Error("registering without a push token should fail")
	}

	if _, err := db.UpsertNotificationDevice(ctx, NotificationDevice{
		ID: "device-1", Platform: "ios", PushToken: "token-a",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := db.SetNotificationDeviceEnabled(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("enabling a missing device = %v, want ErrNotFound", err)
	}

	// Pruning a token the push service rejected is how a dead destination stops
	// costing every future notification an attempt.
	if err := db.DeleteNotificationDeviceByToken(ctx, "token-a"); err != nil {
		t.Fatalf("delete by token: %v", err)
	}
	if _, err := db.GetNotificationDevice(ctx, "device-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after prune = %v, want ErrNotFound", err)
	}
	// Both deletes are idempotent: a repeat is not an error.
	if err := db.DeleteNotificationDeviceByToken(ctx, "token-a"); err != nil {
		t.Errorf("repeat delete by token: %v", err)
	}
	if err := db.DeleteNotificationDevice(ctx, "device-1"); err != nil {
		t.Errorf("repeat delete: %v", err)
	}
}

// TestNotificationDevicePlatformIsConstrained checks the schema rejects a platform
// the delivery path has no idea how to reach.
func TestNotificationDevicePlatformIsConstrained(t *testing.T) {
	db := newNotificationTestStore(t)
	if _, err := db.UpsertNotificationDevice(context.Background(), NotificationDevice{
		ID: "device-1", Platform: "blackberry", PushToken: "token-a",
	}); err == nil {
		t.Error("an unsupported platform should be rejected")
	}
}
