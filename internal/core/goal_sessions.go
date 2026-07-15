package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// GoalPlanningPrompt renders the decomposition contract for a goal's initial
// planning session.
func GoalPlanningPrompt(goal store.Goal, feedback []store.GoalEvent) string {
	var b strings.Builder
	b.WriteString("You are the lead agent for a new Podiom goal. Plan how to reach it.\n\n")
	writeGoalBrief(&b, goal)
	writeUserFeedback(&b, feedback)
	b.WriteString(`## How you run

You have full autonomous access (yolo mode): you may run shell commands, edit
files, and install tools directly — there are no permission prompts. Every tool
call you make is recorded on this goal's audit timeline, so the user can see
exactly what you did while they were away.

## Your job right now (planning session)

1. Decompose the goal into concrete work:
   - Create roadmap tasks with podiom_create_task (assign other agents where sensible; you stay accountable). Pass goal_id (this goal's ID, above) so the task's runs are linked to this goal, run autonomously, and are audited on this timeline.
   - Create recurring schedules with podiom_create_schedule for work that must repeat, passing goal_id so they run as part of this goal's autonomous chain.
2. Do quick setup and investigation directly (install a CLI tool, read the repo, run a probe command) — but push the substantial and recurring work into the tasks and schedules above so it is tracked and survives this session.
3. Consider the user's feedback above as strategic guidance when shaping the plan, unless it conflicts with the goal definition or success criteria.
4. Record your plan with podiom_record_goal_progress (kind "plan_change"): what you created and why it reaches the goal.
5. File podiom_request_access only for things you genuinely cannot do yourself: assigning an MCP server, installing a marketplace skill, or a credential / environment variable (never ask for the secret value itself). You do not need to request CLI-tool installs or a permission level — you already have full access.
6. If — and only if — you are genuinely blocked on a decision that is the user's to make (a strategic choice, a missing value, a preference you cannot infer), call podiom_ask_user with the question and a few selectable answers. This pauses the goal's reviews and surfaces the question on the goal page; the user's answer is fed into your next session. Do not ask about things you can decide yourself.

The user is away. They will see your plan, your access requests, and this goal's timeline when they return.
`)
	return b.String()
}

