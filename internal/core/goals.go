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
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
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
	if patch.LeadAgent != nil {
		if _, err := c.store.GetAgent(ctx, *patch.LeadAgent); err != nil {
			return store.Goal{}, fmt.Errorf("lead agent %q: %w", *patch.LeadAgent, err)
		}
		goal.LeadAgent = *patch.LeadAgent
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
	payload, _ := json.Marshal(map[string]string{"from": string(from), "to": string(to)})
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
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
	ev, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
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
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
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
// It is idempotent because goal_rate_limits.session_id is unique.
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

	phaseBySession := map[string]store.GoalRateLimitPhase{}
	for goalID := range activeGoals {
		events, err := c.store.ListGoalEvents(ctx, goalID, 0, 0)
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			switch ev.Kind {
			case store.GoalEventPlanningStarted:
				phaseBySession[ev.SessionID] = store.GoalRateLimitPlanning
			case store.GoalEventReviewStarted:
				phaseBySession[ev.SessionID] = store.GoalRateLimitReview
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
			phase := phaseBySession[sess.ID]
			if phase == "" {
				phase = store.GoalRateLimitReview
			}
			block, err := c.ensureGoalRateLimitBlock(ctx, goal, sess, phase, msg.Content)
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
// plan_change note, optionally moving metrics.
type RecordGoalProgressRequest struct {
	GoalID        string
	SessionID     string
	Kind          store.GoalEventKind // progress (default) or plan_change
	Body          string
	MetricUpdates []GoalMetricUpdate
}

// RecordGoalProgress appends a progress/plan_change event and, when metrics
// moved, a metric_update event whose payload carries the old → new deltas —
// applied to the goal's metric projection in the same transaction.
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

	var events []store.GoalEvent
	if strings.TrimSpace(req.Body) != "" {
		ev, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
			GoalID:    goal.ID,
			SessionID: req.SessionID,
			Kind:      kind,
			Body:      req.Body,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
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
		ev, err := c.store.AppendGoalEventWithMetrics(ctx, store.GoalEvent{
			GoalID:    goal.ID,
			SessionID: req.SessionID,
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
	goal.Status = store.GoalReview
	goal.ClosingReport = closingReport
	goal.NextReviewAt = ""
	updated, err := c.store.UpdateGoal(ctx, goal)
	if err != nil {
		return store.Goal{}, err
	}
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:    updated.ID,
		SessionID: sessionID,
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
		// Installable requests (installer field present) carry a declarative
		// spec that the workspace-tool installer validates; host-only requests
		// just need the tool name (workspace-tool-installs spec §3).
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
		if mode != "approve" && mode != "yolo" {
			return fmt.Errorf("permission_mode request needs payload field \"mode\" of approve or yolo")
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
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
		GoalID:    goal.ID,
		SessionID: in.SessionID,
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
	if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
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
		if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
			GoalID:  req.GoalID,
			Kind:    store.GoalEventAccessDecided,
			Body:    "Grant failed — " + string(req.Kind) + ": " + execErr,
			Payload: string(payload),
		}); err != nil {
			return store.AccessRequest{}, err
		}
	} else if strings.TrimSpace(evidence) != "" {
		payload, _ := json.Marshal(map[string]string{"request_id": req.ID, "kind": string(req.Kind), "status": string(req.Status)})
		if _, err := c.store.AppendGoalEvent(ctx, store.GoalEvent{
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
