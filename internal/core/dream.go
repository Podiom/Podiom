package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// ErrDreamInProgress reports that a dream is already running for the agent.
var ErrDreamInProgress = errors.New("a dream is already running for this agent")

// Dream phases, streamed to the UI so the dream-sequence overlay can animate.
const (
	DreamPhaseGathering   = "gathering"   // reading the day's sessions
	DreamPhaseDistilling  = "distilling"  // the model is consolidating
	DreamPhaseIntegrating = "integrating" // writing the new memory
	DreamPhaseDone        = "done"        // finished, memory grew
	DreamPhaseNoop        = "noop"        // nothing to dream about
	DreamPhaseError       = "error"       // failed; nothing changed
)

// Caps that keep a dream prompt bounded regardless of how much history exists.
const (
	dreamMaxSessions     = 30
	dreamMaxCharsPerSess = 8000
)

// DreamOptions configures a single dream.
type DreamOptions struct {
	Trigger store.DreamTrigger
	// OnPhase, when set, is called as the dream advances so callers (the WS layer)
	// can stream progress. It must not block.
	OnPhase func(phase string)
}

// DreamResult reports the outcome of a dream.
type DreamResult struct {
	// NoOp is true when there were no un-dreamed sessions; MEMORY.md was untouched
	// and no dream row was written (MEM7).
	NoOp bool
	// Dream is the persisted journal row for a completed dream (zero when NoOp).
	Dream store.Dream
}

// MemoryStatus summarizes an agent's memory for the UI and CLI.
type MemoryStatus struct {
	LastDream       *store.Dream
	PendingSessions int
	MemoryLines     int
	BudgetLines     int
	UpdatedAt       string
}

// dreamPayload is the structured reply the consolidating model must return.
type dreamPayload struct {
	Memory   string               `json:"memory"`
	Note     string               `json:"note"`
	Kept     int                  `json:"kept"`
	Merged   int                  `json:"merged"`
	Pruned   int                  `json:"pruned"`
	NewItems []store.DreamNewItem `json:"new_items"`
}

// MemoryStatus returns the current memory state for one agent.
func (c *Core) MemoryStatus(ctx context.Context, name string) (MemoryStatus, error) {
	if _, err := c.GetAgent(ctx, name); err != nil {
		return MemoryStatus{}, err
	}
	memory, err := c.ReadAgentMemory(name)
	if err != nil {
		return MemoryStatus{}, err
	}
	pending, err := c.store.CountUndreamedSessions(ctx, name)
	if err != nil {
		return MemoryStatus{}, err
	}
	status := MemoryStatus{
		PendingSessions: pending,
		MemoryLines:     memoryLineCount(memory),
		BudgetLines:     memoryBudgetLines,
	}
	if last, err := c.store.LastSuccessfulDream(ctx, name); err == nil {
		status.LastDream = &last
	} else if !errors.Is(err, store.ErrNotFound) {
		return MemoryStatus{}, err
	}
	return status, nil
}