// GoalReviewPrompt renders the periodic review contract: recent timeline and
// access-request decisions (including the user's notes — their channel back to
// the agent) plus the review duties.
func GoalReviewPrompt(goal store.Goal, events []store.GoalEvent, requests []store.AccessRequest, feedback []store.GoalEvent, answers []store.AgentQuestion) string {
	var b strings.Builder
	b.WriteString("You are the lead agent for a Podiom goal. This is a scheduled review session.\n\n")
	writeGoalBrief(&b, goal)
	writeUserFeedback(&b, feedback)
	writeAnsweredQuestions(&b, answers)

	if len(requests) > 0 {
		b.WriteString("## Your access requests\n\n")
		for _, r := range requests {
			fmt.Fprintf(&b, "- [%s] %s — %s", r.Status, r.Kind, strings.TrimSpace(r.Reason))
			if strings.TrimSpace(r.DecisionNote) != "" {
				fmt.Fprintf(&b, "\n  User's note: %s", strings.TrimSpace(r.DecisionNote))
			}
			if strings.TrimSpace(r.ExecutionError) != "" {
				fmt.Fprintf(&b, "\n  Grant error: %s", strings.TrimSpace(r.ExecutionError))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(events) > 0 {
		b.WriteString("## Recent timeline (newest first)\n\n")
		for _, e := range events {
			line := strings.TrimSpace(e.Body)
			if line == "" {
				line = string(e.Kind)
			}
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			fmt.Fprintf(&b, "- %s [%s] %s\n", e.CreatedAt, e.Kind, line)
		}
		b.WriteString("\n")
	}

	b.WriteString(`## How you run

You have full autonomous access (yolo mode): you may run shell commands, edit
files, and install tools directly — no permission prompts. Every tool call is
recorded on this goal's audit timeline. (The timeline above omits individual
tool-call entries to stay readable; the full record is on the goal page.)

## Your job right now (review session)

1. Assess progress against the success criteria. Check the state of the tasks and schedules you created (podiom_list_tasks, podiom_list_schedules) and adjust them where the plan has drifted.
2. Consider the user's recent feedback above as strategic guidance when adjusting tasks, schedules, or next steps, unless it conflicts with explicit success criteria or status.
3. Record a progress entry with podiom_record_goal_progress: what moved since the last review, with evidence. Update metric values there when they changed.
4. Take direct corrective action when it is quick and unblocks progress (run a command, fix a file, unstick a task); push larger or recurring work into tasks and schedules (with goal_id) so it is tracked.
5. File podiom_request_access only for what you cannot do yourself (an MCP server, a marketplace skill, a credential). If the user answered a previous request (see above), act on their note.
6. If — and only if — you are genuinely blocked on a decision that is the user's to make, call podiom_ask_user with the question and a few selectable answers. This pauses reviews and surfaces it on the goal page; the answer reaches your next session. If the user answered a previous question (see above), act on their answer. Do not ask about things you can decide yourself.
7. If — and only if — every success criterion is met, call podiom_propose_goal_completion with a closing report that walks through each criterion. The user makes the final call.
`)
	return b.String()
}

func writeGoalBrief(b *strings.Builder, goal store.Goal) {
	fmt.Fprintf(b, "## The goal\n\n- ID: %s\n- Title: %s\n", goal.ID, goal.Title)
	if strings.TrimSpace(goal.Description) != "" {
		fmt.Fprintf(b, "- Description: %s\n", strings.TrimSpace(goal.Description))
	}
	if strings.TrimSpace(goal.SuccessCriteria) != "" {
		fmt.Fprintf(b, "- Success criteria (what \"done\" means): %s\n", strings.TrimSpace(goal.SuccessCriteria))
	}
	if goal.ProjectID != "" {
		fmt.Fprintf(b, "- Project: %s\n", goal.ProjectID)
	}
	if goal.ReviewEvery != "" {
		fmt.Fprintf(b, "- Review cadence: every %s\n", goal.ReviewEvery)
	}
	if len(goal.Metrics) > 0 {
		b.WriteString("- Metrics:\n")
		for _, m := range goal.Metrics {
			fmt.Fprintf(b, "  - %s: %g / %g %s\n", m.Name, m.Current, m.Target, m.Unit)
		}
	}
	b.WriteString("\n")
}

func writeUserFeedback(b *strings.Builder, feedback []store.GoalEvent) {
	if len(feedback) == 0 {
		return
	}
	b.WriteString("## Recent user feedback (newest first)\n\n")
	for _, ev := range feedback {
		body := strings.TrimSpace(ev.Body)
		if body == "" {
			continue
		}
		if len(body) > 500 {
			body = body[:500] + "…"
		}
		fmt.Fprintf(b, "- %s: %s\n", ev.CreatedAt, body)
	}
	b.WriteString("\n")
}

// writeAnsweredQuestions renders the answers the user gave to questions this
// goal's agent asked in earlier sessions (via podiom_ask_user) — their channel
// back to the agent for a blocking decision.
func writeAnsweredQuestions(b *strings.Builder, answers []store.AgentQuestion) {
	if len(answers) == 0 {
		return
	}
	b.WriteString("## Your answered questions (newest first)\n\n")
	for _, q := range answers {
		for _, item := range q.Questions {
			prompt := strings.TrimSpace(item.Question)
			if prompt == "" {
				continue
			}
			ans := strings.Join(q.Answers[item.ID], ", ")
			if strings.TrimSpace(ans) == "" {
				ans = "(no answer)"
			}
			fmt.Fprintf(b, "- Q: %s\n  A: %s\n", prompt, ans)
		}
	}
	b.WriteString("\n")
}

// runGoalSession creates an OriginGoal session for the lead agent, appends the
// start-of-session audit event, and runs one unattended turn. Goals run the
// whole chain in yolo mode (full autonomous access) — the point of a goal is to
// reach an outcome without the user in the loop — so every tool call is recorded
// on the goal timeline (EventToolUse → goal_events) as the audit counterweight.
func (c *Core) runGoalSession(ctx context.Context, goal store.Goal, kind store.GoalEventKind, prompt string) (store.Session, error) {
	phase := goalRateLimitPhase(kind)
	sess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName:      goal.LeadAgent,
		Origin:         store.OriginGoal,
		Provider:       goal.Provider,
		Profile:        goal.Profile,
		Model:          goal.Model,
		Effort:         goal.Effort,
		PermissionMode: config.PermissionYolo,
		GoalID:         goal.ID,
		ProjectID:      goal.ProjectID,
	})
	if err != nil {
		return store.Session{}, err
	}
	payload, _ := json.Marshal(map[string]string{"session_id": sess.ID})
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:    goal.ID,
		SessionID: sess.ID,
		Kind:      kind,
		Payload:   string(payload),
	}); err != nil {
		return sess, err
	}

	events, err := c.StreamTurn(ctx, sess.ID, prompt, TurnOptions{
		PermissionTurnID: sess.ID,
		Unattended:       true,
	})
	if err != nil {
		return sess, err
	}
	var turnErr string
	for event := range events {
		if event.Kind == "error" {
			turnErr = event.Content
		}
	}
	if turnErr != "" {
		if IsRateLimitErrorMessage(turnErr) {
			if _, err := c.ensureGoalRateLimitBlock(ctx, goal, sess, phase, turnErr); err != nil {
				c.log.Warn("goal rate-limit block failed", "event", "goal", "goal", goal.ID, "session", sess.ID, "err", err)
			}
		}
		return sess, &ScheduledRunError{Message: turnErr}
	}
	return sess, nil
}

