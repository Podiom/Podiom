package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

// seedNotification records one notification directly, so API tests do not have to
// drive a whole domain operation to get a row to read.
func seedNotification(t *testing.T, db *store.Store, n store.Notification) store.Notification {
	t.Helper()
	if n.Type == "" {
		n.Type = notify.TypeGoalActionRequested
	}
	if n.Category == "" {
		n.Category = "goals"
	}
	if n.Title == "" {
		n.Title = "Alice needs your help"
	}
	saved, err := db.CreateNotification(context.Background(), n)
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	return saved
}

func TestNotificationListIncludesUnreadCount(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)

	read := seedNotification(t, db, store.Notification{Title: "already seen"})
	if _, err := db.SetNotificationRead(context.Background(), read.ID, true); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	seedNotification(t, db, store.Notification{Title: "unseen"})

	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got notificationListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Notifications) != 2 {
		t.Errorf("len(notifications) = %d, want 2", len(got.Notifications))
	}
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2", got.Total)
	}
	// The badge count comes back with the page so the two cannot disagree.
	if got.Unread != 1 {
		t.Errorf("Unread = %d, want 1", got.Unread)
	}
}

// TestAttentionCountExcludesRoutineActivity is what keeps the badge meaningful.
//
// Counting every unread notification would leave it permanently lit on any busy
// installation — goal runs and progress updates alone would do it — and a badge that is
// always on says nothing about whether anything needs the user.
func TestAttentionCountExcludesRoutineActivity(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)

	// Routine activity: recorded, and visible in the Center, but not badge-worthy.
	for _, notifType := range []string{notify.TypeGoalProgress, notify.TypeGoalRunStarted, notify.TypeGoalMetricChanged} {
		info, ok := notify.Lookup(notifType)
		if !ok {
			t.Fatalf("%q is not registered", notifType)
		}
		seedNotification(t, db, store.Notification{
			Type: notifType, Category: string(info.Category), Importance: info.Importance,
			Title: notifType,
		})
	}
	// One thing that actually needs the user.
	askInfo, _ := notify.Lookup(notify.TypeGoalActionRequested)
	seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalActionRequested, Category: string(askInfo.Category),
		Importance: askInfo.Importance, Title: "Alice needs your help",
		ResourceKind: "goal_action_item", ResourceID: "item-1", Actionable: true,
	})

	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	var got notificationListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Unread != 4 {
		t.Errorf("Unread = %d, want 4 (everything is still recorded and unread)", got.Unread)
	}
	if got.Attention != 1 {
		t.Errorf("Attention = %d, want 1 — routine activity must not light the badge", got.Attention)
	}
}

// TestNotificationListUnreadCountIgnoresTheFilter checks the badge counts every
// unread notification, not just those matching the current view — a category
// filter must not make the badge undercount.
func TestNotificationListUnreadCountIgnoresTheFilter(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	seedNotification(t, db, store.Notification{Category: "goals", Title: "goal ask"})
	seedNotification(t, db, store.Notification{
		Type: notify.TypeScheduleFailed, Category: "schedules", Title: "schedule broke",
	})

	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, httptest.NewRequest(http.MethodGet, "/api/notifications?category=goals", nil))
	var got notificationListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Notifications) != 1 || got.Total != 1 {
		t.Errorf("filtered page = %d rows, total %d; want 1 and 1", len(got.Notifications), got.Total)
	}
	if got.Unread != 2 {
		t.Errorf("Unread = %d, want 2 (the badge counts everything unread, not just this view)", got.Unread)
	}
}

func TestNotificationReadAndResolveEndpoints(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	n := seedNotification(t, db, store.Notification{
		ResourceKind: "goal_action_item", ResourceID: "item-1", Actionable: true,
	})

	post := func(path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleNotification(rr, httptest.NewRequest(http.MethodPost, path, nil))
		return rr
	}

	rr := post("/api/notifications/" + n.ID + "/read")
	if rr.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var view notificationView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.ReadAt == "" {
		t.Error("ReadAt is empty after marking read")
	}
	// Reading is not handling: the underlying ask must still be open.
	if view.ResolvedAt != "" {
		t.Errorf("ResolvedAt = %q after marking read, want empty", view.ResolvedAt)
	}

	if rr := post("/api/notifications/" + n.ID + "/unread"); rr.Code != http.StatusOK {
		t.Fatalf("unread status = %d, want 200", rr.Code)
	}
	if rr := post("/api/notifications/" + n.ID + "/resolve"); rr.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200", rr.Code)
	}
	// The first resolution wins; a repeat is not found rather than re-stamped.
	if rr := post("/api/notifications/" + n.ID + "/resolve"); rr.Code != http.StatusNotFound {
		t.Errorf("second resolve status = %d, want 404", rr.Code)
	}
}

