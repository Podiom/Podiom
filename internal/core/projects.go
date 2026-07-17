package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
	"gopkg.in/yaml.v3"
)

var projectIDSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

// ListProjects returns the shared project ledger (§5.3).
func (c *Core) ListProjects(ctx context.Context) ([]projects.Project, error) {
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return nil, err
	}
	return c.ledger.List()
}

// GetProject returns one project by id.
func (c *Core) GetProject(ctx context.Context, id string) (projects.Project, error) {
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return projects.Project{}, err
	}
	return c.ledger.Get(id)
}

// CreateProject adds a project to the shared ledger and creates its directory.
func (c *Core) CreateProject(ctx context.Context, p projects.Project) (projects.Project, error) {
	created, err := c.ledger.Create(p)
	if err != nil {
		return projects.Project{}, err
	}
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return projects.Project{}, err
	}
	project, err := c.ledger.Get(created.ID)
	if err != nil {
		return projects.Project{}, err
	}
	c.log.Info("project created",
		"event", "project",
		"project", project.ID,
		"name_set", strings.TrimSpace(project.Name) != "",
		"repo_set", project.Repo != nil,
		"stack_count", len(project.Stack),
	)
	return project, nil
}

// UniqueProjectID returns a safe, URL-friendly project id derived from name,
// suffixing on collisions in the shared ledger.
func (c *Core) UniqueProjectID(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	id = projectIDSlugRE.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	if id == "" {
		id = "project"
	}
	existing, err := c.ledger.List()
	if err != nil {
		return id
	}
	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.ID] = true
	}
	if !taken[id] {
		return id
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", id, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// UpdateProject applies a partial patch (name/description/colour/etc) to a
// project in the shared ledger.
func (c *Core) UpdateProject(ctx context.Context, id string, patch projects.ProjectPatch) (projects.Project, error) {
	before, _ := c.ledger.Get(id)
	updated, err := c.ledger.Update(id, patch)
	if err != nil {
		return projects.Project{}, err
	}
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return projects.Project{}, err
	}
	project, err := c.ledger.Get(updated.ID)
	if err != nil {
		return projects.Project{}, err
	}
	c.log.Info("project updated",
		"event", "project",
		"project", project.ID,
		"changed", podiomlog.ChangedFields(projectLogFields(before), projectLogFields(project)),
		"repo_set", project.Repo != nil,
		"stack_count", len(project.Stack),
	)
	return project, nil
}

// DeleteProjectResult reports what deleting a project left behind. Tasks and
// sessions are never deleted with the project; they are orphaned (their
// ProjectID now points at a project that no longer exists) so any history is
// preserved. The counts let callers warn the user about that side effect.
type DeleteProjectResult struct {
	Deleted          string `json:"deleted"`
	OrphanedTasks    int    `json:"orphaned_tasks"`
	OrphanedSessions int    `json:"orphaned_sessions"`
}

// ProjectInstructions is the editable project-specific instruction block stored
// on the project's ledger entry.
type ProjectInstructions struct {
	ProjectID    string `json:"project_id"`
	Path         string `json:"path"`
	Instructions string `json:"instructions"`
}

// ReadProjectInstructions returns the project instructions stored in
// projects.yaml.
func (c *Core) ReadProjectInstructions(projectID string) (ProjectInstructions, error) {
	project, err := c.ledger.Get(strings.TrimSpace(projectID))
	if err != nil {
		return ProjectInstructions{}, err
	}
	return ProjectInstructions{ProjectID: project.ID, Path: c.paths.ProjectsYAML, Instructions: project.Instructions}, nil
}

// WriteProjectInstructions overwrites the project instructions in projects.yaml.
func (c *Core) WriteProjectInstructions(projectID, instructions string) (ProjectInstructions, error) {
	updated, err := c.UpdateProject(context.Background(), strings.TrimSpace(projectID), projects.ProjectPatch{Instructions: &instructions})
	if err != nil {
		return ProjectInstructions{}, err
	}
	return ProjectInstructions{ProjectID: updated.ID, Path: c.paths.ProjectsYAML, Instructions: updated.Instructions}, nil
}

