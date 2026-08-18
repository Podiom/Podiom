package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/store"
)

// queueDepth bounds the pending event buffer. Publishing never blocks, so this is
// the number of events that can pile up while a slow channel is being retried
// before new ones are dropped with a warning.
const queueDepth = 256

// deliveryTimeout caps one channel's attempt. A wedged push service must not hold
// the worker, since everything behind it in the queue is also waiting.
const deliveryTimeout = 10 * time.Second

// historyLimit is how many notifications are kept. History is what makes an old
// notification openable; unbounded history is just a growing database.
const historyLimit = 2000

// pruneInterval is how many inserts pass between prunes. Pruning on every insert
// would run a delete for every notification recorded.
const pruneInterval = 256

// EngineStore is the persistence the engine needs. *store.Store satisfies it.
//
// Declared here, at the consumer, so the engine can be tested against a fake and
// so this package keeps depending on store's types rather than its behaviour.
type EngineStore interface {
	CreateNotification(ctx context.Context, n store.Notification) (store.Notification, error)
	FindUnresolvedNotification(ctx context.Context, notifType, kind, id string) (store.Notification, error)
	ResolveNotificationsByResource(ctx context.Context, kind, id string) ([]store.Notification, error)
	ListNotificationPreferences(ctx context.Context) ([]store.NotificationPreference, error)
	SetNotificationPreference(ctx context.Context, p store.NotificationPreference) error
	CreateNotificationDelivery(ctx context.Context, d store.NotificationDelivery) (store.NotificationDelivery, error)
	FinishNotificationDelivery(ctx context.Context, id, destination string, status store.NotificationDeliveryStatus, errMsg string) error
	PruneNotifications(ctx context.Context, keep int) (int, error)

	// Lookups for the display names a notification's text needs.
	GetSession(ctx context.Context, id string) (store.Session, error)
	GetGoal(ctx context.Context, id string) (store.Goal, error)
	GetTask(ctx context.Context, id string) (store.Task, error)

	// Lookups for narrowing a notification's actions against live domain state.
	GetGoalActionItem(ctx context.Context, id string) (store.GoalActionItem, error)
	GetAccessRequest(ctx context.Context, id string) (store.AccessRequest, error)
	GetAgentQuestion(ctx context.Context, id string) (store.AgentQuestion, error)
}

// Options configures the engine.
type Options struct {
	// Store is required.
	Store EngineStore
	// Channels are the external delivery technologies. Nil entries are dropped so
	// callers can pass optional channels inline.
	Channels []Channel
	// InstallationID identifies this Podiom installation in every payload.
	InstallationID string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Engine turns domain events into notifications: it maps an event onto a
// notification type, renders it, persists it, broadcasts it in-app, and hands it
// to whichever channels the user has left enabled.
//
// Publishing is non-blocking and delivery is best-effort by construction. A
// notification is not something an agent turn, a schedule run, a goal run or any
// domain operation should be able to fail on, so nothing on those paths can be
// slowed down or broken by a push service that is down.
type Engine struct {
	store          EngineStore
	channels       []Channel
	installationID string
	log            *slog.Logger

	queue chan Event
	stop  chan struct{}
	done  chan struct{}

	mu           sync.RWMutex
	onCreated    func(n store.Notification)
	onUpdated    func(n store.Notification)
	pendingCheck func(kind ResourceKind, requestID string) bool

	inserts int
}

// New builds an engine and starts its worker. Close stops it.
func New(opts Options) *Engine {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	live := make([]Channel, 0, len(opts.Channels))
	for _, ch := range opts.Channels {
		if ch != nil {
			live = append(live, ch)
		}
	}
	e := &Engine{
		store:          opts.Store,
		channels:       live,
		installationID: opts.InstallationID,
		log:            log,
		queue:          make(chan Event, queueDepth),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
	go e.run()
	return e
}

// SetBroadcaster registers the in-app fan-out that keeps the Notification Center
// and its badge live on every open client.
//
// Creation and update are separate callbacks because clients treat them
// differently: a new notification may raise a toast and bump the unread count,
// while an update — read elsewhere, or resolved by acting on another device —
// must only revise a row that is already on screen.
//
// Safe to call once during daemon wiring.
func (e *Engine) SetBroadcaster(created, updated func(n store.Notification)) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.onCreated = created
	e.onUpdated = updated
	e.mu.Unlock()
}

// SetPendingCheck registers the predicate that reports whether an in-memory
// request (a permission prompt, a live session question) is still awaiting a
// decision. Without it those notifications keep offering actions after the
// request has already been answered elsewhere. Safe to call once during wiring.
func (e *Engine) SetPendingCheck(fn func(kind ResourceKind, requestID string) bool) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.pendingCheck = fn
	e.mu.Unlock()
}

