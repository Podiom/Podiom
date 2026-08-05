package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/Podiom/Podiom/internal/tokenmeter"
	"github.com/Podiom/Podiom/internal/usage"
	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	// The auth middleware already validated the gateway token (browsers carry
	// it in the Sec-WebSocket-Protocol list — the browser WebSocket API cannot
	// set headers). Accept echoing only the non-secret application protocol:
	// browsers require the server to select one of the offered protocols, and
	// echoing the token entry would reflect the secret.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
		Subprotocols:   []string{gateway.WSProtocol},
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	writer := &wsWriter{conn: conn}
	// Track the live connection so a token rotation can force-close it (HA12)
	// and goal-event broadcasts can reach it. Recheck the handshake token while
	// holding wsMu: the upgrade can finish concurrently with a token rotation,
	// before the connection has reached the registry snapshot in closeAllWS.
	if !s.registerWS(conn, writer, r) {
		_ = conn.Close(wsCloseTokenRotated, "gateway token rotated")
		return
	}
	defer s.unregisterWS(conn)

	ctx := r.Context()
	defer s.turns.detach(writer)
	_ = writer.write(ctx, ServerMessage{Type: "hello"})
	_ = s.writeState(ctx, writer)

	incoming := make(chan ClientMessage, 16)
	errc := make(chan error, 1)
	go readWS(ctx, conn, incoming, errc)

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errc:
			if err != nil && websocket.CloseStatus(err) == -1 {
				_ = writer.write(ctx, ServerMessage{Type: "error", Error: err.Error()})
			}
			return
		case msg, ok := <-incoming:
			if !ok {
				return
			}
			if err := s.handleWSMessage(ctx, writer, msg); err != nil {
				_ = writer.write(ctx, ServerMessage{
					Type:      "error",
					RequestID: msg.RequestID,
					Error:     err.Error(),
				})
			}
		}
	}
}

func (s *Server) registerWS(conn *websocket.Conn, writer *wsWriter, r *http.Request) bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.tokens != nil && !s.tokens.Authorize(r) {
		return false
	}
	if s.wsConns == nil {
		s.wsConns = map[*websocket.Conn]*wsWriter{}
	}
	s.wsConns[conn] = writer
	return true
}

func (s *Server) unregisterWS(conn *websocket.Conn) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	delete(s.wsConns, conn)
}

// closeAllWS force-closes every live WebSocket connection with the given
// application close code; clients decide from the code whether to re-prompt
// for the token (4401) or reconnect. Closes run concurrently because Close
// performs a full close handshake (it waits briefly for the peer's echo) and
// the caller — the rotate endpoint — must not block on slow clients.
func (s *Server) closeAllWS(code websocket.StatusCode, reason string) {
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		conns = append(conns, c)
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		go func(c *websocket.Conn) { _ = c.Close(code, reason) }(c)
	}
}

// broadcastWS fans one message out to every live WebSocket connection. Writes
// run concurrently with their own timeout so one dead or slow client can never
// stall the caller or the other clients; connection cleanup stays with the
// per-connection read loop (unregisterWS). Used for goal events only — turn
// traffic keeps its per-turn subscriber fanout.
func (s *Server) broadcastWS(msg ServerMessage) {
	s.wsMu.Lock()
	writers := make([]*wsWriter, 0, len(s.wsConns))
	for _, w := range s.wsConns {
		writers = append(writers, w)
	}
	s.wsMu.Unlock()
	for _, w := range writers {
		go func(w *wsWriter) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = w.write(ctx, msg)
		}(w)
	}
}

type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) write(ctx context.Context, msg ServerMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return wsjson.Write(ctx, w.conn, msg)
}

func readWS(ctx context.Context, conn *websocket.Conn, out chan<- ClientMessage, errc chan<- error) {
	defer close(out)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			errc <- err
			return
		}
		msg, err := decodeClientMessage(data)
		if err != nil {
			errc <- err
			return
		}
		select {
		case <-ctx.Done():
			return
		case out <- msg:
		}
	}
}

