package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/Podiom/Podiom/internal/tokenmeter"
)

// sessionContext is the agent-facing view of its own session: who it is, why the
// session exists, what it is linked to in both directions, and how much room is
// left.
//
// It deliberately omits History and RollingSummary. The agent already has the
// conversation; replaying it would cost thousands of tokens to tell the agent
// what it just said. That omission is also why this lives on its own route
// rather than on /api/sessions/<id>: the coverage guardrail matches route
// prefixes, so wrapping the sessions family would mark session creation and
// deletion as agent-manageable, which they are not.
type sessionContext struct {
	SessionID string              `json:"session_id"`
	AgentName string              `json:"agent_name"`
	Name      string              `json:"name,omitempty"`
	Origin    store.SessionOrigin `json:"origin"`
	// Unattended is derived, not stored: a schedule, roadmap, or goal run has no
	// user watching, so a question asked in the reply reaches nobody. Making it a
	// fact the agent can read beats repeating the rule in prose across several
	// tool descriptions.
	Unattended bool   `json:"unattended"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`

	Provider       config.Provider       `json:"provider"`
	Profile        string                `json:"profile,omitempty"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode"`
	PlanState      store.PlanState       `json:"plan_state,omitempty"`

	// What brought this session into being.
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	TaskTitle   string `json:"task_title,omitempty"`
	GoalID      string `json:"goal_id,omitempty"`
	GoalTitle   string `json:"goal_title,omitempty"`
	ScheduleID  string `json:"schedule_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`

	SourceControlWarning string `json:"source_control_warning,omitempty"`

	// What this session created.
	CreatedTasks     []string `json:"created_tasks,omitempty"`
	CreatedSchedules []string `json:"created_schedules,omitempty"`

	ContextTokens int64                `json:"context_tokens"`
	ContextLimit  int64                `json:"context_limit"`
	Usage         store.SessionUsage   `json:"usage"`
	UsageEstimate *tokenmeter.Estimate `json:"usage_estimate,omitempty"`
}

// unattendedOrigins are the origins with no user watching the run.
var unattendedOrigins = map[store.SessionOrigin]bool{
	store.OriginSchedule: true,
	store.OriginRoadmap:  true,
	store.OriginGoal:     true,
}

// handleSessionContext serves the session-scoped context endpoint backing the
// podiom_session_context MCP tool.
//
// The session id is in the path rather than a request body because the helper is
// launched with it fixed: an agent can only ever describe the session it is
// running in, never anyone else's.
func (s *Server) handleSessionContext(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	sessionID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/session-context/"), "/")
	if sessionID == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPatch {
		var req struct {
			ProjectID *string `json:"project_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ProjectID == nil {
			http.Error(w, "project_id is required", http.StatusBadRequest)
			return
		}
		session, err := s.core.UpdateSessionProject(r.Context(), sessionID, *req.ProjectID)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		// The MCP call does not travel through the browser socket, so every open
		// client needs the durable update pushed explicitly.
		s.broadcastWS(ServerMessage{Type: "session", SessionID: session.ID, Session: &session})
		s.broadcastWS(ServerMessage{Type: "context", SessionID: session.ID, Context: &ContextUsage{Used: 0, Max: session.ContextLimit}})
		writeJSON(w, map[string]any{
			"session": session,
			"message": "Project updated. The current turn remains in its existing workspace; the new project context applies from the next turn.",
		}, nil)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, err := s.core.GetSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}

	out := sessionContext{
		SessionID:            session.ID,
		AgentName:            session.AgentName,
		Name:                 session.Name,
		Origin:               session.Origin,
		Unattended:           unattendedOrigins[session.Origin],
		CreatedAt:            session.CreatedAt,
		UpdatedAt:            session.UpdatedAt,
		Provider:             session.Provider,
		Profile:              session.Profile,
		Model:                session.Model,
		Effort:               session.Effort,
		PermissionMode:       session.PermissionMode,
		PlanState:            session.PlanState,
		ProjectID:            session.ProjectID,
		TaskID:               session.TaskID,
		GoalID:               session.GoalID,
		ScheduleID:           session.ScheduleID,
		RunID:                session.RunID,
		SourceControlWarning: session.SourceControlWarning,
		ContextTokens:        session.ContextTokens,
		ContextLimit:         session.ContextLimit,
		Usage:                session.Usage,
		UsageEstimate:        s.sessionUsageEstimate(session),
	}

	// Resolve the linked entities to names. Every lookup failure is swallowed:
	// context is an aid, and a dangling reference must never make the tool fail.
	if out.TaskID != "" {
		if task, err := s.core.GetTask(r.Context(), out.TaskID); err == nil {
			out.TaskTitle = task.Title
			if out.ProjectID == "" {
				out.ProjectID = task.ProjectID
			}
		}
	}
	if out.GoalID != "" {
		if goal, err := s.core.GetGoal(r.Context(), out.GoalID); err == nil {
			out.GoalTitle = goal.Title
		}
	}
	if out.ProjectID != "" {
		if project, err := s.core.GetProject(r.Context(), out.ProjectID); err == nil {
			out.ProjectName = project.Name
		}
	}

	// The upward half: what this session created. Titles and names only — the
	// agent can call podiom_get_task or podiom_get_schedule for the detail.
	if tasks, err := s.core.ListTasksCreatedBySession(r.Context(), sessionID); err == nil {
		for _, task := range tasks {
			out.CreatedTasks = append(out.CreatedTasks, task.Title)
		}
	}
	if s.scheduler != nil {
		if names, err := s.scheduler.CreatedBySession(sessionID); err == nil {
			out.CreatedSchedules = names
		}
	}

	writeJSON(w, out, nil)
}
