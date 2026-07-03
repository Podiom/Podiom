package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

type planSubmitRequest struct {
	FilePath string `json:"file_path"`
	Markdown string `json:"markdown"`
	TurnID   string `json:"turn_id,omitempty"`
}

type planFeedbackRequest struct {
	Feedback string `json:"feedback"`
}

type planStatusResponse struct {
	SessionID string          `json:"session_id"`
	State     store.PlanState `json:"state"`
	Explicit  bool            `json:"explicit"`
	Plan      store.PlanInfo  `json:"plan"`
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/plans/")
	sessionID, action, _ := strings.Cut(rest, "/")
	if sessionID == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	switch action {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		session, err := s.core.GetSession(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, planStatus(session), nil)
	case "status":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		session, err := s.core.GetSession(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, planStatus(session), nil)
	case "submit":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req planSubmitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session, err := s.core.SubmitPlan(r.Context(), core.SubmitPlanRequest{
			SessionID: sessionID,
			FilePath:  req.FilePath,
			Markdown:  req.Markdown,
		})
		if err == nil {
			s.turns.recordSession(session)
		}
		writeJSON(w, session, err)
	case "approve":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		decision, err := s.core.ApprovePlan(r.Context(), sessionID)
		if err == nil {
			s.turns.recordSession(decision.Session)
		}
		writeJSON(w, decision, err)
	case "feedback":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req planFeedbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		decision, err := s.core.FeedbackPlan(r.Context(), sessionID, req.Feedback)
		if err == nil {
			s.turns.recordSession(decision.Session)
		}
		writeJSON(w, decision, err)
	case "reject":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		session, err := s.core.RejectPlan(r.Context(), sessionID)
		if err == nil {
			s.turns.recordSession(session)
		}
		writeJSON(w, session, err)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func planStatus(session store.Session) planStatusResponse {
	return planStatusResponse{
		SessionID: session.ID,
		State:     session.PlanState,
		Explicit:  session.PlanExplicit,
		Plan:      session.PlanInfo,
	}
}