func (s *Server) handleWSMessage(ctx context.Context, writer *wsWriter, msg ClientMessage) error {
	switch msg.Type {
	case "list":
		return s.writeState(ctx, writer)
	case "create_session":
		session, err := s.core.CreateSession(ctx, core.CreateSessionRequest{
			AgentName:                      msg.AgentName,
			Origin:                         store.OriginWeb,
			Provider:                       msg.Provider,
			Profile:                        msg.Profile,
			Model:                          msg.Model,
			Effort:                         msg.Effort,
			PermissionMode:                 msg.PermissionMode,
			ProjectID:                      msg.ProjectID,
			CreatePlanBeforeImplementation: msg.CreatePlanBeforeImplementation,
		})
		if err != nil {
			return err
		}
		if err := writer.write(ctx, ServerMessage{Type: "session", RequestID: msg.RequestID, Session: &session}); err != nil {
			return err
		}
		history, err := s.core.History(ctx, session.ID)
		if err != nil {
			return err
		}
		return writer.write(ctx, ServerMessage{Type: "history", RequestID: msg.RequestID, History: history})
	case "send_turn":
		go s.runWSTurn(ctx, writer, msg)
		return nil
	case "start_interview":
		// "Get to know me": create an interview session and open it with the
		// USER.md interview prompt. Subsequent answer turns arrive as normal
		// send_turn messages from the interview panel.
		if msg.AgentName == "" {
			return errors.New("agent_name is required")
		}
		session, err := s.core.CreateSession(ctx, core.CreateSessionRequest{
			AgentName:      msg.AgentName,
			Origin:         store.OriginInterview,
			Provider:       msg.Provider,
			Profile:        msg.Profile,
			Model:          msg.Model,
			Effort:         msg.Effort,
			PermissionMode: config.PermissionApprove,
		})
		if err != nil {
			return err
		}
		s.interviews.start(session.ID)
		current, err := s.core.ReadUserProfile()
		if err != nil {
			return err
		}
		next := msg
		next.SessionID = session.ID
		next.Message = core.UserProfileInterviewPrompt(current)
		go s.runWSTurn(ctx, writer, next)
		return nil
	case "plan_approve":
		go s.runWSPlanDecision(ctx, writer, msg, "approve")
		return nil
	case "plan_feedback":
		go s.runWSPlanDecision(ctx, writer, msg, "feedback")
		return nil
	case "plan_reject":
		return s.rejectWSPlan(ctx, writer, msg)
	case "dream":
		if msg.AgentName == "" {
			return errors.New("agent_name is required")
		}
		go s.runWSDream(writer, msg)
		return nil
	case "attach_session":
		if msg.SessionID == "" {
			return errors.New("session_id is required")
		}
		attached := false
		if state, ok := s.turns.attach(msg.SessionID, writer); ok {
			attached = true
			if err := writer.write(ctx, ServerMessage{Type: "turn_state", RequestID: msg.RequestID, SessionID: msg.SessionID, TurnState: &state}); err != nil {
				return err
			}
		}
		if interview, ok := s.interviews.get(msg.SessionID); ok {
			if !attached && interview.Status != "draft" && interview.Status != "failed" {
				state, prompt, retry := s.interviews.recover(msg.SessionID)
				if err := writer.write(ctx, ServerMessage{Type: "interview_state", RequestID: msg.RequestID, SessionID: msg.SessionID, Interview: &state}); err != nil {
					return err
				}
				if retry {
					next := msg
					next.Type = "send_turn"
					next.Message = prompt
					go s.runWSTurn(ctx, writer, next)
				}
				return nil
			}
			return writer.write(ctx, ServerMessage{Type: "interview_state", RequestID: msg.RequestID, SessionID: msg.SessionID, Interview: &interview})
		}
		if session, err := s.core.GetSession(ctx, msg.SessionID); err == nil && session.Origin == store.OriginInterview {
			expired := InterviewState{SessionID: msg.SessionID, Status: "failed", Error: "Interview state expired. Start a new interview."}
			return writer.write(ctx, ServerMessage{Type: "interview_state", RequestID: msg.RequestID, SessionID: msg.SessionID, Interview: &expired})
		}
		return nil
	case "resume_interview":
		if msg.SessionID == "" {
			return errors.New("session_id is required")
		}
		session, err := s.core.GetSession(ctx, msg.SessionID)
		if err != nil {
			return err
		}
		if session.Origin != store.OriginInterview {
			return errors.New("session is not a USER.md interview")
		}
		state, prompt, retry := s.interviews.recover(msg.SessionID)
		if err := writer.write(ctx, ServerMessage{Type: "interview_state", RequestID: msg.RequestID, SessionID: msg.SessionID, Interview: &state}); err != nil {
			return err
		}
		if retry {
			next := msg
			next.Type = "send_turn"
			next.Message = prompt
			go s.runWSTurn(ctx, writer, next)
		}
		return nil
	case "stop_turn":
		if msg.SessionID == "" {
			return errors.New("session_id is required")
		}
		if !s.turns.stop(msg.SessionID) {
			if session, err := s.core.GetSession(ctx, msg.SessionID); err == nil && session.Origin == store.OriginInterview {
				return nil
			}
			return errors.New("active turn not found")
		}
		return nil
	case "update_session_settings":
		if msg.SessionID == "" {
			return errors.New("session_id is required")
		}
		session, err := s.core.UpdateSessionSettings(context.Background(), msg.SessionID, msg.Model, msg.Effort, msg.PermissionMode)
		if err != nil {
			return err
		}
		if msg.PlanMode != nil {
			session, err = s.core.SetPlanMode(context.Background(), msg.SessionID, *msg.PlanMode)
			if err != nil {
				return err
			}
			s.turns.recordSession(session)
		}
		if err := writer.write(ctx, ServerMessage{Type: "session", RequestID: msg.RequestID, SessionID: session.ID, Session: &session}); err != nil {
			return err
		}
		return s.writeState(ctx, writer)
	case "permission_decision":
		if msg.Decision == nil {
			return errors.New("permission decision is required")
		}
		decided := s.broker.decide(msg.RequestID, *msg.Decision)
		restored := s.markRoadmapPermissionResolved(ctx, msg.RequestID)
		if !decided && !restored {
			return errors.New("permission request not found")
		}
		return nil
	case "user_input_decision":
		if msg.Input == nil {
			return errors.New("user input decision is required")
		}
		decided := s.input.decide(msg.RequestID, *msg.Input)
		restored := s.markRoadmapQuestionResolved(ctx, msg.RequestID)
		if !decided && !restored {
			return errors.New("user input request not found")
		}
		return nil
	case "fallback_decision":
		if msg.FallbackDecision == nil {
			return errors.New("fallback decision is required")
		}
		if !s.fallback.decide(msg.RequestID, *msg.FallbackDecision) {
			return errors.New("fallback request not found")
		}
		return nil
	default:
		return errors.New("unknown websocket message type")
	}
}

