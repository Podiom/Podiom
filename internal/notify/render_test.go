package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTitleIsBoundedForTheRelay guards a failure that would be total rather than partial:
// the push relay rejects a title over 200 bytes, so an over-long one loses the push
// entirely instead of shortening it.
func TestTitleIsBoundedForTheRelay(t *testing.T) {
	// A goal title long enough to blow the limit on its own, which is realistic — goals
	// are user-named and the wording wraps them in quotes and a sentence.
	longGoal := strings.Repeat("Release the entire platform ", 20)

	for _, notifType := range []string{
		TypeGoalRunFailed, TypeGoalCompletionProposed, TypeGoalRateLimited, TypeGoalProgress,
	} {
		t.Run(notifType, func(t *testing.T) {
			title, _ := render(Event{Type: notifType, GoalID: "goal-1"}, displayNames{
				Goal: longGoal, Agent: "alice",
			})
			if len(title) > titleLimit {
				t.Errorf("title is %d bytes, over the relay's limit of %d", len(title), titleLimit)
			}
			if !utf8.ValidString(title) {
				t.Error("truncation split a character")
			}
		})
	}
}

// TestTitleTruncationKeepsCharactersWhole checks a multi-byte name is not cut mid-rune,
// which would make the payload invalid UTF-8 and be rejected for a different reason.
func TestTitleTruncationKeepsCharactersWhole(t *testing.T) {
	// Each of these is three bytes, so the byte limit lands mid-character repeatedly.
	title, _ := render(Event{Type: TypeGoalProgress, GoalID: "goal-1"}, displayNames{
		Goal: strings.Repeat("目", 200),
	})
	if len(title) > titleLimit {
		t.Errorf("title is %d bytes, over the limit of %d", len(title), titleLimit)
	}
	if !utf8.ValidString(title) {
		t.Errorf("truncation produced invalid UTF-8: %q", title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Errorf("a truncated title should say so: %q", title)
	}
}

// TestShortTitlesAreUntouched checks the bound does not mangle ordinary wording.
func TestShortTitlesAreUntouched(t *testing.T) {
	title, _ := render(Event{Type: TypeGoalActionRequested, AgentName: "alice"}, displayNames{})
	if strings.Contains(title, "…") {
		t.Errorf("a short title was truncated: %q", title)
	}
	if title != "alice needs your help" {
		t.Errorf("title = %q", title)
	}
}
