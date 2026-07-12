package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/Podiom/Podiom/internal/tokenmeter"
)

var errActiveTurnExists = errors.New("session already has an active turn")

const (
	turnStatusRunning = "running"
	turnStatusDone    = "done"
	turnStatusError   = "error"
	turnStatusStopped = "stopped"
)

type ActiveTurnSummary struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	Status    string `json:"status"`
	Pending   string `json:"pending,omitempty"`
}

type TurnState struct {
	SessionID         string                     `json:"session_id"`
	TurnID            string                     `json:"turn_id"`
	Status            string                     `json:"status"`
	PendingReasoning  string                     `json:"pending_reasoning,omitempty"`
	PendingAssistant  string                     `json:"pending_assistant,omitempty"`
	PendingPermission *adapter.PermissionRequest `json:"pending_permission,omitempty"`
	PendingUserInput  *adapter.UserInputRequest  `json:"pending_user_input,omitempty"`
	PendingFallback   *core.FallbackRequest      `json:"pending_fallback,omitempty"`
	Error             string                     `json:"error,omitempty"`
}

type activeTurn struct {
	sessionID         string
	turnID            string
	requestID         string
	status            string
	pendingReasoning  string
	pendingAssistant  string
	pendingPermission *adapter.PermissionRequest
	pendingUserInput  *adapter.UserInputRequest
	pendingFallback   *core.FallbackRequest
	err               string
	cancel            context.CancelFunc
	subscribers       map[*wsWriter]bool
}

type activeTurnHub struct {
	mu    sync.Mutex
	turns map[string]*activeTurn
	// notifier + resolveAgent drive out-of-app (Web Push / native) notifications
	// when a turn blocks on the user. Both are optional; nil disables push.
	notifier     *notify.Dispatcher
	resolveAgent func(ctx context.Context, sessionID string) (string, error)
}

func newActiveTurnHub() *activeTurnHub {
	return &activeTurnHub{turns: map[string]*activeTurn{}}
}

// attachNotifier wires the out-of-app notification dispatcher and an agent-name
// resolver (the hub only knows session IDs) into the hub.
func (h *activeTurnHub) attachNotifier(n *notify.Dispatcher, resolve func(ctx context.Context, sessionID string) (string, error)) {
	h.notifier = n
	h.resolveAgent = resolve
}

// notifyAttention fires an out-of-app notification for a blocked turn. It runs
// off the hot path (own goroutine) so push latency never delays the turn, and
// resolves the agent name for the notification text.
func (h *activeTurnHub) notifyAttention(sessionID, kind string, approval *adapter.PermissionRequest) {
	if h.notifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		agent := ""
		if h.resolveAgent != nil {
			if a, err := h.resolveAgent(ctx, sessionID); err == nil {
				agent = a
			}
		}
		title, body := attentionText(agent, kind)
		var action *notify.ApprovalAction
		if approval != nil {
			action = &notify.ApprovalAction{
				RequestID: approval.ID,
				Input:     append([]byte(nil), approval.Input...),
			}
		}
		h.notifier.Notify(ctx, notify.Notification{
			SessionID: sessionID,
			AgentName: agent,
			Title:     title,
			Body:      body,
			Kind:      kind,
			Approval:  action,
		})
	}()
}

// attentionText renders the human-facing notification strings for a blocked
// turn. kind is "permission" or "question".
func attentionText(agent, kind string) (title, body string) {
	if agent == "" {
		agent = "An agent"
	}
	switch kind {
	case "permission":
		return agent + " needs approval", "A tool action is waiting for your decision."
	case "question":
		return agent + " has a question", "Answer to let the agent continue."
	case "fallback":
		return agent + " hit a session limit", "Choose how to continue after the rate limit."
	default:
		return agent + " needs your attention", ""
	}
}

func (h *activeTurnHub) start(sessionID, turnID, requestID string, writer *wsWriter, cancel context.CancelFunc) (TurnState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing := h.turns[sessionID]; existing != nil && existing.status == turnStatusRunning {
		if writer != nil {
			existing.subscribers[writer] = true
		}
		return activeTurnStateLocked(existing), errActiveTurnExists
	}
	turn := &activeTurn{
		sessionID:   sessionID,
		turnID:      turnID,
		requestID:   requestID,
		status:      turnStatusRunning,
		cancel:      cancel,
		subscribers: map[*wsWriter]bool{},
	}
	if writer != nil {
		turn.subscribers[writer] = true
	}
	h.turns[sessionID] = turn
	return activeTurnStateLocked(turn), nil
}

