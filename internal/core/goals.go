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
	podiomtools "github.com/Podiom/Podiom/internal/tools"
)

// minReviewEvery is the cadence floor for automatic goal reviews (§5 of the
// goals spec): more frequent than this is a token drain with no human upside.
const minReviewEvery = 15 * time.Minute

// parseReviewEvery validates a goal review cadence. Empty is allowed and
// disables automatic reviews.
func parseReviewEvery(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("review_every %q: %w", raw, err)
	}
	if d < minReviewEvery {
		return 0, fmt.Errorf("review_every %q is below the %s floor", raw, minReviewEvery)
	}
	return d, nil
}

// nextReviewFrom computes the next review timestamp for a cadence, or "" when
// automatic reviews are disabled.
func nextReviewFrom(now time.Time, every time.Duration) string {
	if every <= 0 {
		return ""
	}
	return now.Add(every).UTC().Format(time.RFC3339)
}

// ListGoals returns goals, newest first, optionally filtered by status.
func (c *Core) ListGoals(ctx context.Context, status string) ([]store.Goal, error) {
	return c.store.ListGoals(ctx, status)
}

// GetGoal returns one goal.
func (c *Core) GetGoal(ctx context.Context, id string) (store.Goal, error) {
	return c.store.GetGoal(ctx, id)
}

// SumGoalUsage rolls up cumulative billed tokens across a goal's sessions,
// grouped by (provider, profile) so each group can be converted to a limit share.
func (c *Core) SumGoalUsage(ctx context.Context, goalID string) ([]store.GoalUsageGroup, error) {
	return c.store.SumGoalUsage(ctx, goalID)
}

// CreateGoal creates a goal, appends its `created` timeline event, and computes
// the first review time from the cadence. The caller kicks off planning
// separately (StartGoalPlanning) so goal creation itself stays fast.
func (c *Core) CreateGoal(ctx context.Context, goal store.Goal) (store.Goal, error) {
	if strings.TrimSpace(goal.Title) == "" {
		return store.Goal{}, fmt.Errorf("goal title is required")
	}
	if strings.TrimSpace(goal.LeadAgent) == "" {
		return store.Goal{}, fmt.Errorf("goal lead agent is required")
	}
	if _, err := c.store.GetAgent(ctx, goal.LeadAgent); err != nil {
		return store.Goal{}, fmt.Errorf("lead agent %q: %w", goal.LeadAgent, err)
	}
	if err := c.ValidateRunTargetForAgent(ctx, goal.LeadAgent, goalRunTarget(goal)); err != nil {
		return store.Goal{}, err
	}
	every, err := parseReviewEvery(goal.ReviewEvery)
	if err != nil {
		return store.Goal{}, err
	}
	if goal.ProjectID != "" {
		if _, err := c.ledger.Get(goal.ProjectID); err != nil {
			return store.Goal{}, err
		}
	}
	goal.Status = store.GoalActive
	goal.NextReviewAt = nextReviewFrom(time.Now(), every)
	goal.ClosingReport = ""

	created, err := c.store.CreateGoal(ctx, goal)
	if err != nil {
		return store.Goal{}, err
	}
	if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID: created.ID,
		Kind:   store.GoalEventCreated,
	}); err != nil {
		return store.Goal{}, err
	}
	c.log.Info("goal created",
		"event", "goal",
		"goal", created.ID,
		"agent", created.LeadAgent,
		"project", created.ProjectID,
		"cadence", created.ReviewEvery,
		"metrics", len(created.Metrics),
	)
	return created, nil
}

func goalRunTarget(goal store.Goal) RunTarget {
	return RunTarget{
		Provider: config.Provider(strings.TrimSpace(string(goal.Provider))),
		Profile:  strings.TrimSpace(goal.Profile),
		Model:    strings.TrimSpace(goal.Model),
		Effort:   strings.TrimSpace(goal.Effort),
	}
}

// goalProjectID returns the project a goal's delegated work inherits. It yields
// "" when the goal has no project or its project has since been deleted from the
// ledger: DeleteProject deliberately orphans rather than cascades, and
// CreateSession rejects an unknown project id, so a dangling reference has to
// degrade to "no project" instead of failing every run in the goal's chain.
func (c *Core) goalProjectID(goal store.Goal) string {
	projectID := strings.TrimSpace(goal.ProjectID)
	if projectID == "" {
		return ""
	}
	if _, err := c.ledger.Get(projectID); err != nil {
		return ""
	}
	return projectID
}

// GoalProjectID resolves a goal id to the project its delegated work inherits.
// Exported for the scheduler, which stamps it into a new schedule file. A goal
// that no longer exists yields "": schedule files and sessions.goal_id are
// deliberately free of foreign keys, and DeleteGoal leaves schedule files on
// disk, so an unresolvable goal must not be an error here.
func (c *Core) GoalProjectID(ctx context.Context, goalID string) string {
	if strings.TrimSpace(goalID) == "" {
		return ""
	}
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return ""
	}
	return c.goalProjectID(goal)
}

