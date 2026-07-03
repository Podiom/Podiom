package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

// CreateSessionRequest creates a durable session bound to an agent. Empty
// settings inherit the agent defaults; origin is immutable after creation.
type CreateSessionRequest struct {
	AgentName                      string
	Origin                         store.SessionOrigin
	Provider                       config.Provider
	Profile                        string
	Model                          string
	Effort                         string
	PermissionMode                 config.PermissionMode
	ScheduleID                     string
	RunID                          string
	TaskID                         string
	ProjectID                      string
	CreatePlanBeforeImplementation bool
}

// CreateSession creates a durable session and starts a fake/provider backing
// handle that can be resumed later.
func (c *Core) CreateSession(ctx context.Context, req CreateSessionRequest) (store.Session, error) {
	agent, err := c.store.GetAgent(ctx, req.AgentName)
	if err != nil {
		return store.Session{}, err
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID != "" {
		if _, err := c.ledger.Get(projectID); err != nil {
			return store.Session{}, err
		}
	}
	provider, profile, err := c.resolveSessionTarget(agent, req.Provider, req.Profile)
	if err != nil {
		return store.Session{}, err
	}
	model := req.Model
	if model == "" && provider == agent.Provider {
		model = agent.Model
	}
	sess := store.Session{
		AgentName:      agent.Name,
		Provider:       provider,
		Profile:        profile,
		Model:          model,
		Effort:         firstNonEmpty(req.Effort, agent.Effort),
		PermissionMode: agent.PermissionMode,
		Origin:         req.Origin,
		ScheduleID:     req.ScheduleID,
		RunID:          req.RunID,
		TaskID:         req.TaskID,
		ProjectID:      projectID,
		PlanState:      store.PlanNone,
	}
	if req.CreatePlanBeforeImplementation {
		sess.PlanState = store.PlanPendingSubmission
		sess.PlanExplicit = true
	}
	if req.PermissionMode != "" {
		sess.PermissionMode = req.PermissionMode
	}
	if sess.Origin == "" {
		return store.Session{}, fmt.Errorf("session origin is required")
	}

	created, err := c.store.CreateSession(ctx, sess)
	if err != nil {
		return store.Session{}, err
	}

	payload, err := c.ComposeInstructionsForProvider(ctx, agent, created.Provider)
	if err != nil {
		return store.Session{}, err
	}
	projectCtx, err := c.sessionProjectExecutionContext(ctx, created)
	if err != nil {
		return store.Session{}, err
	}
	mcpServers, mcpAll, err := c.agentMCPServers(agent)
	if err != nil {
		return store.Session{}, err
	}
	mcpServers, mcpAll = c.withInternalPlanMCP(created, created.ID, mcpServers, mcpAll)
	handle, err := c.adapter.Start(ctx, adapter.StartRequest{
		SessionID:          created.ID,
		AgentName:          agent.Name,
		Provider:           created.Provider,
		Profile:            created.Profile,
		ProfileDir:         c.profileDir(created.Provider, created.Profile),
		Model:              created.Model,
		Effort:             created.Effort,
		PermissionMode:     created.PermissionMode,
		WorkspaceDir:       c.AgentPaths(agent.Name).Workspace,
		ExtraWorkspaceDirs: c.sessionExtraWorkspaceDirs(projectCtx),
		Instructions:       payload.Bytes,
		MCPServers:         mcpServers,
		MCPAllServers:      mcpAll,
	})
	if err != nil {
		return store.Session{}, err
	}
	updated, err := c.store.UpdateSessionProviderHandle(ctx, created.ID, handle.ID)
	if err != nil {
		return store.Session{}, err
	}
	c.log.Info("session created",
		"event", "run",
		"session", updated.ID,
		"agent", updated.AgentName,
		"provider", string(updated.Provider),
		"profile", updated.Profile,
		"origin", string(updated.Origin),
		"permission", string(updated.PermissionMode),
		"schedule", updated.ScheduleID,
		"run", updated.RunID,
		"task", updated.TaskID,
		"project", updated.ProjectID,
		"provider_handle_set", updated.ProviderHandle != "",
		"mcp_servers", len(mcpServers),
		"extra_workspaces", len(c.sessionExtraWorkspaceDirs(projectCtx)),
	)
	return updated, nil
}

func (c *Core) resolveSessionTarget(agent store.Agent, requestedProvider config.Provider, requestedProfile string) (config.Provider, string, error) {
	provider := requestedProvider
	profile := strings.TrimSpace(requestedProfile)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if profile != "" {
		configured, ok := c.profiles[profile]
		if !ok {
			return "", "", fmt.Errorf("unknown profile %q", profile)
		}
		if provider == "" {
			provider = configured.Provider
		} else if configured.Provider != provider {
			return "", "", fmt.Errorf("profile %q belongs to provider %q, not %q", profile, configured.Provider, provider)
		}
	}
	if provider == "" {
		provider = agent.Provider
	} else if provider != config.ProviderClaude && provider != config.ProviderCodex {
		return "", "", fmt.Errorf("unknown provider %q (want claude|codex)", provider)
	}
	if profile == "" && provider == agent.Provider {
		profile = agent.Profile
	}
	return provider, profile, nil
}

// ListSessions returns all durable sessions, with a compatibility ProjectID
// fallback for roadmap sessions created before sessions.project_id existed.
func (c *Core) ListSessions(ctx context.Context) ([]store.Session, error) {
	sessions, err := c.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	c.enrichLegacyProjectIDs(ctx, sessions)
	return sessions, nil
}

func (c *Core) enrichLegacyProjectIDs(ctx context.Context, sessions []store.Session) {
	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return // sessions are still usable without project enrichment
	}
	projectByTask := make(map[string]string, len(tasks))
	for _, t := range tasks {
		projectByTask[t.ID] = t.ProjectID
	}
	for i := range sessions {
		if sessions[i].ProjectID == "" && sessions[i].TaskID != "" {
			sessions[i].ProjectID = projectByTask[sessions[i].TaskID]
		}
	}
}