// DreamAgent consolidates an agent's un-dreamed sessions into its MEMORY.md.
//
// It reads the current MEMORY.md as the authoritative starting point (MEM12),
// asks the agent's own model to distill the day's sessions into an updated memory
// within the injection budget, then writes the result and marks the sessions
// dreamed. Any failure leaves MEMORY.md and the sessions untouched so the work is
// retried on the next cycle. With no un-dreamed sessions it is a no-op (MEM7).
func (c *Core) DreamAgent(ctx context.Context, name string, opts DreamOptions) (DreamResult, error) {
	if opts.Trigger == "" {
		opts.Trigger = store.DreamManual
	}
	agent, err := c.GetAgent(ctx, name)
	if err != nil {
		return DreamResult{}, err
	}
	if !c.acquireDream(name) {
		return DreamResult{}, ErrDreamInProgress
	}
	defer c.releaseDream(name)

	phase := func(p string) {
		if opts.OnPhase != nil {
			opts.OnPhase(p)
		}
	}

	sessions, err := c.store.ListUndreamedSessions(ctx, name)
	if err != nil {
		return DreamResult{}, err
	}
	sessions = c.dreamableSessions(sessions)
	if len(sessions) == 0 {
		phase(DreamPhaseNoop)
		return DreamResult{NoOp: true}, nil
	}

	phase(DreamPhaseGathering)
	current, err := c.ReadAgentMemory(name)
	if err != nil {
		return DreamResult{}, err
	}
	beforeHash := sha256.Sum256([]byte(current))

	// Cap the sessions folded into a single dream; the rest wait for the next
	// cycle (they stay un-dreamed), so a large backlog still converges.
	selected := sessions
	if len(selected) > dreamMaxSessions {
		selected = selected[:dreamMaxSessions]
	}
	transcripts, err := c.dreamTranscripts(ctx, selected)
	if err != nil {
		return DreamResult{}, err
	}

	dreamRow, err := c.store.CreateDream(ctx, store.Dream{
		AgentName:    name,
		Trigger:      opts.Trigger,
		Status:       store.DreamRunning,
		SessionCount: len(selected),
	})
	if err != nil {
		return DreamResult{}, err
	}

	phase(DreamPhaseDistilling)
	prompt := buildDreamPrompt(agent.Name, current, transcripts)
	raw, dreamErr := c.oneShotCompletionErr(ctx, agent, prompt, oneShotOptions{Unattended: true})
	if dreamErr == nil {
		var payload dreamPayload
		payload, dreamErr = parseDreamPayload(raw)
		if dreamErr == nil && strings.TrimSpace(current) != "" && strings.TrimSpace(payload.Memory) == "" {
			dreamErr = fmt.Errorf("dream would erase a non-empty memory")
		}
		if dreamErr == nil {
			phase(DreamPhaseIntegrating)
			// If the user edited MEMORY.md while the model was thinking, abort so we
			// never overwrite their change (MEM12).
			latest, readErr := c.ReadAgentMemory(name)
			if readErr != nil {
				dreamErr = readErr
			} else if sha256.Sum256([]byte(latest)) != beforeHash {
				dreamErr = fmt.Errorf("memory changed during the dream; skipping this run")
			}
			if dreamErr == nil {
				dreamErr = c.applyDream(ctx, name, dreamRow.ID, selected, payload)
			}
		}
	}

	if dreamErr != nil {
		// Record the failure but do not touch MEMORY.md or the sessions — they stay
		// un-dreamed and are retried next cycle. Never log the memory itself (MEM22).
		failed, finishErr := c.store.FinishDream(ctx, dreamRow.ID, store.DreamErrored, dreamErr.Error(), "", 0, 0, 0, nil)
		if finishErr != nil {
			c.log.Error("record failed dream", "event", "dream", "agent", name, "error", finishErr)
		}
		phase(DreamPhaseError)
		if errors.Is(dreamErr, context.Canceled) || errors.Is(dreamErr, context.DeadlineExceeded) {
			return DreamResult{Dream: failed}, dreamErr
		}
		c.log.Warn("dream failed", "event", "dream", "agent", name, "sessions", len(selected), "error", dreamErr.Error())
		return DreamResult{Dream: failed}, dreamErr
	}

	final, err := c.store.GetDream(ctx, dreamRow.ID)
	if err != nil {
		return DreamResult{}, err
	}
	c.log.Info("dream complete", "event", "dream", "agent", name, "sessions", len(selected),
		"kept", final.Kept, "merged", final.Merged, "pruned", final.Pruned)
	phase(DreamPhaseDone)
	return DreamResult{Dream: final}, nil
}

// applyDream writes the new memory, marks the sessions dreamed, and finalizes the
// journal row — the point of no return, run only after all validation passed.
func (c *Core) applyDream(ctx context.Context, name, dreamID string, sessions []store.Session, payload dreamPayload) error {
	if err := c.WriteAgentMemory(name, ensureTrailingNewline(payload.Memory)); err != nil {
		return err
	}
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	at := time.Now().UTC().Format(time.RFC3339)
	if err := c.store.MarkSessionsDreamed(ctx, ids, at); err != nil {
		return err
	}
	_, err := c.store.FinishDream(ctx, dreamID, store.DreamSuccess, "", strings.TrimSpace(payload.Note),
		payload.Kept, payload.Merged, payload.Pruned, payload.NewItems)
	return err
}

// dreamableSessions drops sessions with a live turn so the dream never contends
// with active work; they are picked up on the next cycle.
func (c *Core) dreamableSessions(sessions []store.Session) []store.Session {
	if c.activeTurn == nil {
		return sessions
	}
	out := sessions[:0]
	for _, s := range sessions {
		if c.activeTurn(s.ID) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// dreamTranscripts builds a compact, bounded transcript for each session using
// the rolling summary (when present) plus the most recent messages.
func (c *Core) dreamTranscripts(ctx context.Context, sessions []store.Session) ([]string, error) {
	out := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		history, err := c.store.ListMessages(ctx, sess.ID)
		if err != nil {
			return nil, err
		}
		replayed := replayHistory(sess, history)
		body := truncateRunes(transcript(replayed), dreamMaxCharsPerSess)
		label := sess.Name
		if label == "" {
			label = sess.ID
		}
		out = append(out, fmt.Sprintf("### Session: %s (%s)\n%s", label, sess.CreatedAt, body))
	}
	return out, nil
}

// RunDueDreams dreams for every agent whose nightly dream is due. An agent is due
// when the current time is past today's configured dream time, no successful
// dream has run since that time, and un-dreamed sessions exist. This single
// predicate covers the nightly run, downtime catch-up, and multi-day gaps
// (MEM7/MEM8).
func (c *Core) RunDueDreams(ctx context.Context) {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		c.log.Error("list agents for dreaming", "event", "dream", "error", err)
		return
	}
	dueAt, ok := todaysDreamTime(c.GetGlobal().DreamTime, time.Now())
	if !ok {
		return
	}
	for _, agent := range agents {
		if ctx.Err() != nil {
			return
		}
		if !c.dreamDue(ctx, agent.Name, dueAt) {
			continue
		}
		if _, err := c.DreamAgent(ctx, agent.Name, DreamOptions{Trigger: store.DreamNightly}); err != nil {
			if errors.Is(err, ErrDreamInProgress) {
				continue
			}
			// Failures are already recorded and logged inside DreamAgent.
			continue
		}
	}
}