func (s *Server) writeState(ctx context.Context, writer *wsWriter) error {
	agents, err := s.core.ListAgents(ctx)
	if err != nil {
		return err
	}
	sessions, err := s.core.ListSessions(ctx)
	if err != nil {
		return err
	}
	var usageSnaps []usage.Snapshot
	if s.usage != nil {
		usageSnaps = s.usage.Snapshots()
	}
	return writer.write(ctx, ServerMessage{Type: "state", Agents: agents, Sessions: sessions, ActiveTurns: s.turns.summaries(), Usage: usageSnaps})
}

func (s *Server) runWSTurn(ctx context.Context, writer *wsWriter, msg ClientMessage) {
	daemonCtx := context.Background()
	var session store.Session
	var err error
	if msg.SessionID == "" {
		if msg.AgentName == "" {
			_ = writer.write(ctx, ServerMessage{Type: "error", RequestID: msg.RequestID, Error: "agent_name is required"})
			return
		}
		session, err = s.core.CreateSession(daemonCtx, core.CreateSessionRequest{
			AgentName:                      msg.AgentName,
			Origin:                         store.OriginWeb,
			Provider:                       msg.Provider,
			Profile:                        msg.Profile,
			Model:                          msg.Model,
			Effort:                         msg.Effort,
			PermissionMode:                 msg.PermissionMode,
			ProjectID:                      msg.ProjectID,
			CreatePlanBeforeImplementation: msg.CreatePlanBeforeImplementation,
		})
	} else {
		session, err = s.core.GetSession(daemonCtx, msg.SessionID)
	}
	if err != nil {
		_ = writer.write(ctx, ServerMessage{Type: "error", RequestID: msg.RequestID, Error: err.Error()})
		return
	}
	_ = writer.write(ctx, ServerMessage{Type: "session", RequestID: msg.RequestID, Session: &session})
	if len(msg.AttachmentIDs) > 0 && strings.HasPrefix(strings.TrimSpace(msg.Message), "/") {
		s.writePersistedSessionError(ctx, writer, msg.RequestID, session.ID, "photos cannot be attached to slash commands")
		return
	}
	slash, err := s.core.HandleSlashCommand(daemonCtx, session.ID, msg.Message)
	if err != nil {
		s.writePersistedSessionError(ctx, writer, msg.RequestID, session.ID, err.Error())
		return
	}
	if slash.Compact {
		s.runWSCompact(ctx, writer, msg, slash.Session)
		return
	}
	if slash.Handled {
		_ = writer.write(ctx, ServerMessage{Type: "session", RequestID: msg.RequestID, Session: &slash.Session})
		_ = writer.write(ctx, ServerMessage{Type: "notice", RequestID: msg.RequestID, Notice: slash.Notice})
		_ = writer.write(ctx, ServerMessage{Type: "done", RequestID: msg.RequestID})
		_ = s.writeState(ctx, writer)
		return
	}

	turnID := uuid.NewString()
	turnCtx, cancel := context.WithCancel(context.Background())
	state, err := s.turns.start(session.ID, turnID, msg.RequestID, writer, cancel)
	if err != nil {
		cancel()
		s.writePersistedSessionError(ctx, writer, msg.RequestID, session.ID, err.Error())
		return
	}
	_ = writer.write(ctx, ServerMessage{Type: "turn_state", RequestID: msg.RequestID, SessionID: session.ID, TurnState: &state})
	defer s.turns.finish(session.ID)

	requests, unsubscribePermissions := s.broker.subscribe(turnID)
	inputs, unsubscribeInputs := s.input.subscribe(turnID)
	fallbacks, unsubscribeFallbacks := s.fallback.subscribe(turnID)
	s.broker.attachTurn(turnID, session.ID)
	defer unsubscribePermissions()
	defer unsubscribeInputs()
	defer unsubscribeFallbacks()
	defer s.broker.detachTurn(turnID)
	defer cancel()

	events, err := s.core.StreamTurn(turnCtx, session.ID, msg.Message, core.TurnOptions{
		AttachmentIDs:    msg.AttachmentIDs,
		PermissionTurnID: turnID,
		PermissionRelay:  s.broker,
		UserInputRelay:   s.input,
		FallbackRelay:    s.fallback,
	})
	if err != nil {
		if turnCtx.Err() != nil {
			return
		}
		if persisted, persistErr := s.core.AppendErrorMessage(context.Background(), session.ID, err.Error()); persistErr == nil {
			s.turns.recordMessage(session.ID, &persisted)
		}
		s.turns.fail(session.ID, err.Error())
		return
	}

	var sawDone, sawError bool
	for events != nil || requests != nil || inputs != nil || fallbacks != nil {
		select {
		case <-turnCtx.Done():
			return
		case request, ok := <-requests:
			if !ok {
				requests = nil
				continue
			}
			s.markRoadmapPermissionPending(turnCtx, session.ID, request.ID)
			s.turns.recordPermission(session.ID, &request)
		case input, ok := <-inputs:
			if !ok {
				inputs = nil
				continue
			}
			s.markRoadmapQuestionPending(turnCtx, session.ID, input.ID)
			s.turns.recordUserInput(session.ID, &input)
		case fallback, ok := <-fallbacks:
			if !ok {
				fallbacks = nil
				continue
			}
			s.turns.recordFallback(session.ID, &fallback)
		case event, ok := <-events:
			if !ok {
				events = nil
				requests = nil
				inputs = nil
				fallbacks = nil
				continue
			}
			done, failed := s.recordWSTurnEvent(turnCtx, session.ID, event)
			sawDone = sawDone || done
			sawError = sawError || failed
		}
	}
	if !sawDone && !sawError && turnCtx.Err() == nil {
		s.markRoadmapSessionFinished(turnCtx, session.ID)
	}
	s.turns.finish(session.ID)
	stateCtx, stateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stateCancel()
	_ = s.writeState(stateCtx, writer)
}

