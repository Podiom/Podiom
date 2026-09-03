package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Podiom/Podiom/internal/store"
)

// GoalStatelessReviewPrompt is a complete, bounded review packet. It contains
// durable state and unread human input instead of conversational history.
func GoalStatelessReviewPrompt(goal store.Goal, memory store.GoalMemory, directives, feedback []store.GoalEvent,
	requests []store.AccessRequest, answers []store.AgentQuestion, actions GoalActionItems, tasks []store.Task, schedules []string) string {
	var b strings.Builder
	b.WriteString("You are the lead agent for a Podiom goal. This is a fresh, stateless review. Everything you may rely on is in this packet; do not assume access to earlier conversation.\n\n")
	writeGoalBrief(&b, goal)
	b.WriteString("## Verified working memory\n\n")
	fmt.Fprintf(&b, "- Revision: %d\n- Current state: %s\n", memory.Revision, strings.TrimSpace(memory.Document.CurrentState))
	if len(memory.Document.ActivePlan) > 0 {
		b.WriteString("- Active plan:\n")
		for _, step := range memory.Document.ActivePlan {
			fmt.Fprintf(&b, "  - %s\n", step)
		}
	}
	for _, kind := range []store.GoalMemoryItemKind{store.GoalMemoryMilestone, store.GoalMemoryDecision, store.GoalMemoryRejected, store.GoalMemoryRisk, store.GoalMemoryArtifact} {
		wrote := false
		for _, item := range memory.Document.Items {
			if item.Kind != kind || item.Retired {
				continue
			}
			if !wrote {
				fmt.Fprintf(&b, "- Active %ss:\n", kind)
				wrote = true
			}
			fmt.Fprintf(&b, "  - [%s] %s", item.ID, item.Title)
			for _, extra := range []string{item.Detail, item.Rationale, item.Evidence, item.URL} {
				if strings.TrimSpace(extra) != "" {
					fmt.Fprintf(&b, " — %s", strings.TrimSpace(extra))
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	writeGoalDirectives(&b, directives)
	if len(feedback) > 0 {
		b.WriteString("## Pending user feedback\n\nThese notes must remain pending unless this review incorporates them into memory. Use their event IDs in the memory commit.\n\n")
		for _, event := range feedback {
			fmt.Fprintf(&b, "- Feedback %d (%s): %s\n", event.ID, event.CreatedAt, strings.TrimSpace(event.Body))
		}
		b.WriteString("\n")
	}
	writeAnsweredQuestions(&b, answers)
	writeGoalActionItems(&b, actions)
	if len(requests) > 0 {
		b.WriteString("## Access requests\n\n")
		for _, request := range requests {
			fmt.Fprintf(&b, "- [%s] %s: %s", request.Status, request.Kind, strings.TrimSpace(request.Reason))
			if note := strings.TrimSpace(request.DecisionNote); note != "" {
				fmt.Fprintf(&b, " — User note: %s", note)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(tasks) > 0 {
		b.WriteString("## Current goal tasks\n\n")
		for _, task := range tasks {
			fmt.Fprintf(&b, "- [%s] %s (%s)\n", task.Status, strings.TrimSpace(task.Title), task.ID)
		}
		b.WriteString("\n")
	}
	if len(schedules) > 0 {
		b.WriteString("## Current goal schedules\n\n")
		for _, schedule := range schedules {
			fmt.Fprintf(&b, "- %s\n", schedule)
		}
		b.WriteString("\n")
	}
	b.WriteString(`## Review contract

1. Assess progress against every success criterion and act on drift. Follow standing directives exactly.
2. Incorporate clear pending feedback. Leave conflicting or unclear notes pending and ask the user a concise question.
3. Update tasks, schedules, metrics, and the next step where needed. Record meaningful progress with evidence.
4. Before this review can succeed, call podiom_commit_goal_memory exactly once using the revision above. Preserve facts that remain true, explicitly retire obsolete items with a reason, and provide a short outcome for the user.
5. For incorporated or completed feedback, include a disposition pointing at the durable memory items that retain its effect. Superseded feedback must point at a newer feedback event.
6. Only after committing memory, propose completion if every success criterion is met.
7. Keep user-facing text short. Put raw commands and diagnostics in technical output, not memory.
`)
	return b.String()
}

const (
	maxGoalMemoryBytes   = 32 * 1024
	maxGoalPacketBytes   = 64 * 1024
	maxMemoryStateChars  = 240
	maxMemoryPlanChars   = 180
	maxMemoryTitleChars  = 120
	maxMemoryDetailChars = 240
	maxRunOutcomeChars   = 240
)

type GoalMemoryUpsert struct {
	ID        string                   `json:"id"`
	Kind      store.GoalMemoryItemKind `json:"kind"`
	Title     string                   `json:"title"`
	Detail    string                   `json:"detail,omitempty"`
	Rationale string                   `json:"rationale,omitempty"`
	Evidence  string                   `json:"evidence,omitempty"`
	URL       string                   `json:"url,omitempty"`
}

type GoalMemoryRetirement struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type GoalFeedbackDispositionInput struct {
	EventID       int64                         `json:"event_id"`
	Disposition   store.GoalFeedbackDisposition `json:"disposition"`
	MemoryItemIDs []string                      `json:"memory_item_ids,omitempty"`
	SupersededBy  int64                         `json:"superseded_by,omitempty"`
}

type CommitGoalMemoryInput struct {
	GoalID               string                         `json:"id"`
	SessionID            string                         `json:"session_id"`
	BaseRevision         int64                          `json:"base_revision"`
	CurrentState         *string                        `json:"current_state,omitempty"`
	ActivePlan           *[]string                      `json:"active_plan,omitempty"`
	Upserts              []GoalMemoryUpsert             `json:"upserts,omitempty"`
	Retirements          []GoalMemoryRetirement         `json:"retirements,omitempty"`
	FeedbackDispositions []GoalFeedbackDispositionInput `json:"feedback_dispositions,omitempty"`
	Outcome              string                         `json:"outcome"`
}

type GoalMemoryRepairResult struct {
	Memory       store.GoalMemory `json:"memory"`
	RunsRead     int              `json:"runs_read"`
	FeedbackRead int              `json:"feedback_read"`
	Added        int              `json:"added"`
	Changed      int              `json:"changed"`
	Retired      int              `json:"retired"`
}

func (c *Core) GetGoalMemory(ctx context.Context, goalID string) (store.GoalMemory, error) {
	return c.store.GetGoalMemoryForDisplay(ctx, goalID)
}

func (c *Core) ListGoalFeedbackReceipts(ctx context.Context, goalID string) ([]store.GoalFeedbackReceipt, error) {
	return c.store.ListGoalFeedbackReceipts(ctx, goalID)
}

func (c *Core) blockGoalMemory(ctx context.Context, goalID, reason, detail string) error {
	if err := c.store.BlockGoalMemory(ctx, goalID, reason, shortText(detail, maxMemoryDetailChars)); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"status": "paused", "reason": "memory_" + reason})
	_, err := c.appendGoalEvent(ctx, store.GoalEvent{GoalID: goalID, Kind: store.GoalEventStatusChange,
		Body: "Goal paused to protect its memory.", Payload: string(payload)})
	return err
}

func validMemoryKind(kind store.GoalMemoryItemKind) bool {
	switch kind {
	case store.GoalMemoryMilestone, store.GoalMemoryDecision, store.GoalMemoryRejected, store.GoalMemoryRisk, store.GoalMemoryArtifact:
		return true
	default:
		return false
	}
}

func checkText(label, value string, limit int, required bool) error {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if utf8.RuneCountInString(value) > limit {
		return fmt.Errorf("%s is limited to %d characters", label, limit)
	}
	return nil
}

// CommitGoalMemory merges a typed patch into the current revision. The running
// goal session supplies provenance; models cannot claim another source run.
func (c *Core) CommitGoalMemory(ctx context.Context, input CommitGoalMemoryInput) (store.GoalMemory, error) {
	goal, err := c.store.GetGoal(ctx, strings.TrimSpace(input.GoalID))
	if err != nil {
		return store.GoalMemory{}, err
	}
	sess, err := c.store.GetSession(ctx, strings.TrimSpace(input.SessionID))
	if err != nil || sess.Origin != store.OriginGoal || sess.GoalID != goal.ID {
		return store.GoalMemory{}, fmt.Errorf("memory can only be committed by this goal's lead run")
	}
	run, err := c.store.GetRunningGoalRunBySession(ctx, sess.ID)
	if err != nil || run.GoalID != goal.ID || (run.Kind != store.GoalRunPlanning && run.Kind != store.GoalRunReview) {
		return store.GoalMemory{}, fmt.Errorf("memory can only be committed during planning or review")
	}
	if err := checkText("outcome", input.Outcome, maxRunOutcomeChars, true); err != nil {
		return store.GoalMemory{}, err
	}
	memory, err := c.store.GetGoalMemory(ctx, goal.ID)
	if err != nil {
		return store.GoalMemory{}, err
	}
	if memory.Revision != input.BaseRevision {
		return store.GoalMemory{}, fmt.Errorf("goal memory changed: base revision %d, current revision %d", input.BaseRevision, memory.Revision)
	}
	doc := memory.Document
	if input.CurrentState != nil {
		if err := checkText("current_state", *input.CurrentState, maxMemoryStateChars, true); err != nil {
			return store.GoalMemory{}, err
		}
		doc.CurrentState = strings.TrimSpace(*input.CurrentState)
	}
	if input.ActivePlan != nil {
		if len(*input.ActivePlan) > 20 {
			return store.GoalMemory{}, fmt.Errorf("active_plan is limited to 20 items")
		}
		plan := make([]string, 0, len(*input.ActivePlan))
		for _, step := range *input.ActivePlan {
			if err := checkText("active plan item", step, maxMemoryPlanChars, true); err != nil {
				return store.GoalMemory{}, err
			}
			plan = append(plan, strings.TrimSpace(step))
		}
		doc.ActivePlan = plan
	}
	items := make(map[string]store.GoalMemoryItem, len(doc.Items))
	order := make([]string, 0, len(doc.Items)+len(input.Upserts))
	for _, item := range doc.Items {
		items[item.ID] = item
		order = append(order, item.ID)
	}
	for _, upsert := range input.Upserts {
		upsert.ID = strings.TrimSpace(upsert.ID)
		if err := checkText("memory item id", upsert.ID, 80, true); err != nil {
			return store.GoalMemory{}, err
		}
		if !validMemoryKind(upsert.Kind) {
			return store.GoalMemory{}, fmt.Errorf("invalid memory item kind %q", upsert.Kind)
		}
		if err := checkText("memory item title", upsert.Title, maxMemoryTitleChars, true); err != nil {
			return store.GoalMemory{}, err
		}
		for label, value := range map[string]string{"detail": upsert.Detail, "rationale": upsert.Rationale, "evidence": upsert.Evidence} {
			if err := checkText(label, value, maxMemoryDetailChars, false); err != nil {
				return store.GoalMemory{}, err
			}
		}
		if _, exists := items[upsert.ID]; !exists {
			order = append(order, upsert.ID)
		}
		items[upsert.ID] = store.GoalMemoryItem{ID: upsert.ID, Kind: upsert.Kind,
			Title: strings.TrimSpace(upsert.Title), Detail: strings.TrimSpace(upsert.Detail),
			Rationale: strings.TrimSpace(upsert.Rationale), Evidence: strings.TrimSpace(upsert.Evidence),
			URL: strings.TrimSpace(upsert.URL), SourceRunID: run.ID}
	}
	for _, retirement := range input.Retirements {
		item, ok := items[strings.TrimSpace(retirement.ID)]
		if !ok {
			return store.GoalMemory{}, fmt.Errorf("cannot retire unknown memory item %q", retirement.ID)
		}
		if err := checkText("retirement reason", retirement.Reason, maxMemoryDetailChars, true); err != nil {
			return store.GoalMemory{}, err
		}
		item.Retired = true
		item.RetirementReason = strings.TrimSpace(retirement.Reason)
		item.SourceRunID = run.ID
		items[item.ID] = item
	}
	doc.Items = doc.Items[:0]
	for _, id := range order {
		doc.Items = append(doc.Items, items[id])
	}
	if err := checkText("current_state", doc.CurrentState, maxMemoryStateChars, true); err != nil {
		return store.GoalMemory{}, err
	}
	known := make(map[string]bool, len(doc.Items))
	for _, item := range doc.Items {
		known[item.ID] = true
	}
	dispositions := make([]store.GoalFeedbackDispositionInput, 0, len(input.FeedbackDispositions))
	for _, disposition := range input.FeedbackDispositions {
		switch disposition.Disposition {
		case store.GoalFeedbackIncorporated, store.GoalFeedbackCompleted:
			if len(disposition.MemoryItemIDs) == 0 {
				return store.GoalMemory{}, fmt.Errorf("feedback %d needs memory_item_ids", disposition.EventID)
			}
			for _, id := range disposition.MemoryItemIDs {
				if !known[id] {
					return store.GoalMemory{}, fmt.Errorf("feedback %d references unknown memory item %q", disposition.EventID, id)
				}
			}
		case store.GoalFeedbackSuperseded:
			if disposition.SupersededBy <= disposition.EventID {
				return store.GoalMemory{}, fmt.Errorf("feedback %d needs a newer superseding feedback id", disposition.EventID)
			}
		default:
			return store.GoalMemory{}, fmt.Errorf("invalid feedback disposition %q", disposition.Disposition)
		}
		dispositions = append(dispositions, store.GoalFeedbackDispositionInput(disposition))
	}
	raw, _ := json.Marshal(doc)
	if len(raw) > maxGoalMemoryBytes {
		return store.GoalMemory{}, fmt.Errorf("goal memory is %d bytes; limit is %d", len(raw), maxGoalMemoryBytes)
	}
	return c.store.CommitGoalMemory(ctx, goal.ID, input.BaseRevision, run.ID,
		strings.TrimSpace(input.Outcome), doc, dispositions, false)
}

// RepairGoalMemory safely rebuilds a conservative snapshot from durable Podiom
// data, then asks the lead model to validate the bounded draft in a fresh,
// tool-denied context. Project, shell, network, and external-service tools are
// unavailable during validation, and the old memory remains active until the
// final transaction succeeds.
func (c *Core) RepairGoalMemory(ctx context.Context, goalID string) (GoalMemoryRepairResult, error) {
	goal, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return GoalMemoryRepairResult{}, err
	}
	displayed, err := c.store.GetGoalMemoryForDisplay(ctx, goal.ID)
	if err != nil {
		return GoalMemoryRepairResult{}, err
	}
	if displayed.Status != store.GoalMemoryBlocked {
		return GoalMemoryRepairResult{}, fmt.Errorf("goal memory does not need repair")
	}
	if goal.Status != store.GoalPaused {
		if err := c.blockGoalMemory(ctx, goal.ID, displayed.BlockReason, displayed.BlockDetail); err != nil {
			return GoalMemoryRepairResult{}, err
		}
		goal.Status = store.GoalPaused
		goal.NextReviewAt = ""
	}
	events, err := c.store.ListGoalContextEvents(ctx, goal.ID, 0)
	if err != nil {
		return GoalMemoryRepairResult{}, err
	}
	feedback, err := c.store.ListPendingGoalFeedback(ctx, goal.ID)
	if err != nil {
		return GoalMemoryRepairResult{}, err
	}
	doc := store.GoalMemoryDocument{CurrentState: "Memory rebuilt from Podiom's saved goal history. The next review will verify and update it."}
	if existing, existingErr := c.store.GetGoalMemory(ctx, goal.ID); existingErr == nil {
		doc = existing.Document
		if strings.TrimSpace(doc.CurrentState) == "" {
			doc.CurrentState = "Memory rebuilt from Podiom's saved goal history. The next review will verify and update it."
		}
	}
	originalItems := len(doc.Items)
	originalPlan := len(doc.ActivePlan)
	knownItems := make(map[string]bool, len(doc.Items))
	for _, item := range doc.Items {
		knownItems[item.ID] = true
	}
	appendPlan := func(step string) {
		step = shortText(step, maxMemoryPlanChars)
		if step == "" || len(doc.ActivePlan) >= 20 {
			return
		}
		for _, existing := range doc.ActivePlan {
			if existing == step {
				return
			}
		}
		doc.ActivePlan = append(doc.ActivePlan, step)
	}
	if tasks, taskErr := c.store.ListTasks(ctx); taskErr == nil {
		for _, task := range tasks {
			if task.GoalID == goal.ID && task.Status != store.TaskDone {
				appendPlan(task.Title)
			}
		}
	}
	for _, schedule := range c.goalScheduleSummaries(goal.ID) {
		appendPlan("Run schedule " + schedule)
	}
	seenRuns := map[string]bool{}
	for _, event := range events {
		if event.RunID != "" {
			seenRuns[event.RunID] = true
		}
		body := shortText(event.Body, maxMemoryDetailChars)
		if body == "" {
			continue
		}
		switch event.Kind {
		case store.GoalEventProgress:
			id := fmt.Sprintf("event-%d", event.ID)
			if len(doc.Items) < originalItems+40 && !knownItems[id] {
				doc.Items = append(doc.Items, store.GoalMemoryItem{ID: id,
					Kind: store.GoalMemoryMilestone, Title: shortText(body, maxMemoryTitleChars),
					Evidence: body, SourceRunID: event.RunID})
				knownItems[id] = true
			}
			if doc.CurrentState == "Memory rebuilt from Podiom's saved goal history. The next review will verify and update it." {
				doc.CurrentState = shortText(body, maxMemoryStateChars)
			}
		case store.GoalEventPlanChange:
			appendPlan(body)
		}
	}
	if len(doc.ActivePlan) == 0 && strings.TrimSpace(goal.NextStep) != "" {
		appendPlan(goal.NextStep)
	}
	raw, _ := json.Marshal(doc)
	for len(raw) > maxGoalMemoryBytes && len(doc.Items) > originalItems {
		doc.Items = doc.Items[:len(doc.Items)-1]
		raw, _ = json.Marshal(doc)
	}
	for len(raw) > maxGoalMemoryBytes && len(doc.ActivePlan) > originalPlan {
		doc.ActivePlan = doc.ActivePlan[:len(doc.ActivePlan)-1]
		raw, _ = json.Marshal(doc)
	}
	if len(raw) > maxGoalMemoryBytes {
		return GoalMemoryRepairResult{}, fmt.Errorf("saved memory is %d bytes; repair cannot preserve it within the %d-byte limit", len(raw), maxGoalMemoryBytes)
	}
	if err := c.validateGoalMemoryRepair(ctx, goal, raw); err != nil {
		return GoalMemoryRepairResult{}, err
	}
	base, err := c.store.GoalMemoryRevision(ctx, goal.ID)
	if err != nil {
		return GoalMemoryRepairResult{}, err
	}
	memory, err := c.store.CommitGoalMemory(ctx, goal.ID, base, "repair",
		"Goal memory rebuilt from saved Podiom history.", doc, nil, true)
	if err != nil {
		return GoalMemoryRepairResult{}, err
	}
	return GoalMemoryRepairResult{Memory: memory, RunsRead: len(seenRuns), FeedbackRead: len(feedback), Added: len(doc.Items) - originalItems}, nil
}

func (c *Core) validateGoalMemoryRepair(ctx context.Context, goal store.Goal, draft []byte) error {
	sess, _, err := c.ensureGoalLeadSession(ctx, goal)
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf(`You are validating a repaired Podiom goal-memory draft.

This is a read-only validation step. Do not use tools, inspect project files,
contact services, or perform project work. Check that the JSON is coherent,
keeps the goal's current state, plan, milestones, decisions, risks, rejected
approaches, and artifacts internally consistent, and stays concise.

Goal: %s
Criteria: %s
Next step: %s

Draft memory JSON:
%s

The server has already validated the schema, references, and size. If this
bounded draft is coherent, return exactly GOAL_MEMORY_VALID and nothing else.`,
		goal.Title, goal.SuccessCriteria, goal.NextStep, draft)
	if len(prompt) > maxGoalPacketBytes {
		return fmt.Errorf("memory validation packet is %d bytes; limit is %d", len(prompt), maxGoalPacketBytes)
	}
	events, err := c.StreamTurn(ctx, sess.ID, prompt, TurnOptions{
		PermissionTurnID: "goal-memory-repair-" + goal.ID,
		PermissionRelay:  NewAllowListRelay(nil, c.log),
		Unattended:       true,
		AllowedTools:     []string{},
		FreshContext:     true,
	})
	if err != nil {
		return fmt.Errorf("memory validation could not start: %w", err)
	}
	var response, turnErr string
	for event := range events {
		if event.Kind == "error" {
			turnErr = event.Content
		}
		if event.Message != nil && event.Message.Role == store.RoleAssistant && event.Message.Kind == store.KindMessage {
			response = event.Message.Content
		}
	}
	if turnErr != "" {
		return fmt.Errorf("memory validation failed: %s", shortText(turnErr, maxMemoryDetailChars))
	}
	if strings.TrimSpace(response) != "GOAL_MEMORY_VALID" {
		return fmt.Errorf("memory validation did not confirm the rebuilt draft")
	}
	return nil
}

// goalScheduleSummaries reads only Podiom's schedule definitions. Keeping this
// tiny projection here avoids making durable memory depend on scheduler state.
func (c *Core) goalScheduleSummaries(goalID string) []string {
	entries, err := os.ReadDir(c.paths.SchedulesDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || len(out) >= 50 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.paths.SchedulesDir, entry.Name()))
		if err != nil {
			continue
		}
		var found bool
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok && key == "goal_id" && strings.Trim(strings.TrimSpace(value), "\"'") == goalID {
				found = true
				break
			}
		}
		if found {
			out = append(out, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		}
	}
	return out
}

func shortText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return strings.TrimSpace(string(runes[:limit-1])) + "…"
	}
	return value
}