// GetSession fetches a durable session.
func (c *Core) GetSession(ctx context.Context, id string) (store.Session, error) {
	sess, err := c.store.GetSession(ctx, id)
	if err != nil {
		return store.Session{}, err
	}
	if sess.ProjectID == "" && sess.TaskID != "" {
		if task, err := c.store.GetTask(ctx, sess.TaskID); err == nil {
			sess.ProjectID = task.ProjectID
		}
	}
	return sess, nil
}

// DeleteSession removes a durable session and its message history. Like agent
// deletion, it does not explicitly stop a running provider adapter — the handle
// simply becomes unreferenced — so an in-flight turn should be avoided.
func (c *Core) DeleteSession(ctx context.Context, id string) error {
	sess, err := c.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if err := c.store.DeleteSession(ctx, id); err != nil {
		return err
	}
	c.log.Info("session deleted",
		"event", "run",
		"session", sess.ID,
		"agent", sess.AgentName,
		"provider", string(sess.Provider),
		"origin", string(sess.Origin),
		"schedule", sess.ScheduleID,
		"run", sess.RunID,
		"task", sess.TaskID,
		"project", sess.ProjectID,
	)
	return nil
}

// UpdateSessionSettings changes mutable per-session turn settings without
// appending a slash command to chat history. Empty fields keep their current
// values.
func (c *Core) UpdateSessionSettings(ctx context.Context, id, model, effort string, permissionMode config.PermissionMode) (store.Session, error) {
	sess, err := c.store.GetSession(ctx, id)
	if err != nil {
		return store.Session{}, err
	}
	if model == "" {
		model = sess.Model
	}
	if effort == "" {
		effort = sess.Effort
	} else if !validEffort(effort) {
		return store.Session{}, fmt.Errorf("invalid effort %q", effort)
	}
	if permissionMode == "" {
		permissionMode = sess.PermissionMode
	} else if permissionMode != config.PermissionApprove && permissionMode != config.PermissionYolo {
		return store.Session{}, fmt.Errorf("invalid permission mode %q", permissionMode)
	}
	updated, err := c.store.UpdateSessionSettings(ctx, id, model, effort, permissionMode)
	if err != nil {
		return store.Session{}, err
	}
	c.log.Info("session settings updated",
		"event", "run",
		"session", updated.ID,
		"agent", updated.AgentName,
		"provider", string(updated.Provider),
		"profile", updated.Profile,
		"changed", podiomlog.ChangedFields(
			map[string]string{"model": sess.Model, "effort": sess.Effort, "permission": string(sess.PermissionMode)},
			map[string]string{"model": updated.Model, "effort": updated.Effort, "permission": string(updated.PermissionMode)},
		),
	)
	return updated, nil
}

