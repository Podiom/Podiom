package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Podiom/Podiom/internal/store"
	"github.com/google/uuid"
)

// ErrQuestionNotUnattended is returned when podiom_ask_user is called from an
// interactive session, where the agent should ask the user directly instead.
var ErrQuestionNotUnattended = errors.New("podiom_ask_user is only available in goal or scheduled runs; in an interactive session, ask the user directly")

// AskUserResult is the outcome of an unattended agent recording a question.
type AskUserResult struct {
	Question store.AgentQuestion
	// Goal is set when the question came from a goal run (including goal-linked
	// schedule/task runs), so the caller can notify and broadcast on the goal.
	Goal *store.Goal
	// Event is the question_asked timeline entry, set for goal-scoped questions.
	Event *store.GoalEvent
}

// AnswerQuestionResult is the outcome of a user answering a deferred question.
type AnswerQuestionResult struct {
	Question store.AgentQuestion
	Goal     *store.Goal
	Event    *store.GoalEvent
}

// CreateAgentQuestion records a question an unattended agent asked the user
// (defer-and-resume). Origin/ref are derived from the session: goal runs (and
// goal-linked schedule/task runs) surface on the goal page; standalone
// scheduled runs surface on the schedule. Interactive sessions are rejected.
func (c *Core) CreateAgentQuestion(ctx context.Context, sessionID string, items []store.AgentQuestionItem) (AskUserResult, error) {
	sess, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return AskUserResult{}, err
	}
	origin, ref := agentQuestionContext(sess)
	if origin == "" {
		return AskUserResult{}, ErrQuestionNotUnattended
	}
	if len(items) == 0 {
		return AskUserResult{}, fmt.Errorf("at least one question is required")
	}
	// A stable id per item lets the answer key back to the question.
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" {
			items[i].ID = uuid.NewString()
		}
	}
	q, err := c.store.CreateAgentQuestion(ctx, store.AgentQuestion{
		Origin:    origin,
		RefID:     ref,
		SessionID: sess.ID,
		Questions: items,
	})
	if err != nil {
		return AskUserResult{}, err
	}
	res := AskUserResult{Question: q}
	if origin == store.AgentQuestionGoal {
		goal, gerr := c.store.GetGoal(ctx, ref)
		if gerr != nil {
			return res, gerr
		}
		res.Goal = &goal
		payload, _ := json.Marshal(map[string]string{"question_id": q.ID})
		ev, aerr := c.appendGoalEvent(ctx, store.GoalEvent{
			GoalID:    goal.ID,
			SessionID: sess.ID,
			RunID:     c.goalRunForAgentEvent(ctx, goal.ID, sess.ID),
			Kind:      store.GoalEventQuestionAsked,
			Body:      agentQuestionSummary(items),
			Payload:   string(payload),
		})
		if aerr != nil {
			return res, aerr
		}
		res.Event = &ev
	}
	c.log.Info("agent question recorded",
		"event", "question", "origin", string(origin), "ref", ref, "session", sess.ID, "questions", len(items))
	return res, nil
}

// AnswerAgentQuestion records the user's answers and, for a goal-scoped
// question, appends a question_answered timeline entry. Answering a goal
// question lets its paused reviews resume (ListDueGoalReviews stops excluding it).
func (c *Core) AnswerAgentQuestion(ctx context.Context, id string, answers map[string][]string) (AnswerQuestionResult, error) {
	q, err := c.store.AnswerAgentQuestion(ctx, id, answers)
	if err != nil {
		return AnswerQuestionResult{}, err
	}
	res := AnswerQuestionResult{Question: q}
	if q.Origin == store.AgentQuestionGoal {
		if goal, gerr := c.store.GetGoal(ctx, q.RefID); gerr == nil {
			res.Goal = &goal
		}
		payload, _ := json.Marshal(map[string]string{"question_id": q.ID})
		ev, aerr := c.appendGoalEvent(ctx, store.GoalEvent{
			GoalID:  q.RefID,
			Kind:    store.GoalEventQuestionAnswered,
			Body:    agentAnswerSummary(q),
			Payload: string(payload),
		})
		if aerr == nil {
			res.Event = &ev
		}
	}
	c.log.Info("agent question answered",
		"event", "question", "origin", string(q.Origin), "ref", q.RefID, "question", q.ID)
	return res, nil
}

// PendingAgentQuestion returns the newest pending question for a goal or
// schedule, or nil when there is none.
func (c *Core) PendingAgentQuestion(ctx context.Context, origin store.AgentQuestionOrigin, refID string) (*store.AgentQuestion, error) {
	q, err := c.store.PendingAgentQuestion(ctx, origin, refID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

// GetAgentQuestion returns one question by id.
func (c *Core) GetAgentQuestion(ctx context.Context, id string) (store.AgentQuestion, error) {
	return c.store.GetAgentQuestion(ctx, id)
}

// DeleteAgentQuestions removes every question for a goal or schedule (cleanup on
// parent deletion).
func (c *Core) DeleteAgentQuestions(ctx context.Context, origin store.AgentQuestionOrigin, refID string) error {
	return c.store.DeleteAgentQuestions(ctx, origin, refID)
}

// ListAnsweredScheduleQuestions returns answered questions for a schedule, so a
// later run can act on the answers.
func (c *Core) ListAnsweredScheduleQuestions(ctx context.Context, scheduleName string, limit int) ([]store.AgentQuestion, error) {
	return c.store.ListAnsweredAgentQuestions(ctx, store.AgentQuestionSchedule, scheduleName, limit)
}

// agentQuestionContext maps a session to its question surface. Goal takes
// precedence: a goal-linked schedule/task run is part of the goal's chain and
// surfaces on the goal page.
func agentQuestionContext(sess store.Session) (store.AgentQuestionOrigin, string) {
	if strings.TrimSpace(sess.GoalID) != "" {
		return store.AgentQuestionGoal, sess.GoalID
	}
	if strings.TrimSpace(sess.ScheduleID) != "" {
		return store.AgentQuestionSchedule, sess.ScheduleID
	}
	return "", ""
}

// agentQuestionSummary renders the one-line timeline/notification body for a set
// of asked questions.
func agentQuestionSummary(items []store.AgentQuestionItem) string {
	if len(items) == 0 {
		return "Asked the user a question."
	}
	first := strings.TrimSpace(items[0].Question)
	if first == "" {
		first = strings.TrimSpace(items[0].Header)
	}
	if len(items) > 1 {
		return fmt.Sprintf("Asked the user: %s (+%d more)", first, len(items)-1)
	}
	return "Asked the user: " + first
}

// agentAnswerSummary renders the timeline body when a question is answered.
func agentAnswerSummary(q store.AgentQuestion) string {
	var parts []string
	for _, item := range q.Questions {
		ans := strings.Join(q.Answers[item.ID], ", ")
		if strings.TrimSpace(ans) == "" {
			continue
		}
		label := strings.TrimSpace(item.Header)
		if label == "" {
			label = strings.TrimSpace(item.Question)
		}
		parts = append(parts, label+": "+ans)
	}
	if len(parts) == 0 {
		return "You answered the question."
	}
	return "You answered — " + strings.Join(parts, "; ")
}