// DeleteProject removes a project record from the shared ledger. It is
// intentionally non-cascading: the on-disk project directory is kept, and any
// tasks or sessions that referenced the project are left in place (orphaned)
// rather than deleted. The returned result reports how many of each were
// orphaned so the caller can surface a warning.
func (c *Core) DeleteProject(ctx context.Context, id string) (DeleteProjectResult, error) {
	project, err := c.ledger.Get(id)
	if err != nil {
		return DeleteProjectResult{}, err
	}
	result := DeleteProjectResult{Deleted: project.ID}
	if tasks, err := c.ListTasks(ctx); err == nil {
		for _, t := range tasks {
			if t.ProjectID == id {
				result.OrphanedTasks++
			}
		}
	}
	if sessions, err := c.ListSessions(ctx); err == nil {
		for _, s := range sessions {
			if s.ProjectID == id {
				result.OrphanedSessions++
			}
		}
	}
	if err := c.ledger.Delete(id); err != nil {
		return DeleteProjectResult{}, err
	}
	c.log.Info("project deleted",
		"event", "project",
		"project", project.ID,
		"orphaned_tasks", result.OrphanedTasks,
		"orphaned_sessions", result.OrphanedSessions,
	)
	return result, nil
}

// DescribeProject asks an agent's engine to draft a one-sentence project
// description. It borrows the named agent's provider/profile/model (its working
// auth context) for a single unattended completion and returns the text. The
// result is not persisted — the caller decides whether to save it.
func (c *Core) DescribeProject(ctx context.Context, id, agentName string) (string, error) {
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return "", err
	}
	proj, err := c.ledger.Get(id)
	if err != nil {
		return "", err
	}
	agent, err := c.store.GetAgent(ctx, agentName)
	if err != nil {
		return "", err
	}
	title := proj.Name
	if strings.TrimSpace(title) == "" {
		title = proj.ID
	}
	draft := "There is no description yet."
	if strings.TrimSpace(proj.Description) != "" {
		draft = "Current draft to improve: \"" + proj.Description + "\"."
	}
	prompt := "You are helping write a short description for a project tracked in Podiom, an AI-agent orchestration tool. " +
		"The project can be software, writing, planning, research, physical work, or any other user effort. " +
		"The project is titled \"" + title + "\". " + draft + " " +
		"Write a single polished sentence (max 18 words), concrete and free of marketing fluff. " +
		"Return only the description text, with no quotes or preamble."

	text := c.oneShotCompletion(ctx, agent, prompt)
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "\"'")
	if text == "" {
		return "", fmt.Errorf("the model returned no description")
	}
	return text, nil
}

// DescribeTaskRequest describes either an existing task (TaskID) or an unsaved
// draft from the new-task modal. AgentName is preferred as the helper model; if
// it is empty, AssignedAgent and then the first configured agent are used.
type DescribeTaskRequest struct {
	TaskID        string
	AgentName     string
	ProjectID     string
	Title         string
	Body          string
	AssignedAgent string
}