// GoalPatch carries partial goal updates. Nil fields are left untouched.
type GoalPatch struct {
	Title           *string
	Description     *string
	SuccessCriteria *string
	Metrics         *[]store.GoalMetric
	ReviewEvery     *string
	LeadAgent       *string
	ProjectID       *string
	Provider        *config.Provider
	Profile         *string
	Model           *string
	Effort          *string
	// FromAgent marks a patch stamped with a session identity (a manage-tool
	// call). Agents may adjust the goal's description, criteria, metrics
	// definitions, and cadence — but never its title, lead, or project (§9).
	FromAgent bool
}

// UpdateGoal applies a partial update. Status transitions go through
// TransitionGoal, never through here.
func (c *Core) UpdateGoal(ctx context.Context, id string, patch GoalPatch) (store.Goal, error) {
	goal, err := c.store.GetGoal(ctx, id)
	if err != nil {
		return store.Goal{}, err
	}
	if patch.FromAgent && (patch.Title != nil || patch.LeadAgent != nil || patch.ProjectID != nil) {
		return store.Goal{}, fmt.Errorf("agents may not change a goal's title, lead agent, or project")
	}
	if patch.Title != nil {
		if strings.TrimSpace(*patch.Title) == "" {
			return store.Goal{}, fmt.Errorf("goal title is required")
		}
		goal.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Description != nil {
		goal.Description = *patch.Description
	}
	if patch.SuccessCriteria != nil {
		goal.SuccessCriteria = *patch.SuccessCriteria
	}
	if patch.Metrics != nil {
		goal.Metrics = *patch.Metrics
	}
	previousLead := goal.LeadAgent
	if patch.LeadAgent != nil {
		if _, err := c.store.GetAgent(ctx, *patch.LeadAgent); err != nil {
			return store.Goal{}, fmt.Errorf("lead agent %q: %w", *patch.LeadAgent, err)
		}
		goal.LeadAgent = *patch.LeadAgent
		if goal.LeadAgent != previousLead {
			// Sessions are agent/workspace-bound. Preserve the old transcript as
			// history and lazily create one handoff conversation for the new lead.
			goal.LeadSessionID = ""
		}
	}
	if patch.ProjectID != nil {
		if *patch.ProjectID != "" {
			if _, err := c.ledger.Get(*patch.ProjectID); err != nil {
				return store.Goal{}, err
			}
		}
		goal.ProjectID = *patch.ProjectID
	}
	if patch.Provider != nil {
		goal.Provider = config.Provider(strings.TrimSpace(string(*patch.Provider)))
	}
	if patch.Profile != nil {
		goal.Profile = strings.TrimSpace(*patch.Profile)
	}
	if patch.Model != nil {
		goal.Model = strings.TrimSpace(*patch.Model)
	}
	if patch.Effort != nil {
		goal.Effort = strings.TrimSpace(*patch.Effort)
	}
	if patch.ReviewEvery != nil {
		every, err := parseReviewEvery(*patch.ReviewEvery)
		if err != nil {
			return store.Goal{}, err
		}
		goal.ReviewEvery = strings.TrimSpace(*patch.ReviewEvery)
		if goal.Status == store.GoalActive {
			goal.NextReviewAt = nextReviewFrom(time.Now(), every)
		}
	}
	if err := c.ValidateRunTargetForAgent(ctx, goal.LeadAgent, goalRunTarget(goal)); err != nil {
		return store.Goal{}, err
	}
	updated, err := c.store.UpdateGoal(ctx, goal)
	if err != nil {
		return store.Goal{}, err
	}
	c.log.Info("goal updated", "event", "goal", "goal", updated.ID, "agent", updated.LeadAgent, "from_agent", patch.FromAgent)
	return updated, nil
}

// goalTransitionAllowed encodes the §3.1 state machine. All transitions here
// are user-initiated; the agent's only transition is via ProposeGoalCompletion.
func goalTransitionAllowed(from, to store.GoalStatus) bool {
	switch to {
	case store.GoalActive: // resume / reopen
		return from == store.GoalPaused || from == store.GoalReview || from == store.GoalDone || from == store.GoalAbandoned
	case store.GoalPaused:
		return from == store.GoalActive || from == store.GoalReview
	case store.GoalDone:
		return from == store.GoalReview
	case store.GoalAbandoned:
		return from == store.GoalActive || from == store.GoalPaused || from == store.GoalReview
	default:
		return false // review is reachable only via ProposeGoalCompletion
	}
}

// TransitionGoal applies a user status change, enforcing the state machine,
// managing next_review_at, and appending the status_change audit event.
func (c *Core) TransitionGoal(ctx context.Context, id string, to store.GoalStatus, note string) (store.Goal, error) {
	goal, err := c.store.GetGoal(ctx, id)
	if err != nil {
		return store.Goal{}, err
	}
	from := goal.Status
	if from == to {
		return goal, nil
	}
	if !goalTransitionAllowed(from, to) {
		return store.Goal{}, fmt.Errorf("goal cannot go from %s to %s", from, to)
	}
	if from == store.GoalPaused && to == store.GoalActive {
		if memory, memoryErr := c.store.GetGoalMemoryForDisplay(ctx, goal.ID); memoryErr == nil && memory.Status == store.GoalMemoryBlocked {
			return store.Goal{}, fmt.Errorf("repair goal memory before resuming")
		}
	}
	goal.Status = to
	switch to {
	case store.GoalActive:
		every, err := parseReviewEvery(goal.ReviewEvery)
		if err != nil {
			every = 0
		}
		goal.NextReviewAt = nextReviewFrom(time.Now(), every)
		goal.ClosingReport = ""
	default:
		// Paused and terminal states suspend the review loop atomically.
		goal.NextReviewAt = ""
	}
	updated, err := c.store.UpdateGoal(ctx, goal)
	if err != nil {
		return store.Goal{}, err
	}
	// A finished goal has no next step. Pausing keeps it, so resuming shows the
	// same intent the user paused on.
	if to == store.GoalDone || to == store.GoalAbandoned {
		if err := c.store.SetGoalNextStep(ctx, updated.ID, "", "", ""); err != nil {
			return store.Goal{}, err
		}
		updated.NextStep, updated.NextStepWhy, updated.NextStepAt = "", "", ""
	}
	// The lead conversation follows the goal: a terminal goal files it away,
	// reopening brings it back. Pausing leaves it alone — a paused goal is one
	// the user still means to return to.
	switch to {
	case store.GoalDone, store.GoalAbandoned:
		c.setGoalLeadArchived(ctx, updated, true)
	case store.GoalActive:
		c.setGoalLeadArchived(ctx, updated, false)
	}
	payload, _ := json.Marshal(map[string]string{"from": string(from), "to": string(to)})
	if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID:  updated.ID,
		Kind:    store.GoalEventStatusChange,
		Body:    note,
		Payload: string(payload),
	}); err != nil {
		return store.Goal{}, err
	}
	c.log.Info("goal status changed", "event", "goal", "goal", updated.ID, "from", string(from), "to", string(to))
	return updated, nil
}