// runWSCompact runs an explicit /compact as a pseudo-turn registered in the
// active-turn hub. Registration gives it the concurrent-turn guard, stop-button
// cancelation, live progress to every attached client, and reconnect survival
// for free — it reuses the existing turn_state/notice/session/context/done/error
// messages rather than introducing a compact-specific protocol.
func (s *Server) runWSCompact(ctx context.Context, writer *wsWriter, msg ClientMessage, session store.Session) {
	turnID := uuid.NewString()
	compactCtx, cancel := context.WithCancel(context.Background())
	state, err := s.turns.start(session.ID, turnID, msg.RequestID, writer, cancel)
	if err != nil {
		cancel()
		_ = writer.write(ctx, ServerMessage{
			Type:      "error",
			RequestID: msg.RequestID,
			SessionID: session.ID,
			Error:     "a turn is already running — compact when it finishes",
		})
		return
	}
	defer cancel()

	_ = writer.write(ctx, ServerMessage{Type: "turn_state", RequestID: msg.RequestID, SessionID: session.ID, TurnState: &state})
	_ = writer.write(ctx, ServerMessage{Type: "notice", RequestID: msg.RequestID, SessionID: session.ID, Notice: "Compacting conversation…"})

	updated, err := s.core.CompactSession(compactCtx, session.ID)
	if err != nil {
		// fail() is a no-op if the user stopped the turn (hub already deleted it),
		// so a cancel does not surface as an error.
		s.turns.fail(session.ID, "Compaction failed: "+err.Error())
		return
	}

	// Broadcast the reset before finish() deletes the turn and its subscribers.
	s.turns.recordSession(updated)
	s.turns.recordContext(session.ID, 0, updated.ContextLimit)
	_ = writer.write(ctx, ServerMessage{
		Type:      "notice",
		RequestID: msg.RequestID,
		SessionID: session.ID,
		Notice:    "Conversation compacted — the next turn continues from a summary plus recent messages.",
	})
	s.turns.finish(session.ID)

	stateCtx, stateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stateCancel()
	_ = s.writeState(stateCtx, writer)
}

