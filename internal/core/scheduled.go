package core

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// AllowListRelay resolves permission requests for unattended runs without a
// human: it allows tools whose name is on the allow-list and denies everything
// else. An empty allow-list denies all side-effecting actions — the stricter
// default for scheduled routines (R7.8). It is the in-process decider Codex (and
// the fake adapter) consult; Claude enforces the same allow-list natively via
// `--allowedTools` (§7.7).
type AllowListRelay struct {
	allowed map[string]bool
	log     *slog.Logger
}

// NewAllowListRelay builds a relay from a list of permitted tool names.
func NewAllowListRelay(tools []string, loggers ...*slog.Logger) *AllowListRelay {
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return &AllowListRelay{allowed: allowed, log: log}
}

// RequestPermission decides immediately from the allow-list; it never blocks on
// a human and ignores the timeout.
func (r *AllowListRelay) RequestPermission(_ context.Context, req adapter.PermissionRequest, _ time.Duration) (adapter.PermissionDecision, error) {
	if r.allowed[req.ToolName] {
		r.log.Info("permission auto-decided", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName, "decision", "allow", "unattended", true)
		return adapter.PermissionDecision{Behavior: "allow", UpdatedInput: req.Input}, nil
	}
	r.log.Info("permission auto-denied", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName, "decision", "deny", "unattended", true, "reason", "not_in_allow_list")
	return adapter.PermissionDecision{Behavior: "deny", Message: "not in preapproved allow-list"}, nil
}

// ScheduledRunRequest describes one unattended run of a schedule against an
// agent. The body becomes the user turn layered on the agent's standing identity
// (R7.3a).
type ScheduledRunRequest struct {
	ScheduleName string
	RunID        string
	AgentName    string
	Provider     config.Provider
	Profile      string
	Model        string
	Effort       string
	// Yolo runs with whole-machine auto-approval. When false the run is
	// preapproved: approve mode plus the AllowedTools allow-list.
	Yolo         bool
	AllowedTools []string
	Task         string
	// GoalID links this run to a goal. A goal-linked run is always forced yolo
	// (part of the goal's autonomous chain) and its tool calls are recorded on
	// the goal timeline. It also supplies the project when ProjectID is empty.
	GoalID string
	// ProjectID binds the run's session to a project, so the run works in the
	// project directory and receives the project's standing instructions. It comes
	// from the schedule file's `project:` field.
	ProjectID string
}

// RunScheduled creates a durable schedule-origin session (R7.9), runs the task
// as one normal Podiom turn with the unattended permission policy, and returns
// the created session. A non-nil error means the turn itself failed.
func (c *Core) RunScheduled(ctx context.Context, req ScheduledRunRequest) (store.Session, error) {
	// A goal-linked schedule run is always yolo (part of the goal's autonomous
	// chain), even if the schedule file's run_permission is still preapproved —
	// defense in depth for older files created before goals normalized to yolo.
	if req.GoalID != "" {
		req.Yolo = true
		// A goal-linked schedule file written before `project:` existed carries no
		// project of its own, so fall back to the goal's: the whole goal chain
		// should share one workspace. Create stamps the project into new files, so
		// this only covers older ones — no migration needed. The lookup error is
		// swallowed deliberately: schedule files have no foreign key to goals and
		// DeleteGoal leaves them on disk, so a dangling goal_id must keep running.
		if strings.TrimSpace(req.ProjectID) == "" {
			if goal, err := c.store.GetGoal(ctx, req.GoalID); err == nil {
				req.ProjectID = c.goalProjectID(goal)
			}
		}
	}
	permission := config.PermissionApprove
	var relay adapter.PermissionRelay
	allowed := req.AllowedTools
	if req.Yolo {
		permission = config.PermissionYolo
		allowed = nil
	} else {
		relay = NewAllowListRelay(req.AllowedTools, c.log)
	}

	sess, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName:      req.AgentName,
		Origin:         store.OriginSchedule,
		Provider:       req.Provider,
		Profile:        req.Profile,
		Model:          req.Model,
		Effort:         req.Effort,
		PermissionMode: permission,
		ScheduleID:     req.ScheduleName,
		RunID:          req.RunID,
		GoalID:         req.GoalID,
		ProjectID:      req.ProjectID,
	})
	if err != nil {
		return store.Session{}, err
	}
	if req.GoalID != "" {
		if named, nameErr := c.store.UpdateSessionMetadata(ctx, sess.ID, "Schedule: "+req.ScheduleName, "Goal schedule execution.", false); nameErr == nil {
			sess = named
		}
	}

	events, err := c.StreamTurn(ctx, sess.ID, req.Task, TurnOptions{
		PermissionTurnID: req.RunID,
		PermissionRelay:  relay,
		Unattended:       true,
		AllowedTools:     allowed,
	})
	if err != nil {
		return sess, err
	}

	var turnErr string
	for event := range events {
		if event.Kind == "error" {
			turnErr = event.Content
		}
	}
	if turnErr != "" {
		return sess, &ScheduledRunError{Message: turnErr}
	}
	return sess, nil
}

// ScheduledRunError reports that an unattended turn produced an error event.
type ScheduledRunError struct {
	Message string
}

func (e *ScheduledRunError) Error() string { return e.Message }