// setGoalLeadArchived files the goal's lead conversation away or brings it back.
// A goal without a lead session yet is a no-op, and the error is logged rather
// than returned: the status change itself has already been recorded.
func (c *Core) setGoalLeadArchived(ctx context.Context, goal store.Goal, archived bool) {
	if goal.LeadSessionID == "" {
		return
	}
	if _, err := c.SetSessionArchived(ctx, goal.LeadSessionID, archived); err != nil {
		c.log.Warn("archive goal lead session failed",
			"event", "goal", "goal", goal.ID, "session", goal.LeadSessionID, "archived", archived, "err", err)
	}
}

// DeleteGoal removes a goal, its timeline, and its access requests. Sessions
// the goal produced are preserved (the durable record of work done).
func (c *Core) DeleteGoal(ctx context.Context, id string) error {
	goal, err := c.store.GetGoal(ctx, id)
	if err != nil {
		return err
	}
	if err := c.store.DeleteGoal(ctx, id); err != nil {
		return err
	}
	// The lead conversation outlives the goal but has nothing left to lead, so
	// it would otherwise sit in the main list forever pointing at a goal that no
	// longer exists.
	c.setGoalLeadArchived(ctx, goal, true)
	c.log.Info("goal deleted", "event", "goal", "goal", goal.ID, "agent", goal.LeadAgent, "status", string(goal.Status))
	return nil
}

// ListGoalEvents pages through a goal's timeline (newest first).
func (c *Core) ListGoalEvents(ctx context.Context, goalID string, limit int, before int64) ([]store.GoalEvent, error) {
	if _, err := c.store.GetGoal(ctx, goalID); err != nil {
		return nil, err
	}
	return c.store.ListGoalEvents(ctx, goalID, limit, before)
}

// AddGoalFeedback records a user-authored note for the lead agent to consider
// during the next goal-origin planning or review run. It deliberately does not
// start a session, change status, or advance review timing.
func (c *Core) AddGoalFeedback(ctx context.Context, goalID, body string) (store.GoalEvent, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.GoalEvent{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return store.GoalEvent{}, fmt.Errorf("goal feedback body is required")
	}
	ev, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID: goal.ID,
		Kind:   store.GoalEventUserFeedback,
		Body:   body,
	})
	if err != nil {
		return store.GoalEvent{}, err
	}
	c.log.Info("goal feedback recorded", "event", "goal", "goal", goal.ID)
	return ev, nil
}