// DescribeTask asks an agent's engine to draft runnable instructions for a
// roadmap task. It embeds project ledger context and nearby roadmap tasks in
// the prompt so the model does not need to inspect the local filesystem.
func (c *Core) DescribeTask(ctx context.Context, req DescribeTaskRequest) (string, error) {
	task := store.Task{
		ID:            req.TaskID,
		ProjectID:     req.ProjectID,
		Title:         strings.TrimSpace(req.Title),
		Body:          req.Body,
		AssignedAgent: req.AssignedAgent,
	}
	if req.TaskID != "" {
		got, err := c.store.GetTask(ctx, req.TaskID)
		if err != nil {
			return "", err
		}
		task = got
	}
	agent, err := c.helperAgent(ctx, req.AgentName, task.AssignedAgent)
	if err != nil {
		return "", err
	}
	projectContext, err := c.taskProjectPromptContext(ctx, task.ProjectID)
	if err != nil {
		return "", err
	}
	title := task.Title
	if strings.TrimSpace(title) == "" {
		title = "Untitled task"
	}
	draft := "There is no task body yet."
	if strings.TrimSpace(task.Body) != "" {
		draft = "Current draft to improve:\n\"\"\"\n" + task.Body + "\n\"\"\""
	}
	prompt := "You are helping write runnable instructions for a roadmap task in Podiom, an AI-agent orchestration tool.\n\n" +
		"Podiom project details are tracked in the configured project ledger, projects.yaml under the Podiom data directory. " +
		"The relevant project context is embedded below; use it as source-of-truth context and do not invent project facts. " +
		"Do not ask the user or agent to inspect local files to discover this context.\n\n" +
		"Project context:\n" + projectContext + "\n\n" +
		"Task title: \"" + title + "\"\n" +
		draft + "\n\n" +
		"Write clear, concrete instructions for the assigned agent to execute this roadmap task. " +
		"Include useful context, expected outcome, constraints, and acceptance criteria when they are implied. " +
		"Return only the task instructions, with no preamble."

	text := c.oneShotCompletion(ctx, agent, prompt)
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "\"'")
	if text == "" {
		return "", fmt.Errorf("the model returned no task body")
	}
	return text, nil
}

// ListTasks returns all roadmap tasks.
func (c *Core) ListTasks(ctx context.Context) ([]store.Task, error) {
	return c.store.ListTasks(ctx)
}

// GetTask returns one task.
func (c *Core) GetTask(ctx context.Context, id string) (store.Task, error) {
	return c.store.GetTask(ctx, id)
}

// CreateTask creates a roadmap task. A title is required; status defaults to
// backlog.
func (c *Core) CreateTask(ctx context.Context, task store.Task) (store.Task, error) {
	if strings.TrimSpace(task.Title) == "" {
		return store.Task{}, fmt.Errorf("task title is required")
	}
	if task.GoalID != "" {
		if _, err := c.store.GetGoal(ctx, task.GoalID); err != nil {
			return store.Task{}, fmt.Errorf("goal %q: %w", task.GoalID, err)
		}
	}
	if task.AssignedAgent != "" {
		if _, err := c.store.GetAgent(ctx, task.AssignedAgent); err != nil {
			return store.Task{}, fmt.Errorf("assigned agent %q: %w", task.AssignedAgent, err)
		}
		if err := c.ValidateRunTargetForAgent(ctx, task.AssignedAgent, taskRunTarget(task)); err != nil {
			return store.Task{}, err
		}
	}
	created, err := c.store.CreateTask(ctx, task)
	if err != nil {
		return store.Task{}, err
	}
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return store.Task{}, err
	}
	c.log.Info("task created",
		"event", "task",
		"task", created.ID,
		"project", created.ProjectID,
		"agent", created.AssignedAgent,
		"status", string(created.Status),
		"pickup_set", strings.TrimSpace(created.PickupAt) != "",
	)
	return created, nil
}

// UpdateTask stores task changes (assignment, status, body, title, pickup).
func (c *Core) UpdateTask(ctx context.Context, task store.Task) (store.Task, error) {
	existing, err := c.store.GetTask(ctx, task.ID)
	if err != nil {
		return store.Task{}, err
	}
	if hasSession, err := c.taskHasSession(ctx, task.ID); err != nil {
		return store.Task{}, err
	} else if hasSession && taskChangesLockedFields(existing, task) {
		return store.Task{}, fmt.Errorf("task %q already has a session; only status can be changed", task.ID)
	}
	if task.AssignedAgent != "" && (task.AssignedAgent != existing.AssignedAgent || taskRunTargetChanged(existing, task)) {
		if _, err := c.store.GetAgent(ctx, task.AssignedAgent); err != nil {
			return store.Task{}, fmt.Errorf("assigned agent %q: %w", task.AssignedAgent, err)
		}
		if err := c.ValidateRunTargetForAgent(ctx, task.AssignedAgent, taskRunTarget(task)); err != nil {
			return store.Task{}, err
		}
	}
	updated, err := c.store.UpdateTask(ctx, task)
	if err != nil {
		return store.Task{}, err
	}
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return store.Task{}, err
	}
	c.log.Info("task updated",
		"event", "task",
		"task", updated.ID,
		"project", updated.ProjectID,
		"agent", updated.AssignedAgent,
		"status", string(updated.Status),
		"changed", podiomlog.ChangedFields(taskLogFields(existing), taskLogFields(updated)),
	)
	return updated, nil
}