// TurnOptions configures one live adapter turn.
type TurnOptions struct {
	PermissionTurnID string
	PermissionRelay  adapter.PermissionRelay
	UserInputRelay   adapter.UserInputRelay
	// Unattended marks a turn with no human approver (a scheduled run). It and
	// AllowedTools select the provider's preapproved policy (§7.7).
	Unattended   bool
	AllowedTools []string
}

// TurnEvent is streamed by core while an adapter turn is running.
type TurnEvent struct {
	Kind              adapter.EventKind
	Content           string
	PermissionRequest *adapter.PermissionRequest
	UserInputRequest  *adapter.UserInputRequest
	Message           *store.Message
}

// AppendTurn persists the user turn, drives the adapter, persists the assistant
// reply, and returns the new history entries.
func (c *Core) AppendTurn(ctx context.Context, sessionID, userMessage string) ([]store.Message, error) {
	events, err := c.StreamTurn(ctx, sessionID, userMessage, TurnOptions{})
	if err != nil {
		return nil, err
	}
	var messages []store.Message
	for event := range events {
		if event.Message != nil {
			messages = append(messages, *event.Message)
		}
	}
	return messages, nil
}

// StreamTurn persists the user turn, streams adapter events, persists the final
// assistant reply, and emits the newly stored messages.
func (c *Core) StreamTurn(ctx context.Context, sessionID, userMessage string, opts TurnOptions) (<-chan TurnEvent, error) {
	if strings.TrimSpace(userMessage) == "" {
		return nil, fmt.Errorf("user message is required")
	}
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	history, err := c.store.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	userMessages, err := c.store.AppendMessages(ctx, sessionID, []store.Message{{
		Role:    store.RoleUser,
		Content: userMessage,
	}})
	if err != nil {
		return nil, err
	}
	streamOut := make(chan TurnEvent, 16)
	go func() {
		defer close(streamOut)
		for _, msg := range userMessages {
			msg := msg
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "message_stored", Message: &msg}) {
				return
			}
		}

		current := sess
		tried := map[string]bool{}
		runLog := c.log.With(
			"event", "run",
			"session", sess.ID,
			"agent", sess.AgentName,
			"origin", string(sess.Origin),
			"unattended", opts.Unattended,
		)
		runLog.Info("turn started",
			"provider", string(current.Provider),
			"profile", current.Profile,
			"permission", string(current.PermissionMode),
		)
		startedAt := time.Now()
		fallbacks := 0
		for {
			tried[targetKey(current.Provider, current.Profile)] = true
			if err := c.ensureSessionInstructions(ctx, current); err != nil {
				runLog.Warn("turn failed", "stage", "compose", "error", err)
				_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
				return
			}
			projectCtx, err := c.sessionProjectExecutionContext(ctx, current)
			if err != nil {
				runLog.Warn("turn failed", "stage", "project_context", "error", err)
				_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
				return
			}
			providerMessage := userMessage
			if strings.TrimSpace(projectCtx.Prompt) != "" {
				providerMessage = projectCtx.Prompt + "\n\nUser message:\n" + userMessage
			}
			mcpServers, mcpAll, err := c.sessionMCPServers(ctx, current)
			if err != nil {
				runLog.Warn("turn failed", "stage", "mcp", "error", err)
				_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
				return
			}
			events, err := c.adapter.SendTurn(ctx, c.turnRequest(current, history, providerMessage, opts, c.sessionExtraWorkspaceDirs(projectCtx), mcpServers, mcpAll))
			if err != nil {
				runLog.Warn("turn failed", "stage", "dispatch", "provider", string(current.Provider), "error", err)
				_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
				return
			}
			assistant, rateLimited, ok := c.consumeAdapterEvents(ctx, streamOut, sessionID, current.Provider, current.Profile, events)
			if !ok {
				runLog.Info("turn aborted", "provider", string(current.Provider))
				return
			}
			if rateLimited {
				fallbacks++
				next, err := c.nextFallbackSession(ctx, current, tried)
				if err != nil {
					runLog.Warn("turn failed", "stage", "fallback", "from", targetLabel(current.Provider, current.Profile), "error", err)
					_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
					return
				}
				runLog.Info("turn fallback",
					"from", targetLabel(current.Provider, current.Profile),
					"to", targetLabel(next.Provider, next.Profile),
					"fallback_from", targetLabel(current.Provider, current.Profile),
					"fallback_to", targetLabel(next.Provider, next.Profile),
					"rate_limited", true,
				)
				current = next
				continue
			}
			duration := time.Since(startedAt)
			if assistant.Len() == 0 {
				runLog.Info("turn finished", "provider", string(current.Provider), "reply_bytes", 0, "fallbacks", fallbacks, podiomlog.DurationMS("duration_ms", duration))
				return
			}
			assistantMessages, err := c.appendFinalMessages(ctx, sessionID, []store.Message{{
				Role:    store.RoleAssistant,
				Content: assistant.String(),
			}})
			if err != nil {
				runLog.Warn("turn failed", "stage", "persist", "error", err)
				_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
				return
			}
			if !c.noBg {
				go c.autoNameSessionBackground(sessionID)
				go c.refreshRollingSummaryBackground(sessionID)
			}
			runLog.Info("turn finished", "provider", string(current.Provider), "reply_bytes", assistant.Len(), "fallbacks", fallbacks, podiomlog.DurationMS("duration_ms", duration))
			for _, msg := range assistantMessages {
				msg := msg
				if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "message_stored", Message: &msg}) {
					return
				}
			}
			_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: adapter.EventTurnDone})
			return
		}
	}()
	return streamOut, nil
}