// dreamDue reports whether the agent should dream now given today's dream time.
func (c *Core) dreamDue(ctx context.Context, name string, dueAt time.Time) bool {
	if time.Now().Before(dueAt) {
		return false
	}
	last, err := c.store.LastSuccessfulDream(ctx, name)
	if err == nil {
		if ran, perr := time.Parse(time.RFC3339, last.RanAt); perr == nil && !ran.Before(dueAt) {
			return false // already dreamed since today's dream time
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		c.log.Error("check last dream", "event", "dream", "agent", name, "error", err)
		return false
	}
	pending, err := c.store.CountUndreamedSessions(ctx, name)
	if err != nil {
		c.log.Error("count undreamed sessions", "event", "dream", "agent", name, "error", err)
		return false
	}
	return pending > 0
}

func (c *Core) acquireDream(name string) bool {
	c.dreamMu.Lock()
	defer c.dreamMu.Unlock()
	if c.dreaming[name] {
		return false
	}
	c.dreaming[name] = true
	return true
}

func (c *Core) releaseDream(name string) {
	c.dreamMu.Lock()
	delete(c.dreaming, name)
	c.dreamMu.Unlock()
}

// todaysDreamTime resolves the "HH:MM" dream time to an absolute instant on the
// current local day. ok is false only if the configured value is unparseable.
func todaysDreamTime(raw string, now time.Time) (time.Time, bool) {
	if raw == "" {
		raw = config.DefaultDreamTime
	}
	hm, err := time.Parse("15:04", raw)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location()), true
}

// buildDreamPrompt frames the consolidation task for the agent's own model. The
// current MEMORY.md is the authoritative base: the model augments and curates it
// but must never re-add anything the user removed (MEM12).
func buildDreamPrompt(agentName, current string, transcripts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s. It is the end of the day and you are consolidating your memory —\n", agentName)
	b.WriteString("reflecting over today's conversations and keeping only what deserves to last.\n\n")

	b.WriteString("Your current memory is the authoritative starting point. It may have been\n")
	b.WriteString("edited by the user. Rules you must follow:\n")
	b.WriteString("- Treat the current memory below as the base. Keep, merge, reorganize, and\n")
	b.WriteString("  prune it — but NEVER re-add anything that is absent from it, because the\n")
	b.WriteString("  user may have deliberately removed it. Respect any wording they changed.\n")
	b.WriteString("- Add only durably significant learnings from today's sessions: the user's\n")
	b.WriteString("  preferences, working patterns, recurring facts, decisions you've settled,\n")
	b.WriteString("  and how the two of you collaborate. Skip one-off trivia.\n")
	fmt.Fprintf(&b, "- Keep the whole memory under %d lines. Distill; do not accumulate.\n", memoryBudgetLines)
	b.WriteString("- Write memory as clean Markdown: a `# Memory — " + agentName + "` heading,\n")
	b.WriteString("  then `## Section` headings with `- ` bullet items. No inline HTML or metadata.\n\n")

	b.WriteString("=== CURRENT MEMORY (authoritative base) ===\n")
	if strings.TrimSpace(current) == "" {
		b.WriteString("(empty — this is the first time there is anything to remember)\n")
	} else {
		b.WriteString(current)
		if !strings.HasSuffix(current, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("=== END CURRENT MEMORY ===\n\n")

	b.WriteString("=== TODAY'S SESSIONS ===\n")
	for _, t := range transcripts {
		b.WriteString(t)
		b.WriteString("\n\n")
	}
	b.WriteString("=== END SESSIONS ===\n\n")

	b.WriteString("Reply with ONLY a compact JSON object, no markdown fences, of this exact shape:\n")
	b.WriteString(`{"memory":"the complete updated MEMORY.md as one string",` +
		`"note":"a short first-person sentence about what stayed with you tonight",` +
		`"kept":<int items carried over>,"merged":<int items merged>,"pruned":<int items dropped>,` +
		`"new_items":[{"section":"section title","text":"the new bullet text"}]}` + "\n")
	return b.String()
}

func parseDreamPayload(raw string) (dreamPayload, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var payload dreamPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return dreamPayload{}, fmt.Errorf("parse dream reply: %w", err)
	}
	return payload, nil
}

func memoryLineCount(memory string) int {
	trimmed := strings.TrimRight(memory, "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
