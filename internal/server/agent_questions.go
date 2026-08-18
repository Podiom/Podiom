package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

type agentQuestionCreateRequest struct {
	SessionID string                    `json:"session_id"`
	AgentName string                    `json:"agent_name"`
	Questions []store.AgentQuestionItem `json:"questions"`
}

type agentQuestionAnswerRequest struct {
	Answers map[string][]string `json:"answers"`
}

// handleAgentQuestions records a question an unattended agent (goal or scheduled
// run) asked the user. This is the endpoint the podiom_ask_user tool posts to;
// the run does not wait — the question is surfaced on the goal or schedule page
// and answered later, then fed into the next run.
func (s *Server) handleAgentQuestions(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req agentQuestionCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.core.CreateAgentQuestion(r.Context(), strings.TrimSpace(req.SessionID), req.Questions)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if res.Event != nil {
		s.broadcastGoalEvent(*res.Event)
	}
	// Goal-scoped questions notify through the goal timeline hook in core. A
	// schedule-scoped one appends no goal event, so there is nothing for that hook
	// to see and the notification is published here instead.
	if res.Goal == nil {
		s.notifications.Publish(notify.Event{
			Type:         notify.TypeScheduleQuestion,
			SessionID:    res.Question.SessionID,
			ScheduleName: res.Question.RefID,
			Resource:     notify.ResourceAgentQuestion,
			ResourceID:   res.Question.ID,
		})
	}
	writeJSON(w, map[string]any{
		"status":      "recorded",
		"question_id": res.Question.ID,
		"message":     "Recorded. The user will answer before your next run; wrap up or proceed with your best judgment — do not re-ask.",
	}, nil)
}

// handleAgentQuestion answers a deferred question at
// /api/agent-questions/{id}/answer. Human-only: the agent asks via
// podiom_ask_user and never answers its own question.
func (s *Server) handleAgentQuestion(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/agent-questions/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "question id is required", http.StatusBadRequest)
		return
	}
	if action != "answer" {
		http.Error(w, "unknown agent-question action", http.StatusNotFound)
		return
	}
	var req agentQuestionAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.core.AnswerAgentQuestion(r.Context(), id, req.Answers)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if res.Event != nil {
		s.broadcastGoalEvent(*res.Event)
	}
	if res.Goal != nil {
		// Answering clears the pause; the next scheduler tick resumes reviews.
		s.broadcastGoalPing(r.Context(), res.Goal.ID)
	}
	writeJSON(w, res.Question, nil)
}
