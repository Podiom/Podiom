package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

const PlanGateMessage = "A plan must be approved before implementation — call podiom_submit_plan with your plan."

var requiredPlanHeadings = []string{
	"# Plan:",
	"## Goal",
	"## Context",
	"## Approach",
	"## Changes",
	"## Steps",
	"## Tests",
	"## Risks And Rollback",
	"## Open Questions",
}

type SubmitPlanRequest struct {
	SessionID string
	FilePath  string
	Markdown  string
}

type PlanDecision struct {
	Session     store.Session `json:"session"`
	NextMessage string        `json:"next_message,omitempty"`
}

func PlanGateActive(sess store.Session) bool {
	return sess.PlanState == store.PlanPendingSubmission || sess.PlanState == store.PlanAwaitingApproval
}

func (c *Core) SubmitPlan(ctx context.Context, req SubmitPlanRequest) (store.Session, error) {
	sess, err := c.store.GetSession(ctx, req.SessionID)
	if err != nil {
		return store.Session{}, err
	}
	markdown := strings.TrimSpace(req.Markdown)
	if markdown == "" {
		return store.Session{}, fmt.Errorf("plan markdown is required")
	}
	if err := validateStructuredPlanMarkdown(markdown); err != nil {
		return store.Session{}, err
	}
	path, err := c.validatePlanPath(ctx, sess, req.FilePath)
	if err != nil {
		return store.Session{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	submittedAt := sess.PlanInfo.SubmittedAt
	if submittedAt == "" {
		submittedAt = now
	}
	info := store.PlanInfo{
		FilePath:    path,
		Markdown:    markdown,
		SubmittedAt: submittedAt,
		UpdatedAt:   now,
	}
	updated, err := c.store.UpdateSessionPlanState(ctx, sess.ID, store.PlanAwaitingApproval, sess.PlanExplicit, info)
	if err != nil {
		return store.Session{}, err
	}
	if _, err := c.store.AppendMessages(ctx, sess.ID, []store.Message{{
		Role:    store.RoleAssistant,
		Content: "Plan submitted for approval.\n\n" + markdown,
	}}); err != nil {
		return store.Session{}, err
	}
	if _, err := c.MoveRoadmapSessionTaskToReview(ctx, sess.ID); err != nil {
		return store.Session{}, err
	}
	c.log.Info("plan submitted", "event", "plan", "session", sess.ID, "agent", sess.AgentName, "path", path)
	return updated, nil
}

func (c *Core) ApprovePlan(ctx context.Context, sessionID string) (PlanDecision, error) {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return PlanDecision{}, err
	}
	if sess.PlanState != store.PlanAwaitingApproval {
		return PlanDecision{}, fmt.Errorf("session %q is not awaiting plan approval", sessionID)
	}
	updated, err := c.store.UpdateSessionPlanState(ctx, sess.ID, store.PlanNone, false, sess.PlanInfo)
	if err != nil {
		return PlanDecision{}, err
	}
	if updated.PermissionMode == config.PermissionYolo {
		updated, err = c.store.UpdateSessionRuntime(ctx, updated.ID, updated.Provider, updated.Profile, updated.Model, updated.Effort, updated.PermissionMode, "")
		if err != nil {
			return PlanDecision{}, err
		}
	}
	c.log.Info("plan approved", "event", "plan", "session", sess.ID, "agent", sess.AgentName, "yolo", updated.PermissionMode == config.PermissionYolo)
	return PlanDecision{
		Session:     updated,
		NextMessage: "Plan approved. Proceed with implementation according to the approved plan.",
	}, nil
}

func (c *Core) FeedbackPlan(ctx context.Context, sessionID, feedback string) (PlanDecision, error) {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return PlanDecision{}, err
	}
	if sess.PlanState != store.PlanAwaitingApproval {
		return PlanDecision{}, fmt.Errorf("session %q is not awaiting plan approval", sessionID)
	}
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return PlanDecision{}, fmt.Errorf("feedback is required")
	}
	return PlanDecision{
		Session:     sess,
		NextMessage: "Please revise the implementation plan using this feedback. Keep the required structured Markdown headings, overwrite the same plan file, and call podiom_submit_plan again with the full updated Markdown:\n\n" + feedback,
	}, nil
}

func (c *Core) planModePrompt(sess store.Session, projectCtx projectExecutionContext) string {
	planDir := filepath.Join(c.paths.ProjectsDir, "<project>", "plans")
	if strings.TrimSpace(projectCtx.ProjectDir) != "" {
		planDir = filepath.Join(projectCtx.ProjectDir, "plans")
	}
	lines := []string{
		"Podiom plan mode is active for this session.",
		"",
		"Stop before implementation. You may do read-only exploration to make the plan accurate, but do not edit files, run mutating commands, install dependencies, delete files, push changes, or otherwise implement anything until the user approves the submitted plan.",
		"",
		"Write the plan as Markdown under this plans directory:",
		planDir,
		"",
		"Use a sortable, collision-resistant filename like YYYYMMDD-HHMM-<short-topic>.md.",
		"",
		"Submit the plan by calling podiom_submit_plan with file_path and markdown. The markdown argument must be the full rendered plan and must use exactly this structure:",
		"",
		structuredPlanMarkdownTemplate(),
	}
	if sess.PlanState == store.PlanAwaitingApproval {
		lines = append(lines,
			"",
			"Revision turn: incorporate the user's feedback, keep the same required Markdown structure, overwrite the same plan file when possible, and call podiom_submit_plan again with the full updated Markdown.",
		)
	}
	return strings.Join(lines, "\n")
}

