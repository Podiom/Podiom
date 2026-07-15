package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

// notifyKindGoalQuestion is the attention kind for a deferred goal question.
const notifyKindGoalQuestion = "goal_question"

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
	if res.Goal != nil {
		s.notifyGoal(notifyKindGoalQuestion, *res.Goal, res.Question.SessionID,
			res.Goal.LeadAgent+" needs an answer",
			"“"+res.Goal.Title+"” — answer the question to continue.")
	} else {
		s.notifyScheduleQuestion(res.Question)
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

// notifyScheduleQuestion fires an out-of-app notification for a scheduled run's
// deferred question, off the hot path.
func (s *Server) notifyScheduleQuestion(q store.AgentQuestion) {
	if s.notifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.notifier.Notify(ctx, notify.Notification{
			SessionID: q.SessionID,
			Title:     "A scheduled task needs an answer",
			Body:      "Schedule “" + q.RefID + "” asked a question — answer it to guide the next run.",
			Kind:      notifyKindGoalQuestion,
		})
	}()
}
