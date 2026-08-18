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
	SessionID             string                        `json:"session_id"`
	TurnID                string                        `json:"turn_id"`
	Status                string                        `json:"status"`
	PendingReasoning      string                        `json:"pending_reasoning,omitempty"`
	PendingAssistant      string                        `json:"pending_assistant,omitempty"`
	PendingPermission     *adapter.PermissionRequest    `json:"pending_permission,omitempty"`
	PendingUserInput      *adapter.UserInputRequest     `json:"pending_user_input,omitempty"`
	PendingFallback       *core.FallbackRequest         `json:"pending_fallback,omitempty"`
	PendingAuth           *core.AuthRequired            `json:"pending_auth,omitempty"`
	Interview             *InterviewState               `json:"interview,omitempty"`
	NativeAgentActivities []adapter.NativeAgentActivity `json:"native_agent_activities,omitempty"`
	Error                 string                        `json:"error,omitempty"`
}

type activeTurn struct {
	sessionID             string
	turnID                string
	requestID             string
	status                string
	pendingReasoning      string
	pendingAssistant      string
	pendingPermission     *adapter.PermissionRequest
	pendingUserInput      *adapter.UserInputRequest
	pendingFallback       *core.FallbackRequest
	pendingAuth           *core.AuthRequired
	interview             *InterviewState
	nativeAgentActivities []adapter.NativeAgentActivity
	err                   string
	cancel                context.CancelFunc
	subscribers           map[*wsWriter]bool
}

type activeTurnHub struct {
	mu    sync.Mutex
	turns map[string]*activeTurn
	// notifications records the fact that a turn is blocked on the user. Optional:
	// nil disables notifications, which is what bare test servers rely on.
	notifications *notify.Engine
}

func newActiveTurnHub() *activeTurnHub {
	return &activeTurnHub{turns: map[string]*activeTurn{}}
}

// attachNotifications wires the notification engine into the hub.
func (h *activeTurnHub) attachNotifications(e *notify.Engine) {
	h.notifications = e
}

// attentionKind classifies what a blocked turn is waiting for. The hub reports
// the state; the notification engine decides what that means for the user.
type attentionKind int

const (
	attentionPermission attentionKind = iota
	attentionQuestion
	attentionFallback
	attentionAuth
)

// notifyAttention records that a turn is blocked on the user.
//
// Publishing is non-blocking, so this stays on the hot path without adding
// latency to the turn — the engine's worker does the rendering, persistence and
// delivery. Agent names are resolved there too, from the session.
func (h *activeTurnHub) notifyAttention(sessionID string, kind attentionKind, approval *adapter.PermissionRequest) {
	ev := notify.Event{SessionID: sessionID}
	switch kind {
	case attentionPermission:
		ev.Type = notify.TypeSessionPermissionRequired
		ev.Resource = notify.ResourcePermissionRequest
		if approval != nil {
			ev.ResourceID = approval.ID
			ev.Approval = &notify.ApprovalAction{
				RequestID: approval.ID,
				Input:     append([]byte(nil), approval.Input...),
			}
		}
	case attentionQuestion:
		ev.Type = notify.TypeSessionQuestion
		ev.Resource = notify.ResourceSessionQuestion
		ev.ResourceID = sessionID
	case attentionFallback:
		ev.Type = notify.TypeSessionActionRequired
		ev.Resource = notify.ResourceFallbackRequest
		ev.ResourceID = sessionID
		ev.Detail = "Choose how to continue after the rate limit."
	case attentionAuth:
		ev.Type = notify.TypeSessionActionRequired
		ev.Resource = notify.ResourceAuthRequest
		ev.ResourceID = sessionID
		ev.Detail = "The account behind this session is signed out."
	default:
		return
	}
	h.notifications.Publish(ev)
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

func (h *activeTurnHub) turnIDForSession(sessionID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	turn := h.turns[sessionID]
	if turn == nil || turn.status != turnStatusRunning {
		return "", false
	}
	return turn.turnID, true
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

// recordMessage broadcasts a stored history row and retires the live buffer it
// replaces. A turn persists its working notes as it goes, so without this a
// client reconnecting mid-turn would see the same text twice: once as the
// restored stream buffer and once as the row.
func (h *activeTurnHub) recordMessage(sessionID string, msg *store.Message) {
	h.mu.Lock()
	if turn := h.turns[sessionID]; turn != nil && msg != nil && msg.Role == store.RoleAssistant {
		if msg.Kind == store.KindReasoning {
			turn.pendingReasoning = ""
		} else {
			turn.pendingAssistant = ""
		}
	}
	h.mu.Unlock()
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

func (h *activeTurnHub) recordNativeAgentActivity(sessionID string, activity *adapter.NativeAgentActivity) {
	if activity == nil {
		return
	}
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	cp := cloneNativeAgentActivity(activity)
	key := nativeAgentActivityKey(&cp)
	if key != "" {
		replaced := false
		for i := range turn.nativeAgentActivities {
			if nativeAgentActivityKey(&turn.nativeAgentActivities[i]) == key {
				turn.nativeAgentActivities[i] = cp
				replaced = true
				break
			}
		}
		if !replaced {
			turn.nativeAgentActivities = append(turn.nativeAgentActivities, cp)
		}
	} else {
		turn.nativeAgentActivities = append(turn.nativeAgentActivities, cp)
	}
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "native_agent_activity", RequestID: requestID, SessionID: sessionID, NativeAgent: &cp})
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
	h.notifyAttention(sessionID, attentionPermission, req)
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
	h.notifyAttention(sessionID, attentionQuestion, nil)
}

func (h *activeTurnHub) recordInterviewState(sessionID string, state *InterviewState) {
	if state == nil {
		return
	}
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	cp := *state
	cp.CoveredTopics = append([]core.InterviewTopic(nil), state.CoveredTopics...)
	turn.interview = &cp
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "interview_state", RequestID: requestID, SessionID: sessionID, Interview: &cp})
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
	h.notifyAttention(sessionID, attentionFallback, nil)
}

