package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// newNotificationTestStore opens a throwaway database for one test.
func newNotificationTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateAndGetNotification(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	created, err := db.CreateNotification(ctx, Notification{
		Type:         "goal.action_requested",
		Category:     "goals",
		Importance:   NotificationImportant,
		Title:        "Alice needs your help",
		Body:         "Publish the release announcement.",
		AgentName:    "Alice",
		GoalID:       "goal-1",
		SessionID:    "sess-1",
		ResourceKind: "goal_action_item",
		ResourceID:   "item-1",
		NavTarget:    "goal_action_item",
		Actionable:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Error("created notification has no id")
	}
	if created.CreatedAt == "" {
		t.Error("created notification has no timestamp")
	}
	// Unread and unresolved are the only sensible starting states, and both are
	// surfaced as empty strings rather than nullable columns.
	if created.ReadAt != "" || created.ResolvedAt != "" {
		t.Errorf("ReadAt/ResolvedAt = %q/%q, want both empty", created.ReadAt, created.ResolvedAt)
	}

	got, err := db.GetNotification(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != created {
		t.Errorf("get returned %+v, want %+v", got, created)
	}

	if _, err := db.GetNotification(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing error = %v, want ErrNotFound", err)
	}
}

func TestCreateNotificationDefaultsImportance(t *testing.T) {
	db := newNotificationTestStore(t)

	n, err := db.CreateNotification(context.Background(), Notification{
		Type: "goal.progress", Category: "goals", Title: "Progress",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.Importance != NotificationNormal {
		t.Errorf("Importance = %q, want %q", n.Importance, NotificationNormal)
	}
}

// TestListNotificationsOrdersNewestFirst covers the same-second case: created_at
// only has one-second resolution, so without rowid as a tiebreaker the order of
// notifications recorded together is arbitrary and paging over them can repeat or
// skip rows.
func TestListNotificationsOrdersNewestFirst(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	var ids []string
	for _, title := range []string{"first", "second", "third", "fourth"} {
		n, err := db.CreateNotification(ctx, Notification{
			Type: "goal.progress", Category: "goals", Title: title,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		ids = append(ids, n.ID)
	}

	all, err := db.ListNotifications(ctx, NotificationFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("len(list) = %d, want 4", len(all))
	}
	for i, want := range []string{ids[3], ids[2], ids[1], ids[0]} {
		if all[i].ID != want {
			t.Errorf("list[%d].ID = %q, want %q", i, all[i].ID, want)
		}
	}

	// Paging must not overlap or drop rows even though every row shares a second.
	page1, err := db.ListNotifications(ctx, NotificationFilter{Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	page2, err := db.ListNotifications(ctx, NotificationFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page1) != 2 || len(page2) != 2 {
		t.Fatalf("page sizes = %d/%d, want 2/2", len(page1), len(page2))
	}
	seen := map[string]bool{}
	for _, n := range append(page1, page2...) {
		if seen[n.ID] {
			t.Errorf("notification %q appears on both pages", n.ID)
		}
		seen[n.ID] = true
	}
	if len(seen) != 4 {
		t.Errorf("paged over %d distinct notifications, want 4", len(seen))
	}
}

func TestListNotificationsFilters(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	read, err := db.CreateNotification(ctx, Notification{
		Type: "goal.progress", Category: "goals", Title: "seen",
	})
	if err != nil {
		t.Fatalf("create read: %v", err)
	}
	if _, err := db.SetNotificationRead(ctx, read.ID, true); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if _, err := db.CreateNotification(ctx, Notification{
		Type: "goal.progress", Category: "goals", Title: "unseen",
	}); err != nil {
		t.Fatalf("create unread: %v", err)
	}
	actionable, err := db.CreateNotification(ctx, Notification{
		Type: "schedule.failed", Category: "schedules", Title: "broke",
		ResourceKind: "schedule_run", ResourceID: "run-1", Actionable: true,
	})
	if err != nil {
		t.Fatalf("create actionable: %v", err)
	}

	tests := []struct {
		name   string
		filter NotificationFilter
		want   int
	}{
		{"everything", NotificationFilter{}, 3},
		{"unread only", NotificationFilter{UnreadOnly: true}, 2},
		{"unresolved actionable only", NotificationFilter{UnresolvedOnly: true}, 1},
		{"by category", NotificationFilter{Category: "goals"}, 2},
		{"unread in category", NotificationFilter{UnreadOnly: true, Category: "goals"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list, err := db.ListNotifications(ctx, tc.filter)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(list) != tc.want {
				t.Errorf("len(list) = %d, want %d", len(list), tc.want)
			}
			// The count query must agree with the list query, since the badge and
			// the list are rendered from the two separately.
			count, err := db.CountNotifications(ctx, tc.filter)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != tc.want {
				t.Errorf("count = %d, want %d", count, tc.want)
			}
		})
	}

	// Resolving the actionable one removes it from the unresolved filter.
	if _, err := db.ResolveNotification(ctx, actionable.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	left, err := db.CountNotifications(ctx, NotificationFilter{UnresolvedOnly: true})
	if err != nil {
		t.Fatalf("count unresolved: %v", err)
	}
	if left != 0 {
		t.Errorf("unresolved count after resolve = %d, want 0", left)
	}
}

func TestSetNotificationReadIsGuarded(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	n, err := db.CreateNotification(ctx, Notification{
		Type: "session.question", Category: "agent_interaction", Title: "Question",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	read, err := db.SetNotificationRead(ctx, n.ID, true)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if read.ReadAt == "" {
		t.Error("ReadAt is empty after marking read")
	}
	// Marking an already-read notification read again must not re-stamp it: that
	// would reorder the user's list every time a second device synced.
	if _, err := db.SetNotificationRead(ctx, n.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("second mark-read error = %v, want ErrNotFound", err)
	}

	unread, err := db.SetNotificationRead(ctx, n.ID, false)
	if err != nil {
		t.Fatalf("mark unread: %v", err)
	}
	if unread.ReadAt != "" {
		t.Errorf("ReadAt = %q after marking unread, want empty", unread.ReadAt)
	}
	if _, err := db.SetNotificationRead(ctx, n.ID, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("second mark-unread error = %v, want ErrNotFound", err)
	}
}

// TestReadDoesNotResolve pins the rule that seeing a notification is not the same
// as handling what it was about.
func TestReadDoesNotResolve(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	n, err := db.CreateNotification(ctx, Notification{
		Type: "goal.access_requested", Category: "goals", Title: "Access",
		ResourceKind: "access_request", ResourceID: "req-1", Actionable: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	read, err := db.SetNotificationRead(ctx, n.ID, true)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if read.ReadAt == "" {
		t.Error("ReadAt is empty after marking read")
	}
	if read.ResolvedAt != "" {
		t.Errorf("ResolvedAt = %q after marking read, want empty", read.ResolvedAt)
	}
}

func TestMarkAllNotificationsRead(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	for range 3 {
		if _, err := db.CreateNotification(ctx, Notification{
			Type: "goal.progress", Category: "goals", Title: "note",
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	changed, err := db.MarkAllNotificationsRead(ctx)
	if err != nil {
		t.Fatalf("mark all: %v", err)
	}
	if changed != 3 {
		t.Errorf("changed = %d, want 3", changed)
	}
	// A second sweep has nothing left to do and must not report work.
	changed, err = db.MarkAllNotificationsRead(ctx)
	if err != nil {
		t.Fatalf("second mark all: %v", err)
	}
	if changed != 0 {
		t.Errorf("second changed = %d, want 0", changed)
	}
}

func TestResolveNotificationIsGuarded(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	n, err := db.CreateNotification(ctx, Notification{
		Type: "goal.action_requested", Category: "goals", Title: "Help",
		ResourceKind: "goal_action_item", ResourceID: "item-1", Actionable: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resolved, err := db.ResolveNotification(ctx, n.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ResolvedAt == "" {
		t.Error("ResolvedAt is empty after resolving")
	}
	if _, err := db.ResolveNotification(ctx, n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second resolve error = %v, want ErrNotFound", err)
	}
}

// TestResolveNotificationsByResource covers notification state following the
// domain: handling an access request anywhere clears every notification about it,
// and leaves notifications about other objects alone.
func TestResolveNotificationsByResource(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	target := Notification{
		Type: "goal.access_requested", Category: "goals", Title: "Access",
		ResourceKind: "access_request", ResourceID: "req-1", Actionable: true,
	}
	first, err := db.CreateNotification(ctx, target)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := db.CreateNotification(ctx, target)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	other, err := db.CreateNotification(ctx, Notification{
		Type: "goal.access_requested", Category: "goals", Title: "Other access",
		ResourceKind: "access_request", ResourceID: "req-2", Actionable: true,
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	changed, err := db.ResolveNotificationsByResource(ctx, "access_request", "req-1")
	if err != nil {
		t.Fatalf("resolve by resource: %v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("len(changed) = %d, want 2", len(changed))
	}
	for _, n := range changed {
		if n.ResolvedAt == "" {
			t.Errorf("returned notification %q has empty ResolvedAt", n.ID)
		}
		if n.ID != first.ID && n.ID != second.ID {
			t.Errorf("unexpected notification %q in changed set", n.ID)
		}
	}

	untouched, err := db.GetNotification(ctx, other.ID)
	if err != nil {
		t.Fatalf("get other: %v", err)
	}
	if untouched.ResolvedAt != "" {
		t.Error("notification about a different resource was resolved")
	}

	// Nothing left to resolve: a repeat must report no changes rather than error.
	changed, err = db.ResolveNotificationsByResource(ctx, "access_request", "req-1")
	if err != nil {
		t.Fatalf("repeat resolve: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("len(changed) on repeat = %d, want 0", len(changed))
	}

	// An empty kind or id is a producer that had nothing to resolve, not an error.
	if _, err := db.ResolveNotificationsByResource(ctx, "", ""); err != nil {
		t.Errorf("resolve with empty resource error = %v, want nil", err)
	}
}

func TestFindUnresolvedNotification(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	if _, err := db.FindUnresolvedNotification(ctx, "goal.access_requested", "access_request", "req-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("find with nothing stored error = %v, want ErrNotFound", err)
	}

	n, err := db.CreateNotification(ctx, Notification{
		Type: "goal.access_requested", Category: "goals", Title: "Access",
		ResourceKind: "access_request", ResourceID: "req-1", Actionable: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := db.FindUnresolvedNotification(ctx, "goal.access_requested", "access_request", "req-1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.ID != n.ID {
		t.Errorf("found %q, want %q", found.ID, n.ID)
	}

	// A different type about the same object is a different notification.
	if _, err := db.FindUnresolvedNotification(ctx, "goal.question", "access_request", "req-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("find with other type error = %v, want ErrNotFound", err)
	}

	// Once resolved it stops matching, so a fresh request notifies again.
	if _, err := db.ResolveNotification(ctx, n.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := db.FindUnresolvedNotification(ctx, "goal.access_requested", "access_request", "req-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("find after resolve error = %v, want ErrNotFound", err)
	}
}

// TestPruneNotificationsCascadesDeliveries checks history stays bounded and that
// pruning a notification takes its delivery rows with it.
func TestPruneNotificationsCascadesDeliveries(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	var oldest string
	for i := range 5 {
		n, err := db.CreateNotification(ctx, Notification{
			Type: "goal.progress", Category: "goals", Title: "note",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if i == 0 {
			oldest = n.ID
			if _, err := db.CreateNotificationDelivery(ctx, NotificationDelivery{
				NotificationID: n.ID, Channel: "webpush", Status: NotificationDeliveryAccepted,
			}); err != nil {
				t.Fatalf("create delivery: %v", err)
			}
		}
	}

	deleted, err := db.PruneNotifications(ctx, 3)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	left, err := db.CountNotifications(ctx, NotificationFilter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 3 {
		t.Errorf("remaining = %d, want 3", left)
	}
	if _, err := db.GetNotification(ctx, oldest); !errors.Is(err, ErrNotFound) {
		t.Errorf("oldest notification error = %v, want ErrNotFound", err)
	}
	deliveries, err := db.ListNotificationDeliveries(ctx, oldest)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("len(deliveries) = %d after prune, want 0 (cascade)", len(deliveries))
	}

	// keep <= 0 means "keep everything": a misconfigured caller must not wipe
	// the user's history.
	deleted, err = db.PruneNotifications(ctx, 0)
	if err != nil {
		t.Fatalf("prune 0: %v", err)
	}
	if deleted != 0 {
		t.Errorf("prune(0) deleted = %d, want 0", deleted)
	}
}

func TestNotificationDeliveryLifecycle(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	n, err := db.CreateNotification(ctx, Notification{
		Type: "schedule.failed", Category: "schedules", Title: "broke",
	})
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}

	pending, err := db.CreateNotificationDelivery(ctx, NotificationDelivery{
		NotificationID: n.ID, Channel: "webpush",
	})
	if err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	if pending.Status != NotificationDeliveryPending {
		t.Errorf("Status = %q, want %q", pending.Status, NotificationDeliveryPending)
	}

	if err := db.FinishNotificationDelivery(ctx, pending.ID, "sub-1", NotificationDeliveryFailed, "gone"); err != nil {
		t.Fatalf("finish delivery: %v", err)
	}

	deliveries, err := db.ListNotificationDeliveries(ctx, n.ID)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("len(deliveries) = %d, want 1", len(deliveries))
	}
	got := deliveries[0]
	if got.Status != NotificationDeliveryFailed {
		t.Errorf("Status = %q, want %q", got.Status, NotificationDeliveryFailed)
	}
	if got.Destination != "sub-1" {
		t.Errorf("Destination = %q, want %q", got.Destination, "sub-1")
	}
	if got.Error != "gone" {
		t.Errorf("Error = %q, want %q", got.Error, "gone")
	}
}

// TestNotificationPreferencesAreSparseOverrides checks that only explicit choices
// are stored — the registry supplies every default — and that re-choosing updates
// in place rather than stacking rows.
func TestNotificationPreferencesAreSparseOverrides(t *testing.T) {
	db := newNotificationTestStore(t)
	ctx := context.Background()

	prefs, err := db.ListNotificationPreferences(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(prefs) != 0 {
		t.Errorf("len(prefs) on a fresh install = %d, want 0", len(prefs))
	}

	if err := db.SetNotificationPreference(ctx, NotificationPreference{
		Type: "goal.progress", Channel: "web_push", Enabled: true,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := db.SetNotificationPreference(ctx, NotificationPreference{
		Type: "goal.progress", Channel: "web_push", Enabled: false,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	prefs, err = db.ListNotificationPreferences(ctx)
	if err != nil {
		t.Fatalf("list after set: %v", err)
	}
	if len(prefs) != 1 {
		t.Fatalf("len(prefs) = %d, want 1 (upsert, not append)", len(prefs))
	}
	if prefs[0].Enabled {
		t.Error("Enabled = true, want false after the update")
	}
	if prefs[0].UpdatedAt == "" {
		t.Error("UpdatedAt is empty")
	}

	// The same type on a different channel is a separate choice.
	if err := db.SetNotificationPreference(ctx, NotificationPreference{
		Type: "goal.progress", Channel: "native_push", Enabled: true,
	}); err != nil {
		t.Fatalf("set other channel: %v", err)
	}
	prefs, err = db.ListNotificationPreferences(ctx)
	if err != nil {
		t.Fatalf("list after second channel: %v", err)
	}
	if len(prefs) != 2 {
		t.Errorf("len(prefs) = %d, want 2", len(prefs))
	}
}
