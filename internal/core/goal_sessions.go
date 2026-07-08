package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/store"
)

// goalAllowedTools is the preapproved allow-list for unattended goal sessions
// (§4 of the goals spec): the Podiom self-management MCP server (server-level
// rule covers every podiom_* tool; each destructive tool keeps its own confirm
// guard) plus read-only inspection. Deliberately no Bash/Edit/Write — real
// work happens in the tasks and schedules the agent spawns, not in the
// planning/review session itself.
var goalAllowedTools = []string{
	"mcp__podiom_manage",
	"Read", "Grep", "Glob", "LS",
	"WebFetch", "WebSearch",
}

// GoalPlanningPrompt renders the decomposition contract for a goal's initial
// planning session.
func GoalPlanningPrompt(goal store.Goal) string {
	var b strings.Builder
	b.WriteString("You are the lead agent for a new Podiom goal. Plan how to reach it.\n\n")
	writeGoalBrief(&b, goal)
	b.WriteString(`## Your job right now (planning session)

1. Decompose the goal into concrete work:
   - Create roadmap tasks with podiom_create_task (assign other agents where sensible; you stay accountable).
   - Create recurring schedules with podiom_create_schedule for work that must repeat, passing goal_id (this goal's ID, above) so it shows up linked to this goal.
2. Record your plan with podiom_record_goal_progress (kind "plan_change"): what you created and why it reaches the goal.
3. If you are missing a capability (an MCP server, a skill, a host CLI tool, a credential, or a permission level), file podiom_request_access with a reason the user can act on. Do not work around a missing capability silently.
4. Do not attempt the work itself in this session — this session only plans and requests.

The user is away. They will see your plan, your access requests, and this goal's timeline when they return.
`)
	return b.String()
}

// GoalReviewPrompt renders the periodic review contract: recent timeline and
// access-request decisions (including the user's notes — their channel back to
// the agent) plus the review duties.
func GoalReviewPrompt(goal store.Goal, events []store.GoalEvent, requests []store.AccessRequest) string {
	var b strings.Builder
	b.WriteString("You are the lead agent for a Podiom goal. This is a scheduled review session.\n\n")
	writeGoalBrief(&b, goal)

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

	b.WriteString(`## Your job right now (review session)

1. Assess progress against the success criteria. Check the state of the tasks and schedules you created (podiom_list_tasks, podiom_list_schedules) and adjust them where the plan has drifted.
2. Record a progress entry with podiom_record_goal_progress: what moved since the last review, with evidence. Update metric values there when they changed.
3. If you are blocked on a missing capability, file podiom_request_access. If the user answered a previous request (see above), act on their note.
4. If — and only if — every success criterion is met, call podiom_propose_goal_completion with a closing report that walks through each criterion. The user makes the final call.
5. Keep this session focused: review, adjust, record. The spawned tasks and schedules do the heavy lifting.
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

// runGoalSession creates an OriginGoal session for the lead agent, appends the
// start-of-session audit event, and runs one unattended turn under the
// preapproved posture (RunScheduled precedent — never yolo unless the agent
// itself is yolo).
func (c *Core) runGoalSession(ctx context.Context, goal store.Goal, kind store.GoalEventKind, prompt string) (store.Session, error) {
	sess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: goal.LeadAgent,
		Origin:    store.OriginGoal,
		GoalID:    goal.ID,
		ProjectID: goal.ProjectID,
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
		PermissionRelay:  NewAllowListRelay(goalAllowedTools, c.log),
		Unattended:       true,
		AllowedTools:     goalAllowedTools,
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
		return sess, &ScheduledRunError{Message: turnErr}
	}
	return sess, nil
}

// StartGoalPlanning runs the goal's initial decomposition session. Callers run
// it asynchronously — goal creation must not block on a model turn.
func (c *Core) StartGoalPlanning(ctx context.Context, goalID string) (store.Session, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.Session{}, err
	}
	c.log.Info("goal planning started", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent)
	return c.runGoalSession(ctx, goal, store.GoalEventPlanningStarted, GoalPlanningPrompt(goal))
}

// goalReviewContextEvents caps how much timeline a review prompt replays.
const goalReviewContextEvents = 20

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
	events, err := c.store.ListGoalEvents(ctx, goal.ID, goalReviewContextEvents, 0)
	if err != nil {
		return store.Session{}, err
	}
	requests, err := c.store.ListAccessRequests(ctx, goal.ID, "")
	if err != nil {
		return store.Session{}, err
	}
	c.log.Info("goal review started", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent)
	return c.runGoalSession(ctx, goal, store.GoalEventReviewStarted, GoalReviewPrompt(goal, events, requests))
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
