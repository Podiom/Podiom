package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/schedule"
)

type scheduleCreateRequest struct {
	Name          string   `json:"name"`
	Agent         string   `json:"agent"`
	Model         string   `json:"model"`
	Effort        string   `json:"effort"`
	Cron          string   `json:"cron"`
	Every         string   `json:"every"`
	RunPermission string   `json:"run_permission"`
	AllowedTools  []string `json:"allowed_tools"`
	GoalID        string   `json:"goal_id"`
	Body          string   `json:"body"`
}

// handleSchedules lists all schedules (GET) and creates a new schedule file
// (POST) under ~/.podiom/schedules (R7.5 / R7.2).
func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		http.Error(w, "scheduler unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		statuses, err := s.scheduler.List(r.Context())
		writeJSON(w, statuses, err)
	case http.MethodPost:
		var req scheduleCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, err := s.scheduler.Create(r.Context(), schedule.CreateParams{
			Name:          req.Name,
			Agent:         strings.TrimSpace(req.Agent),
			Model:         strings.TrimSpace(req.Model),
			Effort:        strings.TrimSpace(req.Effort),
			Cron:          strings.TrimSpace(req.Cron),
			Every:         strings.TrimSpace(req.Every),
			RunPermission: schedule.RunPermission(strings.TrimSpace(req.RunPermission)),
			AllowedTools:  req.AllowedTools,
			GoalID:        strings.TrimSpace(req.GoalID),
			Body:          req.Body,
		})
		writeJSON(w, status, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSchedule handles per-schedule actions under /api/schedules/<name>/...
// Currently: POST /api/schedules/<name>/run triggers a manual run.
func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		http.Error(w, "scheduler unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/schedules/")
	name, action, _ := strings.Cut(rest, "/")
	if name == "" {
		http.Error(w, "schedule name is required", http.StatusBadRequest)
		return
	}
	switch action {
	case "":
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.scheduler.Delete(r.Context(), name); err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, map[string]string{"deleted": name}, nil)
	case "run":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		run, err := s.scheduler.RunNow(r.Context(), name)
		writeJSON(w, run, err)
	default:
		http.Error(w, "unknown schedule action", http.StatusNotFound)
	}
}
