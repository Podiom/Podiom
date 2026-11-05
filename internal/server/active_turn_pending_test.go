package server

import (
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/core"
)

// #370: activeTurnPendingLocked's arm precedence is load-bearing — it decides
// what the UI says a turn is waiting on — and nothing pinned it. Every expected
// output here was checked against the real function. No locking needed despite
// the Locked suffix: construct the struct literals directly.
func TestActiveTurnPending(t *testing.T) {
	t.Run("with all fields set the result is permission", func(t *testing.T) {
		turn := &activeTurn{
			pendingPermission: &adapter.PermissionRequest{},
			pendingUserInput:  &adapter.UserInputRequest{},
			pendingFallback:   &core.FallbackRequest{},
			pendingAssistant:  "draft",
		}
		if got := activeTurnPendingLocked(turn); got != "permission" {
			t.Errorf("got %q, want \"permission\"", got)
		}
	})

	t.Run("permission beats user input", func(t *testing.T) {
		turn := &activeTurn{
			pendingPermission: &adapter.PermissionRequest{},
			pendingUserInput:  &adapter.UserInputRequest{},
		}
		if got := activeTurnPendingLocked(turn); got != "permission" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("user input beats fallback and reports the wire string question", func(t *testing.T) {
		turn := &activeTurn{
			pendingUserInput: &adapter.UserInputRequest{},
			pendingFallback:  &core.FallbackRequest{},
		}
		// "question", not "user_input": it matches neither the field name nor the
		// pending_user_input JSON tag on TurnState. Surprising, pinned.
		if got := activeTurnPendingLocked(turn); got != "question" {
			t.Errorf("got %q, want \"question\"", got)
		}
	})

	t.Run("fallback beats assistant", func(t *testing.T) {
		turn := &activeTurn{
			pendingFallback:  &core.FallbackRequest{},
			pendingAssistant: "draft",
		}
		if got := activeTurnPendingLocked(turn); got != "fallback" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("pendingAssistant is tested with != \"\", not trimmed", func(t *testing.T) {
		if got := activeTurnPendingLocked(&activeTurn{pendingAssistant: " "}); got != "assistant" {
			t.Errorf("a single space: got %q, want \"assistant\"", got)
		}
		if got := activeTurnPendingLocked(&activeTurn{pendingAssistant: "draft"}); got != "assistant" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("pendingReasoning, interview and err do not participate", func(t *testing.T) {
		turn := &activeTurn{
			pendingReasoning: "thinking",
			interview:        &InterviewState{},
			err:              "an error",
		}
		if got := activeTurnPendingLocked(turn); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
