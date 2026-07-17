package core

import (
	"context"
	"errors"
	"fmt"

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
	return c.store.CreateGoalRun(ctx, store.GoalRun{
		GoalID:    sess.GoalID,
		SessionID: sess.ID,
		Kind:      kind,
		AgentName: sess.AgentName,
		SourceID:  sourceID,
	})
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