// UpdateGoalFeedback edits a user-authored goal feedback note. An ordinary note
// is editable until a later goal planning/review session has started and
// consumed it; a pinned one stays editable for the goal's whole life.
func (c *Core) UpdateGoalFeedback(ctx context.Context, goalID string, eventID int64, body string) (store.GoalEvent, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.GoalEvent{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return store.GoalEvent{}, fmt.Errorf("goal feedback body is required")
	}
	current, err := c.store.GetGoalEvent(ctx, eventID)
	if err != nil {
		return store.GoalEvent{}, err
	}
	if current.Pinned && len(body) > maxPinnedFeedbackChars {
		return store.GoalEvent{}, errPinnedFeedbackTooLong
	}
	ev, err := c.store.UpdateGoalFeedbackBody(ctx, goal.ID, eventID, body)
	if err != nil {
		return store.GoalEvent{}, err
	}
	c.log.Info("goal feedback updated", "event", "goal", "goal", goal.ID, "feedback", eventID)
	return ev, nil
}

// maxPinnedGoalFeedback bounds a goal's standing directives. Pinned notes are
// rendered in full into every run in the goal's chain, so the prompt cost has to
// be bounded somewhere; making it an error the user sees when pinning — rather
// than a LIMIT that quietly drops the oldest, as the recent-feedback window does
// — is the point. Nothing the agent is told to obey may vanish silently.
const maxPinnedGoalFeedback = 10

// maxPinnedFeedbackChars bounds one directive's length for the same reason.
// Ordinary feedback stays unbounded; only the untruncated path needs a ceiling.
const maxPinnedFeedbackChars = 2000

var errPinnedFeedbackTooLong = fmt.Errorf("a standing directive is limited to %d characters; shorten it or leave it as ordinary feedback", maxPinnedFeedbackChars)

// checkPinnable reports whether a note may be pinned as a standing directive.
// alreadyPinned exempts a re-pin from the count, since it adds nothing.
func (c *Core) checkPinnable(ctx context.Context, goalID, body string, alreadyPinned bool) error {
	if len(strings.TrimSpace(body)) > maxPinnedFeedbackChars {
		return errPinnedFeedbackTooLong
	}
	if alreadyPinned {
		return nil
	}
	existing, err := c.store.ListPinnedGoalFeedback(ctx, goalID)
	if err != nil {
		return err
	}
	if len(existing) >= maxPinnedGoalFeedback {
		return fmt.Errorf("this goal already has %d standing directives; unpin one first", maxPinnedGoalFeedback)
	}
	return nil
}

// ListGoalDirectives returns a goal's standing directives, oldest first — the
// same list, in the same order, its runs are given.
func (c *Core) ListGoalDirectives(ctx context.Context, goalID string) ([]store.GoalEvent, error) {
	if _, err := c.store.GetGoal(ctx, goalID); err != nil {
		return nil, err
	}
	return c.store.ListPinnedGoalFeedback(ctx, goalID)
}

// CheckNewGoalDirective validates the bounds before a note the user asked to pin
// is created at all. Without it a refused pin would leave the text behind as
// ordinary feedback while reporting an error, so retrying would duplicate it.
func (c *Core) CheckNewGoalDirective(ctx context.Context, goalID, body string) error {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return err
	}
	return c.checkPinnable(ctx, goal.ID, body, false)
}

// SetGoalFeedbackPin marks a feedback note as a standing directive, or retires
// one. Pinning is human-only for the same reason writing feedback is: a
// guardrail an agent can unpin is not a guardrail, so no MCP tool maps here.
func (c *Core) SetGoalFeedbackPin(ctx context.Context, goalID string, eventID int64, pinned bool) (store.GoalEvent, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.GoalEvent{}, err
	}
	if pinned {
		current, err := c.store.GetGoalEvent(ctx, eventID)
		if err != nil {
			return store.GoalEvent{}, err
		}
		// Re-pinning an already pinned note is a no-op, so it must not count
		// against the cap.
		if err := c.checkPinnable(ctx, goal.ID, current.Body, current.Pinned); err != nil {
			return store.GoalEvent{}, err
		}
	}
	ev, err := c.store.SetGoalFeedbackPin(ctx, goal.ID, eventID, pinned)
	if err != nil {
		return store.GoalEvent{}, err
	}
	c.log.Info("goal feedback pin changed", "event", "goal", "goal", goal.ID, "feedback", eventID, "pinned", pinned)
	return ev, nil
}

