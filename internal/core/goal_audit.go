package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/store"
)

const (
	// goalToolUseBodyMax caps the one-line markdown body of a tool_use event.
	goalToolUseBodyMax = 600
	// goalToolUseInputMax caps the raw tool input stored in the event payload so
	// a large Write (a whole file) or Bash heredoc can't bloat the timeline.
	goalToolUseInputMax = 2048
	// goalToolUseContentMax caps free-form content fields (a file's new contents)
	// far tighter than the command/path we deliberately keep — file paths yes,
	// full file bodies no (docs/security.md redaction posture).
	goalToolUseContentMax = 200
)

// goalReadOnlyTools classifies tools whose calls the UI may collapse into a
// single grouped row. Names cover both Claude tools and Codex item types.
var goalReadOnlyTools = map[string]bool{
	"Read":         true,
	"Grep":         true,
	"Glob":         true,
	"LS":           true,
	"WebFetch":     true,
	"WebSearch":    true,
	"webSearch":    true,
	"NotebookRead": true,
	"TodoWrite":    true,
}

// goalToolContentFields are input keys that can carry large free-form bodies we
// truncate hard (unlike commands and paths, which we keep to show what ran).
var goalToolContentFields = []string{"content", "new_string", "old_string", "new_str", "old_str", "file_text"}

// appendGoalToolUseEvent records one provider tool invocation on the goal
// timeline. It runs on a detached, short-lived context (like appendFinalMessages)
// so a client that disconnects mid-turn never loses the audit row, and it never
// returns an error to the caller: a failed append is logged, not fatal.
func (c *Core) appendGoalToolUseEvent(ctx context.Context, goalID, sessionID, runID string, tu adapter.ToolUse) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	readOnly := goalReadOnlyTools[tu.Name]
	summary := strings.TrimSpace(tu.Summary)

	body := "`" + tu.Name + "`"
	if summary != "" {
		body += " — " + summary
	}
	body = truncate(body, goalToolUseBodyMax)

	input, inputTruncated := truncateToolInput(tu.Input)
	payload, _ := json.Marshal(map[string]any{
		"tool":            tu.Name,
		"tool_use_id":     tu.ToolUseID,
		"provider":        string(tu.Provider),
		"read_only":       readOnly,
		"summary":         truncate(summary, goalToolUseBodyMax),
		"input":           input,
		"input_truncated": inputTruncated,
	})

	ev, err := c.store.AppendGoalEvent(writeCtx, store.GoalEvent{
		GoalID:    goalID,
		SessionID: sessionID,
		RunID:     runID,
		Kind:      store.GoalEventToolUse,
		Body:      body,
		Payload:   string(payload),
	})
	if err != nil {
		c.log.Warn("append goal tool_use event failed", "event", "goal", "goal", goalID, "session", sessionID, "tool", tu.Name, "error", err)
		return
	}
	if c.onGoalEvent != nil {
		c.onGoalEvent(ev)
	}
}

// truncateToolInput shrinks a raw tool input for storage: it hard-truncates
// large free-form content fields (file bodies) to keep paths/commands visible
// without persisting whole files, then caps the overall JSON length. It returns
// the (possibly truncated) input as a string and whether anything was trimmed.
func truncateToolInput(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	truncated := false
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err == nil {
		for _, key := range goalToolContentFields {
			if v, ok := fields[key].(string); ok && len(v) > goalToolUseContentMax {
				fields[key] = v[:goalToolUseContentMax] + "…"
				truncated = true
			}
		}
		if reduced, err := json.Marshal(fields); err == nil {
			raw = reduced
		}
	}
	s := string(raw)
	if len(s) > goalToolUseInputMax {
		s = s[:goalToolUseInputMax] + "…"
		truncated = true
	}
	return s, truncated
}

// truncate shortens s to at most max runes, appending an ellipsis when it does.
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