func TestNotificationEndpointsRejectBadRequests(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	n := seedNotification(t, db, store.Notification{})

	tests := []struct {
		name    string
		method  string
		path    string
		handler func(http.ResponseWriter, *http.Request)
		want    int
	}{
		{"unknown id", http.MethodGet, "/api/notifications/missing", srv.handleNotification, http.StatusNotFound},
		{"unknown action", http.MethodPost, "/api/notifications/" + n.ID + "/explode", srv.handleNotification, http.StatusNotFound},
		{"missing id", http.MethodGet, "/api/notifications/", srv.handleNotification, http.StatusBadRequest},
		{"list rejects post", http.MethodPost, "/api/notifications", srv.handleNotifications, http.StatusMethodNotAllowed},
		{"read-all rejects get", http.MethodGet, "/api/notifications/read-all", srv.handleNotificationsReadAll, http.StatusMethodNotAllowed},
		{"item read rejects get", http.MethodGet, "/api/notifications/" + n.ID + "/read", srv.handleNotification, http.StatusMethodNotAllowed},
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

func TestNotificationsReadAll(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	for range 3 {
		seedNotification(t, db, store.Notification{})
	}

	rr := httptest.NewRecorder()
	srv.handleNotificationsReadAll(rr, httptest.NewRequest(http.MethodPost, "/api/notifications/read-all", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["updated"] != 3 {
		t.Errorf("updated = %d, want 3", got["updated"])
	}
	unread, err := db.CountNotifications(context.Background(), store.NotificationFilter{UnreadOnly: true})
	if err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if unread != 0 {
		t.Errorf("unread after read-all = %d, want 0", unread)
	}
}

// TestNotificationPreferencesServeTheWholeModel checks the settings screen can be
// rendered entirely from the server: every category, every label, and the shipped
// defaults, with nothing for a client to hardcode.
func TestNotificationPreferencesServeTheWholeModel(t *testing.T) {
	srv, _, _ := newNotifyTestServer(t)

	rr := httptest.NewRecorder()
	srv.handleNotificationPreferences(rr, httptest.NewRequest(http.MethodGet, "/api/notifications/preferences", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got notificationPreferencesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Groups) != len(notify.Categories()) {
		t.Errorf("len(groups) = %d, want %d", len(got.Groups), len(notify.Categories()))
	}
	byType := map[string]notify.PreferenceRow{}
	for _, group := range got.Groups {
		if group.Title == "" {
			t.Errorf("group %q has no title", group.Category)
		}
		for _, row := range group.Rows {
			if row.Label == "" {
				t.Errorf("%q has no label", row.Type)
			}
			byType[row.Type] = row
		}
	}
	if len(byType) != len(notify.All()) {
		t.Errorf("served %d types, want %d", len(byType), len(notify.All()))
	}
	// An install with no stored choices reports the registry defaults.
	if !byType[notify.TypeGoalActionRequested].Enabled {
		t.Error("goal.action_requested should be enabled by default")
	}
	if byType[notify.TypeGoalProgress].Enabled {
		t.Error("goal.progress should be disabled by default")
	}
}

// TestNotificationPreferencesPersistAcrossChannels checks one switch writes a
// choice for every known channel, including channels this daemon is not running.
// Otherwise turning a type off would quietly revert to the default the day native
// push is added.
func TestNotificationPreferencesPersistAcrossChannels(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)

	body, _ := json.Marshal(notificationPreferencesRequest{
		Preferences: []notify.PreferenceUpdate{{Type: notify.TypeGoalActionRequested, Enabled: false}},
	})
	rr := httptest.NewRecorder()
	srv.handleNotificationPreferences(rr,
		httptest.NewRequest(http.MethodPut, "/api/notifications/preferences", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	stored, err := db.ListNotificationPreferences(context.Background())
	if err != nil {
		t.Fatalf("list preferences: %v", err)
	}
	if len(stored) != len(notify.AllChannels()) {
		t.Fatalf("stored %d preference rows, want one per channel (%d)", len(stored), len(notify.AllChannels()))
	}
	for _, row := range stored {
		if row.Enabled {
			t.Errorf("%s/%s is still enabled", row.Type, row.Channel)
		}
	}

	// The response reflects the new state, so the UI needs no follow-up read.
	var got notificationPreferencesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, group := range got.Groups {
		for _, prefRow := range group.Rows {
			if prefRow.Type == notify.TypeGoalActionRequested && prefRow.Enabled {
				t.Error("response still reports goal.action_requested as enabled")
			}
		}
	}
}

func TestNotificationPreferencesRejectUnknownType(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)

	body, _ := json.Marshal(notificationPreferencesRequest{
		Preferences: []notify.PreferenceUpdate{
			{Type: notify.TypeGoalProgress, Enabled: true},
			{Type: "goal.invented", Enabled: true},
		},
	})
	rr := httptest.NewRecorder()
	srv.handleNotificationPreferences(rr,
		httptest.NewRequest(http.MethodPut, "/api/notifications/preferences", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	// Validation happens up front, so the valid half of a bad request is not
	// half-applied.
	stored, err := db.ListNotificationPreferences(context.Background())
	if err != nil {
		t.Fatalf("list preferences: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("stored %d preference rows after a rejected request, want 0", len(stored))
	}
}

// TestNotificationViewNarrowsActionsToLiveState checks the list does not offer an
// action the domain no longer permits: a notification about an action item that has
// already been answered must come back with navigation only.
func TestNotificationViewNarrowsActionsToLiveState(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, item := seedGoalActionItem(t, srv)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalActionRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceGoalActionItem), ResourceID: item.ID, Actionable: true,
	})

	view := srv.notificationView(httptest.NewRequest(http.MethodGet, "/api/notifications", nil), n)
	if len(view.Actions) != 3 {
		t.Fatalf("open item offers %d actions, want 3 (open, done, blocked): %+v", len(view.Actions), view.Actions)
	}

	if _, err := srv.core.RespondGoalActionItem(ctx, item.ID, store.GoalActionDone, "handled"); err != nil {
		t.Fatalf("respond: %v", err)
	}
	view = srv.notificationView(httptest.NewRequest(http.MethodGet, "/api/notifications", nil), n)
	if len(view.Actions) != 1 || view.Actions[0].ID != notify.ActionOpen {
		t.Errorf("answered item offers %+v, want navigation only", view.Actions)
	}
}