// DeleteTask removes a roadmap task. A task that is currently in progress cannot
// be deleted — it must be moved out of in_progress first — which keeps the CLI
// and API consistent with the hidden delete affordance in the UI. Any sessions
// started from the task are preserved; only the task record is removed.
func (c *Core) DeleteTask(ctx context.Context, id string) error {
	task, err := c.store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	if task.Status == store.TaskInProgress {
		return fmt.Errorf("task %q is in progress; move it out of in-progress before deleting", id)
	}
	if err := c.store.DeleteTask(ctx, id); err != nil {
		return err
	}
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return err
	}
	c.log.Info("task deleted",
		"event", "task",
		"task", task.ID,
		"project", task.ProjectID,
		"agent", task.AssignedAgent,
		"status", string(task.Status),
	)
	return nil
}

// ArchiveDoneTasksResult reports what an archive-done operation wrote and removed.
type ArchiveDoneTasksResult struct {
	ArchivePath      string `json:"archive_path,omitempty"`
	ArchivedTasks    int    `json:"archived_tasks"`
	ArchivedSessions int    `json:"archived_sessions"`
}

// ArchiveDoneTasks archives every done task (optionally scoped to one project)
// together with its sessions and full message history to a timestamped folder on
// disk, then removes both the tasks and their sessions from the active app. This
// mirrors how deleting an agent archives its sessions before removing them.
func (c *Core) ArchiveDoneTasks(ctx context.Context, projectID string) (ArchiveDoneTasksResult, error) {
	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return ArchiveDoneTasksResult{}, err
	}
	var done []store.Task
	for _, task := range tasks {
		if task.Status != store.TaskDone {
			continue
		}
		if projectID != "" && task.ProjectID != projectID {
			continue
		}
		done = append(done, task)
	}
	if len(done) == 0 {
		return ArchiveDoneTasksResult{}, nil
	}
	archivePath, sessionCount, err := c.archiveTasks(ctx, done, time.Now().UTC())
	if err != nil {
		return ArchiveDoneTasksResult{}, err
	}
	for _, task := range done {
		if err := c.store.DeleteSessionsByTask(ctx, task.ID); err != nil {
			return ArchiveDoneTasksResult{}, err
		}
		if err := c.store.DeleteTask(ctx, task.ID); err != nil {
			return ArchiveDoneTasksResult{}, err
		}
	}
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return ArchiveDoneTasksResult{}, err
	}
	c.log.Info("tasks archived",
		"event", "task",
		"project", projectID,
		"archived_tasks", len(done),
		"archived_sessions", sessionCount,
		"archive_path_set", archivePath != "",
	)
	return ArchiveDoneTasksResult{
		ArchivePath:      archivePath,
		ArchivedTasks:    len(done),
		ArchivedSessions: sessionCount,
	}, nil
}

type taskArchive struct {
	ExportedAt string               `json:"exported_at"`
	Task       store.Task           `json:"task"`
	Sessions   []taskArchiveSession `json:"sessions"`
}

type taskArchiveSession struct {
	Session  archivedSession `json:"session"`
	Messages []store.Message `json:"messages"`
}

