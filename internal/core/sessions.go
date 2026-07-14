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
	podiomtools "github.com/Podiom/Podiom/internal/tools"
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
	GoalID                         string
	ProjectID                      string
	CreatePlanBeforeImplementation bool
}

// RunTarget is the reusable provider/profile/model/effort template for a future
// session. An all-empty target means "use the agent default"; any explicit field
// makes model and effort required so a run is never half-specified.
type RunTarget struct {
	Provider config.Provider
	Profile  string
	Model    string
	Effort   string
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
	target, err := c.resolveRunTarget(agent, RunTarget{
		Provider: req.Provider,
		Profile:  req.Profile,
		Model:    req.Model,
		Effort:   req.Effort,
	})
	if err != nil {
		return store.Session{}, err
	}
	sess := store.Session{
		AgentName:      agent.Name,
		Provider:       target.Provider,
		Profile:        target.Profile,
		Model:          target.Model,
		Effort:         target.Effort,
		PermissionMode: agent.PermissionMode,
		Origin:         req.Origin,
		ScheduleID:     req.ScheduleID,
		RunID:          req.RunID,
		TaskID:         req.TaskID,
		GoalID:         req.GoalID,
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

	projectCtx, err := c.sessionProjectExecutionContext(ctx, created)
	if err != nil {
		return store.Session{}, err
	}
	payload, err := c.ComposeInstructionsForProvider(ctx, agent, created.Provider, projectCtx)
	if err != nil {
		return store.Session{}, err
	}
	mcpServers, mcpAll, err := c.agentMCPServers(agent)
	if err != nil {
		return store.Session{}, err
	}
	mcpServers, mcpAll = c.withInternalMCPServers(created, created.ID, mcpServers, mcpAll)
	nativeAgentName, nativeAgents, nativeErr := c.nativeAgentsForProvider(ctx, created.Provider, agent.Name)
	if nativeErr != nil {
		c.log.Warn("native agent projection failed",
			"event", "provider",
			"session", created.ID,
			"agent", agent.Name,
			"provider", string(created.Provider),
			"profile", created.Profile,
			"error", nativeErr,
		)
	}
	workspaceDir := c.sessionWorkspaceDir(agent.Name, projectCtx)
	extraWorkspaceDirs := c.sessionExtraWorkspaceDirs(workspaceDir, c.AgentPaths(agent.Name).Workspace, projectCtx)
	instructions := providerInstructionsForAdapter(created.Provider, projectCtx, payload.Bytes)
	handle, err := c.adapter.Start(ctx, adapter.StartRequest{
		SessionID:          created.ID,
		AgentName:          agent.Name,
		Provider:           created.Provider,
		Profile:            created.Profile,
		ProfileDir:         c.profileDir(created.Provider, created.Profile),
		Model:              created.Model,
		Effort:             created.Effort,
		PermissionMode:     created.PermissionMode,
		WorkspaceDir:       workspaceDir,
		ExtraWorkspaceDirs: extraWorkspaceDirs,
		ToolPathDirs:       podiomtools.PathDirs(c.AgentPaths(agent.Name).Tools),
		InstructionPath:    payload.Path,
		Instructions:       instructions,
		NativeAgentName:    nativeAgentName,
		NativeAgents:       nativeAgents,
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
		"workspace", workspaceDir,
		"extra_workspaces", len(extraWorkspaceDirs),
	)
	return updated, nil
}

func (c *Core) ValidateRunTargetForAgent(ctx context.Context, agentName string, target RunTarget) error {
	agent, err := c.store.GetAgent(ctx, agentName)
	if err != nil {
		return err
	}
	_, err = c.resolveRunTarget(agent, target)
	return err
}

func (c *Core) resolveRunTarget(agent store.Agent, requested RunTarget) (RunTarget, error) {
	provider := requested.Provider
	profile := strings.TrimSpace(requested.Profile)
	model := strings.TrimSpace(requested.Model)
	effort := strings.TrimSpace(requested.Effort)
	explicit := provider != "" || profile != "" || model != "" || effort != ""

	if explicit {
		if model == "" {
			return RunTarget{}, fmt.Errorf("model is required when choosing a run target")
		}
		if effort == "" {
			return RunTarget{}, fmt.Errorf("effort is required when choosing a run target")
		}
		if !validEffort(effort) {
			return RunTarget{}, fmt.Errorf("invalid effort %q", effort)
		}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if profile != "" {
		configured, ok := c.profiles[profile]
		if !ok {
			return RunTarget{}, fmt.Errorf("unknown profile %q", profile)
		}
		if provider == "" {
			provider = configured.Provider
		} else if configured.Provider != provider {
			return RunTarget{}, fmt.Errorf("profile %q belongs to provider %q, not %q", profile, configured.Provider, provider)
		}
	}
	if provider == "" {
		provider = agent.Provider
	} else if provider != config.ProviderClaude && provider != config.ProviderCodex {
		return RunTarget{}, fmt.Errorf("unknown provider %q (want claude|codex)", provider)
	}
	if profile == "" && provider == agent.Provider {
		profile = agent.Profile
	}
	if model == "" {
		model = agent.Model
	}
	if effort == "" {
		effort = agent.Effort
	}
	return RunTarget{Provider: provider, Profile: profile, Model: model, Effort: effort}, nil
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
	// FallbackRelay, when set, turns a reached session limit into an interactive
	// decision instead of a silent auto-fallback: on EventRateLimited the turn
	// blocks and asks the user whether to use the configured fallback or switch to
	// a chosen provider/profile. A nil relay (all non-interactive runs) keeps the
	// automatic fallback-chain behavior.
	FallbackRelay FallbackRelay
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
	NativeAgent       *adapter.NativeAgentActivity
	ToolUse           *adapter.ToolUse
	Message           *store.Message
	ContextStatus     *adapter.ContextStatus
	// Usage carries the session's updated cumulative billed-token totals after a
	// turn completes, so the client can refresh its usage bar live.
	Usage *store.SessionUsage
}

type turnOutput struct {
	assistant string
	reasoning string
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
			projectCtx, err := c.sessionProjectExecutionContext(ctx, current)
			if err != nil {
				runLog.Warn("turn failed", "stage", "project_context", "error", err)
				_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, err.Error())
				return
			}
			payload, err := c.sessionInstructionPayload(ctx, current, projectCtx)
			if err != nil {
				runLog.Warn("turn failed", "stage", "compose", "error", err)
				_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, err.Error())
				return
			}
			providerMessage := c.providerMessage(current, projectCtx, userMessage)
			mcpServers, mcpAll, err := c.sessionMCPServers(ctx, current)
			if err != nil {
				runLog.Warn("turn failed", "stage", "mcp", "error", err)
				_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, err.Error())
				return
			}
			nativeAgentName, nativeAgents, nativeErr := c.nativeAgentsForProvider(ctx, current.Provider, current.AgentName)
			if nativeErr != nil {
				runLog.Warn("native agent projection failed",
					"stage", "native_agents",
					"provider", string(current.Provider),
					"profile", current.Profile,
					"error", nativeErr,
				)
			}
			workspaceDir := c.sessionWorkspaceDir(current.AgentName, projectCtx)
			extraWorkspaceDirs := c.sessionExtraWorkspaceDirs(workspaceDir, c.AgentPaths(current.AgentName).Workspace, projectCtx)
			instructions := providerInstructionsForAdapter(current.Provider, projectCtx, payload.Bytes)
			// Each attempt gets its own cancelable context: a rate-limited provider
			// process can still be alive (the Claude CLI keeps retrying after an
			// api_retry event), and it must not keep executing tools while the
			// fallback target reruns the turn.
			attemptCtx, cancelAttempt := context.WithCancel(ctx)
			defer cancelAttempt()
			events, err := c.adapter.SendTurn(attemptCtx, c.turnRequest(current, history, providerMessage, opts, workspaceDir, extraWorkspaceDirs, payload.Path, instructions, nativeAgentName, nativeAgents, mcpServers, mcpAll))
			if err != nil {
				runLog.Warn("turn failed", "stage", "dispatch", "provider", string(current.Provider), "error", err)
				_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, err.Error())
				return
			}
			output, rateLimited, ok := c.consumeAdapterEvents(ctx, streamOut, sessionID, current.GoalID, current.Provider, current.Profile, current.ProviderHandle, events)
			if !ok {
				runLog.Info("turn aborted", "provider", string(current.Provider))
				return
			}
			if rateLimited {
				cancelAttempt()
				fallbacks++
				var next store.Session
				var err error
				if opts.FallbackRelay != nil {
					// Interactive turn: ask the user whether to use the configured
					// fallback or switch to a chosen provider/profile.
					next, err = c.interactiveFallbackSession(ctx, current, tried, opts.FallbackRelay, opts.PermissionTurnID)
				} else {
					// Non-interactive run: keep the silent auto-fallback behavior.
					next, err = c.nextFallbackSession(ctx, current, tried)
				}
				if err != nil {
					reportErr := err
					if rateLimited && !IsRateLimitErrorMessage(err.Error()) {
						reportErr = fmt.Errorf("rate limited on %s; fallback failed: %w", targetLabel(current.Provider, current.Profile), err)
					}
					runLog.Warn("turn failed", "stage", "fallback", "from", targetLabel(current.Provider, current.Profile), "error", err)
					_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, reportErr.Error())
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
			if output.assistant == "" && output.reasoning == "" {
				runLog.Info("turn finished", "provider", string(current.Provider), "reply_bytes", 0, "fallbacks", fallbacks, podiomlog.DurationMS("duration_ms", duration))
				return
			}
			var finalMessages []store.Message
			if output.reasoning != "" {
				finalMessages = append(finalMessages, store.Message{
					Role:    store.RoleAssistant,
					Kind:    store.KindReasoning,
					Content: output.reasoning,
				})
			}
			if output.assistant != "" {
				finalMessages = append(finalMessages, store.Message{
					Role:    store.RoleAssistant,
					Content: output.assistant,
				})
			}
			assistantMessages, err := c.appendFinalMessages(ctx, sessionID, finalMessages)
			if err != nil {
				runLog.Warn("turn failed", "stage", "persist", "error", err)
				_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, err.Error())
				return
			}
			if !c.noBg {
				go c.autoNameSessionBackground(sessionID)
				go c.refreshRollingSummaryBackground(sessionID)
			}
			runLog.Info("turn finished", "provider", string(current.Provider), "reply_bytes", len(output.assistant), "reasoning_bytes", len(output.reasoning), "fallbacks", fallbacks, podiomlog.DurationMS("duration_ms", duration))
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