func structuredPlanMarkdownTemplate() string {
	return strings.Join([]string{
		"```markdown",
		"# Plan: <short title>",
		"",
		"## Goal",
		"<What the user wants and what done means.>",
		"",
		"## Context",
		"<Relevant files, project state, constraints, and assumptions discovered so far.>",
		"",
		"## Approach",
		"<High-level implementation strategy.>",
		"",
		"## Changes",
		"- <Subsystem/file area and intended change>",
		"- <Subsystem/file area and intended change>",
		"",
		"## Steps",
		"1. <Concrete implementation step>",
		"2. <Concrete implementation step>",
		"3. <Concrete implementation step>",
		"",
		"## Tests",
		"- <Test/check to run>",
		"- <Manual verification if relevant>",
		"",
		"## Risks And Rollback",
		"<Risks, edge cases, and how to recover/revert if needed.>",
		"",
		"## Open Questions",
		"- <Only include real blockers or decisions needed from the user; otherwise write \"None.\">",
		"```",
	}, "\n")
}

func validateStructuredPlanMarkdown(markdown string) error {
	found := map[string]bool{}
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "# plan:") && strings.TrimSpace(line[strings.Index(line, ":")+1:]) != "" {
			found["# Plan:"] = true
			continue
		}
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
		for _, required := range requiredPlanHeadings[1:] {
			want := strings.TrimSpace(strings.TrimPrefix(required, "## "))
			if strings.EqualFold(heading, want) {
				found[required] = true
			}
		}
	}
	var missing []string
	for _, heading := range requiredPlanHeadings {
		if !found[heading] {
			missing = append(missing, heading)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("plan markdown is missing required headings: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c *Core) RejectPlan(ctx context.Context, sessionID string) (store.Session, error) {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	if sess.PlanState != store.PlanAwaitingApproval {
		return store.Session{}, fmt.Errorf("session %q is not awaiting plan approval", sessionID)
	}
	if path := strings.TrimSpace(sess.PlanInfo.FilePath); path != "" {
		if err := c.removeValidatedPlanFile(ctx, sess, path); err != nil {
			return store.Session{}, err
		}
	}
	updated, err := c.store.UpdateSessionPlanState(ctx, sess.ID, store.PlanNone, false, store.PlanInfo{})
	if err != nil {
		return store.Session{}, err
	}
	if _, err := c.store.AppendMessages(ctx, sess.ID, []store.Message{{
		Role:    store.RoleUser,
		Content: "Plan rejected. Delete the submitted plan file and leave plan mode off.",
	}}); err != nil {
		return store.Session{}, err
	}
	c.log.Info("plan rejected", "event", "plan", "session", sess.ID, "agent", sess.AgentName)
	return updated, nil
}

func (c *Core) validatePlanPath(ctx context.Context, sess store.Session, raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("plan file_path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	allowedRoots := []string{}
	if strings.TrimSpace(sess.ProjectID) != "" {
		projectPath := sess.ProjectID
		if project, err := c.ledger.Get(sess.ProjectID); err == nil && strings.TrimSpace(project.Path) != "" {
			projectPath = project.Path
		}
		allowedRoots = append(allowedRoots, filepath.Join(c.paths.ProjectsDir, projectPath, "plans"))
	} else {
		allowedRoots = append(allowedRoots, c.paths.ProjectsDir)
	}
	for _, root := range allowedRoots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			continue
		}
		if sess.ProjectID == "" {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) < 3 || parts[1] != "plans" {
				continue
			}
		}
		return abs, nil
	}
	return "", fmt.Errorf("plan file must be under the active project's plans directory")
}

func (c *Core) removeValidatedPlanFile(ctx context.Context, sess store.Session, raw string) error {
	path, err := c.validatePlanPath(ctx, sess, raw)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete plan file: %w", err)
	}
	return nil
}

type PlanGateRelay struct {
	log *slog.Logger
}

func NewPlanGateRelay(loggers ...*slog.Logger) *PlanGateRelay {
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return &PlanGateRelay{log: log}
}

func (r *PlanGateRelay) RequestPermission(_ context.Context, req adapter.PermissionRequest, _ time.Duration) (adapter.PermissionDecision, error) {
	switch {
	case isPlanSubmitTool(req):
		r.log.Info("plan gate allowed submit tool", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName)
		return adapter.PermissionDecision{Behavior: "allow", UpdatedInput: req.Input}, nil
	case isReadOnlyTool(req):
		r.log.Info("plan gate allowed read-only tool", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName)
		return adapter.PermissionDecision{Behavior: "allow", UpdatedInput: req.Input}, nil
	default:
		r.log.Info("plan gate denied mutating tool", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName)
		return adapter.PermissionDecision{Behavior: "deny", Message: PlanGateMessage}, nil
	}
}

func isPlanSubmitTool(req adapter.PermissionRequest) bool {
	name := strings.ToLower(req.ToolName)
	return strings.Contains(name, "podiom_submit_plan") || strings.Contains(name, "podiom_plan")
}

func isReadOnlyTool(req adapter.PermissionRequest) bool {
	name := strings.ToLower(req.ToolName)
	switch {
	case name == "read", name == "ls", name == "glob", name == "grep", name == "webfetch", name == "websearch":
		return true
	case strings.Contains(name, ".read"), strings.Contains(name, ".search"), strings.Contains(name, ".list"):
		return true
	case strings.Contains(name, "codex.command"), strings.Contains(name, "codex.file_change"), strings.Contains(name, "applypatch"):
		return false
	default:
		return false
	}
}