func (s *Server) writePersistedSessionError(ctx context.Context, writer *wsWriter, requestID, sessionID, content string) {
	if persisted, err := s.core.AppendErrorMessage(context.Background(), sessionID, content); err == nil {
		_ = writer.write(ctx, ServerMessage{
			Type:      "message",
			RequestID: requestID,
			SessionID: sessionID,
			Message:   &persisted,
		})
	}
	_ = writer.write(ctx, ServerMessage{
		Type:      "error",
		RequestID: requestID,
		SessionID: sessionID,
		Error:     content,
	})
}

func (s *Server) runWSPlanDecision(ctx context.Context, writer *wsWriter, msg ClientMessage, action string) {
	if msg.SessionID == "" {
		_ = writer.write(ctx, ServerMessage{Type: "error", RequestID: msg.RequestID, Error: "session_id is required"})
		return
	}
	var decision core.PlanDecision
	var err error
	switch action {
	case "approve":
		decision, err = s.core.ApprovePlan(context.Background(), msg.SessionID)
	case "feedback":
		decision, err = s.core.FeedbackPlan(context.Background(), msg.SessionID, msg.Feedback)
	default:
		err = errors.New("unknown plan action")
	}
	if err != nil {
		s.writePersistedSessionError(ctx, writer, msg.RequestID, msg.SessionID, err.Error())
		return
	}
	_ = writer.write(ctx, ServerMessage{Type: "session", RequestID: msg.RequestID, SessionID: decision.Session.ID, Session: &decision.Session, NextMessage: decision.NextMessage})
	stateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.writeState(stateCtx, writer)
	if strings.TrimSpace(decision.NextMessage) == "" {
		return
	}
	next := msg
	next.Message = decision.NextMessage
	next.AgentName = ""
	next.SessionID = decision.Session.ID
	s.runWSTurn(ctx, writer, next)
}