// AppendErrorMessage persists a session-scoped diagnostic entry for display in
// Podiom history. It is not replayed back to providers.
func (c *Core) AppendErrorMessage(ctx context.Context, sessionID, content string) (store.Message, error) {
	messages, err := c.appendErrorMessage(ctx, sessionID, content)
	if err != nil {
		return store.Message{}, err
	}
	if len(messages) == 0 {
		return store.Message{}, fmt.Errorf("append error message to session %q: no message inserted", sessionID)
	}
	return messages[0], nil
}

func (c *Core) appendErrorMessage(ctx context.Context, sessionID, content string) ([]store.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "Unknown server error"
	}
	return c.appendFinalMessages(ctx, sessionID, []store.Message{{
		Role:    store.RoleAssistant,
		Kind:    store.KindError,
		Content: content,
	}})
}

func (c *Core) sendPersistedTurnError(ctx context.Context, streamOut chan<- TurnEvent, sessionID, content string) bool {
	messages, err := c.appendErrorMessage(ctx, sessionID, content)
	if err == nil {
		for _, msg := range messages {
			msg := msg
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "message_stored", Message: &msg}) {
				return false
			}
		}
	}
	if err != nil && strings.TrimSpace(content) == "" {
		content = err.Error()
	}
	return sendTurnEvent(ctx, streamOut, TurnEvent{Kind: "error", Content: content})
}