// ListGoalRateLimitBlocks returns a goal's durable rate-limit attention items.
func (c *Core) ListGoalRateLimitBlocks(ctx context.Context, goalID string) ([]store.GoalRateLimitBlock, error) {
	if _, err := c.store.GetGoal(ctx, goalID); err != nil {
		return nil, err
	}
	return c.store.ListGoalRateLimitBlocks(ctx, goalID)
}

// PendingGoalRateLimit returns the newest pending recovery item for a goal.
func (c *Core) PendingGoalRateLimit(ctx context.Context, goalID string) (*store.GoalRateLimitBlock, error) {
	block, err := c.store.PendingGoalRateLimit(ctx, goalID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &block, nil
}

// ResolveGoalRateLimitInput is the user's selected recovery target.
type ResolveGoalRateLimitInput struct {
	BlockID  string
	Provider config.Provider
	Profile  string
	Model    string
	Effort   string
}

// ResolveGoalRateLimit persists the chosen target on the goal, marks the block
// resolved, and appends an audit event. Retrying the phase is the server's job
// so it can broadcast the asynchronous result.
func (c *Core) ResolveGoalRateLimit(ctx context.Context, in ResolveGoalRateLimitInput) (store.GoalRateLimitBlock, store.Goal, error) {
	block, err := c.store.GetGoalRateLimitBlock(ctx, in.BlockID)
	if err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}
	if block.Status != store.GoalRateLimitPending {
		return store.GoalRateLimitBlock{}, store.Goal{}, fmt.Errorf("goal rate limit %q is already %s", block.ID, block.Status)
	}
	goal, err := c.store.GetGoal(ctx, block.GoalID)
	if err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}
	agent, err := c.store.GetAgent(ctx, goal.LeadAgent)
	if err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}
	target, err := c.resolveRunTarget(agent, RunTarget{
		Provider: config.Provider(strings.TrimSpace(string(in.Provider))),
		Profile:  strings.TrimSpace(in.Profile),
		Model:    strings.TrimSpace(in.Model),
		Effort:   strings.TrimSpace(in.Effort),
	})
	if err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}

	goal.Provider = target.Provider
	goal.Profile = target.Profile
	goal.Model = target.Model
	goal.Effort = target.Effort
	updated, err := c.store.UpdateGoal(ctx, goal)
	if err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}
	resolved, err := c.store.ResolveGoalRateLimitBlock(ctx, block.ID, target.Provider, target.Profile, target.Model, target.Effort)
	if err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}
	payload, _ := json.Marshal(map[string]string{
		"block_id": block.ID,
		"phase":    string(block.Phase),
		"provider": string(target.Provider),
		"profile":  target.Profile,
		"model":    target.Model,
		"effort":   target.Effort,
	})
	body := fmt.Sprintf("Retry target selected: %s (%s, %s).", targetLabel(target.Provider, target.Profile), target.Model, target.Effort)
	if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID:  updated.ID,
		Kind:    store.GoalEventRateLimitResolved,
		Body:    body,
		Payload: string(payload),
	}); err != nil {
		return store.GoalRateLimitBlock{}, store.Goal{}, err
	}
	c.log.Info("goal rate-limit block resolved",
		"event", "goal",
		"goal", updated.ID,
		"block", resolved.ID,
		"phase", string(resolved.Phase),
		"provider", string(target.Provider),
		"profile", target.Profile,
	)
	return resolved, updated, nil
}

// ReconcileGoalRateLimits backfills pending recovery items for old goal runs
// that already persisted a rate-limit error before durable recovery existed.
// It is idempotent because goal_rate_limits.run_id is unique.
func (c *Core) ReconcileGoalRateLimits(ctx context.Context) ([]store.GoalRateLimitBlock, error) {
	goals, err := c.store.ListGoals(ctx, "")
	if err != nil {
		return nil, err
	}
	sessions, err := c.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	activeGoals := make(map[string]store.Goal, len(goals))
	for _, goal := range goals {
		if goal.Status == store.GoalDone || goal.Status == store.GoalAbandoned {
			continue
		}
		activeGoals[goal.ID] = goal
	}

	type runPhase struct {
		phase store.GoalRateLimitPhase
		runID string
	}
	phaseBySession := map[string]runPhase{}
	for goalID := range activeGoals {
		events, err := c.store.ListGoalEvents(ctx, goalID, 0, 0)
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			switch ev.Kind {
			case store.GoalEventPlanningStarted:
				phaseBySession[ev.SessionID] = runPhase{phase: store.GoalRateLimitPlanning, runID: ev.RunID}
			case store.GoalEventReviewStarted:
				phaseBySession[ev.SessionID] = runPhase{phase: store.GoalRateLimitReview, runID: ev.RunID}
			}
		}
	}

	var created []store.GoalRateLimitBlock
	for _, sess := range sessions {
		goal, ok := activeGoals[sess.GoalID]
		if !ok || sess.Origin != store.OriginGoal {
			continue
		}
		if _, err := c.store.GetGoalRateLimitBlockBySession(ctx, sess.ID); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		history, err := c.store.ListMessages(ctx, sess.ID)
		if err != nil {
			return nil, err
		}
		for _, msg := range history {
			if msg.Kind != store.KindError || !IsRateLimitErrorMessage(msg.Content) {
				continue
			}
			info := phaseBySession[sess.ID]
			if info.phase == "" {
				info.phase = store.GoalRateLimitReview
			}
			if info.runID == "" {
				info.runID = "legacy-session:" + sess.ID
			}
			block, err := c.ensureGoalRateLimitBlock(ctx, goal, sess, info.runID, info.phase, msg.Content)
			if err != nil {
				return nil, err
			}
			created = append(created, block)
			break
		}
	}
	if len(created) > 0 {
		c.log.Info("goal rate-limit backfill finished", "event", "goal", "created", len(created))
	}
	return created, nil
}