func (c *Core) appendFinalMessages(ctx context.Context, sessionID string, messages []store.Message) ([]store.Message, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return c.store.AppendMessages(persistCtx, sessionID, messages)
}

// sessionExtraWorkspaceDirs returns the directories exposed to a session's
// provider process beyond its own workspace. The shared project ledger under
// ~/.podiom/projects/ is always included so agents can honor the base operating
// rule to consult projects.yaml; a roadmap session additionally gets its bound
// project's downloaded source snapshot repo directory (projectCtx.Root).
func (c *Core) sessionExtraWorkspaceDirs(projectCtx projectExecutionContext) []string {
	return nonEmptyStrings(c.paths.ProjectsDir, projectCtx.Root)
}

func (c *Core) turnRequest(sess store.Session, history []store.Message, userMessage string, opts TurnOptions, extraWorkspaceDirs []string, mcpServers, mcpAll []podiommcp.Server) adapter.TurnRequest {
	effectivePermission := sess.PermissionMode
	relay := opts.PermissionRelay
	if PlanGateActive(sess) {
		effectivePermission = config.PermissionApprove
		relay = NewPlanGateRelay(c.log)
	}
	mcpServers, mcpAll = c.withInternalPlanMCP(sess, opts.PermissionTurnID, mcpServers, mcpAll)
	return adapter.TurnRequest{
		SessionID: sess.ID,
		Handle: adapter.Handle{
			Provider: sess.Provider,
			ID:       sess.ProviderHandle,
		},
		Message: userMessage,
		History: replayHistory(sess, history),
		Settings: adapter.TurnSettings{
			AgentName:          sess.AgentName,
			Profile:            sess.Profile,
			ProfileDir:         c.profileDir(sess.Provider, sess.Profile),
			Model:              sess.Model,
			Effort:             sess.Effort,
			PermissionMode:     effectivePermission,
			WorkspaceDir:       c.AgentPaths(sess.AgentName).Workspace,
			ExtraWorkspaceDirs: extraWorkspaceDirs,
			PermissionTurnID:   firstNonEmpty(opts.PermissionTurnID, fmt.Sprintf("%s-%d", sess.ID, time.Now().UnixNano())),
			PermissionTimeout:  c.permissionTimeout(),
			Unattended:         opts.Unattended,
			AllowedTools:       opts.AllowedTools,
			MCPServers:         mcpServers,
			MCPAllServers:      mcpAll,
		},
		Relay: relay,
		Input: opts.UserInputRelay,
	}
}

func (c *Core) withInternalPlanMCP(sess store.Session, turnID string, assigned, all []podiommcp.Server) ([]podiommcp.Server, []podiommcp.Server) {
	if c.daemonAddr == "" {
		return assigned, all
	}
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return assigned, all
	}
	if turnID == "" {
		turnID = sess.ID
	}
	server := podiommcp.Server{
		Name:      "podiom_plan",
		Transport: podiommcp.TransportStdio,
		Command:   exe,
		Args: []string{
			"plan-mcp",
			"--addr", c.daemonAddr,
			"--session", sess.ID,
			"--turn", turnID,
		},
		Sources: []podiommcp.Source{podiommcp.SourcePodiom},
	}
	return append(assigned, server), append(all, server)
}

