package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Podiom/Podiom/internal/store"
)

// ErrActionNotGoalRun is returned when podiom_request_user_action is called from
// a run that is not part of a goal, where there is no goal page to hand the work
// back on.
var ErrActionNotGoalRun = errors.New("podiom_request_user_action is only available in goal runs; there is no goal to hand this back to")

// ErrActionTitleRequired is returned when an action item carries no ask.
var ErrActionTitleRequired = errors.New("an action item needs a title saying what the user should do")

// GoalActionResult is the outcome of an agent filing an action item, carrying
// what the server needs to broadcast and notify.
type GoalActionResult struct {
	Item  store.GoalActionItem
	Goal  store.Goal
	Event store.GoalEvent
}

// GoalActionResponseResult is the outcome of the user answering an action item.
type GoalActionResponseResult struct {
	Item  store.GoalActionItem
	Goal  *store.Goal
	Event *store.GoalEvent
}

// RequestGoalAction records a step the agent decided only the user can carry
// out. The goal is derived from the session, so a goal-linked task or schedule
// run files onto the goal it belongs to — the same routing rule questions use.
// Unlike a question this never pauses the goal's reviews: the item is a hand-off,
// not a gate, and the agent is expected to keep working around it.
func (c *Core) RequestGoalAction(ctx context.Context, sessionID string, item store.GoalActionItem) (GoalActionResult, error) {
	sess, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return GoalActionResult{}, err
	}
	origin, ref := agentQuestionContext(sess)
	if origin != store.AgentQuestionGoal {
		return GoalActionResult{}, ErrActionNotGoalRun
	}
	if strings.TrimSpace(item.Title) == "" {
		return GoalActionResult{}, ErrActionTitleRequired
	}
	goal, err := c.store.GetGoal(ctx, ref)
	if err != nil {
		return GoalActionResult{}, err
	}
	item.GoalID = goal.ID
	item.SessionID = sess.ID
	item.RunID = c.goalRunForAgentEvent(ctx, goal.ID, sess.ID)
	item.AgentName = sess.AgentName
	item.Status = store.GoalActionOpen
	created, err := c.store.CreateGoalActionItem(ctx, item)
	if err != nil {
		return GoalActionResult{}, err
	}
	payload, _ := json.Marshal(map[string]string{"action_item_id": created.ID})
	ev, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:    goal.ID,
		SessionID: sess.ID,
		RunID:     created.RunID,
		Kind:      store.GoalEventActionRequested,
		Body:      "Needs you: " + strings.TrimSpace(created.Title),
		Payload:   string(payload),
	})
	if err != nil {
		return GoalActionResult{}, err
	}
	c.log.Info("goal action item filed",
		"event", "goal", "goal", goal.ID, "session", sess.ID, "item", created.ID)
	return GoalActionResult{Item: created, Goal: goal, Event: ev}, nil
}

// RespondGoalActionItem records the user's verdict and note. Like feedback and
// access-request notes it is stored, not delivered: the agent reads it in its
// next planning or review prompt, so responding never starts a run.
func (c *Core) RespondGoalActionItem(ctx context.Context, id string, status store.GoalActionItemStatus, response string) (GoalActionResponseResult, error) {
	switch status {
	case store.GoalActionDone, store.GoalActionBlocked, store.GoalActionDeclined:
	default:
		return GoalActionResponseResult{}, fmt.Errorf("action item verdict %q must be done, blocked, or declined", status)
	}
	item, err := c.store.RespondGoalActionItem(ctx, id, status, response)
	if err != nil {
		return GoalActionResponseResult{}, err
	}
	res := GoalActionResponseResult{Item: item}
	if goal, gerr := c.store.GetGoal(ctx, item.GoalID); gerr == nil {
		res.Goal = &goal
	}
	payload, _ := json.Marshal(map[string]string{"action_item_id": item.ID, "status": string(item.Status)})
	// No session or run id: this is the user acting, like access_decided and
	// question_answered.
	ev, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:  item.GoalID,
		Kind:    store.GoalEventActionResponded,
		Body:    goalActionResponseSummary(item),
		Payload: string(payload),
	})
	if err == nil {
		res.Event = &ev
	}
	c.log.Info("goal action item answered",
		"event", "goal", "goal", item.GoalID, "item", item.ID, "status", string(item.Status))
	return res, nil
}

// ListOpenGoalActionItems returns the action items a goal is waiting on.
func (c *Core) ListOpenGoalActionItems(ctx context.Context, goalID string) ([]store.GoalActionItem, error) {
	return c.store.ListOpenGoalActionItems(ctx, goalID)
}

// ListRespondedGoalActionItems returns the goal's recently answered action items.
func (c *Core) ListRespondedGoalActionItems(ctx context.Context, goalID string, limit int) ([]store.GoalActionItem, error) {
	return c.store.ListRespondedGoalActionItems(ctx, goalID, limit)
}

// CountOpenGoalActionItems reports how many action items a goal is waiting on.
func (c *Core) CountOpenGoalActionItems(ctx context.Context, goalID string) (int, error) {
	return c.store.CountOpenGoalActionItems(ctx, goalID)
}

// goalActionVerdicts renders each verdict the way the user chose it, so the
// timeline and the review prompt say the same thing the buttons did.
var goalActionVerdicts = map[store.GoalActionItemStatus]string{
	store.GoalActionDone:     "Done",
	store.GoalActionBlocked:  "Couldn't do",
	store.GoalActionDeclined: "Not doing",
}

// goalActionResponseSummary renders the timeline body for an answered item.
func goalActionResponseSummary(item store.GoalActionItem) string {
	verdict := goalActionVerdicts[item.Status]
	if verdict == "" {
		verdict = string(item.Status)
	}
	body := verdict + " — " + strings.TrimSpace(item.Title)
	if note := strings.TrimSpace(item.Response); note != "" {
		body += "\n\n" + note
	}
	return body
}