// hasRunning reports whether a session currently has an in-flight turn. The
// dream runner uses this (via Server.HasActiveTurn) to skip live sessions.
func (h *activeTurnHub) hasRunning(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	turn := h.turns[sessionID]
	return turn != nil && turn.status == turnStatusRunning
}

func (h *activeTurnHub) sessionForTurn(turnID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, turn := range h.turns {
		if turn.turnID == turnID && turn.status == turnStatusRunning {
			return turn.sessionID, true
		}
	}
	return "", false
}

func (h *activeTurnHub) attach(sessionID string, writer *wsWriter) (TurnState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	turn := h.turns[sessionID]
	if turn == nil {
		return TurnState{}, false
	}
	if writer != nil {
		turn.subscribers[writer] = true
	}
	return activeTurnStateLocked(turn), true
}

func (h *activeTurnHub) detach(writer *wsWriter) {
	if writer == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, turn := range h.turns {
		delete(turn.subscribers, writer)
	}
}

func (h *activeTurnHub) summaries() []ActiveTurnSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ActiveTurnSummary, 0, len(h.turns))
	for _, turn := range h.turns {
		out = append(out, ActiveTurnSummary{
			SessionID: turn.sessionID,
			TurnID:    turn.turnID,
			Status:    turn.status,
			Pending:   activeTurnPendingLocked(turn),
		})
	}
	return out
}

func (h *activeTurnHub) stop(sessionID string) bool {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return false
	}
	if turn.status != turnStatusRunning {
		h.mu.Unlock()
		return true
	}
	turn.status = turnStatusStopped
	turn.pendingPermission = nil
	turn.pendingUserInput = nil
	turn.pendingFallback = nil
	cancel := turn.cancel
	state := activeTurnStateLocked(turn)
	writers := activeTurnWritersLocked(turn)
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	h.broadcast(writers, ServerMessage{Type: "turn_state", SessionID: sessionID, TurnState: &state})
	return true
}

func (h *activeTurnHub) recordMessage(sessionID string, msg *store.Message) {
	writers, requestID := h.turnWriters(sessionID)
	h.broadcast(writers, ServerMessage{Type: "message", RequestID: requestID, SessionID: sessionID, Message: msg})
}

func (h *activeTurnHub) recordSession(session store.Session) {
	writers, requestID := h.turnWriters(session.ID)
	h.broadcast(writers, ServerMessage{Type: "session", RequestID: requestID, SessionID: session.ID, Session: &session})
}

func (h *activeTurnHub) recordDelta(sessionID, delta string) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	turn.pendingAssistant += delta
	// The agent has resumed producing output, so it is no longer blocked on the
	// user — clear any pending request that attention indicators keyed off.
	turn.pendingPermission = nil
	turn.pendingUserInput = nil
	turn.pendingFallback = nil
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "delta", RequestID: requestID, SessionID: sessionID, Delta: delta})
}

func (h *activeTurnHub) recordReasoning(sessionID, text string, final bool) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	if final {
		turn.pendingReasoning = text
	} else {
		turn.pendingReasoning += text
	}
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	msgType := "reasoning_delta"
	if final {
		msgType = "reasoning"
	}
	h.broadcast(writers, ServerMessage{Type: msgType, RequestID: requestID, SessionID: sessionID, Delta: text})
}

// recordContext broadcasts the latest context-window utilization to the session's
// subscribers so the composer ring updates live mid-turn. The value is also
// persisted on the session (in core), so idle sessions restore it from state.
func (h *activeTurnHub) recordContext(sessionID string, used, max int64) {
	writers, requestID := h.turnWriters(sessionID)
	h.broadcast(writers, ServerMessage{Type: "context", RequestID: requestID, SessionID: sessionID, Context: &ContextUsage{Used: used, Max: max}})
}

// recordSessionUsage broadcasts a session's updated token-usage estimate to its
// subscribers so the chat usage bar updates live after a turn completes. A nil
// estimate (no meter wired) is a no-op.
func (h *activeTurnHub) recordSessionUsage(sessionID string, est *tokenmeter.Estimate) {
	if est == nil {
		return
	}
	writers, requestID := h.turnWriters(sessionID)
	h.broadcast(writers, ServerMessage{Type: "session_usage", RequestID: requestID, SessionID: sessionID, SessionUsage: est})
}

func (h *activeTurnHub) recordAssistant(sessionID, text string) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	if text != "" {
		turn.pendingAssistant = text
	}
	// A finalized assistant message means the agent is no longer waiting on the
	// user; clear pending request state that attention indicators keyed off.
	turn.pendingPermission = nil
	turn.pendingUserInput = nil
	turn.pendingFallback = nil
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "assistant", RequestID: requestID, SessionID: sessionID, Delta: text})
}