// GoalMetricUpdate moves one named metric to a new current value.
type GoalMetricUpdate struct {
	Name    string  `json:"name"`
	Current float64 `json:"current"`
}

// RecordGoalProgressRequest is an agent-recorded timeline entry: a progress or
// plan_change note, optionally moving metrics and restating the next step.
type RecordGoalProgressRequest struct {
	GoalID        string
	SessionID     string
	Kind          store.GoalEventKind // progress (default) or plan_change
	Body          string
	MetricUpdates []GoalMetricUpdate
	// NextStep and NextStepWhy restate the agent's strategic intent. Empty leaves
	// the goal's current next step untouched, so a progress entry never has to
	// carry one and never silently erases one.
	NextStep    string
	NextStepWhy string
}

// RecordGoalProgress appends a progress/plan_change event and, when metrics
// moved, a metric_update event whose payload carries the old → new deltas —
// applied to the goal's metric projection in the same transaction. A stated next
// step is projected onto the goal and echoed in the event payload, so the
// history of intentions stays derivable from the timeline.
func (c *Core) RecordGoalProgress(ctx context.Context, req RecordGoalProgressRequest) ([]store.GoalEvent, error) {
	goal, err := c.store.GetGoal(ctx, req.GoalID)
	if err != nil {
		return nil, err
	}
	kind := req.Kind
	if kind == "" {
		kind = store.GoalEventProgress
	}
	if kind != store.GoalEventProgress && kind != store.GoalEventPlanChange {
		return nil, fmt.Errorf("progress kind must be progress or plan_change, got %q", kind)
	}
	if strings.TrimSpace(req.Body) == "" && len(req.MetricUpdates) == 0 {
		return nil, fmt.Errorf("a progress entry needs a body or metric updates")
	}
	runID := c.goalRunForAgentEvent(ctx, goal.ID, req.SessionID)
	nextStep := strings.TrimSpace(req.NextStep)
	nextStepWhy := strings.TrimSpace(req.NextStepWhy)

	var events []store.GoalEvent
	if strings.TrimSpace(req.Body) != "" {
		var payload string
		if nextStep != "" || nextStepWhy != "" {
			raw, _ := json.Marshal(map[string]string{"next_step": nextStep, "next_step_why": nextStepWhy})
			payload = string(raw)
		}
		ev, err := c.appendGoalEvent(ctx, store.GoalEvent{
			GoalID:    goal.ID,
			SessionID: req.SessionID,
			RunID:     runID,
			Kind:      kind,
			Body:      req.Body,
			Payload:   payload,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}

	if nextStep != "" || nextStepWhy != "" {
		if err := c.store.SetGoalNextStep(ctx, goal.ID, nextStep, nextStepWhy, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return nil, err
		}
	}

	if len(req.MetricUpdates) > 0 {
		metrics := append([]store.GoalMetric(nil), goal.Metrics...)
		type delta struct {
			Name string  `json:"name"`
			From float64 `json:"from"`
			To   float64 `json:"to"`
		}
		var deltas []delta
		for _, upd := range req.MetricUpdates {
			found := false
			for i := range metrics {
				if metrics[i].Name == upd.Name {
					deltas = append(deltas, delta{Name: upd.Name, From: metrics[i].Current, To: upd.Current})
					metrics[i].Current = upd.Current
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("goal has no metric named %q", upd.Name)
			}
		}
		payload, _ := json.Marshal(map[string]any{"updates": deltas})
		ev, err := c.appendGoalEventWithMetrics(ctx, store.GoalEvent{
			GoalID:    goal.ID,
			SessionID: req.SessionID,
			RunID:     runID,
			Kind:      store.GoalEventMetricUpdate,
			Payload:   string(payload),
		}, metrics)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

// ProposeGoalCompletion is the agent's claim that the success criteria are met:
// the goal enters review with the closing report attached, and only the user
// can take it to done (§3.1).
func (c *Core) ProposeGoalCompletion(ctx context.Context, goalID, sessionID, closingReport string) (store.Goal, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return store.Goal{}, err
	}
	if goal.Status != store.GoalActive {
		return store.Goal{}, fmt.Errorf("only an active goal can be proposed complete (status is %s)", goal.Status)
	}
	if strings.TrimSpace(closingReport) == "" {
		return store.Goal{}, fmt.Errorf("a closing report is required to propose completion")
	}
	if c.daemonAddr != "" {
		runID := c.goalRunForAgentEvent(ctx, goal.ID, sessionID)
		memory, memoryErr := c.store.GetGoalMemory(ctx, goal.ID)
		if memoryErr != nil || runID == "" || memory.LastRunID != runID {
			return store.Goal{}, fmt.Errorf("commit goal memory before proposing completion")
		}
	}
	goal.Status = store.GoalReview
	goal.ClosingReport = closingReport
	goal.NextReviewAt = ""
	updated, err := c.store.UpdateGoal(ctx, goal)
	if err != nil {
		return store.Goal{}, err
	}
	// Claiming the criteria are met is a claim that nothing is left to do.
	if err := c.store.SetGoalNextStep(ctx, updated.ID, "", "", ""); err != nil {
		return store.Goal{}, err
	}
	updated.NextStep, updated.NextStepWhy, updated.NextStepAt = "", "", ""
	if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID:    updated.ID,
		SessionID: sessionID,
		RunID:     c.goalRunForAgentEvent(ctx, updated.ID, sessionID),
		Kind:      store.GoalEventCompletionProposed,
		Body:      closingReport,
	}); err != nil {
		return store.Goal{}, err
	}
	c.log.Info("goal completion proposed", "event", "goal", "goal", updated.ID, "agent", updated.LeadAgent, "session", sessionID)
	return updated, nil
}

// --- access requests ---------------------------------------------------------

// accessPayloadRules names the required payload field per request kind and, for
// env_var, enforces the never-store-secrets rule (§6).
func validateAccessPayload(kind store.AccessRequestKind, payload map[string]string) error {
	need := func(key string) error {
		if strings.TrimSpace(payload[key]) == "" {
			return fmt.Errorf("%s request needs payload field %q", kind, key)
		}
		return nil
	}
	switch kind {
	case store.AccessMCPServer:
		return need("server_name")
	case store.AccessSkill:
		if strings.TrimSpace(payload["id"]) == "" && strings.TrimSpace(payload["url"]) == "" {
			return fmt.Errorf("skill request needs payload field \"id\" or \"url\"")
		}
		return nil
	case store.AccessCLITool:
		// A cli_tool request only ever names a tool the user must install
		// host-wide; agents install their own through the toolset. Validation
		// still runs through the toolset spec so an agent that sends the old
		// installer fields gets the same field rules rather than silence —
		// the fields themselves are inert on approval.
		return podiomtools.SpecFromPayload(payload).Validate()
	case store.AccessEnvVar:
		if err := need("var_name"); err != nil {
			return err
		}
		// Secrets never transit Podiom: the request names the variable, nothing
		// value-shaped is accepted.
		if _, ok := payload["value"]; ok {
			return fmt.Errorf("env_var requests must never carry the secret value — name the variable and its purpose only")
		}
		if strings.ContainsAny(payload["var_name"], "= \t") {
			return fmt.Errorf("env_var var_name must be a bare variable name")
		}
		return nil
	case store.AccessPermissionMode:
		mode := strings.TrimSpace(payload["mode"])
		if !config.KnownPermission(config.PermissionMode(mode)) {
			return fmt.Errorf("permission_mode request needs payload field %q of %s", "mode", config.PermissionModesLabel())
		}
		return nil
	default:
		return fmt.Errorf("unknown access request kind %q", kind)
	}
}

// CreateAccessRequestInput files a typed capability request from a goal session.
type CreateAccessRequestInput struct {
	GoalID    string
	AgentName string
	SessionID string
	Kind      store.AccessRequestKind
	Payload   map[string]string
	Reason    string
}

// CreateAccessRequest validates and files an access request, appending the
// access_requested audit event. Notifying the user is the server's job.
func (c *Core) CreateAccessRequest(ctx context.Context, in CreateAccessRequestInput) (store.AccessRequest, error) {
	goal, err := c.store.GetGoal(ctx, in.GoalID)
	if err != nil {
		return store.AccessRequest{}, err
	}
	if in.Payload == nil {
		in.Payload = map[string]string{}
	}
	if err := validateAccessPayload(in.Kind, in.Payload); err != nil {
		return store.AccessRequest{}, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return store.AccessRequest{}, fmt.Errorf("an access request needs a reason the user can act on")
	}
	agent := strings.TrimSpace(in.AgentName)
	if agent == "" {
		agent = goal.LeadAgent
	}
	payload, err := json.Marshal(in.Payload)
	if err != nil {
		return store.AccessRequest{}, fmt.Errorf("marshal access request payload: %w", err)
	}
	req, err := c.store.CreateAccessRequest(ctx, store.AccessRequest{
		GoalID:    goal.ID,
		AgentName: agent,
		SessionID: in.SessionID,
		Kind:      in.Kind,
		Payload:   string(payload),
		Reason:    in.Reason,
	})
	if err != nil {
		return store.AccessRequest{}, err
	}
	evPayload, _ := json.Marshal(map[string]string{"request_id": req.ID, "kind": string(req.Kind)})
	if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID:    goal.ID,
		SessionID: in.SessionID,
		RunID:     c.goalRunForAgentEvent(ctx, goal.ID, in.SessionID),
		Kind:      store.GoalEventAccessRequested,
		Body:      req.Reason,
		Payload:   string(evPayload),
	}); err != nil {
		return store.AccessRequest{}, err
	}
	c.log.Info("access request filed",
		"event", "goal",
		"goal", goal.ID,
		"request", req.ID,
		"kind", string(req.Kind),
		"agent", req.AgentName,
		"session", req.SessionID,
	)
	return req, nil
}

