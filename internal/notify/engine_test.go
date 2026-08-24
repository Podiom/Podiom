package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/store"
)

// fakeChannel records what it was asked to deliver.
type fakeChannel struct {
	name string
	mu   sync.Mutex
	got  []Envelope
	// err fails the whole channel; results scripts a per-destination answer.
	err     error
	results []Result
	// panics is read by the engine's worker goroutine and written by the test, so it is
	// guarded like everything else on this struct.
	panics bool
}

// setPanics scripts whether the next Send explodes.
func (f *fakeChannel) setPanics(v bool) {
	f.mu.Lock()
	f.panics = v
	f.mu.Unlock()
}

func (f *fakeChannel) shouldPanic() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.panics
}

func (f *fakeChannel) Name() string { return f.name }

func (f *fakeChannel) Send(_ context.Context, env Envelope) ([]Result, error) {
	if f.shouldPanic() {
		panic("channel exploded")
	}
	f.mu.Lock()
	f.got = append(f.got, env)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.results != nil {
		return f.results, nil
	}
	return []Result{{Destination: f.name + "-dest"}}, nil
}

func (f *fakeChannel) received() []Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Envelope, len(f.got))
	copy(out, f.got)
	return out
}

// newTestEngine builds an engine over a real store and the given channels, plus a
// broadcast recorder.
func newTestEngine(t *testing.T, channels ...Channel) (*Engine, *store.Store, *broadcastRecorder) {
	t.Helper()
	db := newTestStore(t)
	rec := &broadcastRecorder{}
	e := New(Options{Store: db, Channels: channels, InstallationID: "install-1"})
	e.SetBroadcaster(rec.recordCreated, rec.recordUpdated)
	t.Cleanup(e.Close)
	return e, db, rec
}

type broadcastRecorder struct {
	mu      sync.Mutex
	created []store.Notification
	updated []store.Notification
}

func (r *broadcastRecorder) recordCreated(n store.Notification) {
	r.mu.Lock()
	r.created = append(r.created, n)
	r.mu.Unlock()
}

func (r *broadcastRecorder) recordUpdated(n store.Notification) {
	r.mu.Lock()
	r.updated = append(r.updated, n)
	r.mu.Unlock()
}

func (r *broadcastRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created)
}

func (r *broadcastRecorder) updateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.updated)
}

// settledDeliveries returns the delivery rows that have an outcome.
//
// A row is created before its attempt and settled after it, so a pending one is a real
// intermediate state rather than a result. Waiting on settled rows is what keeps a test
// from racing the attempt it is asserting about.
func settledDeliveries(t *testing.T, db *store.Store, notificationID string) []store.NotificationDelivery {
	t.Helper()
	rows, err := db.ListNotificationDeliveries(context.Background(), notificationID)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	var out []store.NotificationDelivery
	for _, row := range rows {
		if row.Status != store.NotificationDeliveryPending {
			out = append(out, row)
		}
	}
	return out
}

// deliveriesByDestination indexes a notification's settled delivery rows.
//
// A row is created before its attempt and settled after it, so an unsettled one is a real
// intermediate state rather than a result. Skipping those is what lets a caller wait for
// the outcomes without racing them: a pending row has no destination yet, or has one and no
// verdict, depending on how much the channel already knew.
func deliveriesByDestination(t *testing.T, db *store.Store, notificationID string) map[string]store.NotificationDelivery {
	t.Helper()
	rows, err := db.ListNotificationDeliveries(context.Background(), notificationID)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	out := map[string]store.NotificationDelivery{}
	for _, row := range rows {
		if row.Destination == "" || row.Status == store.NotificationDeliveryPending {
			continue
		}
		out[row.Destination] = row
	}
	return out
}