func (c *Core) appendFinalMessages(ctx context.Context, sessionID string, messages []store.Message) ([]store.Message, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return c.store.AppendMessages(persistCtx, sessionID, messages)
}

func (c *Core) sessionInstructionPayload(ctx context.Context, sess store.Session, projectCtx projectExecutionContext) (InstructionPayload, error) {
	agent, err := c.store.GetAgent(ctx, sess.AgentName)
	if err != nil {
		return InstructionPayload{}, err
	}
	return c.ComposeInstructionsForProvider(ctx, agent, sess.Provider, projectCtx)
}

func (c *Core) sessionWorkspaceDir(agentName string, projectCtx projectExecutionContext) string {
	if strings.TrimSpace(projectCtx.Root) != "" {
		return projectCtx.Root
	}
	return c.AgentPaths(agentName).Workspace
}

func (c *Core) providerMessage(sess store.Session, projectCtx projectExecutionContext, userMessage string) string {
	var parts []string
	if PlanGateActive(sess) {
		parts = append(parts, c.planModePrompt(sess, projectCtx))
	}
	if strings.TrimSpace(projectCtx.Prompt) != "" {
		parts = append(parts, projectCtx.Prompt)
	}
	if len(parts) == 0 {
		return userMessage
	}
	return strings.Join(parts, "\n\n") + "\n\nUser message:\n" + userMessage
}