// archiveTasks writes one JSON file per task (task + its sessions + messages)
// into <ArchiveDir>/tasks/<timestamp>/, committing the batch atomically via a
// .tmp-* directory rename. It returns the final directory and the total number
// of sessions archived.
func (c *Core) archiveTasks(ctx context.Context, tasks []store.Task, archivedAt time.Time) (string, int, error) {
	archiveRoot := filepath.Join(c.paths.ArchiveDir, "tasks")
	dirName := archivedAt.Format("20060102T150405.000000000Z")
	tmpDir := filepath.Join(archiveRoot, ".tmp-"+dirName)
	finalDir := filepath.Join(archiveRoot, dirName)

	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", 0, fmt.Errorf("create task archive dir: %w", err)
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	exportedAt := archivedAt.Format(time.RFC3339Nano)
	sessionCount := 0
	for _, task := range tasks {
		sessions, err := c.store.ListSessionsByTask(ctx, task.ID)
		if err != nil {
			return "", 0, err
		}
		archivedSessions := make([]taskArchiveSession, 0, len(sessions))
		for _, sess := range sessions {
			messages, err := c.store.ListMessages(ctx, sess.ID)
			if err != nil {
				return "", 0, err
			}
			archivedSessions = append(archivedSessions, taskArchiveSession{
				Session:  archiveSession(sess),
				Messages: messages,
			})
		}
		sessionCount += len(sessions)
		payload := taskArchive{
			ExportedAt: exportedAt,
			Task:       task,
			Sessions:   archivedSessions,
		}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", 0, fmt.Errorf("encode task archive %q: %w", task.ID, err)
		}
		raw = append(raw, '\n')
		if err := os.WriteFile(filepath.Join(tmpDir, archiveTaskFilename(task)), raw, 0o600); err != nil {
			return "", 0, fmt.Errorf("write task archive %q: %w", task.ID, err)
		}
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return "", 0, fmt.Errorf("finalize task archive: %w", err)
	}
	removeTmp = false
	return finalDir, sessionCount, nil
}

func archiveTaskFilename(task store.Task) string {
	stamp := firstNonEmpty(task.UpdatedAt, task.CreatedAt, "unknown-time")
	return sanitizeArchiveName(stamp) + "_" + sanitizeArchiveName(task.ID) + ".json"
}

// MoveRoadmapSessionTaskToReview moves an in-progress roadmap task to review.
// It returns true only when this call changed the task, so callers can restore
// that exact transition when a temporary user-blocking request is answered.
func (c *Core) MoveRoadmapSessionTaskToReview(ctx context.Context, sessionID string) (bool, error) {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if sess.Origin != store.OriginRoadmap || sess.TaskID == "" {
		return false, nil
	}
	task, err := c.store.GetTask(ctx, sess.TaskID)
	if err != nil {
		return false, err
	}
	if task.Status != store.TaskInProgress {
		return false, nil
	}
	task.Status = store.TaskReview
	if _, err := c.UpdateTask(ctx, task); err != nil {
		return false, err
	}
	return true, nil
}

// RestoreRoadmapSessionTaskToInProgress moves a review roadmap task back to
// in_progress after a temporary user-blocking request has been answered.
func (c *Core) RestoreRoadmapSessionTaskToInProgress(ctx context.Context, sessionID string) error {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Origin != store.OriginRoadmap || sess.TaskID == "" {
		return nil
	}
	task, err := c.store.GetTask(ctx, sess.TaskID)
	if err != nil {
		return err
	}
	if task.Status != store.TaskReview {
		return nil
	}
	task.Status = store.TaskInProgress
	_, err = c.UpdateTask(ctx, task)
	return err
}

// MoveRoadmapSessionTaskForQuestion moves an in-progress roadmap task to review
// while a human answer is needed. It returns true only when this call changed
// the task, so callers can restore that exact transition after the answer.
func (c *Core) MoveRoadmapSessionTaskForQuestion(ctx context.Context, sessionID string) (bool, error) {
	return c.MoveRoadmapSessionTaskToReview(ctx, sessionID)
}

// RestoreRoadmapSessionTaskAfterQuestion moves a task back to in_progress after
// a question that previously moved it to review has been answered.
func (c *Core) RestoreRoadmapSessionTaskAfterQuestion(ctx context.Context, sessionID string) error {
	return c.RestoreRoadmapSessionTaskToInProgress(ctx, sessionID)
}

