package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/schedule"
	"github.com/Podiom/Podiom/internal/store"
)

// scheduleListItem is a schedule's status with its pending deferred question (a
// question a run asked the user via podiom_ask_user), mirroring goalListItem.
type scheduleListItem struct {
	schedule.Status
	PendingQuestion *store.AgentQuestion `json:"pending_question,omitempty"`
}

type scheduleCreateRequest struct {
	Name          string          `json:"name"`
	Agent         string          `json:"agent"`
	Provider      config.Provider `json:"provider"`
	Profile       string          `json:"profile"`
	Model         string          `json:"model"`
	Effort        string          `json:"effort"`
	Cron          string          `json:"cron"`
	Every         string          `json:"every"`
	RunPermission string          `json:"run_permission"`
	AllowedTools  []string        `json:"allowed_tools"`
	GoalID        string          `json:"goal_id"`
	Body          string          `json:"body"`
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
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		items := make([]scheduleListItem, 0, len(statuses))
		for _, st := range statuses {
			item := scheduleListItem{Status: st}
			if s.core != nil && st.Name != "" {
				if q, qerr := s.core.PendingAgentQuestion(r.Context(), store.AgentQuestionSchedule, st.Name); qerr == nil {
					item.PendingQuestion = q
				}
			}
			items = append(items, item)
		}
		writeJSON(w, items, nil)
	case http.MethodPost:
		var req scheduleCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, err := s.scheduler.Create(r.Context(), schedule.CreateParams{
			Name:          req.Name,
			Agent:         strings.TrimSpace(req.Agent),
			Provider:      req.Provider,
			Profile:       strings.TrimSpace(req.Profile),
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
		// Drop any deferred questions this schedule accumulated (the file is gone;
		// there is no FK to cascade from).
		if s.core != nil {
			if err := s.core.DeleteAgentQuestions(r.Context(), store.AgentQuestionSchedule, name); err != nil {
				s.log.Warn("delete schedule questions failed", "event", "question", "schedule", name, "err", err)
			}
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

// deleteGoalSchedules removes every schedule file linked to a goal. Goal-linked
// schedules are authored by the goal's plan; once the goal is no longer active
// (done, abandoned, or deleted) they have nothing left to serve, so they are
// unregistered and their files removed. The sessions those schedules produced
// are preserved for audit. Failures are logged, never fatal: cleaning up
// schedules must not block the goal state change that triggered it.
func (s *Server) deleteGoalSchedules(ctx context.Context, goalID string) {
	if s.scheduler == nil || strings.TrimSpace(goalID) == "" {
		return
	}
	statuses, err := s.scheduler.List(ctx)
	if err != nil {
		s.log.Warn("goal schedule cleanup failed", "event", "goal", "goal", goalID, "stage", "list", "err", err)
		return
	}
	for _, st := range statuses {
		if st.GoalID != goalID {
			continue
		}
		if err := s.scheduler.Delete(ctx, st.Name); err != nil {
			s.log.Warn("goal schedule cleanup failed", "event", "goal", "goal", goalID, "schedule", st.Name, "err", err)
			continue
		}
		s.log.Info("goal schedule removed", "event", "goal", "goal", goalID, "schedule", st.Name)
	}
}