// sessionExtraWorkspaceDirs returns directories exposed beyond the primary cwd.
// Project sessions keep the agent workspace visible for skills/instruction
// artifacts while making durable project files land under ~/.podiom/projects.
func (c *Core) sessionExtraWorkspaceDirs(workspaceDir, agentWorkspace string, projectCtx projectExecutionContext) []string {
	candidates := nonEmptyStrings(agentWorkspace, c.paths.ProjectsDir, projectCtx.ProjectDir, projectCtx.Root)
	var out []string
	for _, dir := range candidates {
		if dir == workspaceDir {
			continue
		}
		out = append(out, dir)
	}
	return out
}

func (c *Core) turnRequest(sess store.Session, history []store.Message, userMessage string, opts TurnOptions, workspaceDir string, extraWorkspaceDirs []string, instructionPath string, instructions []byte, nativeAgentName string, nativeAgents []adapter.NativeAgent, mcpServers, mcpAll []podiommcp.Server) adapter.TurnRequest {
	effectivePermission := sess.PermissionMode
	relay := opts.PermissionRelay
	if PlanGateActive(sess) {
		effectivePermission = config.PermissionApprove
		relay = NewPlanGateRelay(c.log)
	}
	// Keep the generated provider MCP profile stable for the life of the
	// session. Codex app-server stores freshly-created thread rollouts in the
	// profile-scoped process; changing the internal plan MCP args between
	// StartRequest and the first TurnRequest forces a different app-server and
	// makes Codex unable to resume the new thread.
	mcpServers, mcpAll = c.withInternalMCPServers(sess, sess.ID, mcpServers, mcpAll)
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
			WorkspaceDir:       workspaceDir,
			ExtraWorkspaceDirs: extraWorkspaceDirs,
			ToolPathDirs:       podiomtools.PathDirs(c.AgentPaths(sess.AgentName).Tools),
			InstructionPath:    instructionPath,
			Instructions:       instructions,
			NativeAgentName:    nativeAgentName,
			NativeAgents:       nativeAgents,
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

// withInternalMCPServers appends Podiom's built-in stdio MCP servers to a
// session's assigned set: the plan-submission helper and the self-management
// helper (roadmap/projects/schedules/skills/mcp/config/logs/agents). Both are
// appended unconditionally after catalogue resolution, so an agent cannot
// un-inject them by changing its own MCP assignments.
//
// Every arg here must be session-stable (never per-turn): Codex stores freshly
// created thread rollouts in the profile-scoped app-server, so a profile that
// changes between StartRequest and the first TurnRequest breaks resume.
func (c *Core) withInternalMCPServers(sess store.Session, turnID string, assigned, all []podiommcp.Server) ([]podiommcp.Server, []podiommcp.Server) {
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
	plan := podiommcp.Server{
		Name:      "podiom_plan",
		Transport: podiommcp.TransportStdio,
		Command:   exe,
		Args: []string{
			"plan-mcp",
			"--addr", c.daemonAddr,
			"--session", sess.ID,
			"--turn", turnID,
		},
		EnvVars: podiommcp.EnvVars{{Name: config.EnvHome, Value: c.paths.Home}},
		Sources: []podiommcp.Source{podiommcp.SourcePodiom},
	}
	manage := podiommcp.Server{
		Name:      "podiom_manage",
		Transport: podiommcp.TransportStdio,
		Command:   exe,
		Args: []string{
			"manage-mcp",
			"--addr", c.daemonAddr,
			"--session", sess.ID,
			"--agent", sess.AgentName,
		},
		EnvVars: podiommcp.EnvVars{{Name: config.EnvHome, Value: c.paths.Home}},
		Sources: []podiommcp.Source{podiommcp.SourcePodiom},
	}
	return append(assigned, plan, manage), append(all, plan, manage)
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

func (c *Core) consumeAdapterEvents(ctx context.Context, streamOut chan<- TurnEvent, sessionID, goalID string, provider config.Provider, profile, providerHandle string, events <-chan adapter.Event) (turnOutput, bool, bool) {
	var assistant, reasoning strings.Builder
	currentProviderHandle := providerHandle
	for event := range events {
		switch event.Kind {
		case adapter.EventReasoningDelta:
			reasoning.WriteString(event.Content)
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Content: event.Content}) {
				return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
			}
		case adapter.EventReasoningMessage:
			reasoning.Reset()
			reasoning.WriteString(event.Content)
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Content: event.Content}) {
				return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
			}
		case adapter.EventAssistantDelta:
			assistant.WriteString(event.Content)
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Content: event.Content}) {
				return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
			}
		case adapter.EventAssistantMessage:
			assistant.Reset()
			assistant.WriteString(event.Content)
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Content: event.Content}) {
				return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
			}
		case adapter.EventPermissionRequest:
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, PermissionRequest: event.PermissionRequest}) {
				return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
			}
		case adapter.EventUserInputRequest:
			if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, UserInputRequest: event.UserInputRequest}) {
				return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
			}
		case adapter.EventHandleUpdated:
			if event.Handle != nil {
				if event.Handle.ID == currentProviderHandle {
					continue
				}
				if _, err := c.store.UpdateSessionProviderHandle(ctx, sessionID, event.Handle.ID); err != nil {
					_ = c.sendPersistedTurnError(ctx, streamOut, sessionID, err.Error())
					return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
				}
				currentProviderHandle = event.Handle.ID
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
		case adapter.EventContextStatus:
			if event.ContextStatus != nil {
				// Persist the latest utilization so the composer ring restores on
				// reload; a failed write is non-fatal to the turn (best effort).
				if err := c.store.UpdateSessionContext(ctx, sessionID, event.ContextStatus.UsedTokens, event.ContextStatus.MaxTokens); err != nil {
					c.log.Warn("persist session context failed", "event", "provider", "session", sessionID, "error", err)
				}
				if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, ContextStatus: event.ContextStatus}) {
					return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
				}
			}
		case adapter.EventTurnUsage:
			if event.TurnUsage != nil {
				delta := store.SessionUsage{
					InputTokens:      event.TurnUsage.Input,
					OutputTokens:     event.TurnUsage.Output,
					CacheReadTokens:  event.TurnUsage.CacheRead,
					CacheWriteTokens: event.TurnUsage.CacheWrite,
				}
				// Accumulate into the session's lifetime total (best effort — a
				// failed write must not abort the turn) and feed the calibrator.
				total, err := c.store.AddSessionUsage(ctx, sessionID, delta)
				if err != nil {
					c.log.Warn("persist session usage failed", "event", "provider", "session", sessionID, "error", err)
				} else {
					if c.onTurnUsage != nil {
						profileKey := profile
						if profileKey == "" {
							profileKey = string(provider)
						}
						c.onTurnUsage(profileKey, provider, delta.Total())
					}
					usage := total
					if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, Usage: &usage}) {
						return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
					}
				}
			}
		case adapter.EventNativeAgentActivity:
			if event.NativeAgent != nil {
				if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, NativeAgent: event.NativeAgent}) {
					return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
				}
			}
		case adapter.EventToolUse:
			if event.ToolUse != nil {
				// Record every tool call on the goal timeline for goal-linked runs —
				// the audit counterweight to yolo. Best effort: a failed append is
				// logged, never aborting the turn.
				if goalID != "" {
					c.appendGoalToolUseEvent(ctx, goalID, sessionID, *event.ToolUse)
				}
				if !sendTurnEvent(ctx, streamOut, TurnEvent{Kind: event.Kind, ToolUse: event.ToolUse}) {
					return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, false
				}
			}
		case adapter.EventRateLimited:
			return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, true, true
		case adapter.EventTurnDone:
		}
	}
	return turnOutput{assistant: assistant.String(), reasoning: reasoning.String()}, false, true
}

