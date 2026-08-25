package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	podiomgit "github.com/Podiom/Podiom/internal/git"
	podiomgithub "github.com/Podiom/Podiom/internal/github"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

type projectCreateRequest struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Stack       []string      `json:"stack"`
	Notes       string        `json:"notes"`
	Git         *projects.Git `json:"git,omitempty"`
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.core.ListProjects(r.Context())
		writeJSON(w, list, err)
	case http.MethodPost:
		var req projectCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		project, err := s.core.CreateProject(r.Context(), projects.Project{
			ID:          strings.TrimSpace(req.ID),
			Name:        strings.TrimSpace(req.Name),
			Description: req.Description,
			Stack:       req.Stack,
			Notes:       req.Notes,
			Git:         req.Git,
		})
		writeJSON(w, project, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// projectGitRequest carries the whole source-control block plus the one field
// that is not policy: Force is the user's answer to the confirmation prompt, so
// it must not live on projects.Git, which is persisted to projects.yaml.
type projectGitRequest struct {
	Git   projects.Git `json:"git"`
	Force bool         `json:"force"`
}

type projectUpdateRequest struct {
	Name        *string       `json:"name,omitempty"`
	Description *string       `json:"description,omitempty"`
	Color       *string       `json:"color,omitempty"`
	Status      *string       `json:"status,omitempty"`
	Stack       *[]string     `json:"stack,omitempty"`
	Notes       *string       `json:"notes,omitempty"`
	Git         *projects.Git `json:"git,omitempty"`
}

type projectRepoRequest struct {
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	DefaultBranch string `json:"default_branch"`
	Ref           string `json:"ref"`
	Description   string `json:"description"`
	Force         bool   `json:"force"`
}

type describeRequest struct {
	Agent string `json:"agent"`
}

type projectInstructionsRequest struct {
	Instructions string `json:"instructions"`
}

// handleProject handles /api/projects/<id> (GET one, PATCH update) and
// /api/projects/<id>/describe (POST: draft a description with an agent's engine).
func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "project id is required", http.StatusBadRequest)
		return
	}

	if id == "from-github" && action == "" {
		s.handleProjectFromGitHub(w, r)
		return
	}

	if action == "describe" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req describeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Agent) == "" {
			http.Error(w, "agent is required", http.StatusBadRequest)
			return
		}
		text, err := s.core.DescribeProject(r.Context(), id, req.Agent)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, map[string]string{"description": text}, nil)
		return
	}

	if action == "instructions" {
		switch r.Method {
		case http.MethodGet:
			info, err := s.core.ReadProjectInstructions(id)
			writeJSON(w, info, err)
		case http.MethodPut:
			var req projectInstructionsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			info, err := s.core.WriteProjectInstructions(id, req.Instructions)
			writeJSON(w, info, err)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	if action == "analyze" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req describeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		project, err := s.core.AnalyzeProjectRepo(ctx, id, req.Agent)
		writeJSON(w, project, err)
		return
	}

	if action == "git" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req projectGitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.log.Info("project git configure requested",
			"event", "project",
			"project", id,
			"enabled", req.Git.Enabled,
			"remote_set", strings.TrimSpace(req.Git.Remote) != "",
			"force", req.Force,
		)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		updated, err := s.core.ConfigureProjectGit(ctx, id, req.Git, req.Force)
		if errors.Is(err, core.ErrGitConfirmationRequired) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, updated, err)
		return
	}

	if action == "repo" || strings.HasPrefix(action, "repo/") {
		s.handleProjectRepo(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		project, err := s.core.GetProject(r.Context(), id)
		writeJSON(w, project, err)
	case http.MethodPatch, http.MethodPut:
		var req projectUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updated, err := s.core.UpdateProject(r.Context(), id, projects.ProjectPatch{
			Name:        req.Name,
			Description: req.Description,
			Color:       req.Color,
			Status:      req.Status,
			Stack:       req.Stack,
			Notes:       req.Notes,
			Git:         req.Git,
		})
		writeJSON(w, updated, err)
	case http.MethodDelete:
		result, err := s.core.DeleteProject(r.Context(), id)
		writeJSON(w, result, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProjectFromGitHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.github == nil {
		http.Error(w, "github unavailable", http.StatusServiceUnavailable)
		return
	}
	var req projectRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	owner, name := repoOwnerName(req)
	if owner == "" || name == "" {
		http.Error(w, "repo owner and name are required", http.StatusBadRequest)
		return
	}
	s.log.Info("github project create requested",
		"event", "github",
		"repo", owner+"/"+name,
		"repo_owner", owner,
		"repo_name", name,
		"ref", req.Ref,
		"force", req.Force,
	)

	project, err := s.projectForGitHubCreate(r.Context(), name, req.Description, req.Force)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	remotes := preferredGitHubRemotes(req.SSHURL, req.CloneURL, req.HTMLURL)
	remote := ""
	if len(remotes) > 0 {
		remote = remotes[0]
	}
	if cloned, cloneErr := s.core.CloneGitHubProject(r.Context(), project.ID, projects.CheckoutRepo(owner, name, req.HTMLURL, req.DefaultBranch, req.Ref), remotes); cloneErr == nil {
		writeJSON(w, cloned, nil)
		return
	} else {
		s.log.Warn("github project clone failed; using snapshot fallback", "event", "github", "project", project.ID, "repo", owner+"/"+name, "error", cloneErr)
	}
	repo := projects.SnapshotRepo(owner, name, req.HTMLURL, req.DefaultBranch, req.Ref)
	result, err := s.github.SyncProject(r.Context(), podiomgithub.SyncRequest{Project: project, Repo: repo, Force: req.Force})
	if writeGitHubProjectError(w, err) {
		return
	}
	synced := result.Repo
	updated, err := s.core.UpdateProject(r.Context(), project.ID, projects.ProjectPatch{
		Repo: &synced,
		Git:  &projects.Git{Enabled: true, Remote: remote, DefaultBranch: req.DefaultBranch},
	})
	writeJSON(w, updated, err)
}

func (s *Server) projectForGitHubCreate(ctx context.Context, name, description string, force bool) (projects.Project, error) {
	if force {
		list, err := s.core.ListProjects(ctx)
		if err != nil {
			return projects.Project{}, err
		}
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].Name == name && list[i].Repo == nil {
				return list[i], nil
			}
		}
	}
	id := s.core.UniqueProjectID(name)
	return s.core.CreateProject(ctx, projects.Project{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(description),
	})
}