// TaskSession returns the most recent roadmap session started from a task, if
// any. The boolean is false when the task has not been started.
func (c *Core) TaskSession(ctx context.Context, taskID string) (store.Session, bool, error) {
	sessions, err := c.store.ListSessionsByTask(ctx, taskID)
	if err != nil {
		return store.Session{}, false, err
	}
	if len(sessions) == 0 {
		return store.Session{}, false, nil
	}
	return sessions[0], true, nil
}

// StartTaskRequest starts work on a roadmap task.
type StartTaskRequest struct {
	TaskID string
	// Unattended runs the first turn server-side under the preapproved policy
	// (used by scheduled pickup); on-demand starts leave the first turn to the
	// caller (the web client sends it interactively).
	Unattended bool
}

// StartTask creates a durable roadmap-origin session bound to the task's
// assigned agent (with provenance back to the task), moves the task to
// in_progress, and — for unattended pickups — runs the task as one turn.
func (c *Core) StartTask(ctx context.Context, req StartTaskRequest) (store.Session, error) {
	task, err := c.store.GetTask(ctx, req.TaskID)
	if err != nil {
		return store.Session{}, err
	}
	if task.AssignedAgent == "" {
		return store.Session{}, fmt.Errorf("task %q has no assigned agent", task.ID)
	}

	// Goal-linked tasks run in yolo mode as part of the goal's autonomous chain,
	// and their plan gate is bypassed (a plan gate forces approve mode, which
	// would deadlock an unattended yolo run). Standalone tasks keep the default
	// approve/preapproved posture.
	goalLinked := task.GoalID != ""
	createReq := CreateSessionRequest{
		AgentName:                      task.AssignedAgent,
		Origin:                         store.OriginRoadmap,
		Provider:                       task.Provider,
		Profile:                        task.Profile,
		Model:                          task.Model,
		Effort:                         task.Effort,
		TaskID:                         task.ID,
		GoalID:                         task.GoalID,
		ProjectID:                      task.ProjectID,
		CreatePlanBeforeImplementation: task.PlanRequired && !goalLinked,
	}
	if goalLinked {
		createReq.PermissionMode = config.PermissionYolo
	}
	sess, err := c.CreateSession(ctx, createReq)
	if err != nil {
		return store.Session{}, err
	}
	if task.GoalID != "" {
		if named, nameErr := c.store.UpdateSessionMetadata(ctx, sess.ID, "Task: "+task.Title, "Goal task execution.", false); nameErr == nil {
			sess = named
		}
	}
	permission := "inherited"
	if goalLinked {
		permission = "yolo"
	}
	c.log.Info("task session started",
		"event", "task",
		"task", task.ID,
		"project", task.ProjectID,
		"agent", task.AssignedAgent,
		"session", sess.ID,
		"goal", task.GoalID,
		"permission", permission,
		"unattended", req.Unattended,
	)

	task.Status = store.TaskInProgress
	if _, err := c.UpdateTask(ctx, task); err != nil {
		return store.Session{}, err
	}

	if req.Unattended {
		opts := TurnOptions{
			PermissionTurnID: sess.ID,
			Unattended:       true,
		}
		// A goal-linked task is yolo (session permission mode above); a standalone
		// unattended task is preapproved with an empty allow-list (deny-all).
		if !goalLinked {
			opts.PermissionRelay = NewAllowListRelay(nil, c.log)
		}
		events, err := c.StreamTurn(ctx, sess.ID, TaskPrompt(task), opts)
		if err != nil {
			return sess, err
		}
		for range events { // drain
		}
	}
	return sess, nil
}

func projectLogFields(p projects.Project) map[string]string {
	repo := ""
	if p.Repo != nil {
		repo = p.Repo.Owner + "/" + p.Repo.Name + "@" + p.Repo.Ref
	}
	return map[string]string{
		"name":         p.Name,
		"description":  boolString(strings.TrimSpace(p.Description) != ""),
		"color":        p.Color,
		"status":       p.Status,
		"stack_count":  fmt.Sprintf("%d", len(p.Stack)),
		"notes":        boolString(strings.TrimSpace(p.Notes) != ""),
		"instructions": boolString(strings.TrimSpace(p.Instructions) != ""),
		"repo":         repo,
	}
}

