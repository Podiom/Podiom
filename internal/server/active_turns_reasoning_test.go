package server

import (
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

// A turn persists its working notes as it runs. Once a note is a stored row, the
// live buffer it came from has to go — otherwise a client reconnecting mid-turn
// restores the buffer *and* loads the row, and sees the same text twice.
func TestRecordMessageRetiresTheBufferItReplaces(t *testing.T) {
	h := newActiveTurnHub()
	const sessionID = "sess-1"
	if _, err := h.start(sessionID, "turn-1", "req-1", nil, nil); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	h.recordReasoning(sessionID, "weighing the options", false)
	h.recordDelta(sessionID, "checking the config")
	state, ok := h.attach(sessionID, nil)
	if !ok {
		t.Fatal("attach: no active turn")
	}
	if state.PendingReasoning != "weighing the options" || state.PendingAssistant != "checking the config" {
		t.Fatalf("buffers before storing = (%q, %q)", state.PendingReasoning, state.PendingAssistant)
	}

	h.recordMessage(sessionID, &store.Message{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "weighing the options"})
	state, _ = h.attach(sessionID, nil)
	if state.PendingReasoning != "" {
		t.Fatalf("reasoning buffer = %q, want cleared by its stored row", state.PendingReasoning)
	}
	if state.PendingAssistant != "checking the config" {
		t.Fatalf("assistant buffer = %q, want untouched by a reasoning row", state.PendingAssistant)
	}

	h.recordMessage(sessionID, &store.Message{Role: store.RoleAssistant, Kind: store.KindNarration, Content: "checking the config"})
	state, _ = h.attach(sessionID, nil)
	if state.PendingAssistant != "" {
		t.Fatalf("assistant buffer = %q, want cleared by its stored narration row", state.PendingAssistant)
	}
}

// A user's own message must not disturb either stream buffer.
func TestRecordMessageLeavesBuffersAloneForUserRows(t *testing.T) {
	h := newActiveTurnHub()
	const sessionID = "sess-2"
	if _, err := h.start(sessionID, "turn-2", "req-2", nil, nil); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	h.recordReasoning(sessionID, "thinking", false)
	h.recordDelta(sessionID, "talking")

	h.recordMessage(sessionID, &store.Message{Role: store.RoleUser, Content: "do the thing"})
	state, _ := h.attach(sessionID, nil)
	if state.PendingReasoning != "thinking" || state.PendingAssistant != "talking" {
		t.Fatalf("buffers = (%q, %q), want both untouched", state.PendingReasoning, state.PendingAssistant)
	}
}
