package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/store"
)

// goalActionDetailItems caps how many answered items the detail view carries
// behind the open ones.
const goalActionDetailItems = 20

type goalActionCreateRequest struct {
	SessionID    string `json:"session_id"`
	AgentName    string `json:"agent_name"`
	GoalID       string `json:"goal_id"`
	Title        string `json:"title"`
	Instructions string `json:"instructions"`
	Why          string `json:"why"`
}

type goalActionRespondRequest struct {
	Status   store.GoalActionItemStatus `json:"status"`
	Response string                     `json:"response"`
}

// goalActionItems assembles the detail view's carousel content: everything still
// open (oldest ask first), then the recently answered items.
func (s *Server) goalActionItems(ctx context.Context, goalID string) ([]store.GoalActionItem, error) {
	open, err := s.core.ListOpenGoalActionItems(ctx, goalID)
	if err != nil {
		return nil, err
	}
	responded, err := s.core.ListRespondedGoalActionItems(ctx, goalID, goalActionDetailItems)
	if err != nil {
		return nil, err
	}
	items := make([]store.GoalActionItem, 0, len(open)+len(responded))
	items = append(items, open...)
	items = append(items, responded...)
	return items, nil
}

// handleGoalActionItems records a step a goal's agent decided only the user can
// carry out. This is the endpoint podiom_request_user_action posts to; the run
// does not wait and the goal's reviews keep firing — the item sits on the goal
// page until the user responds, and their verdict reaches the next review.
func (s *Server) handleGoalActionItems(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req goalActionCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.core.RequestGoalAction(r.Context(), strings.TrimSpace(req.SessionID), store.GoalActionItem{
		Title:        strings.TrimSpace(req.Title),
		Instructions: strings.TrimSpace(req.Instructions),
		Why:          strings.TrimSpace(req.Why),
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.broadcastGoalEvent(res.Event)
	writeJSON(w, map[string]any{
		"status":         "recorded",
		"action_item_id": res.Item.ID,
		"message":        "Recorded and shown on the goal page. Do not wait on it and do not file it again — carry on with the rest of the goal; their verdict reaches you at your next review.",
	}, nil)
}

// handleGoalActionItem answers an action item at
// /api/goal-action-items/{id}/respond. Human-only: the agent hands work over
// with podiom_request_user_action and never reports on the user's behalf.
func (s *Server) handleGoalActionItem(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/goal-action-items/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "action item id is required", http.StatusBadRequest)
		return
	}
	if action != "respond" {
		http.Error(w, "unknown action-item action", http.StatusNotFound)
		return
	}
	var req goalActionRespondRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.core.RespondGoalActionItem(r.Context(), id, req.Status, strings.TrimSpace(req.Response))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if res.Event != nil {
		s.broadcastGoalEvent(*res.Event)
	}
	if res.Goal != nil {
		s.broadcastGoalPing(r.Context(), res.Goal.ID)
	}
	writeJSON(w, res.Item, nil)
}