// ListAccessRequests returns access requests, optionally filtered.
func (c *Core) ListAccessRequests(ctx context.Context, goalID, status string) ([]store.AccessRequest, error) {
	return c.store.ListAccessRequests(ctx, goalID, status)
}

// GetAccessRequest returns one access request.
func (c *Core) GetAccessRequest(ctx context.Context, id string) (store.AccessRequest, error) {
	return c.store.GetAccessRequest(ctx, id)
}

// DecideAccessRequest records the user's approve/deny plus the note relayed to
// the agent, and appends the access_decided audit event. Grant execution for
// automatable kinds happens above this call (server layer) and is reported via
// MarkAccessRequestExecuted.
func (c *Core) DecideAccessRequest(ctx context.Context, id string, approve bool, note string) (store.AccessRequest, error) {
	status := store.AccessDenied
	if approve {
		status = store.AccessApproved
	}
	req, err := c.store.DecideAccessRequest(ctx, id, status, note)
	if err != nil {
		return store.AccessRequest{}, err
	}
	body := "Denied — " + string(req.Kind)
	if approve {
		body = "Approved — " + string(req.Kind)
	}
	if strings.TrimSpace(note) != "" {
		body += "\n\nNote to agent: " + note
	}
	payload, _ := json.Marshal(map[string]string{"request_id": req.ID, "kind": string(req.Kind), "status": string(req.Status)})
	if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
		GoalID:  req.GoalID,
		Kind:    store.GoalEventAccessDecided,
		Body:    body,
		Payload: string(payload),
	}); err != nil {
		return store.AccessRequest{}, err
	}
	c.log.Info("access request decided",
		"event", "goal",
		"goal", req.GoalID,
		"request", req.ID,
		"kind", string(req.Kind),
		"decision", string(req.Status),
		"note", strings.TrimSpace(note) != "",
	)
	return req, nil
}