func goalRateLimitPhase(kind store.GoalEventKind) store.GoalRateLimitPhase {
	if kind == store.GoalEventPlanningStarted {
		return store.GoalRateLimitPlanning
	}
	return store.GoalRateLimitReview
}

func (c *Core) ensureGoalRateLimitBlock(ctx context.Context, goal store.Goal, sess store.Session, phase store.GoalRateLimitPhase, message string) (store.GoalRateLimitBlock, error) {
	if existing, err := c.store.GetGoalRateLimitBlockBySession(ctx, sess.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.GoalRateLimitBlock{}, err
	}
	block, err := c.store.CreateGoalRateLimitBlock(ctx, store.GoalRateLimitBlock{
		GoalID:    goal.ID,
		SessionID: sess.ID,
		Phase:     phase,
		Provider:  sess.Provider,
		Profile:   sess.Profile,
		Model:     sess.Model,
		Effort:    sess.Effort,
		Error:     message,
		Status:    store.GoalRateLimitPending,
	})
	if err != nil {
		return store.GoalRateLimitBlock{}, err
	}
	payload, _ := json.Marshal(map[string]string{
		"block_id": block.ID,
		"phase":    string(block.Phase),
		"provider": string(block.Provider),
		"profile":  block.Profile,
		"model":    block.Model,
		"effort":   block.Effort,
		"error":    block.Error,
	})
	body := fmt.Sprintf("Rate limit reached on %s. Choose a model or provider to retry this goal.", targetLabel(block.Provider, block.Profile))
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:    goal.ID,
		SessionID: sess.ID,
		Kind:      store.GoalEventRateLimited,
		Body:      body,
		Payload:   string(payload),
	}); err != nil {
		return block, err
	}
	c.log.Info("goal rate-limit block created",
		"event", "goal",
		"goal", goal.ID,
		"session", sess.ID,
		"phase", string(phase),
		"provider", string(sess.Provider),
		"profile", sess.Profile,
	)
	return block, nil
}

// StartGoalPlanning runs the goal's initial decomposition session. Callers run
// it asynchronously — goal creation must not block on a model turn.
func (c *Core) StartGoalPlanning(ctx context.Context, goalID string) (store.Session, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.Session{}, err
	}
	feedback, err := c.store.ListGoalEventsByKind(ctx, goal.ID, store.GoalEventUserFeedback, goalFeedbackContextEvents)
	if err != nil {
		return store.Session{}, err
	}
	c.log.Info("goal planning started", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent)
	return c.runGoalSession(ctx, goal, store.GoalEventPlanningStarted, GoalPlanningPrompt(goal, feedback))
}

// goalReviewContextEvents caps how much timeline a review prompt replays.
const goalReviewContextEvents = 20

// goalFeedbackContextEvents caps how many user-authored feedback notes a goal
// session receives as durable guidance.
const goalFeedbackContextEvents = 20

// RunGoalReview runs one unattended review session for an active goal: assess,
// adjust, record, request, propose.
func (c *Core) RunGoalReview(ctx context.Context, goalID string) (store.Session, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.Session{}, err
	}
	if goal.Status != store.GoalActive {
		return store.Session{}, fmt.Errorf("goal %q is %s; only active goals are reviewed", goal.ID, goal.Status)
	}
	events, err := c.store.ListGoalContextEvents(ctx, goal.ID, goalReviewContextEvents)
	if err != nil {
		return store.Session{}, err
	}
	requests, err := c.store.ListAccessRequests(ctx, goal.ID, "")
	if err != nil {
		return store.Session{}, err
	}
	feedback, err := c.store.ListGoalEventsByKind(ctx, goal.ID, store.GoalEventUserFeedback, goalFeedbackContextEvents)
	if err != nil {
		return store.Session{}, err
	}
	answers, err := c.store.ListAnsweredAgentQuestions(ctx, store.AgentQuestionGoal, goal.ID, goalFeedbackContextEvents)
	if err != nil {
		return store.Session{}, err
	}
	c.log.Info("goal review started", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent)
	return c.runGoalSession(ctx, goal, store.GoalEventReviewStarted, GoalReviewPrompt(goal, events, requests, feedback, answers))
}

// AdvanceGoalReviewClock moves next_review_at one cadence forward from now.
// The scheduler calls this BEFORE running a review (§5 firing discipline).
func (c *Core) AdvanceGoalReviewClock(ctx context.Context, goalID string) error {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return err
	}
	every, err := parseReviewEvery(goal.ReviewEvery)
	if err != nil || every <= 0 {
		return c.store.SetGoalNextReview(ctx, goalID, "")
	}
	return c.store.SetGoalNextReview(ctx, goalID, nextReviewFrom(time.Now(), every))
}