// Publish queues an event. It never blocks and never returns an error: a
// notification that cannot be recorded is worth a log line, not a failed agent
// turn or a failed schedule run.
//
// A nil engine is a no-op, so tests and callers that run without notifications
// need no branch at the call site.
func (e *Engine) Publish(ev Event) {
	if e == nil {
		return
	}
	// Resolutions carry no type of their own; everything else must be registered.
	if len(ev.Resolves) == 0 {
		if _, ok := Lookup(ev.Type); !ok {
			// Checked before queueing, and cheaply: goal tool-use events run to
			// hundreds per run and must not reach the queue at all.
			e.log.Warn("dropping notification of unknown type", "type", ev.Type)
			return
		}
	}
	select {
	case e.queue <- ev:
	default:
		e.log.Warn("notification queue full; event dropped", "type", ev.Type)
	}
}

// OnGoalEvent maps a goal timeline entry onto a notification or a resolution. It
// is the single subscription that covers most goal activity: creation and
// resolution both flow through the same append, so both are handled here.
func (e *Engine) OnGoalEvent(ev store.GoalEvent) {
	if e == nil {
		return
	}
	// A single event can do both: a status change settles any pending completion
	// proposal and is itself worth reporting.
	if kind, ok := GoalEventResolves(ev.Kind); ok {
		id := goalEventResourceID(ev)
		if kind == ResourceGoalCompletion {
			// A completion proposal is about the goal, so the goal id is its handle.
			id = ev.GoalID
		}
		if id != "" {
			e.Publish(Event{Resolves: []ResourceRef{{Kind: kind, ID: id}}})
		}
	}
	notifType, ok := GoalEventType(ev.Kind)
	if !ok {
		return
	}
	info, ok := Lookup(notifType)
	if !ok {
		return
	}
	resourceID := ev.GoalID
	if info.Resource != ResourceGoal {
		if id := goalEventResourceID(ev); id != "" {
			resourceID = id
		}
	}
	e.Publish(Event{
		Type:       notifType,
		SessionID:  ev.SessionID,
		GoalID:     ev.GoalID,
		Resource:   info.Resource,
		ResourceID: resourceID,
		Detail:     ev.Body,
	})
}

// Close stops the worker and waits for the event in flight to finish.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	close(e.stop)
	<-e.done
}

// run is the worker loop. Events are handled one at a time and in order, so a
// resolution that follows a creation cannot overtake it.
//
// The loop is restarted on panic: this goroutine is the only thing turning domain
// activity into notifications, and its silent death would disable them for the
// rest of the daemon's life without any other symptom.
func (e *Engine) run() {
	defer close(e.done)
	for {
		if e.pump() {
			return
		}
		e.log.Warn("notification worker restarted after panic")
	}
}

// pump processes events until asked to stop. It reports whether the loop should
// exit for good, as opposed to recovering from a panic.
func (e *Engine) pump() (stopped bool) {
	defer func() {
		if r := recover(); r != nil {
			e.log.Error("notification worker panicked", "panic", r)
			stopped = false
		}
	}()
	for {
		select {
		case <-e.stop:
			return true
		case ev := <-e.queue:
			e.handle(ev)
		}
	}
}

// handle runs one event through the pipeline.
func (e *Engine) handle(ev Event) {
	ctx, cancel := context.WithTimeout(context.Background(), deliveryTimeout)
	defer cancel()

	if len(ev.Resolves) > 0 {
		e.resolve(ctx, ev.Resolves)
		return
	}
	info, ok := Lookup(ev.Type)
	if !ok {
		return
	}

	// A type marked as deduplicating collapses repeats about the same still-open
	// resource: one pending decision is one ask, however many times the producer
	// fires. Types not marked are a stream of news and are never collapsed.
	if info.Dedupe && ev.ResourceID != "" {
		if _, err := e.store.FindUnresolvedNotification(ctx, ev.Type, string(ev.Resource), ev.ResourceID); err == nil {
			return
		}
	}

	title, body := render(ev, e.resolveNames(ctx, ev))
	saved, err := e.store.CreateNotification(ctx, store.Notification{
		Type:         info.Type,
		Category:     string(info.Category),
		Importance:   info.Importance,
		Title:        title,
		Body:         body,
		AgentName:    ev.AgentName,
		SessionID:    ev.SessionID,
		GoalID:       ev.GoalID,
		ScheduleName: ev.ScheduleName,
		TaskID:       ev.TaskID,
		ResourceKind: string(ev.Resource),
		ResourceID:   ev.ResourceID,
		NavTarget:    info.NavTarget,
		Actionable:   info.Actionable(),
	})
	if err != nil {
		e.log.Error("persist notification failed", "event", "notification", "type", ev.Type, "err", err)
		return
	}

	// In-app first, and unconditionally: the Notification Center is not a delivery
	// channel to opt out of, it is where the notification lives.
	e.emitCreated(saved)

	e.deliver(ctx, info, saved, ev)
	e.maybePrune(ctx)
}

