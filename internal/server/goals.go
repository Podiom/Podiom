package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

type goalCreateRequest struct {
	Title           string             `json:"title"`
	Description     string             `json:"description"`
	SuccessCriteria string             `json:"success_criteria"`
	Metrics         []store.GoalMetric `json:"metrics"`
	ReviewEvery     string             `json:"review_every"`
	LeadAgent       string             `json:"lead_agent"`
	ProjectID       string             `json:"project_id"`
}

// goalUpdateRequest is the PATCH body. SessionID/AgentName are the manage-tool
// provenance stamp: when present the patch is agent-originated and the
// restricted-field policy applies (§9 of the goals spec). Status transitions
// ride the same PATCH but are user-only.
type goalUpdateRequest struct {
	Title           *string             `json:"title,omitempty"`
	Description     *string             `json:"description,omitempty"`
	SuccessCriteria *string             `json:"success_criteria,omitempty"`
	Metrics         *[]store.GoalMetric `json:"metrics,omitempty"`
	ReviewEvery     *string             `json:"review_every,omitempty"`
	LeadAgent       *string             `json:"lead_agent,omitempty"`
	ProjectID       *string             `json:"project_id,omitempty"`
	Status          *string             `json:"status,omitempty"`
	StatusNote      string              `json:"status_note,omitempty"`
	SessionID       string              `json:"session_id,omitempty"`
	AgentName       string              `json:"agent_name,omitempty"`
}

type goalProgressRequest struct {
	Kind          string                  `json:"kind,omitempty"` // progress (default) | plan_change
	Body          string                  `json:"body,omitempty"`
	MetricUpdates []core.GoalMetricUpdate `json:"metric_updates,omitempty"`
	SessionID     string                  `json:"session_id,omitempty"`
}

type goalProposeCompletionRequest struct {
	ClosingReport string `json:"closing_report"`
	SessionID     string `json:"session_id,omitempty"`
}

type accessRequestCreateRequest struct {
	GoalID    string            `json:"goal_id"`
	Kind      string            `json:"kind"`
	Payload   map[string]string `json:"payload"`
	Reason    string            `json:"reason"`
	SessionID string            `json:"session_id,omitempty"`
	AgentName string            `json:"agent_name,omitempty"`
}

type accessDecisionRequest struct {
	Note string `json:"note,omitempty"`
}

// GoalDetail is the GET /api/goals/{id} response: the goal plus the audit
// surfaces the detail view renders.
type GoalDetail struct {
	Goal           store.Goal            `json:"goal"`
	Events         []store.GoalEvent     `json:"events"`
	AccessRequests []store.AccessRequest `json:"access_requests"`
}

// goalDetailEvents is how much timeline the detail endpoint returns up front;
// older entries page in via /events?before=.
const goalDetailEvents = 50