func (s *Server) handleProjectRepo(w http.ResponseWriter, r *http.Request, id string) {
	if s.github == nil {
		http.Error(w, "github unavailable", http.StatusServiceUnavailable)
		return
	}
	project, err := s.core.GetProject(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), id+"/repo")
	if rest == "/sync" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if project.Repo == nil {
			http.Error(w, "project has no connected repo", http.StatusBadRequest)
			return
		}
		if project.Repo.Mode == "git" {
			updated, err := s.core.SyncProjectGit(r.Context(), id)
			writeJSON(w, updated, err)
			return
		}
		var req struct {
			Force bool `json:"force"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.log.Info("github project sync requested",
			"event", "github",
			"project", project.ID,
			"repo", project.Repo.Owner+"/"+project.Repo.Name,
			"repo_owner", project.Repo.Owner,
			"repo_name", project.Repo.Name,
			"ref", project.Repo.Ref,
			"force", req.Force,
		)
		result, err := s.github.SyncProject(r.Context(), podiomgithub.SyncRequest{Project: project, Repo: *project.Repo, Force: req.Force})
		if writeGitHubProjectError(w, err) {
			return
		}
		repo := result.Repo
		updated, err := s.core.UpdateProject(r.Context(), id, projects.ProjectPatch{Repo: &repo})
		writeJSON(w, updated, err)
		return
	}
	if rest != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req projectRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		owner, name := repoOwnerName(req)
		if owner == "" || name == "" {
			http.Error(w, "repo owner and name are required", http.StatusBadRequest)
			return
		}
		s.log.Info("github project repo connect requested",
			"event", "github",
			"project", project.ID,
			"repo", owner+"/"+name,
			"repo_owner", owner,
			"repo_name", name,
			"ref", req.Ref,
			"force", req.Force,
		)
		repo := projects.SnapshotRepo(owner, name, req.HTMLURL, req.DefaultBranch, req.Ref)
		result, err := s.github.SyncProject(r.Context(), podiomgithub.SyncRequest{Project: project, Repo: repo, Force: req.Force})
		if writeGitHubProjectError(w, err) {
			return
		}
		synced := result.Repo
		updated, err := s.core.UpdateProject(r.Context(), id, projects.ProjectPatch{Repo: &synced})
		writeJSON(w, updated, err)
	case http.MethodDelete:
		s.log.Info("github project repo cleared", "event", "github", "project", id)
		updated, err := s.core.UpdateProject(r.Context(), id, projects.ProjectPatch{ClearRepo: true})
		writeJSON(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func repoOwnerName(req projectRepoRequest) (string, string) {
	if strings.TrimSpace(req.Owner) != "" && strings.TrimSpace(req.Name) != "" {
		return strings.TrimSpace(req.Owner), strings.TrimSpace(req.Name)
	}
	parts := strings.Split(strings.TrimSpace(req.FullName), "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

func preferredGitHubRemotes(sshURL, cloneURL, htmlURL string) []string {
	sshURL, cloneURL, htmlURL = strings.TrimSpace(sshURL), strings.TrimSpace(cloneURL), strings.TrimSpace(htmlURL)
	var candidates []string
	if podiomgit.PublicKey() != "" {
		candidates = []string{sshURL, cloneURL, htmlURL}
	} else {
		candidates = []string{cloneURL, htmlURL, sshURL}
	}
	seen := map[string]bool{}
	var out []string
	for _, candidate := range candidates {
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

type taskCreateRequest struct {
	ProjectID     string          `json:"project_id"`
	Title         string          `json:"title"`
	Body          string          `json:"body"`
	AssignedAgent string          `json:"assigned_agent"`
	Provider      config.Provider `json:"provider"`
	Profile       string          `json:"profile"`
	Model         string          `json:"model"`
	Effort        string          `json:"effort"`
	Status        string          `json:"status"`
	PlanRequired  bool            `json:"plan_required"`
	PickupAt      string          `json:"pickup_at"`
	GoalID        string          `json:"goal_id"`
	// CreatedBySession/CreatedByAgent are set by podiom_create_task from the
	// calling session's own identity, not by the model. The web UI and CLI leave
	// them empty, which is what marks a task as user-authored.
	CreatedBySession string `json:"created_by_session,omitempty"`
	CreatedByAgent   string `json:"created_by_agent,omitempty"`
}

// taskUpdateRequest deliberately has no created_by_* fields: authorship is fixed
// at creation (see store.UpdateTask).
type taskUpdateRequest struct {
	ProjectID     *string          `json:"project_id,omitempty"`
	Title         *string          `json:"title,omitempty"`
	Body          *string          `json:"body,omitempty"`
	AssignedAgent *string          `json:"assigned_agent,omitempty"`
	Provider      *config.Provider `json:"provider,omitempty"`
	Profile       *string          `json:"profile,omitempty"`
	Model         *string          `json:"model,omitempty"`
	Effort        *string          `json:"effort,omitempty"`
	Status        *string          `json:"status,omitempty"`
	PlanRequired  *bool            `json:"plan_required,omitempty"`
	PickupAt      *string          `json:"pickup_at,omitempty"`
	GoalID        *string          `json:"goal_id,omitempty"`
}

type taskDescribeRequest struct {
	Agent         string `json:"agent,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	Title         string `json:"title,omitempty"`
	Body          string `json:"body,omitempty"`
	AssignedAgent string `json:"assigned_agent,omitempty"`
}

