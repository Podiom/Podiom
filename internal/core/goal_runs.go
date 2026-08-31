package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

func goalRunShape(sess store.Session) (store.GoalRunKind, string) {
	switch sess.Origin {
	case store.OriginRoadmap:
		return store.GoalRunTask, sess.TaskID
	case store.OriginSchedule:
		return store.GoalRunSchedule, sess.ScheduleID
	default:
		return store.GoalRunConversation, ""
	}
}

func (c *Core) beginGoalRun(ctx context.Context, sess store.Session, kind store.GoalRunKind, sourceID string) (store.GoalRun, error) {
	if sess.GoalID == "" {
		return store.GoalRun{}, fmt.Errorf("session %q is not linked to a goal", sess.ID)
	}
	if kind == "" {
		kind, sourceID = goalRunShape(sess)
	}
	run, err := c.store.CreateGoalRun(ctx, store.GoalRun{
		GoalID:    sess.GoalID,
		SessionID: sess.ID,
		Kind:      kind,
		AgentName: sess.AgentName,
		SourceID:  sourceID,
	})
	if err != nil {
		return run, err
	}
	c.notifications.Publish(notify.Event{
		Type:       notify.TypeGoalRunStarted,
		SessionID:  sess.ID,
		GoalID:     sess.GoalID,
		AgentName:  sess.AgentName,
		Resource:   notify.ResourceGoalRun,
		ResourceID: run.ID,
	})
	return run, nil
}

// finishGoalRun records a goal run's terminal state and reports it.
//
// Every core path that ends a run goes through here rather than calling the store,
// so a run cannot finish without the outcome being reported. Two statuses
// deliberately produce no notification:
//
//   - interrupted, because InterruptRunningGoalRuns flips every running row at
//     daemon start and would turn each restart into a burst of notifications. That
//     bulk update calls the store directly, bypassing this function by design.
//   - rate_limited, because the rate-limit block already emits goal.rate_limited,
//     which carries the block id the recovery view needs and this does not.
func (c *Core) finishGoalRun(ctx context.Context, id string, status store.GoalRunStatus, runErr string) (store.GoalRun, error) {
	run, err := c.store.FinishGoalRun(ctx, id, status, runErr)
	if err != nil {
		return run, err
	}
	var notifType string
	// A failed run's detail is why it failed. A successful one has no error to report,
	// so it carries the agent's closing words instead — the notification then says what
	// the run achieved rather than only that it ended.
	detail := runErr
	switch status {
	case store.GoalRunSucceeded:
		notifType = notify.TypeGoalRunSucceeded
		detail = c.TurnAnswer(ctx, run.SessionID)
	case store.GoalRunFailed:
		notifType = notify.TypeGoalRunFailed
	default:
		return run, nil
	}
	c.notifications.Publish(notify.Event{
		Type:       notifType,
		SessionID:  run.SessionID,
		GoalID:     run.GoalID,
		AgentName:  run.AgentName,
		Resource:   notify.ResourceGoalRun,
		ResourceID: run.ID,
		Detail:     detail,
	})
	return run, nil
}

func (c *Core) goalRunForAgentEvent(ctx context.Context, goalID, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	run, err := c.store.GetRunningGoalRunBySession(ctx, sessionID)
	if err != nil || run.GoalID != goalID {
		return ""
	}
	return run.ID
}

func (c *Core) RunningGoalRun(ctx context.Context, goalID string) (*store.GoalRun, error) {
	run, err := c.store.GetRunningGoalRunByGoal(ctx, goalID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (c *Core) ListGoalRuns(ctx context.Context, goalID string, limit int) ([]store.GoalRun, error) {
	return c.store.ListGoalRuns(ctx, goalID, limit)
}

// GetGoalRunDetail returns the exact bounded transcript and events for one run.
func (c *Core) GetGoalRunDetail(ctx context.Context, goalID, runID string) (store.GoalRun, store.Session, []store.Message, []store.GoalEvent, error) {
	run, err := c.store.GetGoalRun(ctx, runID)
	if err != nil {
		return store.GoalRun{}, store.Session{}, nil, nil, err
	}
	if run.GoalID != goalID {
		return store.GoalRun{}, store.Session{}, nil, nil, store.ErrNotFound
	}
	events, err := c.store.ListGoalEventsByRun(ctx, goalID, runID)
	if err != nil {
		return store.GoalRun{}, store.Session{}, nil, nil, err
	}
	sess, err := c.store.GetSession(ctx, run.SessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return run, store.Session{}, nil, events, nil
		}
		return store.GoalRun{}, store.Session{}, nil, nil, err
	}
	messages, err := c.store.ListMessagesForGoalRun(ctx, run)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.GoalRun{}, store.Session{}, nil, nil, err
	}
	return run, sess, messages, events, nil
}