func (s *Server) handleGoals(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		goals, err := s.core.ListGoals(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")))
		if goals == nil {
			goals = []store.Goal{}
		}
		writeJSON(w, goals, err)
	case http.MethodPost:
		var req goalCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		goal, err := s.core.CreateGoal(r.Context(), store.Goal{
			Title:           strings.TrimSpace(req.Title),
			Description:     req.Description,
			SuccessCriteria: req.SuccessCriteria,
			Metrics:         req.Metrics,
			ReviewEvery:     strings.TrimSpace(req.ReviewEvery),
			LeadAgent:       strings.TrimSpace(req.LeadAgent),
			ProjectID:       strings.TrimSpace(req.ProjectID),
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		// Planning runs in the background on the daemon's own lifetime (the
		// runWSDream precedent): goal creation returns immediately, and the
		// planning session's own events land on the timeline as they happen.
		go func(goalID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if _, err := s.core.StartGoalPlanning(ctx, goalID); err != nil {
				s.log.Warn("goal planning failed", "event", "goal", "goal", goalID, "err", err)
			}
			s.broadcastGoalPing(ctx, goalID)
		}(goal.ID)
		s.broadcastGoalPing(r.Context(), goal.ID)
		writeJSON(w, goal, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGoal handles /api/goals/{id} (GET detail, PATCH, DELETE) and the
// sub-actions /events, /propose-completion, and /review.
func (s *Server) handleGoal(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/goals/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "goal id is required", http.StatusBadRequest)
		return
	}

	switch action {
	case "":
		s.handleGoalItem(w, r, id)
	case "events":
		s.handleGoalEvents(w, r, id)
	case "propose-completion":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req goalProposeCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		goal, err := s.core.ProposeGoalCompletion(r.Context(), id, req.SessionID, req.ClosingReport)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.notifyGoal(notifyKindGoalReview, goal, req.SessionID,
			goal.LeadAgent+" proposes goal completion",
			"“"+goal.Title+"” — review the closing report and confirm.")
		s.broadcastGoalPing(r.Context(), goal.ID)
		writeJSON(w, goal, nil)
	case "review":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goal, err := s.core.GetGoal(r.Context(), id)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if goal.Status != store.GoalActive {
			http.Error(w, "only an active goal can be reviewed", http.StatusBadRequest)
			return
		}
		go func(goalID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if _, err := s.core.RunGoalReview(ctx, goalID); err != nil {
				s.log.Warn("manual goal review failed", "event", "goal", "goal", goalID, "err", err)
			}
			s.broadcastGoalPing(ctx, goalID)
		}(goal.ID)
		writeJSON(w, map[string]string{"status": "review_started", "goal_id": goal.ID}, nil)
	default:
		http.Error(w, "unknown goal action", http.StatusNotFound)
	}
}

func (s *Server) handleGoalItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		goal, err := s.core.GetGoal(r.Context(), id)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		events, err := s.core.ListGoalEvents(r.Context(), id, goalDetailEvents, 0)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		requests, err := s.core.ListAccessRequests(r.Context(), id, "")
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if events == nil {
			events = []store.GoalEvent{}
		}
		if requests == nil {
			requests = []store.AccessRequest{}
		}
		writeJSON(w, GoalDetail{Goal: goal, Events: events, AccessRequests: requests}, nil)
	case http.MethodPatch:
		var req goalUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fromAgent := strings.TrimSpace(req.SessionID) != "" || strings.TrimSpace(req.AgentName) != ""
		if req.Status != nil {
			// Status transitions are user-only (§3.1): the agent's path to
			// review is propose-completion, and done is the user's call.
			if fromAgent {
				http.Error(w, "agents may not change a goal's status", http.StatusForbidden)
				return
			}
			goal, err := s.core.TransitionGoal(r.Context(), id, store.GoalStatus(*req.Status), req.StatusNote)
			if err != nil {
				writeJSON(w, nil, err)
				return
			}
			// A goal that is done or abandoned no longer drives its plan, so the
			// schedules that plan created are torn down (sessions are kept).
			if goal.Status == store.GoalDone || goal.Status == store.GoalAbandoned {
				s.deleteGoalSchedules(r.Context(), goal.ID)
			}
			s.broadcastGoalPing(r.Context(), goal.ID)
			writeJSON(w, goal, nil)
			return
		}
		goal, err := s.core.UpdateGoal(r.Context(), id, core.GoalPatch{
			Title:           req.Title,
			Description:     req.Description,
			SuccessCriteria: req.SuccessCriteria,
			Metrics:         req.Metrics,
			ReviewEvery:     req.ReviewEvery,
			LeadAgent:       req.LeadAgent,
			ProjectID:       req.ProjectID,
			FromAgent:       fromAgent,
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.broadcastGoalPing(r.Context(), goal.ID)
		writeJSON(w, goal, nil)
	case http.MethodDelete:
		if err := s.core.DeleteGoal(r.Context(), id); err != nil {
			writeJSON(w, nil, err)
			return
		}
		// The goal is gone; tear down the schedules its plan created so they
		// don't keep firing against a goal that no longer exists.
		s.deleteGoalSchedules(r.Context(), id)
		writeJSON(w, map[string]string{"status": "deleted", "id": id}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGoalEvents(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 {
			limit = goalDetailEvents
		}
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		events, err := s.core.ListGoalEvents(r.Context(), id, limit, before)
		if events == nil {
			events = []store.GoalEvent{}
		}
		writeJSON(w, events, err)
	case http.MethodPost:
		var req goalProgressRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		events, err := s.core.RecordGoalProgress(r.Context(), core.RecordGoalProgressRequest{
			GoalID:        id,
			SessionID:     req.SessionID,
			Kind:          store.GoalEventKind(req.Kind),
			Body:          req.Body,
			MetricUpdates: req.MetricUpdates,
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		for _, ev := range events {
			s.broadcastGoalEvent(ev)
		}
		writeJSON(w, events, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAccessRequests(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		reqs, err := s.core.ListAccessRequests(r.Context(), strings.TrimSpace(q.Get("goal_id")), strings.TrimSpace(q.Get("status")))
		if reqs == nil {
			reqs = []store.AccessRequest{}
		}
		writeJSON(w, reqs, err)
	case http.MethodPost:
		var req accessRequestCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created, err := s.core.CreateAccessRequest(r.Context(), core.CreateAccessRequestInput{
			GoalID:    strings.TrimSpace(req.GoalID),
			AgentName: strings.TrimSpace(req.AgentName),
			SessionID: strings.TrimSpace(req.SessionID),
			Kind:      store.AccessRequestKind(req.Kind),
			Payload:   req.Payload,
			Reason:    req.Reason,
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if goal, gerr := s.core.GetGoal(r.Context(), created.GoalID); gerr == nil {
			s.notifyGoal(notifyKindGoalAccessRequest, goal, created.SessionID,
				created.AgentName+" requests access",
				accessRequestSummary(created)+" — “"+goal.Title+"”")
		}
		s.broadcastGoalPing(r.Context(), created.GoalID)
		writeJSON(w, created, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAccessRequest handles /api/access-requests/{id}/approve and /deny.
// Decisions are human-only: there is deliberately no manage-mcp tool for this
// route, so an agent can never approve its own request.
func (s *Server) handleAccessRequest(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/access-requests/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "access request id is required", http.StatusBadRequest)
		return
	}
	if action != "approve" && action != "deny" {
		http.Error(w, "unknown access request action", http.StatusNotFound)
		return
	}
	var req accessDecisionRequest
	// An empty body is a decision without a note.
	_ = json.NewDecoder(r.Body).Decode(&req)

	decided, err := s.core.DecideAccessRequest(r.Context(), id, action == "approve", req.Note)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if action == "approve" {
		decided = s.executeAccessGrant(r.Context(), decided)
	}
	s.broadcastGoalPing(r.Context(), decided.GoalID)
	writeJSON(w, decided, nil)
}

// accessRequestSummary renders the one-line human description of what was asked
// for, used in notification bodies.
func accessRequestSummary(req store.AccessRequest) string {
	var payload map[string]string
	_ = json.Unmarshal([]byte(req.Payload), &payload)
	switch req.Kind {
	case store.AccessMCPServer:
		return "MCP server " + payload["server_name"]
	case store.AccessSkill:
		if payload["id"] != "" {
			return "skill " + payload["id"]
		}
		return "skill install"
	case store.AccessCLITool:
		return "host tool " + payload["tool"]
	case store.AccessEnvVar:
		return "credential " + payload["var_name"]
	case store.AccessPermissionMode:
		return "permission mode " + payload["mode"]
	default:
		return string(req.Kind)
	}
}

// Notification kinds for goal-scoped attention (extends permission/question).
const (
	notifyKindGoalAccessRequest = "goal_access_request"
	notifyKindGoalReview        = "goal_review"
)

// notifyGoal fires an out-of-app notification off the hot path (the
// notifyAttention precedent): push latency never delays the API response.
func (s *Server) notifyGoal(kind string, goal store.Goal, sessionID, title, body string) {
	if s.notifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.notifier.Notify(ctx, notify.Notification{
			SessionID: sessionID,
			AgentName: goal.LeadAgent,
			GoalID:    goal.ID,
			Title:     title,
			Body:      body,
			Kind:      kind,
		})
	}()
}

// broadcastGoalEvent fans one appended timeline entry out to every live
// dashboard connection.
func (s *Server) broadcastGoalEvent(ev store.GoalEvent) {
	s.broadcastWS(ServerMessage{Type: "goal_event", GoalEvent: &ev})
}

// broadcastGoalPing broadcasts the goal's newest timeline entry — the "something
// changed on goal X" signal for mutations whose events were appended inside
// core. Falls back to a bare goal_id ping when the timeline is unreadable.
func (s *Server) broadcastGoalPing(ctx context.Context, goalID string) {
	events, err := s.core.ListGoalEvents(ctx, goalID, 1, 0)
	if err != nil || len(events) == 0 {
		s.broadcastWS(ServerMessage{Type: "goal_event", GoalEvent: &store.GoalEvent{GoalID: goalID}})
		return
	}
	s.broadcastGoalEvent(events[0])
}
