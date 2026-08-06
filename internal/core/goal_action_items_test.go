package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// newActionItemGoal sets up a lead agent, a goal, and a goal-origin session for
// it — the state an action item is filed from.
func newActionItemGoal(t *testing.T, c *Core) (store.Goal, store.Session) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := c.CreateGoal(ctx, store.Goal{Title: "Launch Podiom", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "lead", Origin: store.OriginGoal, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return goal, sess
}

func TestRequestGoalActionRecordsItemAndTimelineEntry(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	goal, sess := newActionItemGoal(t, c)

	res, err := c.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{
		Title:        "Post the launch thread on r/selfhosted",
		Instructions: "Title: “I built Podiom”. Post 14:00–17:00 UTC.",
		Why:          "I have no Reddit account.",
	})
	if err != nil {
		t.Fatalf("request action: %v", err)
	}
	if res.Item.GoalID != goal.ID || res.Item.Status != store.GoalActionOpen {
		t.Fatalf("item = %+v, want the goal's id and open status", res.Item)
	}
	// Identity is stamped from the session, not taken from the model.
	if res.Item.SessionID != sess.ID || res.Item.AgentName != "lead" {
		t.Fatalf("item provenance = %+v, want session %s and agent lead", res.Item, sess.ID)
	}
	if res.Event.Kind != store.GoalEventActionRequested || !strings.Contains(res.Event.Body, "r/selfhosted") {
		t.Fatalf("event = %+v, want an action_requested entry naming the ask", res.Event)
	}
	if res.Event.SessionID != sess.ID {
		t.Fatalf("event session = %q, want the filing session for attribution", res.Event.SessionID)
	}
}

// An action item needs a goal to hand work back on: a plain chat session has no
// page for the user to answer it from, so the tool must refuse rather than
// silently drop the ask.
func TestRequestGoalActionRejectsNonGoalSession(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "lead", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "lead", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := c.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{Title: "Do a thing"}); !errors.Is(err, ErrActionNotGoalRun) {
		t.Fatalf("err = %v, want ErrActionNotGoalRun", err)
	}
}

// A goal-linked task run is part of the goal's chain, so the work it hands back
// surfaces on the goal — the same routing precedence deferred questions use.
func TestRequestGoalActionFromGoalLinkedTaskRun(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	goal, _ := newActionItemGoal(t, c)

	taskSess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: "lead",
		Origin:    store.OriginRoadmap,
		GoalID:    goal.ID,
	})
	if err != nil {
		t.Fatalf("create task session: %v", err)
	}
	res, err := c.RequestGoalAction(ctx, taskSess.ID, store.GoalActionItem{Title: "Sign the vendor contract"})
	if err != nil {
		t.Fatalf("request action from task run: %v", err)
	}
	if res.Item.GoalID != goal.ID {
		t.Fatalf("item goal = %q, want %q", res.Item.GoalID, goal.ID)
	}
}

func TestRequestGoalActionRequiresTitle(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	_, sess := newActionItemGoal(t, c)

	if _, err := c.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{Instructions: "no ask here"}); !errors.Is(err, ErrActionTitleRequired) {
		t.Fatalf("err = %v, want ErrActionTitleRequired", err)
	}
}

// Answering is the user acting, so the timeline entry carries no producer
// session or run — the same shape as access_decided and question_answered.
func TestRespondGoalActionIsHumanActivityWithoutProducerSession(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	_, sess := newActionItemGoal(t, c)

	filed, err := c.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{Title: "Post the thread"})
	if err != nil {
		t.Fatalf("request action: %v", err)
	}
	res, err := c.RespondGoalActionItem(ctx, filed.Item.ID, store.GoalActionDone, "Posted it — https://redd.it/x")
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if res.Item.Status != store.GoalActionDone {
		t.Fatalf("status = %q, want done", res.Item.Status)
	}
	if res.Event == nil || res.Event.SessionID != "" || res.Event.RunID != "" {
		t.Fatalf("response event = %+v, want human activity without producer provenance", res.Event)
	}
	if !strings.Contains(res.Event.Body, "Done") || !strings.Contains(res.Event.Body, "redd.it") {
		t.Fatalf("response body = %q, want the verdict and the note", res.Event.Body)
	}
}