func taskLogFields(t store.Task) map[string]string {
	return map[string]string{
		"project":  t.ProjectID,
		"title":    boolString(strings.TrimSpace(t.Title) != ""),
		"body":     boolString(strings.TrimSpace(t.Body) != ""),
		"agent":    t.AssignedAgent,
		"provider": string(t.Provider),
		"profile":  t.Profile,
		"model":    t.Model,
		"effort":   t.Effort,
		"status":   string(t.Status),
		"plan":     boolString(t.PlanRequired),
		"pickup":   boolString(strings.TrimSpace(t.PickupAt) != ""),
	}
}

func taskRunTarget(task store.Task) RunTarget {
	return RunTarget{
		Provider: config.Provider(strings.TrimSpace(string(task.Provider))),
		Profile:  strings.TrimSpace(task.Profile),
		Model:    strings.TrimSpace(task.Model),
		Effort:   strings.TrimSpace(task.Effort),
	}
}

func taskRunTargetChanged(before, after store.Task) bool {
	return before.Provider != after.Provider ||
		before.Profile != after.Profile ||
		before.Model != after.Model ||
		before.Effort != after.Effort
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// TaskPrompt renders a task into the prompt used to seed its session.
func TaskPrompt(task store.Task) string {
	if strings.TrimSpace(task.Body) == "" {
		return task.Title
	}
	return task.Title + "\n\n" + task.Body
}

func (c *Core) helperAgent(ctx context.Context, preferred, assigned string) (store.Agent, error) {
	for _, name := range []string{preferred, assigned} {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		return c.store.GetAgent(ctx, name)
	}
	agents, err := c.store.ListAgents(ctx)
	if err != nil {
		return store.Agent{}, err
	}
	if len(agents) == 0 {
		return store.Agent{}, fmt.Errorf("hire an agent first")
	}
	return agents[0], nil
}

func (c *Core) taskHasSession(ctx context.Context, taskID string) (bool, error) {
	_, ok, err := c.TaskSession(ctx, taskID)
	return ok, err
}

func taskChangesLockedFields(before, after store.Task) bool {
	return before.ProjectID != after.ProjectID ||
		before.Title != after.Title ||
		before.Body != after.Body ||
		before.AssignedAgent != after.AssignedAgent ||
		before.Provider != after.Provider ||
		before.Profile != after.Profile ||
		before.Model != after.Model ||
		before.Effort != after.Effort ||
		before.PlanRequired != after.PlanRequired ||
		before.PickupAt != after.PickupAt
}

func (c *Core) syncProjectRoadmaps(ctx context.Context) error {
	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return err
	}
	byProject := map[string][]string{}
	for _, task := range tasks {
		if strings.TrimSpace(task.ProjectID) == "" {
			continue
		}
		byProject[task.ProjectID] = append(byProject[task.ProjectID], task.ID)
	}
	return c.ledger.SyncRoadmaps(byProject)
}

type promptProjectContext struct {
	ID          string              `yaml:"id"`
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Color       string              `yaml:"color,omitempty"`
	Path        string              `yaml:"path"`
	Status      string              `yaml:"status"`
	Stack       []string            `yaml:"stack"`
	Repo        *projects.Repo      `yaml:"repo"`
	Roadmap     []promptTaskContext `yaml:"roadmap"`
	Notes       string              `yaml:"notes"`
}

type promptTaskContext struct {
	ID            string `yaml:"id"`
	Title         string `yaml:"title"`
	Status        string `yaml:"status"`
	AssignedAgent string `yaml:"assigned_agent,omitempty"`
	PickupAt      string `yaml:"pickup_at,omitempty"`
	Body          string `yaml:"body,omitempty"`
}