// recordAuthRequired marks the turn as blocked on a signed-out account. It is
// kept on the turn state (not just broadcast) so a tab that reconnects still
// sees the sign-in card instead of an unexplained dead turn.
func (h *activeTurnHub) recordAuthRequired(sessionID string, req *core.AuthRequired) {
	h.mu.Lock()
	turn := h.turns[sessionID]
	if turn == nil {
		h.mu.Unlock()
		return
	}
	turn.pendingAuth = cloneAuthRequired(req)
	writers := activeTurnWritersLocked(turn)
	requestID := turn.requestID
	h.mu.Unlock()
	h.broadcast(writers, ServerMessage{Type: "auth_required", RequestID: requestID, SessionID: sessionID, AuthRequired: req})
	h.notifyAttention(sessionID, attentionAuth, nil)
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
		SessionID:             turn.sessionID,
		TurnID:                turn.turnID,
		Status:                turn.status,
		PendingReasoning:      turn.pendingReasoning,
		PendingAssistant:      turn.pendingAssistant,
		PendingPermission:     clonePermissionRequest(turn.pendingPermission),
		PendingUserInput:      cloneUserInputRequest(turn.pendingUserInput),
		PendingFallback:       cloneFallbackRequest(turn.pendingFallback),
		PendingAuth:           cloneAuthRequired(turn.pendingAuth),
		Interview:             cloneInterviewStatePtr(turn.interview),
		NativeAgentActivities: cloneNativeAgentActivities(turn.nativeAgentActivities),
		Error:                 turn.err,
	}
}

func cloneInterviewStatePtr(state *InterviewState) *InterviewState {
	if state == nil {
		return nil
	}
	cp := *state
	cp.CoveredTopics = append([]core.InterviewTopic(nil), state.CoveredTopics...)
	return &cp
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

func cloneAuthRequired(req *core.AuthRequired) *core.AuthRequired {
	if req == nil {
		return nil
	}
	cp := *req
	return &cp
}

func cloneNativeAgentActivity(activity *adapter.NativeAgentActivity) adapter.NativeAgentActivity {
	if activity == nil {
		return adapter.NativeAgentActivity{}
	}
	return *activity
}

func cloneNativeAgentActivities(activities []adapter.NativeAgentActivity) []adapter.NativeAgentActivity {
	if len(activities) == 0 {
		return nil
	}
	out := make([]adapter.NativeAgentActivity, len(activities))
	copy(out, activities)
	return out
}

func nativeAgentActivityKey(activity *adapter.NativeAgentActivity) string {
	if activity == nil {
		return ""
	}
	if activity.TaskID != "" {
		return "task:" + activity.TaskID
	}
	if activity.ToolUseID != "" {
		return "tool:" + activity.ToolUseID
	}
	if activity.ProviderAgentName != "" {
		return string(activity.Provider) + ":" + activity.ProviderAgentName + ":" + activity.Description
	}
	return ""
}