// taskArchiveDoneRequest optionally scopes an archive-done operation to a single
// project. An empty ProjectID archives every done task.
type taskArchiveDoneRequest struct {
	ProjectID string `json:"project_id,omitempty"`
}

// taskStartRequest opts a caller into running the task's first turn server-side.
// The web client leaves it false and sends the first turn interactively; agents
// (podiom_start_task) set it, because there is no browser to send the prompt.
type taskStartRequest struct {
	Unattended bool `json:"unattended,omitempty"`
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		tasks, err := s.core.ListTasks(r.Context())
		writeJSON(w, tasks, err)
	case http.MethodPost:
		var req taskCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status := store.TaskStatus(req.Status)
		if status == "" {
			status = store.TaskBacklog
		}
		task, err := s.core.CreateTask(r.Context(), store.Task{
			ProjectID:     req.ProjectID,
			Title:         strings.TrimSpace(req.Title),
			Body:          req.Body,
			AssignedAgent: req.AssignedAgent,
			Provider:      req.Provider,
			Profile:       strings.TrimSpace(req.Profile),
			Model:         strings.TrimSpace(req.Model),
			Effort:        strings.TrimSpace(req.Effort),
			Status:        status,
			PlanRequired:  req.PlanRequired,
			PickupAt:      req.PickupAt,
			GoalID:        strings.TrimSpace(req.GoalID),

			CreatedBySession: strings.TrimSpace(req.CreatedBySession),
			CreatedByAgent:   strings.TrimSpace(req.CreatedByAgent),
		})
		writeJSON(w, task, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTask handles /api/tasks/<id> (PATCH update) and /api/tasks/<id>/start.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "task id is required", http.StatusBadRequest)
		return
	}

	if id == "describe" && action == "" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req taskDescribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := s.core.DescribeTask(r.Context(), core.DescribeTaskRequest{
			AgentName:     req.Agent,
			ProjectID:     req.ProjectID,
			Title:         req.Title,
			Body:          req.Body,
			AssignedAgent: req.AssignedAgent,
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, map[string]string{"body": body}, nil)
		return
	}

	if id == "archive-done" && action == "" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req taskArchiveDoneRequest
		// An empty body is valid: archive every done task.
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.log.Info("task archive requested", "event", "task", "project", strings.TrimSpace(req.ProjectID))
		result, err := s.core.ArchiveDoneTasks(r.Context(), strings.TrimSpace(req.ProjectID))
		writeJSON(w, result, err)
		return
	}

	if action == "start" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req taskStartRequest
		// An empty body is valid: the web client starts a task attended.
		_ = json.NewDecoder(r.Body).Decode(&req)
		session, err := s.core.StartTask(r.Context(), core.StartTaskRequest{TaskID: id})
		if err == nil {
			s.log.Info("task start requested", "event", "task", "task", id, "session", session.ID, "agent", session.AgentName, "project", session.ProjectID, "unattended", req.Unattended)
			if req.Unattended {
				// The caller has no browser to send the first turn, so run it here.
				// Detached from the request context, which ends with the response.
				go func(sess store.Session) {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
					defer cancel()
					if err := s.core.RunTaskTurn(ctx, sess); err != nil {
						s.log.Warn("unattended task run failed", "event", "task", "task", sess.TaskID, "session", sess.ID, "err", err)
					}
					if sess.GoalID != "" {
						s.broadcastGoalPing(ctx, sess.GoalID)
					}
				}(session)
			}
		}
		writeJSON(w, session, err)
		return
	}

	if action == "session" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		session, ok, err := s.core.TaskSession(r.Context(), id)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if !ok {
			http.Error(w, "task has no session yet", http.StatusNotFound)
			return
		}
		writeJSON(w, session, nil)
		return
	}

	if action == "describe" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req taskDescribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := s.core.DescribeTask(r.Context(), core.DescribeTaskRequest{
			TaskID:    id,
			AgentName: req.Agent,
		})
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, map[string]string{"body": body}, nil)
		return
	}

	switch r.Method {
	case http.MethodGet:
		task, err := s.core.GetTask(r.Context(), id)
		writeJSON(w, task, err)
	case http.MethodPatch, http.MethodPut:
		task, err := s.core.GetTask(r.Context(), id)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		var req taskUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		before := task.Status
		applyTaskUpdate(&task, req)
		updated, err := s.core.UpdateTask(r.Context(), task)
		if err == nil && before != store.TaskDone && updated.Status == store.TaskDone {
			// Notified from here rather than from core.UpdateTask because only this
			// handler has both the previous and the new status in hand. core's own
			// update is also used for transient review/in-progress moves, which would
			// produce notifications for work that is still running.
			s.notifications.Publish(notify.Event{
				Type:       notify.TypeTaskCompleted,
				GoalID:     updated.GoalID,
				TaskID:     updated.ID,
				AgentName:  updated.AssignedAgent,
				Resource:   notify.ResourceTask,
				ResourceID: updated.ID,
			})
		}
		writeJSON(w, updated, err)
	case http.MethodDelete:
		if err := s.core.DeleteTask(r.Context(), id); err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, map[string]string{"deleted": id}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func applyTaskUpdate(task *store.Task, req taskUpdateRequest) {
	if req.ProjectID != nil {
		task.ProjectID = *req.ProjectID
	}
	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Body != nil {
		task.Body = *req.Body
	}
	if req.AssignedAgent != nil {
		task.AssignedAgent = *req.AssignedAgent
	}
	if req.Provider != nil {
		task.Provider = *req.Provider
	}
	if req.Profile != nil {
		task.Profile = *req.Profile
	}
	if req.Model != nil {
		task.Model = *req.Model
	}
	if req.Effort != nil {
		task.Effort = *req.Effort
	}
	if req.Status != nil {
		task.Status = store.TaskStatus(*req.Status)
	}
	if req.PlanRequired != nil {
		task.PlanRequired = *req.PlanRequired
	}
	if req.PickupAt != nil {
		task.PickupAt = *req.PickupAt
	}
	if req.GoalID != nil {
		task.GoalID = *req.GoalID
	}
}