func (h *activeTurnHub) recordPermission(sessionID string, req *adapter.PermissionRequest) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	turn.pendingPermission = clonePermissionRequest(req)
	turn.pendingUserInput = nil
	turn.pendingFallback = nil
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "permission_request", RequestID: requestID, SessionID: sessionID, Request: req})
	h.notifyAttention(sessionID, "permission", req)
}

func (h *activeTurnHub) recordUserInput(sessionID string, req *adapter.UserInputRequest) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	turn.pendingUserInput = cloneUserInputRequest(req)
	turn.pendingPermission = nil
	turn.pendingFallback = nil
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "user_input_request", RequestID: requestID, SessionID: sessionID, Input: req})
	h.notifyAttention(sessionID, "question", nil)
}

func (h *activeTurnHub) recordFallback(sessionID string, req *core.FallbackRequest) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	turn.pendingFallback = cloneFallbackRequest(req)
	turn.pendingPermission = nil
	turn.pendingUserInput = nil
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "fallback_request", RequestID: requestID, SessionID: sessionID, Fallback: req})
	h.notifyAttention(sessionID, "fallback", nil)
}

func (h *activeTurnHub) finish(sessionID string) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	if turn.status == turnStatusStopped {
		delete(h.turns, sessionID)
		h.mu.Unlock()
		return
	}
	turn.status = turnStatusDone
	turn.pendingPermission = nil
	turn.pendingUserInput = nil
	turn.pendingFallback = nil
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	delete(h.turns, sessionID)
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "done", RequestID: requestID, SessionID: sessionID})
}

func (h *activeTurnHub) fail(sessionID, message string) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	if turn.status == turnStatusStopped {
		delete(h.turns, sessionID)
		h.mu.Unlock()
		return
	}
	turn.status = turnStatusError
	turn.err = message
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	delete(h.turns, sessionID)
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "error", RequestID: requestID, SessionID: sessionID, Error: message})
}

func (h *activeTurnHub) turnWriters(sessionID string) ([]*wsWriter, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	turn := h.turns[sessionID]
	if turn == nil {
		return nil, ""
	}
	return activeTurnWritersLocked(turn), turn.requestID
}

func (h *activeTurnHub) broadcast(writers []*wsWriter, msg ServerMessage) {
	for _, writer := range writers {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := writer.write(ctx, msg)
		cancel()
		if err != nil {
			h.detach(writer)
		}
	}
}

func activeTurnWritersLocked(turn *activeTurn) []*wsWriter {
	writers := make([]*wsWriter, 0, len(turn.subscribers))
	for writer := range turn.subscribers {
		writers = append(writers, writer)
	}
	return writers
}

func activeTurnStateLocked(turn *activeTurn) TurnState {
	return TurnState{
		SessionID:         turn.sessionID,
		TurnID:            turn.turnID,
		Status:            turn.status,
		PendingReasoning:  turn.pendingReasoning,
		PendingAssistant:  turn.pendingAssistant,
		PendingPermission: clonePermissionRequest(turn.pendingPermission),
		PendingUserInput:  cloneUserInputRequest(turn.pendingUserInput),
		PendingFallback:   cloneFallbackRequest(turn.pendingFallback),
		Error:             turn.err,
	}
}

func activeTurnPendingLocked(turn *activeTurn) string {
	switch {
	case turn.pendingPermission != nil:
		return "permission"
	case turn.pendingUserInput != nil:
		return "question"
	case turn.pendingFallback != nil:
		return "fallback"
	case turn.pendingAssistant != "":
		return "assistant"
	default:
		return ""
	}
}

func clonePermissionRequest(req *adapter.PermissionRequest) *adapter.PermissionRequest {
	if req == nil {
		return nil
	}
	cp := *req
	if req.Input != nil {
		cp.Input = append([]byte(nil), req.Input...)
	}
	return &cp
}

func cloneUserInputRequest(req *adapter.UserInputRequest) *adapter.UserInputRequest {
	if req == nil {
		return nil
	}
	cp := *req
	cp.Questions = append([]adapter.UserInputQuestion(nil), req.Questions...)
	for i := range cp.Questions {
		cp.Questions[i].Options = append([]adapter.UserInputOption(nil), req.Questions[i].Options...)
	}
	return &cp
}

func cloneFallbackRequest(req *core.FallbackRequest) *core.FallbackRequest {
	if req == nil {
		return nil
	}
	cp := *req
	cp.Targets = append([]core.FallbackTarget(nil), req.Targets...)
	return &cp
}
