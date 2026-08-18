package core

import (
	"context"

	"github.com/Podiom/Podiom/internal/store"
)

// appendGoalEvent persists a goal timeline entry and publishes it to the live
// broadcast and the notification engine.
//
// Every goal event in core goes through here. It is the single point at which a
// timeline entry becomes visible outside the database, so a new event kind cannot
// silently skip live updates or notifications — and because both requests and
// their resolutions are timeline entries, one subscription covers a goal's whole
// notification lifecycle.
func (c *Core) appendGoalEvent(ctx context.Context, ev store.GoalEvent) (store.GoalEvent, error) {
	saved, err := c.store.AppendGoalEvent(ctx, ev)
	if err != nil {
		return saved, err
	}
	c.emitGoalEvent(saved)
	return saved, nil
}

// appendGoalEventWithMetrics is appendGoalEvent for the metric-update case, where
// the event and the goal's new metric values must land in one transaction.
func (c *Core) appendGoalEventWithMetrics(ctx context.Context, ev store.GoalEvent, metrics []store.GoalMetric) (store.GoalEvent, error) {
	saved, err := c.store.AppendGoalEventWithMetrics(ctx, ev, metrics)
	if err != nil {
		return saved, err
	}
	c.emitGoalEvent(saved)
	return saved, nil
}

// emitGoalEvent fans a stored timeline entry out to its observers. Both are
// optional: the daemon wires them, tests generally do not.
func (c *Core) emitGoalEvent(ev store.GoalEvent) {
	if c.onGoalEvent != nil {
		c.onGoalEvent(ev)
	}
	// Publishing is non-blocking and nil-safe, so a notification can never slow
	// down or fail the domain operation that produced the event.
	c.notifications.OnGoalEvent(ev)
}