func (s *Server) rejectWSPlan(ctx context.Context, writer *wsWriter, msg ClientMessage) error {
	if msg.SessionID == "" {
		return errors.New("session_id is required")
	}
	session, err := s.core.RejectPlan(context.Background(), msg.SessionID)
	if err != nil {
		return err
	}
	if err := writer.write(ctx, ServerMessage{Type: "session", RequestID: msg.RequestID, SessionID: session.ID, Session: &session}); err != nil {
		return err
	}
	return s.writeState(ctx, writer)
}

// runWSDream runs a manual dream and streams its phases to the requesting
// connection so the dream-sequence overlay can animate. It uses a daemon-scoped
// context so closing the overlay (which ends the request context) does not cancel
// the dream mid-flight. The terminal message carries the finished journal row.
func (s *Server) runWSDream(writer *wsWriter, msg ClientMessage) {
	daemonCtx := context.Background()
	emit := func(phase string, dream *store.Dream) {
		_ = writer.write(daemonCtx, ServerMessage{
			Type:       "dream_state",
			RequestID:  msg.RequestID,
			AgentName:  msg.AgentName,
			DreamPhase: phase,
			Dream:      dream,
		})
	}
	res, err := s.core.DreamAgent(daemonCtx, msg.AgentName, core.DreamOptions{
		Trigger: store.DreamManual,
		OnPhase: func(phase string) {
			// The terminal phases are emitted below with their payload.
			switch phase {
			case core.DreamPhaseDone, core.DreamPhaseNoop, core.DreamPhaseError:
				return
			}
			emit(phase, nil)
		},
	})
	if err != nil {
		_ = writer.write(daemonCtx, ServerMessage{
			Type:       "dream_state",
			RequestID:  msg.RequestID,
			AgentName:  msg.AgentName,
			DreamPhase: core.DreamPhaseError,
			Error:      err.Error(),
		})
		return
	}
	if res.NoOp {
		emit(core.DreamPhaseNoop, nil)
	} else {
		dream := res.Dream
		emit(core.DreamPhaseDone, &dream)
	}
	// Refresh shared state so DreamedAt and session lists update everywhere.
	stateCtx, cancel := context.WithTimeout(daemonCtx, 5*time.Second)
	defer cancel()
	_ = s.writeState(stateCtx, writer)
}

// sessionUsageEstimate converts a session's cumulative billed tokens into an
// estimated share of the 5-hour and weekly limits. Returns nil when no meter is
// wired (e.g. bare test servers).
func (s *Server) sessionUsageEstimate(sess store.Session) *tokenmeter.Estimate {
	if s.tokenMeter == nil {
		return nil
	}
	est := s.tokenMeter.Estimate(sess.Provider, sess.Profile, sess.Usage.Total())
	return &est
}

// turnSessionUsage builds the current usage estimate for a session by id,
// tolerating a missing session or meter (returns nil).
func (s *Server) turnSessionUsage(ctx context.Context, sessionID string) *tokenmeter.Estimate {
	if s.tokenMeter == nil {
		return nil
	}
	sess, err := s.core.GetSession(ctx, sessionID)
	if err != nil {
		return nil
	}
	return s.sessionUsageEstimate(sess)
}

