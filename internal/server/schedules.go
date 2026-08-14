package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	Webhook       bool            `json:"webhook"`
	RunPermission string          `json:"run_permission"`
	AllowedTools  []string        `json:"allowed_tools"`
	GoalID        string          `json:"goal_id"`
	// CreatedBySession/CreatedByAgent are set by podiom_create_schedule from the
	// calling session's own identity, not by the model. The web UI and CLI leave
	// them empty, which is what marks a schedule as human-authored.
	CreatedBySession string `json:"created_by_session,omitempty"`
	CreatedByAgent   string `json:"created_by_agent,omitempty"`
	Body             string `json:"body"`
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
			Name:     req.Name,
			Agent:    strings.TrimSpace(req.Agent),
			Provider: req.Provider,
			Profile:  strings.TrimSpace(req.Profile),
			Model:    strings.TrimSpace(req.Model),
			Effort:   strings.TrimSpace(req.Effort),
			Cron:     strings.TrimSpace(req.Cron),
			Every:    strings.TrimSpace(req.Every),
			Webhook:  req.Webhook,
			// WebhookSecret is deliberately not taken from the request: the
			// scheduler mints it, so a caller cannot install a weak one.
			RunPermission: schedule.RunPermission(strings.TrimSpace(req.RunPermission)),
			AllowedTools:  req.AllowedTools,
			GoalID:        strings.TrimSpace(req.GoalID),

			CreatedBySession: strings.TrimSpace(req.CreatedBySession),
			CreatedByAgent:   strings.TrimSpace(req.CreatedByAgent),
			Body:             req.Body,
		})
		writeJSON(w, status, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// scheduleUpdateRequest patches one schedule. Pointer fields mean "leave this
// alone" when absent, so a caller never has to restate the whole file. Name and
// goal_id are absent on purpose — see schedule.UpdateParams.
type scheduleUpdateRequest struct {
	Agent         *string          `json:"agent,omitempty"`
	Provider      *config.Provider `json:"provider,omitempty"`
	Profile       *string          `json:"profile,omitempty"`
	Model         *string          `json:"model,omitempty"`
	Effort        *string          `json:"effort,omitempty"`
	Cron          *string          `json:"cron,omitempty"`
	Every         *string          `json:"every,omitempty"`
	Webhook       *bool            `json:"webhook,omitempty"`
	RunPermission *string          `json:"run_permission,omitempty"`
	AllowedTools  *[]string        `json:"allowed_tools,omitempty"`
	Enabled       *bool            `json:"enabled,omitempty"`
	Body          *string          `json:"body,omitempty"`
}

// handleSchedule handles /api/schedules/<name> (GET read, PATCH update, DELETE
// remove), /api/schedules/<name>/run, and /api/schedules/<name>/webhook.
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
		switch r.Method {
		case http.MethodGet:
			status, err := s.scheduler.Status(r.Context(), name)
			writeJSON(w, status, err)
			return
		case http.MethodPatch, http.MethodPut:
			var req scheduleUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			params := schedule.UpdateParams{
				Agent:        req.Agent,
				Provider:     req.Provider,
				Profile:      req.Profile,
				Model:        req.Model,
				Effort:       req.Effort,
				Cron:         req.Cron,
				Every:        req.Every,
				Webhook:      req.Webhook,
				AllowedTools: req.AllowedTools,
				Enabled:      req.Enabled,
				Body:         req.Body,
			}
			if req.RunPermission != nil {
				perm := schedule.RunPermission(strings.TrimSpace(*req.RunPermission))
				params.RunPermission = &perm
			}
			status, err := s.scheduler.Update(r.Context(), name, params)
			writeJSON(w, status, err)
			return
		case http.MethodDelete:
			// handled below
		default:
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
	case "webhook":
		s.handleScheduleWebhook(w, r, name)
	default:
		http.Error(w, "unknown schedule action", http.StatusNotFound)
	}
}

// webhookBodyLimit caps how much of an inbound webhook request is read. This
// endpoint is reachable without the gateway token, and the server sets no
// global body limit, so the cap is enforced here.
const webhookBodyLimit = 64 << 10

// handleScheduleWebhook fires a schedule from an external POST. It is the one
// write endpoint that does not require the gateway token (see
// scheduleWebhookRoute); authorization is the schedule's own secret, presented
// as the X-Podiom-Webhook-Secret header, a bearer token, or a ?secret= query
// parameter — the last because plenty of senders can only be given a URL.
//
// The request body reaches the agent as part of its task, so the run can react
// to what fired it.
func (s *Server) handleScheduleWebhook(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	secret := webhookSecretFrom(r)
	sched, run, err := s.scheduler.PrepareWebhookRun(r.Context(), name, secret)
	switch {
	case errors.Is(err, schedule.ErrWebhookUnauthorized):
		// Deliberately identical for a bad secret, an unknown schedule, and a
		// schedule with no webhook trigger: this endpoint must not tell an
		// unauthenticated caller which schedules exist. The scheduler logs which
		// it actually was.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case errors.Is(err, schedule.ErrWebhookDisabled):
		http.Error(w, "schedule disabled", http.StatusConflict)
		return
	case err != nil:
		writeJSON(w, nil, err)
		return
	}

	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookBodyLimit))
	if err != nil {
		http.Error(w, "webhook payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	// The run outlives this request: an agent session takes minutes and the
	// sender will not wait. Answer with the run id so the caller can follow it.
	go s.scheduler.ExecuteWebhookRun(sched, run, string(payload))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(run)
}

// webhookSecretFrom pulls the schedule secret out of a webhook request, in
// order of preference: a dedicated header, a bearer token, then the query
// string for senders that can only be handed a URL.
func webhookSecretFrom(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Podiom-Webhook-Secret")); v != "" {
		return v
	}
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("secret"))
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