// MarkAccessRequestExecuted records a grant execution outcome (server layer
// runs the grant) and folds the result into the audit trail. evidence is an
// optional human line for the timeline on success (e.g. the installed tool's
// version) — async grants use it so their outcome is auditable.
func (c *Core) MarkAccessRequestExecuted(ctx context.Context, id, execErr, evidence string) (store.AccessRequest, error) {
	req, err := c.store.MarkAccessRequestExecuted(ctx, id, execErr)
	if err != nil {
		return store.AccessRequest{}, err
	}
	if execErr != "" {
		payload, _ := json.Marshal(map[string]string{"request_id": req.ID, "kind": string(req.Kind), "status": string(req.Status), "error": execErr})
		if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
			GoalID:  req.GoalID,
			Kind:    store.GoalEventAccessDecided,
			Body:    "Grant failed — " + string(req.Kind) + ": " + execErr,
			Payload: string(payload),
		}); err != nil {
			return store.AccessRequest{}, err
		}
	} else if strings.TrimSpace(evidence) != "" {
		payload, _ := json.Marshal(map[string]string{"request_id": req.ID, "kind": string(req.Kind), "status": string(req.Status)})
		if _, err := c.appendGoalEvent(ctx, store.GoalEvent{
			GoalID:  req.GoalID,
			Kind:    store.GoalEventAccessDecided,
			Body:    evidence,
			Payload: string(payload),
		}); err != nil {
			return store.AccessRequest{}, err
		}
	}
	c.log.Info("access request executed", "event", "goal", "goal", req.GoalID, "request", req.ID, "kind", string(req.Kind), "status", string(req.Status), "error", execErr)
	return req, nil
}