// resolve marks domain objects handled and broadcasts the rows it changed.
func (e *Engine) resolve(ctx context.Context, refs []ResourceRef) {
	for _, ref := range refs {
		changed, err := e.store.ResolveNotificationsByResource(ctx, string(ref.Kind), ref.ID)
		if err != nil {
			e.log.Error("resolve notifications failed", "event", "notification",
				"resource_kind", ref.Kind, "resource_id", ref.ID, "err", err)
			continue
		}
		for _, n := range changed {
			e.emitUpdated(n)
		}
	}
}

// deliver hands the notification to every channel the user has left enabled.
func (e *Engine) deliver(ctx context.Context, info Info, n store.Notification, ev Event) {
	if len(e.channels) == 0 {
		return
	}
	prefs, err := e.preferences(ctx)
	if err != nil {
		// Falling back to registry defaults is the safer failure: it can send a
		// notification the user had turned off, but it never silently swallows one
		// they are waiting on.
		e.log.Warn("read notification preferences failed; using defaults",
			"event", "notification", "err", err)
	}
	env := e.envelope(ctx, info, n, ev)

	for _, ch := range e.channels {
		if !prefs.enabled(info, ch.Name()) {
			// No delivery row: nothing was attempted, and an empty history is the
			// honest record of a channel the user switched off.
			continue
		}
		row, err := e.store.CreateNotificationDelivery(ctx, store.NotificationDelivery{
			NotificationID: n.ID,
			Channel:        ch.Name(),
		})
		if err != nil {
			e.log.Error("record delivery failed", "event", "notification",
				"channel", ch.Name(), "err", err)
			continue
		}
		results, sendErr := ch.Send(ctx, env)
		if sendErr != nil {
			e.log.Warn("notification channel failed", "event", "notification",
				"channel", ch.Name(), "type", n.Type, "err", sendErr)
			e.finish(ctx, row.ID, "", store.NotificationDeliveryFailed, sendErr.Error())
			continue
		}
		if len(results) == 0 {
			// The channel has no destinations registered. Record it as accepted with
			// no destination rather than as a failure: nothing went wrong.
			e.finish(ctx, row.ID, "", store.NotificationDeliveryAccepted, "")
			continue
		}
		e.recordResults(ctx, row, results)
	}
}

// recordResults writes one delivery row per destination, reusing the row already
// created for the first result so the common single-destination case costs one
// insert.
func (e *Engine) recordResults(ctx context.Context, row store.NotificationDelivery, results []Result) {
	for i, res := range results {
		status := store.NotificationDeliveryAccepted
		errMsg := ""
		if res.Err != nil {
			status = store.NotificationDeliveryFailed
			errMsg = res.Err.Error()
		}
		id := row.ID
		if i > 0 {
			extra, err := e.store.CreateNotificationDelivery(ctx, store.NotificationDelivery{
				NotificationID: row.NotificationID,
				Channel:        row.Channel,
			})
			if err != nil {
				e.log.Error("record delivery failed", "event", "notification",
					"channel", row.Channel, "err", err)
				continue
			}
			id = extra.ID
		}
		e.finish(ctx, id, res.Destination, status, errMsg)
	}
}

func (e *Engine) finish(ctx context.Context, id, destination string, status store.NotificationDeliveryStatus, errMsg string) {
	if err := e.store.FinishNotificationDelivery(ctx, id, destination, status, errMsg); err != nil {
		e.log.Error("finish delivery failed", "event", "notification", "delivery", id, "err", err)
	}
}