// History returns a session's canonical history.
func (c *Core) History(ctx context.Context, sessionID string) ([]store.Message, error) {
	return c.store.ListMessages(ctx, sessionID)
}

// ComposeInstructions composes instructions using the session provider's
// delivery mode without sending them to a real provider.
func (c *Core) ComposeInstructions(ctx context.Context, agent store.Agent) (InstructionPayload, error) {
	return c.ComposeInstructionsForProvider(ctx, agent, agent.Provider, projectExecutionContext{})
}

// ComposeInstructionsForProvider composes the same agent identity for a
// specific provider target. It is used when a session switches provider while
// staying bound to the same Podiom agent.
func (c *Core) ComposeInstructionsForProvider(ctx context.Context, agent store.Agent, provider config.Provider, projectCtx projectExecutionContext) (InstructionPayload, error) {
	switch provider {
	case "claude":
		return c.composer.Compose(ctx, agent, DeliveryClaudeImport, projectCtx.Instructions)
	case "codex":
		return c.composer.Compose(ctx, agent, DeliveryCodexBundle, projectCtx.Instructions)
	default:
		return InstructionPayload{}, fmt.Errorf("unknown provider %q", provider)
	}
}

func providerInstructionsForAdapter(provider config.Provider, projectCtx projectExecutionContext, instructions []byte) []byte {
	if provider == config.ProviderCodex && strings.TrimSpace(projectCtx.ProjectDir) == "" {
		return nil
	}
	return instructions
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
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
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