// waitFor polls until cond holds or the deadline passes. The engine's worker is
// asynchronous by design, so tests wait on the effect rather than on a fixed
// sleep.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countNotifications(t *testing.T, db *store.Store) int {
	t.Helper()
	n, err := db.CountNotifications(context.Background(), store.NotificationFilter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestEnginePersistsAndBroadcastsNotification(t *testing.T) {
	ch := &fakeChannel{name: "webpush"}
	e, db, rec := newTestEngine(t, ch)

	e.Publish(Event{
		Type:       TypeGoalActionRequested,
		GoalID:     "goal-1",
		AgentName:  "Alice",
		Resource:   ResourceGoalActionItem,
		ResourceID: "item-1",
		Detail:     "Publish the release announcement.",
	})

	waitFor(t, "the notification to be stored", func() bool { return countNotifications(t, db) == 1 })

	list, err := db.ListNotifications(context.Background(), store.NotificationFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := list[0]
	if got.Type != TypeGoalActionRequested {
		t.Errorf("Type = %q, want %q", got.Type, TypeGoalActionRequested)
	}
	if got.Category != string(CategoryGoals) {
		t.Errorf("Category = %q, want %q", got.Category, CategoryGoals)
	}
	if got.Importance != store.NotificationImportant {
		t.Errorf("Importance = %q, want %q", got.Importance, store.NotificationImportant)
	}
	if !got.Actionable {
		t.Error("Actionable = false, want true")
	}
	if got.NavTarget != NavGoalActionItem {
		t.Errorf("NavTarget = %q, want %q", got.NavTarget, NavGoalActionItem)
	}
	// Producers supply ids and a detail string; the wording comes from the
	// renderer, so the title must not be empty and must name the agent.
	if got.Title != "Alice needs your help" {
		t.Errorf("Title = %q, want %q", got.Title, "Alice needs your help")
	}
	if got.Body != "Publish the release announcement." {
		t.Errorf("Body = %q", got.Body)
	}

	waitFor(t, "the in-app broadcast", func() bool { return rec.count() == 1 })
	// Wait on the channel separately: handle() broadcasts in-app before it
	// delivers, so the broadcast landing says nothing about the channel having
	// been written yet. Between the two the worker still has to read
	// preferences and insert a delivery row, which is enough of a gap for a
	// loaded machine to run the assertions below first.
	waitFor(t, "the channel delivery", func() bool { return len(ch.received()) == 1 })

	envs := ch.received()
	if len(envs) != 1 {
		t.Fatalf("channel received %d envelopes, want 1", len(envs))
	}
	if envs[0].InstallationID != "install-1" {
		t.Errorf("InstallationID = %q, want install-1", envs[0].InstallationID)
	}
	if envs[0].PushKind != legacyKindGoalActionItem {
		t.Errorf("PushKind = %q, want %q", envs[0].PushKind, legacyKindGoalActionItem)
	}
}

// TestEngineDropsUnknownTypes covers the tool-use flood guard: an unregistered
// type is rejected before it can reach the queue or the database.
func TestEngineDropsUnknownTypes(t *testing.T) {
	ch := &fakeChannel{name: "webpush"}
	e, db, _ := newTestEngine(t, ch)

	e.Publish(Event{Type: "goal.tool_use", GoalID: "goal-1"})
	e.Publish(Event{Type: TypeGoalProgress, GoalID: "goal-1", Detail: "real"})

	waitFor(t, "the valid notification", func() bool { return countNotifications(t, db) == 1 })
	if n := countNotifications(t, db); n != 1 {
		t.Errorf("stored %d notifications, want only the registered one", n)
	}
}

// TestPreferenceOverrideSuppressesDelivery checks that turning a type off stops
// external delivery without stopping the notification itself: the Notification
// Center is where notifications live, not a channel to opt out of.
func TestPreferenceOverrideSuppressesDelivery(t *testing.T) {
	ch := &fakeChannel{name: "webpush"}
	e, db, _ := newTestEngine(t, ch)
	ctx := context.Background()

	if err := db.SetNotificationPreference(ctx, store.NotificationPreference{
		Type: TypeGoalActionRequested, Channel: "webpush", Enabled: false,
	}); err != nil {
		t.Fatalf("set preference: %v", err)
	}

	e.Publish(Event{
		Type: TypeGoalActionRequested, GoalID: "goal-1",
		Resource: ResourceGoalActionItem, ResourceID: "item-1",
	})
	waitFor(t, "the notification to be stored", func() bool { return countNotifications(t, db) == 1 })

	list, _ := db.ListNotifications(ctx, store.NotificationFilter{})
	deliveries, err := db.ListNotificationDeliveries(ctx, list[0].ID)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	// No attempt was made, so there is nothing to record — an empty delivery
	// history is the honest account of a channel the user switched off.
	if len(deliveries) != 0 {
		t.Errorf("len(deliveries) = %d, want 0", len(deliveries))
	}
	if envs := ch.received(); len(envs) != 0 {
		t.Errorf("channel was called %d times despite being disabled", len(envs))
	}
}

// TestEmptyPreferencesUseRegistryDefaults checks the sparse-overrides rule: with
// no rows stored, a default-on type delivers and a default-off one does not.
func TestEmptyPreferencesUseRegistryDefaults(t *testing.T) {
	ch := &fakeChannel{name: "webpush"}
	e, db, _ := newTestEngine(t, ch)

	// goal.progress is default-off, goal.action_requested default-on.
	e.Publish(Event{Type: TypeGoalProgress, GoalID: "goal-1", Detail: "moved"})
	e.Publish(Event{
		Type: TypeGoalActionRequested, GoalID: "goal-1",
		Resource: ResourceGoalActionItem, ResourceID: "item-1",
	})

	waitFor(t, "both notifications", func() bool { return countNotifications(t, db) == 2 })
	waitFor(t, "the default-on delivery", func() bool { return len(ch.received()) == 1 })

	envs := ch.received()
	if len(envs) != 1 {
		t.Fatalf("channel received %d envelopes, want 1 (only the default-on type)", len(envs))
	}
	if envs[0].Type != TypeGoalActionRequested {
		t.Errorf("delivered %q, want %q", envs[0].Type, TypeGoalActionRequested)
	}
}

// TestChannelFailureIsolated checks one dead transport cannot suppress another,
// and that the notification survives either way.
func TestChannelFailureIsolated(t *testing.T) {
	failing := &fakeChannel{name: "relay", err: errors.New("relay down")}
	ok := &fakeChannel{name: "webpush"}
	e, db, _ := newTestEngine(t, failing, ok)
	ctx := context.Background()

	e.Publish(Event{
		Type: TypeGoalActionRequested, GoalID: "goal-1",
		Resource: ResourceGoalActionItem, ResourceID: "item-1",
	})
	waitFor(t, "both channels to be tried", func() bool {
		return len(failing.received()) == 1 && len(ok.received()) == 1
	})
	waitFor(t, "the notification to be stored", func() bool { return countNotifications(t, db) == 1 })

	list, _ := db.ListNotifications(ctx, store.NotificationFilter{})
	// Waits for both attempts to have settled rather than for both rows to exist: a row is
	// written before its attempt is made, so counting rows can observe one that is still
	// pending and read that as its verdict.
	waitFor(t, "both deliveries to settle", func() bool {
		return len(settledDeliveries(t, db, list[0].ID)) == 2
	})
	byChannel := map[string]store.NotificationDelivery{}
	for _, d := range settledDeliveries(t, db, list[0].ID) {
		byChannel[d.Channel] = d
	}
	if got := byChannel["relay"].Status; got != store.NotificationDeliveryFailed {
		t.Errorf("relay status = %q, want %q", got, store.NotificationDeliveryFailed)
	}
	if byChannel["relay"].Error == "" {
		t.Error("failed delivery recorded no error")
	}
	if got := byChannel["webpush"].Status; got != store.NotificationDeliveryAccepted {
		t.Errorf("webpush status = %q, want %q", got, store.NotificationDeliveryAccepted)
	}
	// A delivery failure must never invalidate the notification.
	if list[0].ResolvedAt != "" {
		t.Error("notification was resolved by a delivery failure")
	}
}

// TestChannelResultsRecordEachDestination checks the per-destination contract:
// three phones with one dead is a different situation from a dead transport.
func TestChannelResultsRecordEachDestination(t *testing.T) {
	ch := &fakeChannel{name: "relay", results: []Result{
		{Destination: "device-1"},
		{Destination: "device-2", Err: errors.New("unregistered")},
		{Destination: "device-3"},
	}}
	e, db, _ := newTestEngine(t, ch)
	ctx := context.Background()

	e.Publish(Event{
		Type: TypeGoalActionRequested, GoalID: "goal-1",
		Resource: ResourceGoalActionItem, ResourceID: "item-1",
	})
	waitFor(t, "the notification to be stored", func() bool { return countNotifications(t, db) == 1 })
	list, _ := db.ListNotifications(ctx, store.NotificationFilter{})
	// Waits for every destination to have an outcome, not merely for three rows to exist.
	// A delivery row is written before its attempt is made and settled afterwards, so both
	// "three rows" and "three destinations" can be true while a row is still pending —
	// real intermediate states, and waiting on either of them raced the result.
	waitFor(t, "a settled delivery row per destination", func() bool {
		return len(deliveriesByDestination(t, db, list[0].ID)) == 3
	})

	byDest := deliveriesByDestination(t, db, list[0].ID)
	for _, want := range []struct {
		dest   string
		status store.NotificationDeliveryStatus
	}{
		{"device-1", store.NotificationDeliveryAccepted},
		{"device-2", store.NotificationDeliveryFailed},
		{"device-3", store.NotificationDeliveryAccepted},
	} {
		got, ok := byDest[want.dest]
		if !ok {
			t.Errorf("no delivery row for %s", want.dest)
			continue
		}
		if got.Status != want.status {
			t.Errorf("%s status = %q, want %q", want.dest, got.Status, want.status)
		}
	}
}

// TestPanickingChannelDoesNotKillWorker guards the worker's lifetime: it is the
// only thing turning domain activity into notifications, so its silent death
// would disable them for the rest of the daemon's life with no other symptom.
func TestPanickingChannelDoesNotKillWorker(t *testing.T) {
	boom := &fakeChannel{name: "boom"}
	boom.setPanics(true)
	e, db, _ := newTestEngine(t, boom)

	e.Publish(Event{
		Type: TypeGoalActionRequested, GoalID: "goal-1",
		Resource: ResourceGoalActionItem, ResourceID: "item-1",
	})
	// The panic happens after the row is written, so the first notification lands.
	waitFor(t, "the first notification", func() bool { return countNotifications(t, db) == 1 })

	boom.setPanics(false)
	e.Publish(Event{Type: TypeGoalProgress, GoalID: "goal-1", Detail: "still working"})
	waitFor(t, "the worker to recover and handle the next event", func() bool {
		return countNotifications(t, db) == 2
	})
}

// TestActionableTypesDeduplicateWhileOpen checks a producer firing twice for one
// still-open request produces one notification, and that informational types are
// never collapsed.
func TestActionableTypesDeduplicateWhileOpen(t *testing.T) {
	e, db, _ := newTestEngine(t)

	actionable := Event{
		Type: TypeGoalAccessRequested, GoalID: "goal-1",
		Resource: ResourceAccessRequest, ResourceID: "req-1",
	}
	e.Publish(actionable)
	waitFor(t, "the first notification", func() bool { return countNotifications(t, db) == 1 })
	e.Publish(actionable)

	informational := Event{Type: TypeGoalProgress, GoalID: "goal-1", Resource: ResourceGoal, ResourceID: "goal-1"}
	e.Publish(informational)
	e.Publish(informational)

	waitFor(t, "both progress notifications", func() bool { return countNotifications(t, db) == 3 })
	if n := countNotifications(t, db); n != 3 {
		t.Errorf("stored %d notifications, want 3 (one access request, two progress)", n)
	}
}

// TestResolveEventClearsNotifications checks a resolution records no new row and
// clears the ones about that object.
func TestResolveEventClearsNotifications(t *testing.T) {
	e, db, rec := newTestEngine(t)
	ctx := context.Background()

	e.Publish(Event{
		Type: TypeGoalAccessRequested, GoalID: "goal-1",
		Resource: ResourceAccessRequest, ResourceID: "req-1",
	})
	waitFor(t, "the notification", func() bool { return countNotifications(t, db) == 1 })

	e.Publish(Event{Resolves: []ResourceRef{{Kind: ResourceAccessRequest, ID: "req-1"}}})

	// A resolution broadcasts as an update, not as a new notification: a client
	// must revise the row it is already showing rather than announce it again.
	waitFor(t, "the resolution broadcast", func() bool { return rec.updateCount() == 1 })
	if rec.count() != 1 {
		t.Errorf("created broadcasts = %d, want 1 (a resolution is not a new notification)", rec.count())
	}
	if n := countNotifications(t, db); n != 1 {
		t.Errorf("stored %d notifications, want 1 (a resolution creates none)", n)
	}
	list, _ := db.ListNotifications(ctx, store.NotificationFilter{})
	if list[0].ResolvedAt == "" {
		t.Error("notification was not resolved")
	}
}

// TestOnGoalEventMapsTimelineEntries covers the single subscription that carries
// most goal activity, in both directions: an event that creates a notification and
// one that resolves it.
func TestOnGoalEventMapsTimelineEntries(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	e.OnGoalEvent(store.GoalEvent{
		GoalID: "goal-1", SessionID: "sess-1",
		Kind: store.GoalEventActionRequested, Body: "Publish the announcement",
		Payload: `{"action_item_id":"item-1"}`,
	})
	waitFor(t, "the action-requested notification", func() bool { return countNotifications(t, db) == 1 })

	list, _ := db.ListNotifications(ctx, store.NotificationFilter{})
	got := list[0]
	if got.Type != TypeGoalActionRequested {
		t.Fatalf("Type = %q, want %q", got.Type, TypeGoalActionRequested)
	}
	// The resource id comes out of the event payload, which is what lets the
	// matching resolution find this notification later.
	if got.ResourceID != "item-1" {
		t.Errorf("ResourceID = %q, want item-1", got.ResourceID)
	}

	e.OnGoalEvent(store.GoalEvent{
		GoalID: "goal-1", Kind: store.GoalEventActionResponded,
		Payload: `{"action_item_id":"item-1","status":"done"}`,
	})
	waitFor(t, "the notification to be resolved", func() bool {
		fresh, err := db.GetNotification(ctx, got.ID)
		return err == nil && fresh.ResolvedAt != ""
	})
}

// TestOnGoalEventIgnoresUnmappedKinds keeps the goal audit trail out of the
// notification stream.
func TestOnGoalEventIgnoresUnmappedKinds(t *testing.T) {
	e, db, _ := newTestEngine(t)

	for _, kind := range []store.GoalEventKind{
		store.GoalEventToolUse,
		store.GoalEventCreated,
		store.GoalEventPlanningStarted,
		store.GoalEventReviewStarted,
		store.GoalEventUserFeedback,
	} {
		e.OnGoalEvent(store.GoalEvent{GoalID: "goal-1", Kind: kind, Body: string(kind)})
	}
	// Publish a mapped event afterwards: once it lands, the unmapped ones ahead of
	// it in the queue have provably been processed too.
	e.OnGoalEvent(store.GoalEvent{GoalID: "goal-1", Kind: store.GoalEventProgress, Body: "moved"})

	waitFor(t, "the progress notification", func() bool { return countNotifications(t, db) == 1 })
	if n := countNotifications(t, db); n != 1 {
		t.Errorf("stored %d notifications, want 1", n)
	}
}

func TestNilEngineIsSafe(t *testing.T) {
	var e *Engine
	// Every entry point must tolerate a nil engine so callers and tests that run
	// without notifications need no branch.
	e.Publish(Event{Type: TypeGoalProgress})
	e.OnGoalEvent(store.GoalEvent{Kind: store.GoalEventProgress})
	e.SetBroadcaster(func(store.Notification) {}, func(store.Notification) {})
	e.SetPendingCheck(func(ResourceKind, string) bool { return true })
	if actions := e.LiveActions(context.Background(), store.Notification{}); actions != nil {
		t.Errorf("LiveActions on a nil engine = %v, want nil", actions)
	}
	e.Close()
}

func TestPublishNeverBlocksWhenQueueIsFull(t *testing.T) {
	// A channel that never returns wedges the worker, so the queue fills up.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	e, _, _ := newTestEngine(t, &blockingChannel{gate: blocked})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range queueDepth * 2 {
			e.Publish(Event{Type: TypeGoalProgress, GoalID: fmt.Sprintf("goal-%d", i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked with a full queue; an agent turn could stall on notifications")
	}
}

type blockingChannel struct {
	gate chan struct{}
}

func (b *blockingChannel) Name() string { return "blocking" }

func (b *blockingChannel) Send(ctx context.Context, _ Envelope) ([]Result, error) {
	select {
	case <-b.gate:
	case <-ctx.Done():
	}
	return nil, nil
}

// TestStatusChangeResolvesCompletionProposal covers an event that both reports
// something and settles something: once the user gives a verdict on a proposed
// completion, the notification asking for that verdict must stop being an open ask
// on every device — while the status change itself is still worth reporting.
func TestStatusChangeResolvesCompletionProposal(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	e.OnGoalEvent(store.GoalEvent{
		GoalID: "goal-1", Kind: store.GoalEventCompletionProposed,
		Body: "Everything shipped",
	})
	waitFor(t, "the completion proposal", func() bool { return countNotifications(t, db) == 1 })
	list, _ := db.ListNotifications(ctx, store.NotificationFilter{})
	proposal := list[0]
	if proposal.ResourceKind != string(ResourceGoalCompletion) || proposal.ResourceID != "goal-1" {
		t.Fatalf("proposal resource = %s/%s, want goal_completion/goal-1",
			proposal.ResourceKind, proposal.ResourceID)
	}

	e.OnGoalEvent(store.GoalEvent{
		GoalID: "goal-1", Kind: store.GoalEventStatusChange,
		Body: "review → done", Payload: `{"from":"review","to":"done"}`,
	})

	waitFor(t, "the proposal to be resolved", func() bool {
		fresh, err := db.GetNotification(ctx, proposal.ID)
		return err == nil && fresh.ResolvedAt != ""
	})
	// The status change is still news in its own right.
	waitFor(t, "the status-change notification", func() bool { return countNotifications(t, db) == 2 })

	list, _ = db.ListNotifications(ctx, store.NotificationFilter{})
	found := false
	for _, n := range list {
		if n.Type == TypeGoalStatusChanged {
			found = true
		}
	}
	if !found {
		t.Error("no goal.status_changed notification was recorded")
	}
}