func (s *Server) recordWSTurnEvent(ctx context.Context, sessionID string, event core.TurnEvent) (bool, bool) {
	switch event.Kind {
	case "message_stored":
		s.turns.recordMessage(sessionID, event.Message)
	case adapter.EventReasoningDelta:
		s.turns.recordReasoning(sessionID, event.Content, false)
	case adapter.EventReasoningMessage:
		s.turns.recordReasoning(sessionID, event.Content, true)
	case adapter.EventAssistantDelta:
		s.turns.recordDelta(sessionID, event.Content)
	case adapter.EventAssistantMessage:
		s.turns.recordAssistant(sessionID, event.Content)
	case adapter.EventPermissionRequest:
		if event.PermissionRequest != nil {
			s.markRoadmapPermissionPending(ctx, sessionID, event.PermissionRequest.ID)
		}
		s.turns.recordPermission(sessionID, event.PermissionRequest)
	case adapter.EventUserInputRequest:
		if event.UserInputRequest != nil {
			s.markRoadmapQuestionPending(ctx, sessionID, event.UserInputRequest.ID)
		}
		s.turns.recordUserInput(sessionID, event.UserInputRequest)
	case adapter.EventContextStatus:
		if event.ContextStatus != nil {
			s.turns.recordContext(sessionID, event.ContextStatus.UsedTokens, event.ContextStatus.MaxTokens)
		}
	case adapter.EventTurnUsage:
		if event.Usage != nil {
			s.turns.recordSessionUsage(sessionID, s.turnSessionUsage(ctx, sessionID))
		}
	case adapter.EventNativeAgentActivity:
		s.turns.recordNativeAgentActivity(sessionID, event.NativeAgent)
	case adapter.EventAuthRequired:
		s.turns.recordAuthRequired(sessionID, event.AuthRequired)
	case adapter.EventTurnDone:
		s.markRoadmapSessionFinished(ctx, sessionID)
		s.turns.finish(sessionID)
		return true, false
	case "error":
		s.turns.fail(sessionID, event.Content)
		return false, true
	}
	return false, false
}

func (s *Server) writeTurnEvent(ctx context.Context, writer *wsWriter, requestID, sessionID string, event core.TurnEvent) error {
	switch event.Kind {
	case "message_stored":
		return writer.write(ctx, ServerMessage{Type: "message", RequestID: requestID, Message: event.Message})
	case adapter.EventReasoningDelta:
		return writer.write(ctx, ServerMessage{Type: "reasoning_delta", RequestID: requestID, Delta: event.Content})
	case adapter.EventReasoningMessage:
		return writer.write(ctx, ServerMessage{Type: "reasoning", RequestID: requestID, Delta: event.Content})
	case adapter.EventAssistantDelta:
		return writer.write(ctx, ServerMessage{Type: "delta", RequestID: requestID, Delta: event.Content})
	case adapter.EventAssistantMessage:
		return writer.write(ctx, ServerMessage{Type: "assistant", RequestID: requestID, Delta: event.Content})
	case adapter.EventPermissionRequest:
		if event.PermissionRequest != nil {
			s.markRoadmapPermissionPending(ctx, sessionID, event.PermissionRequest.ID)
		}
		return writer.write(ctx, ServerMessage{Type: "permission_request", RequestID: requestID, Request: event.PermissionRequest})
	case adapter.EventUserInputRequest:
		if event.UserInputRequest != nil {
			s.markRoadmapQuestionPending(ctx, sessionID, event.UserInputRequest.ID)
		}
		return writer.write(ctx, ServerMessage{Type: "user_input_request", RequestID: requestID, Input: event.UserInputRequest})
	case adapter.EventContextStatus:
		if event.ContextStatus == nil {
			return nil
		}
		return writer.write(ctx, ServerMessage{Type: "context", RequestID: requestID, SessionID: sessionID, Context: &ContextUsage{Used: event.ContextStatus.UsedTokens, Max: event.ContextStatus.MaxTokens}})
	case adapter.EventTurnUsage:
		est := s.turnSessionUsage(ctx, sessionID)
		if est == nil {
			return nil
		}
		return writer.write(ctx, ServerMessage{Type: "session_usage", RequestID: requestID, SessionID: sessionID, SessionUsage: est})
	case adapter.EventNativeAgentActivity:
		return writer.write(ctx, ServerMessage{Type: "native_agent_activity", RequestID: requestID, SessionID: sessionID, NativeAgent: event.NativeAgent})
	case adapter.EventAuthRequired:
		return writer.write(ctx, ServerMessage{Type: "auth_required", RequestID: requestID, SessionID: sessionID, AuthRequired: event.AuthRequired})
	case adapter.EventTurnDone:
		s.markRoadmapSessionFinished(ctx, sessionID)
		return writer.write(ctx, ServerMessage{Type: "done", RequestID: requestID})
	case "error":
		return writer.write(ctx, ServerMessage{Type: "error", RequestID: requestID, Error: event.Content})
	default:
		return nil
	}
}