func (c *Core) taskProjectPromptContext(ctx context.Context, projectID string) (string, error) {
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return "", err
	}
	if strings.TrimSpace(projectID) == "" {
		return "No project is selected for this task.", nil
	}
	proj, err := c.ledger.Get(projectID)
	if err != nil {
		return fmt.Sprintf("No matching project ledger entry for project_id %q.", projectID), nil
	}
	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return "", err
	}
	byID := map[string]store.Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	ctxProj := promptProjectContext{
		ID:          proj.ID,
		Name:        proj.Name,
		Description: proj.Description,
		Color:       proj.Color,
		Path:        proj.Path,
		Status:      proj.Status,
		Stack:       proj.Stack,
		Repo:        proj.Repo,
		Roadmap:     []promptTaskContext{},
		Notes:       proj.Notes,
	}
	for _, id := range proj.Roadmap {
		task, ok := byID[id]
		if !ok {
			ctxProj.Roadmap = append(ctxProj.Roadmap, promptTaskContext{ID: id, Status: "missing"})
			continue
		}
		ctxProj.Roadmap = append(ctxProj.Roadmap, promptTaskContext{
			ID:            task.ID,
			Title:         task.Title,
			Status:        string(task.Status),
			AssignedAgent: task.AssignedAgent,
			PickupAt:      task.PickupAt,
			Body:          task.Body,
		})
	}
	raw, err := yaml.Marshal(ctxProj)
	if err != nil {
		return "", fmt.Errorf("marshal project context: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

type projectExecutionContext struct {
	Root         string
	ProjectDir   string
	Instructions string
	Prompt       string
}

func (c *Core) sessionProjectExecutionContext(ctx context.Context, sess store.Session) (projectExecutionContext, error) {
	projectID := strings.TrimSpace(sess.ProjectID)
	if projectID == "" && strings.TrimSpace(sess.TaskID) != "" {
		task, err := c.store.GetTask(ctx, sess.TaskID)
		if err != nil {
			return projectExecutionContext{}, err
		}
		projectID = strings.TrimSpace(task.ProjectID)
	}
	if projectID == "" {
		return projectExecutionContext{}, nil
	}
	proj, err := c.ledger.Get(projectID)
	if err != nil {
		return projectExecutionContext{}, nil
	}
	projectDir := filepath.Join(c.paths.ProjectsDir, proj.Path)
	root := projectDir
	if proj.Repo != nil {
		root = filepath.Join(projectDir, "repo")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return projectExecutionContext{}, fmt.Errorf("create project workspace %s: %w", root, err)
	}
	payload := map[string]any{
		"project": map[string]any{
			"id":          proj.ID,
			"name":        proj.Name,
			"description": proj.Description,
			"status":      proj.Status,
			"stack":       proj.Stack,
			"notes":       proj.Notes,
			"local_path":  projectDir,
		},
		"workspace": map[string]any{
			"local_path": root,
		},
	}
	if proj.Repo != nil {
		payload["repo"] = map[string]any{
			"provider":       proj.Repo.Provider,
			"mode":           proj.Repo.Mode,
			"full_name":      proj.Repo.FullName,
			"default_branch": proj.Repo.DefaultBranch,
			"ref":            proj.Repo.Ref,
			"synced_at":      proj.Repo.SyncedAt,
			"local_path":     root,
		}
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return projectExecutionContext{}, err
	}
	prompt := "Podiom project context for this session:\n" +
		strings.TrimSpace(string(raw)) + "\n\n" +
		"This session is bound to project " + proj.ID + ". Create and edit durable project files under " + root + ". " +
		"Do not create project artifacts in the agent workspace."
	if proj.Repo != nil {
		prompt += " The connected GitHub repository has been downloaded as a local source snapshot at " + root + ". " +
			"You may inspect files there for project facts. It is not a Git checkout: do not assume .git, branches, commits, pushes, or PR operations are available."
	}
	return projectExecutionContext{Root: root, ProjectDir: projectDir, Instructions: proj.Instructions, Prompt: prompt}, nil
}
