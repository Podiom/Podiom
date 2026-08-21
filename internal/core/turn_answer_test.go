package core

import (
	"context"
	"strings"
	"testing"
)

// TestFlattenAnswerReadsAsProse covers the shapes an agent actually ends a turn with.
// A notification body is one line, so markdown ornament has to go rather than be
// carried through as literal asterisks and hyphens.
func TestFlattenAnswerReadsAsProse(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "verdict and bullets",
			in:   "**Done.**\n\n- Changed src/auth.go\n- Added a regression test",
			want: "Done. Changed src/auth.go Added a regression test",
		},
		{
			name: "heading",
			in:   "## Summary\nFixed the redirect loop.",
			want: "Summary Fixed the redirect loop.",
		},
		{
			name: "ordered list",
			in:   "1. Read the config\n2) Rewrote the parser",
			want: "Read the config Rewrote the parser",
		},
		{
			name: "quote",
			in:   "> The build was already broken.\nReverted it.",
			want: "The build was already broken. Reverted it.",
		},
		{
			name: "inline code and italics",
			in:   "Renamed `oldName` to *newName*.",
			want: "Renamed oldName to newName.",
		},
		{
			name: "fenced block is dropped",
			in:   "Applied this patch:\n```go\nfunc main() {}\n```\nTests pass.",
			want: "Applied this patch: Tests pass.",
		},
		{
			name: "unterminated fence is dropped",
			in:   "Ran into this:\n```\nsome truncated output",
			want: "Ran into this:",
		},
		{
			name: "snake_case survives",
			in:   "The session_id was never set on turn_message_id.",
			want: "The session_id was never set on turn_message_id.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := flattenAnswer(tc.in); got != tc.want {
				t.Errorf("flattenAnswer() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFlattenAnswerIsBounded checks a long closing report cannot ride inside an Event
// at full length, whatever the renderer's own cap happens to be.
func TestFlattenAnswerIsBounded(t *testing.T) {
	got := flattenAnswer(strings.Repeat("shipped the thing ", 400))
	if len([]rune(got)) > answerHardLimit {
		t.Errorf("flattened answer is %d runes, over the limit of %d", len([]rune(got)), answerHardLimit)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated answer should say so: %q", got)
	}
}

// TestTurnAnswerIsBestEffort checks a missing session yields no text rather than an
// error. Callers publish a notification either way and fall back to static wording,
// so a failure here must never be able to block one.
func TestTurnAnswerIsBestEffort(t *testing.T) {
	var c *Core
	if got := c.TurnAnswer(context.Background(), "session-1"); got != "" {
		t.Errorf("TurnAnswer on a nil Core = %q, want empty", got)
	}
	if got := (&Core{}).TurnAnswer(context.Background(), ""); got != "" {
		t.Errorf("TurnAnswer with no session = %q, want empty", got)
	}
}
