package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mar-schmidt/Podium/internal/adapter"
	"github.com/mar-schmidt/Podium/internal/config"
	"github.com/mar-schmidt/Podium/internal/core"
	podiumlog "github.com/mar-schmidt/Podium/internal/logging"
	"github.com/mar-schmidt/Podium/internal/store"
)

const defaultHTTPPermissionTimeout = 3 * time.Minute

type agentCreateRequest struct {
	Name           string                `json:"name"`
	Provider       config.Provider       `json:"provider,omitempty"`
	Profile        string                `json:"profile,omitempty"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode,omitempty"`
	Fallback       []string              `json:"fallback,omitempty"`
	MCPServers     []string              `json:"mcp_servers,omitempty"`
}

type sessionCreateRequest struct {
	AgentName      string                `json:"agent_name"`
	Origin         store.SessionOrigin   `json:"origin"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode,omitempty"`
	ProjectID      string                `json:"project_id,omitempty"`
}

type chatRequest struct {
	SessionID      string                `json:"session_id,omitempty"`
	AgentName      string                `json:"agent_name,omitempty"`
	Message        string                `json:"message"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode,omitempty"`
	ProjectID      string                `json:"project_id,omitempty"`
}

type streamEvent struct {
	Type       string                     `json:"type"`
	Session    *store.Session             `json:"session,omitempty"`
	Message    *store.Message             `json:"message,omitempty"`
	Delta      string                     `json:"delta,omitempty"`
	Notice     string                     `json:"notice,omitempty"`
	Request    *adapter.PermissionRequest `json:"request,omitempty"`
	Input      *adapter.UserInputRequest  `json:"input,omitempty"`
	Error      string                     `json:"error,omitempty"`
	AutoDenied bool                       `json:"auto_denied,omitempty"`
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		agents, err := s.core.ListAgents(r.Context())
		writeJSON(w, agents, err)
	case http.MethodPost:
		if err := s.refreshProfilesFromConfig(); err != nil {
			writeJSON(w, nil, err)
			return
		}
		var req agentCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		agent, err := s.core.CreateAgent(r.Context(), core.CreateAgentRequest{
			Name:           req.Name,
			Provider:       req.Provider,
			Profile:        req.Profile,
			Model:          req.Model,
			Effort:         req.Effort,
			PermissionMode: req.PermissionMode,
			Fallback:       req.Fallback,
			MCPServers:     req.MCPServers,
		})
		writeJSON(w, agent, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type profileRequest struct {
	Name      string          `json:"name,omitempty"`
	Provider  config.Provider `json:"provider,omitempty"`
	ConfigDir string          `json:"config_dir,omitempty"`
	HomeDir   string          `json:"home_dir,omitempty"`
}

// handleProfiles serves configured auth profiles and creates new ones.
//
//	GET  /api/profiles -> profiles from config.yaml
//	POST /api/profiles -> create/update a profile in config.yaml
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if err := s.refreshProfilesFromConfig(); err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, s.core.ListProfileDetails(), nil)
	case http.MethodPost:
		s.saveProfile(w, r, "")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleProfile serves update/delete for one configured auth profile.
//
//	PUT/PATCH /api/profiles/<name> -> update provider/path
//	DELETE    /api/profiles/<name> -> remove when not referenced
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	if name == "" {
		http.Error(w, "profile name is required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		s.saveProfile(w, r, name)
	case http.MethodDelete:
		s.deleteProfile(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request, currentName string) {
	if !localRequest(r) {
		http.Error(w, "profiles are only editable from loopback clients", http.StatusForbidden)
		return
	}
	var req profileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if currentName != "" {
		req.Name = currentName
		old, ok := s.profileByName(currentName)
		if !ok {
			http.Error(w, "profile not found", http.StatusNotFound)
			return
		}
		if req.Provider == "" {
			req.Provider = old.Provider
		}
		if req.ConfigDir == "" {
			req.ConfigDir = old.ConfigDir
		}
		if req.HomeDir == "" {
			req.HomeDir = old.HomeDir
		}
	}
	beforeProfile := config.Profile{}
	if currentName != "" {
		beforeProfile, _ = s.profileByName(currentName)
	}
	profile, err := s.profileFromRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	existing := map[string]config.Provider{}
	for _, p := range s.core.ListProfileDetails() {
		if p.Name != currentName {
			existing[p.Name] = p.Provider
		}
	}
	if err := config.ValidateProfile(profile, existing); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if currentName != "" {
		old, _ := s.profileByName(currentName)
		if old.Provider != profile.Provider {
			if err := s.ensureProfileProviderChangeSafe(currentName); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	if err := os.MkdirAll(s.profileDir(profile), 0o700); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if err := config.UpsertProfile(s.paths.ConfigYAML, profile); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if err := s.refreshProfilesFromConfig(); err != nil {
		writeJSON(w, nil, err)
		return
	}
	saved, ok := s.profileByName(profile.Name)
	if !ok {
		writeJSON(w, nil, errors.New("saved profile was not found after config reload"))
		return
	}
	action := "profile created"
	changed := []string{"name", "provider", "path_set"}
	if currentName != "" {
		action = "profile updated"
		changed = podiumlog.ChangedFields(profileLogFields(beforeProfile), profileLogFields(saved))
	}
	s.log.Info(action,
		"event", "config",
		"profile", saved.Name,
		"provider", string(saved.Provider),
		"path_set", s.profileDir(saved) != "",
		"changed", changed,
	)
	writeJSON(w, saved, nil)
}

func profileLogFields(p config.Profile) map[string]string {
	pathSet := ""
	switch p.Provider {
	case config.ProviderCodex:
		pathSet = boolLogString(p.HomeDir != "")
	default:
		pathSet = boolLogString(p.ConfigDir != "")
	}
	return map[string]string{
		"name":     p.Name,
		"provider": string(p.Provider),
		"path_set": pathSet,
	}
}

func boolLogString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request, name string) {
	if !localRequest(r) {
		http.Error(w, "profiles are only editable from loopback clients", http.StatusForbidden)
		return
	}
	if _, ok := s.profileByName(name); !ok {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}
	if err := s.ensureProfileUnused(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.RemoveProfile(s.paths.ConfigYAML, name); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if err := s.refreshProfilesFromConfig(); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.log.Info("profile deleted", "event", "config", "profile", name)
	writeJSON(w, s.core.ListProfileDetails(), nil)
}

func (s *Server) profileFromRequest(req profileRequest) (config.Profile, error) {
	p := config.Profile{
		Name:     strings.TrimSpace(req.Name),
		Provider: req.Provider,
	}
	if p.Provider == "" {
		p.Provider = config.ProviderClaude
	}
	switch p.Provider {
	case config.ProviderClaude:
		p.ConfigDir = strings.TrimSpace(req.ConfigDir)
		if p.ConfigDir == "" && p.Name != "" {
			p.ConfigDir = filepath.Join(s.paths.Home, "profiles", "claude-"+p.Name)
		}
	case config.ProviderCodex:
		p.HomeDir = strings.TrimSpace(req.HomeDir)
		if p.HomeDir == "" && p.Name != "" {
			p.HomeDir = filepath.Join(s.paths.Home, "profiles", "codex-"+p.Name)
		}
	default:
		// Let config.ValidateProfile produce the canonical provider error.
		p.ConfigDir = strings.TrimSpace(req.ConfigDir)
		p.HomeDir = strings.TrimSpace(req.HomeDir)
	}
	return p, nil
}

func (s *Server) profileDir(p config.Profile) string {
	if p.Provider == config.ProviderCodex {
		return p.HomeDir
	}
	return p.ConfigDir
}

func (s *Server) profileByName(name string) (config.Profile, bool) {
	if err := s.refreshProfilesFromConfig(); err != nil {
		return config.Profile{}, false
	}
	for _, p := range s.core.ListProfileDetails() {
		if p.Name == name {
			return p, true
		}
	}
	return config.Profile{}, false
}

func (s *Server) refreshProfilesFromConfig() error {
	cfg, err := config.Load(s.paths.ConfigYAML)
	if err != nil {
		return err
	}
	s.core.SetProfiles(cfg.Profiles)
	return nil
}

func (s *Server) ensureProfileProviderChangeSafe(name string) error {
	return s.ensureProfileUnused(name)
}

func (s *Server) ensureProfileUnused(name string) error {
	cfg, err := config.Load(s.paths.ConfigYAML)
	if err != nil {
		return err
	}
	for _, entry := range cfg.Global.Fallback {
		if entry == name {
			return errors.New("profile is used in the global fallback chain")
		}
	}
	for _, agent := range cfg.Agents {
		if agent.Profile == name {
			return &profileInUseError{message: "profile is used by configured agent " + agent.Name}
		}
		for _, entry := range agent.Fallback {
			if entry == name {
				return &profileInUseError{message: "profile is used in fallback for configured agent " + agent.Name}
			}
		}
	}
	agents, err := s.core.ListAgents(context.Background())
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent.Profile == name {
			return &profileInUseError{message: "profile is used by agent " + agent.Name}
		}
		for _, entry := range agent.Fallback {
			if entry == name {
				return &profileInUseError{message: "profile is used in fallback for agent " + agent.Name}
			}
		}
	}
	return nil
}

type profileInUseError struct {
	message string
}

func (e *profileInUseError) Error() string { return e.message }

// agentDetail bundles an agent's durable defaults with its editable SOUL.md
// body so the web edit modal can load and save them together. MCPConfig stays
// redacted via the store.Agent json:"-" tag.
type agentDetail struct {
	store.Agent
	MCPServers []string `json:"MCPServers"`
	Soul       string   `json:"Soul"`
}

// agentUpdateRequest is the PUT body for editing an agent. Nil/empty engine
// fields fall back to the agent's current values; Soul, when non-nil, replaces
// the agent's SOUL.md.
type agentUpdateRequest struct {
	Provider       config.Provider       `json:"provider,omitempty"`
	Profile        *string               `json:"profile,omitempty"`
	Model          *string               `json:"model,omitempty"`
	Effort         *string               `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode,omitempty"`
	Fallback       *[]string             `json:"fallback,omitempty"`
	MCPServers     *[]string             `json:"mcp_servers,omitempty"`
	Soul           *string               `json:"soul,omitempty"`
}

type agentDeleteRequest struct {
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	if rest == "" {
		http.Error(w, "agent name is required", http.StatusBadRequest)
		return
	}
	// Sub-resources: /api/agents/{name}/memory, /{name}/dreams, /{name}/dream.
	// Dispatch on the segment after the name — unambiguous even for an agent
	// literally named "memory", since that only ever appears as the first segment.
	if name, sub, ok := strings.Cut(rest, "/"); ok {
		s.handleAgentSubresource(w, r, name, sub)
		return
	}
	name := rest

	switch r.Method {
	case http.MethodGet:
		agent, err := s.core.GetAgent(r.Context(), name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		soul, err := s.core.ReadAgentSoul(name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, agentDetail{Agent: agent, MCPServers: agent.MCPServers, Soul: soul}, nil)
	case http.MethodPut, http.MethodPatch:
		if err := s.refreshProfilesFromConfig(); err != nil {
			writeJSON(w, nil, err)
			return
		}
		agent, err := s.core.GetAgent(r.Context(), name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		var req agentUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Provider != "" {
			agent.Provider = req.Provider
		}
		if req.Profile != nil {
			agent.Profile = *req.Profile
		}
		if req.Model != nil {
			agent.Model = *req.Model
		}
		if req.Effort != nil {
			agent.Effort = *req.Effort
		}
		if req.PermissionMode != "" {
			agent.PermissionMode = req.PermissionMode
		}
		if req.Fallback != nil {
			agent.Fallback = *req.Fallback
		}
		if req.MCPServers != nil {
			agent.MCPServers = *req.MCPServers
		}
		updated, err := s.core.UpdateAgent(r.Context(), agent)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if req.Soul != nil {
			if err := s.core.WriteAgentSoul(name, *req.Soul); err != nil {
				writeJSON(w, nil, err)
				return
			}
		}
		soul, err := s.core.ReadAgentSoul(name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, agentDetail{Agent: updated, MCPServers: updated.MCPServers, Soul: soul}, nil)
	case http.MethodDelete:
		var req agentDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Confirmation) != name {
			http.Error(w, "confirmation must match agent name", http.StatusBadRequest)
			return
		}
		result, err := s.core.DeleteAgent(r.Context(), name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, result, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions, err := s.core.ListSessions(r.Context())
		writeJSON(w, sessions, err)
	case http.MethodPost:
		var req sessionCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Origin == "" {
			req.Origin = store.OriginCLI
		}
		session, err := s.core.CreateSession(r.Context(), core.CreateSessionRequest{
			AgentName:      req.AgentName,
			Origin:         req.Origin,
			Model:          req.Model,
			Effort:         req.Effort,
			PermissionMode: req.PermissionMode,
			ProjectID:      req.ProjectID,
		})
		if err == nil {
			s.log.Info("session create requested", "event", "run", "session", session.ID, "agent", session.AgentName, "provider", string(session.Provider), "origin", string(session.Origin), "project", session.ProjectID)
		}
		writeJSON(w, session, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type sessionDetail struct {
	Session     store.Session   `json:"session"`
	History     []store.Message `json:"history"`
	Task        *store.Task     `json:"task,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	ProjectName string          `json:"project_name,omitempty"`
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// handled below
	case http.MethodDelete:
		if err := s.core.DeleteSession(r.Context(), id); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.log.Info("session delete requested", "event", "run", "session", id)
		writeJSON(w, map[string]string{"deleted": id}, nil)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, err := s.core.GetSession(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	history, err := s.core.History(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	detail := sessionDetail{Session: session, History: history}
	detail.ProjectID = session.ProjectID
	if detail.ProjectID != "" {
		if project, err := s.core.GetProject(r.Context(), detail.ProjectID); err == nil {
			detail.ProjectName = project.Name
		}
	}
	// Attach roadmap task provenance when this session came from a task.
	if session.TaskID != "" {
		if task, err := s.core.GetTask(r.Context(), session.TaskID); err == nil {
			detail.Task = &task
			if detail.ProjectID == "" {
				detail.ProjectID = task.ProjectID
				if task.ProjectID != "" {
					if project, err := s.core.GetProject(r.Context(), task.ProjectID); err == nil {
						detail.ProjectName = project.Name
					}
				}
			}
			if detail.ProjectName == "" && detail.ProjectID != "" {
				if project, err := s.core.GetProject(r.Context(), detail.ProjectID); err == nil {
					detail.ProjectName = project.Name
				}
			}
		}
	}
	writeJSON(w, detail, nil)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var session store.Session
	var err error
	if req.SessionID == "" {
		if req.AgentName == "" {
			http.Error(w, "agent_name is required when session_id is omitted", http.StatusBadRequest)
			return
		}
		session, err = s.core.CreateSession(ctx, core.CreateSessionRequest{
			AgentName:      req.AgentName,
			Origin:         store.OriginCLI,
			Model:          req.Model,
			Effort:         req.Effort,
			PermissionMode: req.PermissionMode,
			ProjectID:      req.ProjectID,
		})
	} else {
		session, err = s.core.GetSession(ctx, req.SessionID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	writeStreamEvent(enc, flusher, streamEvent{Type: "session", Session: &session})

	slash, err := s.core.HandleSlashCommand(ctx, session.ID, req.Message)
	if err != nil {
		writeStreamEvent(enc, flusher, streamEvent{Type: "error", Error: err.Error()})
		return
	}
	if slash.Handled {
		writeStreamEvent(enc, flusher, streamEvent{Type: "session", Session: &slash.Session})
		writeStreamEvent(enc, flusher, streamEvent{Type: "notice", Notice: slash.Notice})
		writeStreamEvent(enc, flusher, streamEvent{Type: "done"})
		return
	}

	turnID := uuid.NewString()
	requests, unsubscribePermissions := s.broker.subscribe(turnID)
	inputs, unsubscribeInputs := s.input.subscribe(turnID)
	subscribed := true
	defer func() {
		if subscribed {
			unsubscribePermissions()
			unsubscribeInputs()
		}
	}()

	events, err := s.core.StreamTurn(ctx, session.ID, req.Message, core.TurnOptions{
		PermissionTurnID: turnID,
		PermissionRelay:  s.broker,
		UserInputRelay:   s.input,
	})
	if err != nil {
		writeStreamEvent(enc, flusher, streamEvent{Type: "error", Error: err.Error()})
		return
	}

	var sawDone, sawError bool
	for events != nil || requests != nil || inputs != nil {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-requests:
			if !ok {
				requests = nil
				continue
			}
			s.markRoadmapPermissionPending(ctx, session.ID, request.ID)
			writeStreamEvent(enc, flusher, streamEvent{Type: "permission_request", Request: &request})
		case input, ok := <-inputs:
			if !ok {
				inputs = nil
				continue
			}
			s.markRoadmapQuestionPending(ctx, session.ID, input.ID)
			writeStreamEvent(enc, flusher, streamEvent{Type: "user_input_request", Input: &input})
		case event, ok := <-events:
			if !ok {
				events = nil
				if subscribed {
					unsubscribePermissions()
					unsubscribeInputs()
					subscribed = false
					requests = nil
					inputs = nil
				}
				continue
			}
			switch event.Kind {
			case "message_stored":
				writeStreamEvent(enc, flusher, streamEvent{Type: "message", Message: event.Message})
			case adapter.EventAssistantDelta:
				writeStreamEvent(enc, flusher, streamEvent{Type: "delta", Delta: event.Content})
			case adapter.EventAssistantMessage:
				writeStreamEvent(enc, flusher, streamEvent{Type: "assistant", Delta: event.Content})
			case adapter.EventPermissionRequest:
				if event.PermissionRequest != nil {
					s.markRoadmapPermissionPending(ctx, session.ID, event.PermissionRequest.ID)
				}
				writeStreamEvent(enc, flusher, streamEvent{Type: "permission_request", Request: event.PermissionRequest})
			case adapter.EventUserInputRequest:
				if event.UserInputRequest != nil {
					s.markRoadmapQuestionPending(ctx, session.ID, event.UserInputRequest.ID)
				}
				writeStreamEvent(enc, flusher, streamEvent{Type: "user_input_request", Input: event.UserInputRequest})
			case adapter.EventTurnDone:
				sawDone = true
				s.markRoadmapSessionFinished(ctx, session.ID)
				writeStreamEvent(enc, flusher, streamEvent{Type: "done"})
			case "error":
				sawError = true
				writeStreamEvent(enc, flusher, streamEvent{Type: "error", Error: event.Content})
			}
		}
	}
	if !sawDone && !sawError && ctx.Err() == nil {
		s.markRoadmapSessionFinished(ctx, session.ID)
	}
	writeStreamEvent(enc, flusher, streamEvent{Type: "done"})
}

func (s *Server) handleUserInputDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/user-input-decisions/")
	var decision adapter.UserInputDecision
	if err := json.NewDecoder(r.Body).Decode(&decision); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if decision.Answers == nil {
		http.Error(w, "answers are required", http.StatusBadRequest)
		return
	}
	decided := s.input.decide(id, decision)
	restored := s.markRoadmapQuestionResolved(r.Context(), id)
	if !decided && !restored {
		http.Error(w, "user input request not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"}, nil)
}

func (s *Server) handlePermissionDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/permission-decisions/")
	var decision adapter.PermissionDecision
	if err := json.NewDecoder(r.Body).Decode(&decision); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if decision.Behavior != "allow" && decision.Behavior != "deny" {
		http.Error(w, "behavior must be allow or deny", http.StatusBadRequest)
		return
	}
	decided := s.broker.decide(id, decision)
	restored := s.markRoadmapPermissionResolved(r.Context(), id)
	if !decided && !restored {
		http.Error(w, "permission request not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"}, nil)
}

func (s *Server) handlePermissionRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	turnID := strings.TrimPrefix(r.URL.Path, "/api/permissions/")
	var req adapter.PermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.TurnID = turnID
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	timeout := defaultHTTPPermissionTimeout
	if rawTimeout := r.URL.Query().Get("timeout"); rawTimeout != "" {
		if parsed, err := time.ParseDuration(rawTimeout); err == nil {
			timeout = parsed
		}
	}
	decision, err := s.broker.RequestPermission(r.Context(), req, timeout)
	if err != nil && err != errPermissionTimeout && err != context.Canceled {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, decision, nil)
}

func writeJSON(w http.ResponseWriter, value any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeStreamEvent(enc *json.Encoder, flusher http.Flusher, event streamEvent) {
	_ = enc.Encode(event)
	if flusher != nil {
		flusher.Flush()
	}
}