// envelope builds the transport payload.
//
// All payload construction funnels through here so what leaves the installation
// is decided in one place: the title and body are already rendered from domain
// fields and truncated, and nothing carries prompts, transcripts, tool output,
// secrets or credentials.
func (e *Engine) envelope(ctx context.Context, info Info, n store.Notification, ev Event) Envelope {
	return Envelope{
		ID:             n.ID,
		Type:           n.Type,
		PushKind:       info.PushKind(),
		InstallationID: e.installationID,
		Importance:     string(n.Importance),
		Title:          n.Title,
		Body:           n.Body,
		AgentName:      n.AgentName,
		SessionID:      n.SessionID,
		GoalID:         n.GoalID,
		ScheduleName:   n.ScheduleName,
		TaskID:         n.TaskID,
		ResourceKind:   n.ResourceKind,
		ResourceID:     n.ResourceID,
		NavTarget:      n.NavTarget,
		ActionSet:      info.ActionSet,
		Actions:        e.LiveActions(ctx, n),
		Approval:       ev.Approval,
	}
}

// emitCreated announces a newly recorded notification.
func (e *Engine) emitCreated(n store.Notification) {
	e.mu.RLock()
	fn := e.onCreated
	e.mu.RUnlock()
	if fn != nil {
		fn(n)
	}
}

// emitUpdated announces a change to a notification that already exists.
func (e *Engine) emitUpdated(n store.Notification) {
	e.mu.RLock()
	fn := e.onUpdated
	e.mu.RUnlock()
	if fn != nil {
		fn(n)
	}
}

// pending reports whether an in-memory request is still awaiting a decision.
// With no predicate wired the answer is "yes": offering an action that turns out
// to be stale is recoverable, since the server validates it again and rejects it.
func (e *Engine) pending(kind ResourceKind, requestID string) bool {
	e.mu.RLock()
	fn := e.pendingCheck
	e.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn(kind, requestID)
}

// maybePrune trims history every pruneInterval inserts.
func (e *Engine) maybePrune(ctx context.Context) {
	e.inserts++
	if e.inserts < pruneInterval {
		return
	}
	e.inserts = 0
	if _, err := e.store.PruneNotifications(ctx, historyLimit); err != nil {
		e.log.Warn("prune notifications failed", "event", "notification", "err", err)
	}
}

// resolveNames looks up the display strings an event's text needs. Every lookup
// failure degrades to an empty name rather than aborting: a notification with a
// slightly plainer title is much better than no notification.
func (e *Engine) resolveNames(ctx context.Context, ev Event) displayNames {
	var names displayNames
	names.Schedule = ev.ScheduleName
	if ev.AgentName == "" && ev.SessionID != "" {
		if sess, err := e.store.GetSession(ctx, ev.SessionID); err == nil {
			names.Agent = sess.AgentName
		}
	}
	if ev.GoalID != "" {
		if goal, err := e.store.GetGoal(ctx, ev.GoalID); err == nil {
			names.Goal = goal.Title
			if names.Agent == "" {
				names.Agent = goal.LeadAgent
			}
		}
	}
	if ev.TaskID != "" {
		if task, err := e.store.GetTask(ctx, ev.TaskID); err == nil {
			names.Task = task.Title
			if names.Agent == "" {
				names.Agent = task.AssignedAgent
			}
		}
	}
	return names
}

// prefs resolves whether a type should be delivered on a channel.
type prefs struct {
	overrides map[prefKey]bool
}

type prefKey struct {
	notifType string
	channel   string
}

// enabled reports whether this type notifies on this channel. A stored row is the
// user's explicit choice; its absence means the registry default, which is what
// lets a notification type added in a later release arrive with the intended
// default and no data migration.
func (p prefs) enabled(info Info, channel string) bool {
	if v, ok := p.overrides[prefKey{info.Type, channel}]; ok {
		return v
	}
	return info.DefaultOn
}

// preferences loads the user's overrides. The table holds one row per explicit
// choice on a single-user installation, so it is read per event rather than
// cached — there is no invalidation to get wrong.
func (e *Engine) preferences(ctx context.Context) (prefs, error) {
	rows, err := e.store.ListNotificationPreferences(ctx)
	if err != nil {
		return prefs{}, err
	}
	out := prefs{overrides: make(map[prefKey]bool, len(rows))}
	for _, row := range rows {
		out.overrides[prefKey{row.Type, row.Channel}] = row.Enabled
	}
	return out, nil
}