func TestRespondGoalActionRejectsUnknownVerdict(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	_, sess := newActionItemGoal(t, c)

	filed, err := c.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{Title: "Post the thread"})
	if err != nil {
		t.Fatalf("request action: %v", err)
	}
	if _, err := c.RespondGoalActionItem(ctx, filed.Item.ID, store.GoalActionOpen, ""); err == nil {
		t.Fatal("responding with 'open' succeeded; only the three terminal verdicts are the user's to give")
	}
}

// The review prompt is where a hand-off actually reaches the agent: it must see
// what is still owed to it and what came back, or it will re-file the same ask.
func TestGoalReviewPromptCarriesActionItems(t *testing.T) {
	goal := store.Goal{ID: "g1", Title: "Launch Podiom"}
	actions := GoalActionItems{
		Open: []store.GoalActionItem{{
			Title:     "Post the launch thread on r/selfhosted",
			Why:       "I have no Reddit account.",
			CreatedAt: "2026-08-01T09:00:00Z",
			Status:    store.GoalActionOpen,
		}},
		Responded: []store.GoalActionItem{{
			Title:    "Sign the vendor contract",
			Status:   store.GoalActionBlocked,
			Response: "Legal wants a redline first.",
		}},
	}
	prompt := GoalReviewPrompt(goal, nil, nil, nil, nil, actions)

	for _, want := range []string{
		"Action items you handed to the user",
		"r/selfhosted",
		"2026-08-01T09:00:00Z",
		"Do not re-file",
		"Couldn't do",
		"Legal wants a redline first.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}

	bare := GoalReviewPrompt(goal, nil, nil, nil, nil, GoalActionItems{})
	if strings.Contains(bare, "Action items you handed to the user") {
		t.Fatal("review prompt rendered an empty action-item section")
	}
}

// next_step is the agent's own move. Both prompts must say so and point at the
// hand-off tool, otherwise user-owned work goes on being claimed as agent intent
// and never reaches the user.
func TestGoalPromptsRouteUserWorkAwayFromNextStep(t *testing.T) {
	goal := store.Goal{ID: "g1", Title: "Launch Podiom"}
	for name, prompt := range map[string]string{
		"planning": GoalPlanningPrompt(goal, nil, GoalActionItems{}),
		"review":   GoalReviewPrompt(goal, nil, nil, nil, nil, GoalActionItems{}),
	} {
		if !strings.Contains(prompt, "podiom_request_user_action") {
			t.Fatalf("%s prompt never mentions podiom_request_user_action:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, "something YOU will do") {
			t.Fatalf("%s prompt does not constrain next_step to the agent's own move:\n%s", name, prompt)
		}
	}
}

// Open action items must never suppress a review: a hand-off is not a gate, and
// this is the line that separates them from podiom_ask_user.
func TestOpenActionItemsDoNotPauseReviews(t *testing.T) {
	ctx := context.Background()
	c, _, cleanup := newScheduledTestCore(t)
	defer cleanup()
	goal, sess := newActionItemGoal(t, c)

	const overdue = "2000-01-01T00:00:00Z"
	now := "2030-01-01T00:00:00Z"
	if err := c.store.SetGoalNextReview(ctx, goal.ID, overdue); err != nil {
		t.Fatalf("set next review: %v", err)
	}
	if _, err := c.RequestGoalAction(ctx, sess.ID, store.GoalActionItem{Title: "Post the thread"}); err != nil {
		t.Fatalf("request action: %v", err)
	}
	due, err := c.store.ListDueGoalReviews(ctx, now)
	if err != nil {
		t.Fatalf("list due reviews: %v", err)
	}
	if len(due) != 1 || due[0].ID != goal.ID {
		t.Fatalf("due = %+v, want the goal to stay due while an action item is open", due)
	}

	// A pending question, by contrast, does hold the goal back.
	if _, err := c.CreateAgentQuestion(ctx, sess.ID, []store.AgentQuestionItem{{ID: "q", Question: "Which path?"}}); err != nil {
		t.Fatalf("ask question: %v", err)
	}
	if due, err = c.store.ListDueGoalReviews(ctx, now); err != nil || len(due) != 0 {
		t.Fatalf("due with a pending question = %+v, %v; want none", due, err)
	}
}