func (c *Core) permissionTimeout() time.Duration {
	raw := c.GetGlobal().PermissionTimeout
	if raw == "" {
		raw = config.DefaultPermissionTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		d, _ = time.ParseDuration(config.DefaultPermissionTimeout)
	}
	return d
}

func (c *Core) sessionMCPServers(ctx context.Context, sess store.Session) ([]podiommcp.Server, []podiommcp.Server, error) {
	agent, err := c.store.GetAgent(ctx, sess.AgentName)
	if err != nil {
		return nil, nil, err
	}
	return c.agentMCPServers(agent)
}

func (c *Core) agentMCPServers(agent store.Agent) ([]podiommcp.Server, []podiommcp.Server, error) {
	cat, err := podiommcp.LoadCatalogue(c.paths.MCPYAML)
	if err != nil {
		return nil, nil, err
	}
	assigned, err := podiommcp.Assigned(cat, agent.MCPServers)
	if err != nil {
		return nil, nil, err
	}
	return assigned, cat.Servers, nil
}

func (c *Core) consumeAdapterEvents(ctx context.Context, streamOut chan<- TurnEvent, sessionID string, provider config.Provider, profile string, events <-chan adapter.Event) (strings.Builder, bool, bool) {
	var assistant strings.Builder
	for event := range events {
		switch event.Kind {
		case adapter.EventAssistantDelta:
			assistant.WriteString(event.Content)
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Content: event.Content}) {
				return assistant, false, false
			}
		case adapter.EventAssistantMessage:
			assistant.Reset()
			assistant.WriteString(event.Content)
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Content: event.Content}) {
				return assistant, false, false
			}
		case adapter.EventPermissionRequest:
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, PermissionRequest: event.PermissionRequest}) {
				return assistant, false, false
			}
		case adapter.EventUserInputRequest:
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, UserInputRequest: event.UserInputRequest}) {
				return assistant, false, false
			}
		case adapter.EventHandleUpdated:
			if event.Handle != nil {
				if _, err := c.store.UpdateSessionProviderHandle(ctx, sessionID, event.Handle.ID); err != nil {
					_ = sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: err.Error()})
					return assistant, false, false
				}
				c.log.Info("provider handle stored",
					"event", "provider",
					"session", sessionID,
					"provider", string(event.Handle.Provider),
					"provider_handle_set", event.Handle.ID != "",
				)
			}
		case adapter.EventRateStatus:
			if event.RateStatus != nil {
				if c.onRateStatus != nil {
					profileKey := profile
					if profileKey == "" {
						profileKey = string(provider)
					}
					c.onRateStatus(profileKey, provider, *event.RateStatus)
				}
				if event.RateStatus.UsedPercent >= 80 {
					go c.refreshRollingSummaryBackground(sessionID)
				}
			}
		case adapter.EventRateLimited:
			return assistant, true, true
		case adapter.EventTurnDone:
		}
	}
	return assistant, false, true
}

// History returns a session's canonical history.
func (c *Core) History(ctx context.Context, sessionID string) ([]store.Message, error) {
	return c.store.ListMessages(ctx, sessionID)
}

// ComposeInstructions composes instructions using the session provider's
// delivery mode without sending them to a real provider.
func (c *Core) ComposeInstructions(ctx context.Context, agent store.Agent) (InstructionPayload, error) {
	return c.ComposeInstructionsForProvider(ctx, agent, agent.Provider)
}

// ComposeInstructionsForProvider composes the same agent identity for a
// specific provider target. It is used when a session switches provider while
// staying bound to the same Podiom agent.
func (c *Core) ComposeInstructionsForProvider(ctx context.Context, agent store.Agent, provider config.Provider) (InstructionPayload, error) {
	switch provider {
	case "claude":
		return c.composer.Compose(ctx, agent, DeliveryClaudeImport)
	case "codex":
		return c.composer.Compose(ctx, agent, DeliveryCodexBundle)
	default:
		return InstructionPayload{}, fmt.Errorf("unknown provider %q", provider)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func sendTurnEvent(ctx context.Context, ch chan<- TurnEvent, event TurnEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case ch <- event:
		return true
	}
}
