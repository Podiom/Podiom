package adapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

var errCodexTransport = errors.New("codex app-server transport failed")

// CodexOptions configures the OpenAI Codex adapter.
type CodexOptions struct {
	Discovery         podiomexec.Discovery
	PermissionTimeout time.Duration
	// ExtraEnv supplies additional NAME=value pairs for the app-server
	// subprocess environment (user-granted credentials). Read at app-server
	// spawn time. Nil means none.
	ExtraEnv func() []string
	Logger   *slog.Logger
}

// Codex drives a long-lived `codex app-server --listen stdio://` process.
// Generated MCP profile content is passed as app-server config overrides
// because current Codex app-server does not accept the root --profile flag.
// A separate app-server is maintained for each
// CODEX_HOME plus generated MCP profile hash.
type Codex struct {
	bin               string
	permissionTimeout time.Duration
	extraEnv          func() []string

	mu      sync.Mutex
	clients map[string]*codexClient
	log     *slog.Logger
}

// NewCodex discovers the Codex CLI and returns an adapter.
func NewCodex(opts CodexOptions) (*Codex, error) {
	found, err := opts.Discovery.Find("codex")
	if err != nil {
		return nil, err
	}
	timeout := opts.PermissionTimeout
	if timeout == 0 {
		timeout = defaultPermissionTimeout
	}
	return &Codex{
		bin:               found.Path,
		permissionTimeout: timeout,
		extraEnv:          opts.ExtraEnv,
		clients:           map[string]*codexClient{},
		log:               loggerOrDefault(opts.Logger),
	}, nil
}

// Start creates a new Codex thread and returns its threadId.
func (c *Codex) Start(ctx context.Context, req StartRequest) (Handle, error) {
	started := time.Now()
	if req.WorkspaceDir == "" {
		return Handle{}, errors.New("codex workspace dir is required")
	}
	c.providerLog(req.SessionID, req.AgentName, req.Profile).Info("provider thread start requested", "event", "provider", "stage", "thread_start", "permission", string(req.PermissionMode), "mcp_servers", len(req.MCPServers), "extra_workspaces", len(req.ExtraWorkspaceDirs))
	profileName, profileHash, profileConfig, nativeUsed, err := c.ensureProfile(req.ProfileDir, req.AgentName, req.MCPServers, req.MCPAllServers, req.NativeAgents)
	if err != nil && errors.Is(err, errCodexNativeAgents) {
		c.providerLog(req.SessionID, req.AgentName, req.Profile).Warn("native agent projection failed; retrying without native agents", "stage", "native_agents", "error", podiomlog.Redact(err.Error()))
		profileName, profileHash, profileConfig, nativeUsed, err = c.ensureProfile(req.ProfileDir, req.AgentName, req.MCPServers, req.MCPAllServers, nil)
	}
	if err != nil {
		return Handle{}, err
	}
	client := c.client(req.ProfileDir, profileName, profileHash, profileConfig)
	result, err := client.call(ctx, "thread/start", codexThreadStartParams(req))
	if err != nil && nativeUsed {
		c.providerLog(req.SessionID, req.AgentName, req.Profile).Warn("native agent projection rejected; retrying without native agents", "stage", "native_agents", "method", "thread/start", "error", podiomlog.Redact(err.Error()))
		client.reset()
		profileName, profileHash, profileConfig, _, err = c.ensureProfile(req.ProfileDir, req.AgentName, req.MCPServers, req.MCPAllServers, nil)
		if err == nil {
			client = c.client(req.ProfileDir, profileName, profileHash, profileConfig)
			result, err = client.call(ctx, "thread/start", codexThreadStartParams(req))
		}
	}
	if err != nil {
		c.providerLog(req.SessionID, req.AgentName, req.Profile).Warn("provider rpc failed", "stage", "thread_start", "method", "thread/start", "error", podiomlog.Redact(err.Error()))
		return Handle{}, err
	}
	if err := codexDoubleLoadGuard(result, req.WorkspaceDir); err != nil {
		c.providerLog(req.SessionID, req.AgentName, req.Profile).Warn("provider thread validation failed", "stage", "thread_start", "method", "thread/start", "error", err)
		return Handle{}, err
	}
	threadID, err := codexThreadID(result)
	if err != nil {
		c.providerLog(req.SessionID, req.AgentName, req.Profile).Warn("provider response parse failed", "stage", "thread_start", "method", "thread/start", "error", podiomlog.Redact(err.Error()))
		return Handle{}, err
	}
	client.markLoaded(threadID, instructionHash(req.Instructions))
	c.providerLog(req.SessionID, req.AgentName, req.Profile).Info("provider thread started", "event", "provider", "stage", "thread_start", "thread", threadID, podiomlog.DurationMS("duration_ms", time.Since(started)))
	return Handle{Provider: config.ProviderCodex, ID: threadID}, nil
}

// Resume rejoins a persisted Codex thread when enough provider context is
// available. The current core path resumes lazily in SendTurn, where profile and
// workspace settings are present.
func (c *Codex) Resume(ctx context.Context, req ResumeRequest) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	if req.Handle.ID == "" {
		return Handle{}, errors.New("codex threadId is required")
	}
	return req.Handle, nil
}

// SendTurn starts a Codex turn and streams agent message deltas until the turn
// completes. Existing handles are lazily resumed if the app-server was restarted.
func (c *Codex) SendTurn(ctx context.Context, req TurnRequest) (<-chan Event, error) {
	started := time.Now()
	if req.Settings.WorkspaceDir == "" {
		return nil, errors.New("codex workspace dir is required")
	}
	c.turnLog(req).Info("provider turn start requested", "event", "provider", "stage", "turn_start", "thread", req.Handle.ID, "permission", string(req.Settings.PermissionMode), "mcp_servers", len(req.Settings.MCPServers), "extra_workspaces", len(req.Settings.ExtraWorkspaceDirs))
	profileName, profileHash, profileConfig, nativeUsed, err := c.ensureProfile(req.Settings.ProfileDir, req.Settings.AgentName, req.Settings.MCPServers, req.Settings.MCPAllServers, req.Settings.NativeAgents)
	if err != nil && errors.Is(err, errCodexNativeAgents) {
		c.turnLog(req).Warn("native agent projection failed; retrying without native agents", "stage", "native_agents", "error", podiomlog.Redact(err.Error()))
		profileName, profileHash, profileConfig, nativeUsed, err = c.ensureProfile(req.Settings.ProfileDir, req.Settings.AgentName, req.Settings.MCPServers, req.Settings.MCPAllServers, nil)
	}
	if err != nil {
		return nil, err
	}
	client := c.client(req.Settings.ProfileDir, profileName, profileHash, profileConfig)
	// Respawn the app-server here if a credential was stored since it started —
	// this is a turn boundary (before thread/turn RPCs and registerTurn).
	client.maybeRespawnForCredentials()
	threadID := req.Handle.ID
	firstEvents := []Event{}
	startedFresh := threadID == ""
	if threadID == "" {
		handle, err := c.Start(ctx, StartRequest{
			SessionID:          req.SessionID,
			AgentName:          req.Settings.AgentName,
			Provider:           config.ProviderCodex,
			Profile:            req.Settings.Profile,
			ProfileDir:         req.Settings.ProfileDir,
			Model:              req.Settings.Model,
			Effort:             req.Settings.Effort,
			PermissionMode:     req.Settings.PermissionMode,
			WorkspaceDir:       req.Settings.WorkspaceDir,
			ExtraWorkspaceDirs: req.Settings.ExtraWorkspaceDirs,
			MCPServers:         req.Settings.MCPServers,
			MCPAllServers:      req.Settings.MCPAllServers,
			NativeAgentName:    req.Settings.NativeAgentName,
			NativeAgents:       req.Settings.NativeAgents,
			Instructions:       req.Settings.Instructions,
		})
		if err != nil {
			c.turnLog(req).Warn("provider thread start failed", "stage", "thread_start", "method", "thread/start", "error", podiomlog.Redact(err.Error()))
			return nil, err
		}
		threadID = handle.ID
		firstEvents = append(firstEvents, Event{Kind: EventHandleUpdated, Handle: &handle})
	} else if err := client.ensureThread(ctx, threadID, req.Settings); err != nil {
		if nativeUsed {
			c.turnLog(req).Warn("native agent projection rejected; retrying resume without native agents", "stage", "native_agents", "method", "thread/resume", "error", podiomlog.Redact(err.Error()))
			client.reset()
			profileName, profileHash, profileConfig, _, err = c.ensureProfile(req.Settings.ProfileDir, req.Settings.AgentName, req.Settings.MCPServers, req.Settings.MCPAllServers, nil)
			if err == nil {
				client = c.client(req.Settings.ProfileDir, profileName, profileHash, profileConfig)
				err = client.ensureThread(ctx, threadID, withoutCodexNativeAgents(req.Settings))
			}
			if err == nil {
				nativeUsed = false
			}
		}
		if err == nil {
			c.turnLog(req).Info("provider thread resumed", "event", "provider", "stage", "thread_resume", "thread", threadID)
		} else {
			c.turnLog(req).Warn("provider thread resume failed", "stage", "thread_resume", "method", "thread/resume", "error", podiomlog.Redact(err.Error()))
			return nil, err
		}
	} else {
		c.turnLog(req).Info("provider thread resumed", "event", "provider", "stage", "thread_resume", "thread", threadID)
	}

	message := messageWithImageFallback(req.Message, req.Images)
	if startedFresh && len(req.History) > 0 {
		message = codexReplayMessage(req.History, message)
	}

	// The collaboration mode rides on every turn, not just plan turns: it is
	// sticky on the thread, so an implementation turn must say "default"
	// explicitly or the thread keeps planning.
	collab := client.collaborationMode(ctx, req.Settings)

	result, err := client.call(ctx, "turn/start", codexTurnStartParams(threadID, message, req.Images, req.Settings, collab))
	if err != nil && threadID != "" {
		c.turnLog(req).Warn("provider turn start failed; retrying after resume", "stage", "turn_start", "method", "turn/start", "error", podiomlog.Redact(err.Error()))
		client.markUnloaded(threadID)
		if resumeErr := client.ensureThread(ctx, threadID, req.Settings); resumeErr == nil {
			result, err = client.call(ctx, "turn/start", codexTurnStartParams(threadID, message, req.Images, req.Settings, collab))
		} else {
			c.turnLog(req).Warn("provider retry resume failed", "stage", "thread_resume", "method", "thread/resume", "error", podiomlog.Redact(resumeErr.Error()))
		}
	}
	if err != nil && nativeUsed {
		c.turnLog(req).Warn("native agent projection rejected; retrying turn without native agents", "stage", "native_agents", "method", "turn/start", "error", podiomlog.Redact(err.Error()))
		client.reset()
		profileName, profileHash, profileConfig, _, err = c.ensureProfile(req.Settings.ProfileDir, req.Settings.AgentName, req.Settings.MCPServers, req.Settings.MCPAllServers, nil)
		if err == nil {
			client = c.client(req.Settings.ProfileDir, profileName, profileHash, profileConfig)
			fallbackSettings := withoutCodexNativeAgents(req.Settings)
			if resumeErr := client.ensureThread(ctx, threadID, fallbackSettings); resumeErr == nil {
				result, err = client.call(ctx, "turn/start", codexTurnStartParams(threadID, message, req.Images, fallbackSettings, client.collaborationMode(ctx, fallbackSettings)))
			} else {
				err = resumeErr
			}
		}
	}
	if err != nil {
		c.turnLog(req).Warn("provider turn start failed", "stage", "turn_start", "method", "turn/start", "error", podiomlog.Redact(err.Error()))
		return nil, err
	}
	turnID, err := codexTurnID(result)
	if err != nil {
		c.turnLog(req).Warn("provider response parse failed", "stage", "turn_start", "method", "turn/start", "error", podiomlog.Redact(err.Error()))
		return nil, err
	}

	key := codexTurnKey{threadID: threadID, turnID: turnID}
	c.turnLog(req).Info("provider turn started", "event", "provider", "stage", "turn_start", "thread", threadID, "turn", turnID, podiomlog.DurationMS("duration_ms", time.Since(started)))
	timeout := req.Settings.PermissionTimeout
	if timeout <= 0 {
		timeout = c.permissionTimeout
	}
	if timeout <= 0 {
		timeout = defaultPermissionTimeout
	}
	turnEvents := client.registerTurn(key, codexActiveTurn{
		ctx:          ctx,
		podiomTurnID: firstNonEmptyString(req.Settings.PermissionTurnID, req.SessionID),
		relay:        req.Relay,
		input:        req.Input,
		timeout:      timeout,
	})

	out := make(chan Event, 64)
	go client.streamTurn(ctx, key, req.Settings, turnEvents, firstEvents, out)
	return out, nil
}

func (c *Codex) turnLog(req TurnRequest) *slog.Logger {
	return c.providerLog(req.SessionID, req.Settings.AgentName, req.Settings.Profile)
}

func (c *Codex) providerLog(sessionID, agentName, profile string) *slog.Logger {
	return c.log.With(
		"provider", string(config.ProviderCodex),
		"profile", profile,
		"session", sessionID,
		"agent", agentName,
	)
}

// Teardown leaves the long-lived app-server running. Podiom currently does not
// carry profile context through this interface, so unsubscribe is deferred until
// a future lifecycle pass can target the correct CODEX_HOME process.
func (c *Codex) Teardown(ctx context.Context, handle Handle) error {
	return ctx.Err()
}

// Capabilities asks Codex app-server for the model catalogue. The app-server
// response includes model-aware reasoning-effort options, so Podiom can keep
// the picker aligned with the installed Codex CLI/account.
func (c *Codex) Capabilities(ctx context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	client := c.client(req.ProfileDir, "", "", "")
	caps, err := client.modelList(ctx, req.Profile)
	if err != nil {
		return capabilities.WithError(capabilities.Fallback(config.ProviderCodex, req.Profile), err), nil
	}
	if len(caps.Models) == 0 {
		return capabilities.WithError(capabilities.Fallback(config.ProviderCodex, req.Profile), errors.New("codex model/list returned no models")), nil
	}
	return caps, nil
}

func (c *Codex) client(profileDir, profileName, profileHash, profileConfig string) *codexClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clients == nil {
		c.clients = map[string]*codexClient{}
	}
	key := profileDir + "|" + profileName + "|" + profileHash
	client := c.clients[key]
	if client == nil {
		client = newCodexClient(c.bin, profileDir, profileName, profileHash, profileConfig, c.extraEnv, c.log)
		c.clients[key] = client
	}
	return client
}

// RefreshCredentials asks every live app-server client to respawn with fresh
// ExtraEnv at a safe turn boundary, so newly stored credentials reach the
// long-lived process. Implements adapter.CredentialRefresher.
func (c *Codex) RefreshCredentials() {
	c.mu.Lock()
	clients := make([]*codexClient, 0, len(c.clients))
	for _, cl := range c.clients {
		clients = append(clients, cl)
	}
	c.mu.Unlock()
	for _, cl := range clients {
		cl.refreshCredentials()
	}
}

var errCodexNativeAgents = errors.New("codex native agents projection failed")

func (c *Codex) ensureProfile(profileDir, agentName string, assigned, all []podiommcp.Server, nativeAgents []NativeAgent) (string, string, string, bool, error) {
	if len(assigned) == 0 && len(all) == 0 && len(nativeAgents) == 0 {
		return "", "", "", false, nil
	}
	content, unavailable := podiommcp.CodexProfile(assigned, all)
	if len(unavailable) > 0 {
		return "", "", "", false, fmt.Errorf("mcp server(s) unavailable on codex: %s", strings.Join(unavailable, ", "))
	}
	nativeContent, err := codexNativeAgentsProfile(nativeAgents)
	if err != nil {
		return "", "", "", false, fmt.Errorf("%w: %v", errCodexNativeAgents, err)
	}
	content = joinTOML(content, nativeContent)
	name, _, err := podiommcp.WriteCodexProfile(profileDir, agentName, content)
	if err != nil {
		return "", "", "", false, fmt.Errorf("write codex profile: %w", err)
	}
	return name, podiommcp.ProfileHash(content), content, nativeContent != "", nil
}

func codexNativeAgentsProfile(agents []NativeAgent) (string, error) {
	if len(agents) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, agent := range agents {
		if strings.TrimSpace(agent.Name) == "" || strings.TrimSpace(agent.Description) == "" || strings.TrimSpace(agent.Instructions) == "" {
			continue
		}
		if strings.TrimSpace(agent.ConfigPath) == "" {
			return "", fmt.Errorf("native agent %q missing config path", agent.Name)
		}
		if err := writeCodexNativeAgentFile(agent); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n[agents.%s]\n", codexTOMLKey(agent.Name))
		fmt.Fprintf(&b, "config_file = %s\n", codexTOMLString(agent.ConfigPath))
		fmt.Fprintf(&b, "description = %s\n", codexTOMLString(agent.Description))
	}
	return strings.TrimLeft(b.String(), "\n"), nil
}

func writeCodexNativeAgentFile(agent NativeAgent) error {
	if err := os.MkdirAll(filepath.Dir(agent.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("create native Codex agent dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\n", codexTOMLString(agent.Name))
	fmt.Fprintf(&b, "description = %s\n", codexTOMLString(agent.Description))
	fmt.Fprintf(&b, "developer_instructions = %s\n", codexTOMLString(agent.Instructions))
	if agent.Model != "" {
		fmt.Fprintf(&b, "model = %s\n", codexTOMLString(agent.Model))
	}
	if agent.Effort != "" {
		fmt.Fprintf(&b, "model_reasoning_effort = %s\n", codexTOMLString(agent.Effort))
	}
	if err := os.WriteFile(agent.ConfigPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write native Codex agent %s: %w", agent.ConfigPath, err)
	}
	return nil
}

func joinTOML(parts ...string) string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n\n")
}

func withoutCodexNativeAgents(settings TurnSettings) TurnSettings {
	settings.NativeAgentName = ""
	settings.NativeAgents = nil
	return settings
}

func codexTOMLString(s string) string {
	return strconv.Quote(s)
}

func codexTOMLKey(s string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(s) {
		return s
	}
	return codexTOMLString(s)
}

type codexClient struct {
	bin           string
	profileDir    string
	profileName   string
	profileHash   string
	profileConfig string
	extraEnv      func() []string
	log           *slog.Logger

	initMu   sync.Mutex
	mu       sync.Mutex
	stderrMu sync.Mutex

	cmd          *osProcess
	stdin        io.WriteCloser
	nextID       int64
	initialized  bool
	needsRespawn bool

	meta     codexMeta
	pending  map[string]chan codexCallResponse
	loaded   map[string]string
	watchers map[codexTurnKey]chan codexStreamEvent
	buffered map[codexTurnKey][]codexStreamEvent
	// Latest account-scoped notification that arrived with no turn to deliver it
	// to, held for the next turn to pick up (see dispatchAccountNotification).
	pendingAccount *codexStreamEvent
	active         map[codexTurnKey]codexActiveTurn
	fileChanges    map[codexTurnKey]map[string]string
	stderrTail     []string
}

type osProcess struct {
	cmdWait    func() error
	kill       func() error
	stderrDone chan struct{}
}

type codexCallResponse struct {
	result json.RawMessage
	err    error
}

type codexRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e codexRPCError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("codex rpc error %d", e.Code)
	}
	return fmt.Sprintf("codex rpc error %d: %s", e.Code, e.Message)
}

type codexTurnKey struct {
	threadID string
	turnID   string
}

type codexActiveTurn struct {
	ctx          context.Context
	podiomTurnID string
	relay        PermissionRelay
	input        UserInputRelay
	timeout      time.Duration
}

type codexStreamEvent struct {
	method string
	params json.RawMessage
	err    error
}

type codexNativeAgentTrack struct {
	nativeAgentTasks map[string]NativeAgentActivity
}

func newCodexClient(bin, profileDir, profileName, profileHash, profileConfig string, extraEnv func() []string, log *slog.Logger) *codexClient {
	return &codexClient{
		bin:           bin,
		profileDir:    profileDir,
		profileName:   profileName,
		profileHash:   profileHash,
		profileConfig: profileConfig,
		extraEnv:      extraEnv,
		log: loggerOrDefault(log).With(
			"provider", string(config.ProviderCodex),
			"profile_dir_set", profileDir != "",
			"mcp_profile", profileName,
			"mcp_profile_hash", profileHash,
		),
		pending:     map[string]chan codexCallResponse{},
		loaded:      map[string]string{},
		watchers:    map[codexTurnKey]chan codexStreamEvent{},
		buffered:    map[codexTurnKey][]codexStreamEvent{},
		active:      map[codexTurnKey]codexActiveTurn{},
		fileChanges: map[codexTurnKey]map[string]string{},
	}
}

func (c *codexClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		started := time.Now()
		if err := c.ensureProcess(ctx); err != nil {
			c.log.Warn("provider app-server unavailable", "stage", "ensure_process", "method", method, "error", podiomlog.Redact(err.Error()))
			return nil, err
		}
		result, err := c.callStarted(ctx, method, params)
		if err == nil {
			c.log.Debug("provider rpc succeeded", "event", "provider", "stage", "rpc", "method", method, podiomlog.DurationMS("duration_ms", time.Since(started)))
			return result, nil
		}
		last = err
		if !errors.Is(err, errCodexTransport) {
			c.log.Warn("provider rpc failed", "stage", "rpc", "method", method, "error", podiomlog.Redact(err.Error()))
			return nil, err
		}
		c.log.Warn("provider transport failed; resetting", "stage", "transport", "method", method, "attempt", attempt+1, "error", podiomlog.Redact(err.Error()))
		c.reset()
	}
	return nil, last
}

func (c *codexClient) ensureProcess(ctx context.Context) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()

	c.mu.Lock()
	if c.stdin == nil {
		if err := c.startLocked(); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	initialized := c.initialized
	c.mu.Unlock()
	if initialized {
		return ctx.Err()
	}

	if _, err := c.callStarted(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "podiom",
			"title":   "Podiom",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi":                true,
			"requestAttestation":             false,
			"mcpServerOpenaiFormElicitation": false,
		},
	}); err != nil {
		c.log.Warn("provider initialize failed", "stage", "initialize", "method", "initialize", "error", podiomlog.Redact(err.Error()))
		c.reset()
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return c.withStderrTail(fmt.Errorf("%w: app-server exited during initialize", errCodexTransport))
	}
	if err := c.writeJSONLocked(map[string]any{"method": "initialized"}); err != nil {
		transportErr := c.withStderrTail(fmt.Errorf("%w: write initialized: %v", errCodexTransport, err))
		c.log.Warn("provider initialized notification failed", "stage", "initialize", "method", "initialized", "error", podiomlog.Redact(transportErr.Error()))
		return transportErr
	}
	c.initialized = true
	c.log.Info("provider initialized", "event", "provider", "stage", "initialize", "mcp_profile", c.profileName, "mcp_profile_hash", c.profileHash)
	return nil
}

func (c *codexClient) startLocked() error {
	args := []string{"app-server"}
	for _, override := range podiommcp.CodexConfigOverrides(c.profileConfig) {
		args = append(args, "-c", override)
	}
	args = append(args, "--listen", "stdio://")
	cmd := podiomexec.Command(context.Background(), c.bin, args...)
	cmd.Env = applyExtraEnv(codexEnv(c.profileDir), c.extraEnv)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("codex stderr: %w", err)
	}
	c.clearStderrTail()
	if err := cmd.Start(); err != nil {
		c.log.Warn("provider app-server start failed", "stage", "start", "error", err)
		return fmt.Errorf("start codex app-server: %w", err)
	}
	c.log.Info("provider app-server started", "event", "provider", "stage", "start", "command", c.bin, "mcp_profile", c.profileName, "profile_dir_set", c.profileDir != "")

	stderrDone := make(chan struct{})
	proc := &osProcess{
		cmdWait:    cmd.Wait,
		kill:       func() error { return podiomexec.Kill(cmd) },
		stderrDone: stderrDone,
	}
	c.cmd = proc
	c.stdin = stdin
	c.initialized = false
	c.loaded = map[string]string{}
	go c.readLoop(proc, stdout)
	go c.readStderr(stderr, stderrDone)
	return nil
}

func (c *codexClient) callStarted(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.stdin == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: app-server is not running", errCodexTransport)
	}
	c.nextID++
	id := c.nextID
	idKey := strconv.FormatInt(id, 10)
	ch := make(chan codexCallResponse, 1)
	c.pending[idKey] = ch
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeJSONLocked(req); err != nil {
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: write %s: %v", errCodexTransport, method, err)
	}
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		return resp.result, resp.err
	}
}

func (c *codexClient) writeJSONLocked(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = c.stdin.Write(raw)
	return err
}

func (c *codexClient) readLoop(proc *osProcess, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg codexRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			c.log.Warn("provider stdout parse failed", "stage", "read_stdout", "error", err, "line_tail", podiomlog.RedactTail(string(line), 4096))
			continue
		}
		c.dispatch(msg)
	}
	err := scanner.Err()
	waitErr := proc.cmdWait()
	if err == nil {
		err = waitErr
	}
	if err == nil {
		err = io.EOF
	}
	if proc.stderrDone != nil {
		select {
		case <-proc.stderrDone:
		case <-time.After(100 * time.Millisecond):
		}
	}
	transportErr := c.withStderrTail(fmt.Errorf("%w: %v", errCodexTransport, err))
	c.log.Warn("provider app-server stream ended", "event", "provider", "stage", "read_stdout", "error", podiomlog.Redact(transportErr.Error()))
	c.mu.Lock()
	if c.cmd == proc {
		c.failLocked(transportErr)
	}
	c.mu.Unlock()
}

func (c *codexClient) readStderr(stderr io.Reader, done chan<- struct{}) {
	defer close(done)
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			redacted := podiomlog.RedactTail(line, 4096)
			c.recordStderr(redacted)
			c.log.Debug("provider stderr", "stage", "read_stderr", "stderr_tail", redacted)
		}
	}
	if err := scanner.Err(); err != nil {
		c.log.Warn("provider stderr read failed", "stage", "read_stderr", "error", err)
	}
}

const codexStderrTailLines = 20

func (c *codexClient) clearStderrTail() {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	c.stderrTail = nil
}

func (c *codexClient) recordStderr(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	c.stderrTail = append(c.stderrTail, line)
	if len(c.stderrTail) > codexStderrTailLines {
		c.stderrTail = c.stderrTail[len(c.stderrTail)-codexStderrTailLines:]
	}
}

func (c *codexClient) stderrTailString() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.Join(append([]string(nil), c.stderrTail...), "\n")
}

func (c *codexClient) withStderrTail(err error) error {
	tail := strings.TrimSpace(c.stderrTailString())
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w; stderr: %s", err, tail)
}

func (c *codexClient) dispatch(msg codexRPCMessage) {
	if len(msg.ID) > 0 && msg.Method == "" {
		c.dispatchResponse(msg)
		return
	}
	if len(msg.ID) > 0 && msg.Method != "" {
		go c.handleServerRequest(msg)
		return
	}
	if msg.Method == "" {
		return
	}
	c.dispatchNotification(msg)
}

func (c *codexClient) dispatchResponse(msg codexRPCMessage) {
	key := codexIDKey(msg.ID)
	c.mu.Lock()
	ch := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if msg.Error != nil {
		c.log.Warn("provider rpc error response", "stage", "rpc_response", "code", msg.Error.Code, "error", podiomlog.Redact(msg.Error.Message))
		ch <- codexCallResponse{err: *msg.Error}
		return
	}
	ch <- codexCallResponse{result: append(json.RawMessage(nil), msg.Result...)}
}

func (c *codexClient) dispatchNotification(msg codexRPCMessage) {
	if msg.Method == "item/fileChange/patchUpdated" {
		c.recordFileChangePatch(msg.Params)
	}
	if msg.Method == "thread/started" {
		if c.dispatchThreadStartedNotification(msg) {
			return
		}
	}
	if msg.Method == "account/rateLimits/updated" {
		c.dispatchAccountNotification(msg)
		return
	}
	key, ok := codexNotificationKey(msg.Method, msg.Params)
	if !ok {
		return
	}
	event := codexStreamEvent{
		method: msg.Method,
		params: append(json.RawMessage(nil), msg.Params...),
	}
	c.mu.Lock()
	ch := c.watchers[key]
	if ch == nil {
		buffered := append(c.buffered[key], event)
		if len(buffered) > 256 {
			buffered = buffered[len(buffered)-256:]
		}
		c.buffered[key] = buffered
		c.mu.Unlock()
		c.log.Debug("provider notification buffered", "event", "provider", "stage", "stream_buffer", "method", msg.Method, "thread", key.threadID, "turn", key.turnID, "buffered", len(buffered))
		return
	}
	c.mu.Unlock()
	ch <- event
}

// dispatchAccountNotification delivers an account-scoped notification to every
// live turn watcher. Rate limits carry no thread or turn id — they describe the
// signed-in account — so they cannot be keyed like the rest, and one client is
// one profile, so every watcher on it shares the account being reported. One
// that lands before the turn registers is held for it: the app-server reports
// limits right after turn/start, which is exactly that window.
func (c *codexClient) dispatchAccountNotification(msg codexRPCMessage) {
	event := codexStreamEvent{
		method: msg.Method,
		params: append(json.RawMessage(nil), msg.Params...),
	}
	c.mu.Lock()
	channels := make([]chan codexStreamEvent, 0, len(c.watchers))
	for _, ch := range c.watchers {
		if ch != nil {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		// Only the newest snapshot matters; an older one it replaces is stale.
		c.pendingAccount = &event
	}
	c.mu.Unlock()
	for _, ch := range channels {
		ch <- event
	}
}

func (c *codexClient) dispatchThreadStartedNotification(msg codexRPCMessage) bool {
	parentThreadID := codexThreadStartedParent(msg.Params)
	if parentThreadID == "" {
		return false
	}
	event := codexStreamEvent{
		method: msg.Method,
		params: append(json.RawMessage(nil), msg.Params...),
	}
	c.mu.Lock()
	for key, ch := range c.watchers {
		if key.threadID != parentThreadID || ch == nil {
			continue
		}
		c.mu.Unlock()
		ch <- event
		return true
	}
	c.mu.Unlock()
	return true
}

// refreshCredentials marks the app-server for respawn so it re-reads ExtraEnv
// (extraEnv is a live func ref, so the next spawn picks up freshly stored
// credentials). If no turn is registered it respawns immediately; otherwise the
// respawn is deferred to the next turn boundary (maybeRespawnForCredentials).
func (c *codexClient) refreshCredentials() {
	c.mu.Lock()
	if c.stdin == nil {
		// No process running; the next spawn reads current ExtraEnv anyway.
		c.needsRespawn = false
		c.mu.Unlock()
		return
	}
	c.needsRespawn = true
	idle := len(c.active) == 0
	c.mu.Unlock()
	if idle {
		// reset() acquires c.mu itself; ensureProcess respawns on the next call.
		c.reset()
		c.mu.Lock()
		c.needsRespawn = false
		c.mu.Unlock()
	}
}

// maybeRespawnForCredentials respawns the app-server before a turn starts when a
// credential refresh is pending and no turn is currently registered. It is
// called at the top of Codex.SendTurn, before this turn is registered in
// c.active, so it never aborts an in-flight turn. A turn still in its startup
// RPC phase that races this reset simply retries on errCodexTransport.
func (c *codexClient) maybeRespawnForCredentials() {
	c.mu.Lock()
	if !c.needsRespawn || c.stdin == nil || len(c.active) > 0 {
		c.mu.Unlock()
		return
	}
	c.needsRespawn = false
	c.mu.Unlock()
	c.reset()
}

func (c *codexClient) reset() {
	c.mu.Lock()
	proc := c.cmd
	c.log.Info("provider app-server reset", "event", "provider", "stage", "transport")
	c.failLocked(fmt.Errorf("%w: connection reset", errCodexTransport))
	c.mu.Unlock()
	if proc != nil && proc.kill != nil {
		_ = proc.kill()
	}
}

func (c *codexClient) failLocked(err error) {
	c.cmd = nil
	c.stdin = nil
	c.initialized = false
	c.loaded = map[string]string{}
	c.fileChanges = map[codexTurnKey]map[string]string{}
	for key, ch := range c.pending {
		delete(c.pending, key)
		ch <- codexCallResponse{err: err}
	}
	for _, ch := range c.watchers {
		select {
		case ch <- codexStreamEvent{err: err}:
		default:
		}
	}
}

func (c *codexClient) ensureThread(ctx context.Context, threadID string, settings TurnSettings) error {
	if threadID == "" {
		return errors.New("codex threadId is required")
	}
	hash := instructionHash(settings.Instructions)
	if c.isLoaded(threadID, hash) {
		return nil
	}
	result, err := c.call(ctx, "thread/resume", codexThreadResumeParams(threadID, settings))
	if err != nil {
		return err
	}
	if _, err := codexThreadID(result); err != nil {
		return err
	}
	c.markLoaded(threadID, hash)
	return nil
}

func (c *codexClient) modelList(ctx context.Context, profile string) (capabilities.ProviderCapabilities, error) {
	var all []capabilities.ModelOption
	cursor := ""
	for {
		params := map[string]any{"includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "model/list", params)
		if err != nil {
			return capabilities.ProviderCapabilities{}, err
		}
		page, err := parseCodexModelList(raw)
		if err != nil {
			return capabilities.ProviderCapabilities{}, err
		}
		all = append(all, page.models...)
		if page.nextCursor == "" {
			break
		}
		cursor = page.nextCursor
	}
	caps := capabilities.ProviderCapabilities{
		Provider:  config.ProviderCodex,
		Profile:   profile,
		Source:    "codex-app-server:model/list",
		FetchedAt: time.Now().UTC(),
		Stale:     false,
		Models:    all,
		Efforts:   unionModelEfforts(all),
	}
	if len(caps.Efforts) == 0 {
		caps.Efforts = capabilities.CloneEfforts(capabilities.DefaultEfforts)
	}
	return caps, nil
}

type codexModelListPage struct {
	models     []capabilities.ModelOption
	nextCursor string
}

func parseCodexModelList(raw json.RawMessage) (codexModelListPage, error) {
	var resp struct {
		Data []struct {
			ID                     string `json:"id"`
			Model                  string `json:"model"`
			DisplayName            string `json:"displayName"`
			Description            string `json:"description"`
			Hidden                 bool   `json:"hidden"`
			IsDefault              bool   `json:"isDefault"`
			DefaultReasoningEffort string `json:"defaultReasoningEffort"`
			SupportedEfforts       []struct {
				ReasoningEffort string `json:"reasoningEffort"`
				Description     string `json:"description"`
			} `json:"supportedReasoningEfforts"`
			InputModalities []string `json:"inputModalities"`
		} `json:"data"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return codexModelListPage{}, err
	}
	out := make([]capabilities.ModelOption, 0, len(resp.Data))
	for _, item := range resp.Data {
		if strings.TrimSpace(item.Model) == "" {
			continue
		}
		efforts := make([]capabilities.EffortOption, 0, len(item.SupportedEfforts))
		for _, effort := range item.SupportedEfforts {
			if strings.TrimSpace(effort.ReasoningEffort) == "" {
				continue
			}
			efforts = append(efforts, capabilities.EffortOption{
				Effort:      effort.ReasoningEffort,
				Description: effort.Description,
			})
		}
		out = append(out, capabilities.ModelOption{
			ID:                     item.ID,
			Model:                  item.Model,
			DisplayName:            item.DisplayName,
			Description:            item.Description,
			Hidden:                 item.Hidden,
			IsDefault:              item.IsDefault,
			DefaultReasoningEffort: item.DefaultReasoningEffort,
			SupportedEfforts:       efforts,
			InputModalities:        append([]string(nil), item.InputModalities...),
		})
	}
	return codexModelListPage{models: out, nextCursor: resp.NextCursor}, nil
}

func unionModelEfforts(models []capabilities.ModelOption) []capabilities.EffortOption {
	seen := map[string]bool{}
	var out []capabilities.EffortOption
	for _, model := range models {
		for _, effort := range model.SupportedEfforts {
			if effort.Effort == "" || seen[effort.Effort] {
				continue
			}
			seen[effort.Effort] = true
			out = append(out, effort)
		}
	}
	return out
}

func (c *codexClient) isLoaded(threadID, instructionsHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	hash, ok := c.loaded[threadID]
	return ok && hash == instructionsHash
}

func (c *codexClient) markLoaded(threadID, instructionsHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded[threadID] = instructionsHash
}

func (c *codexClient) markUnloaded(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.loaded, threadID)
}

func (c *codexClient) registerTurn(key codexTurnKey, active codexActiveTurn) <-chan codexStreamEvent {
	c.mu.Lock()
	buffered := c.buffered[key]
	delete(c.buffered, key)
	if account := c.pendingAccount; account != nil {
		c.pendingAccount = nil
		// Ahead of the buffered turn notifications: those end with
		// turn/completed, after which the stream loop stops reading.
		buffered = append([]codexStreamEvent{*account}, buffered...)
	}
	// Size the channel to hold every replayed notification so the sends below
	// cannot block while the lock is held (dispatchNotification caps a key's
	// backlog at 256).
	capacity := 128
	if len(buffered) > capacity {
		capacity = len(buffered)
	}
	ch := make(chan codexStreamEvent, capacity)
	// Replay notifications that landed before this turn was registered, and
	// publish the watcher only afterwards — both under the same lock as
	// dispatchNotification. Replaying from a goroutine instead races live
	// dispatch: it can deliver buffered events after later live ones, or lose
	// them entirely when the turn completes before the goroutine is scheduled.
	for _, event := range buffered {
		ch <- event
	}
	c.watchers[key] = ch
	c.active[key] = active
	c.mu.Unlock()
	return ch
}

func (c *codexClient) unregisterTurn(key codexTurnKey) {
	c.mu.Lock()
	delete(c.watchers, key)
	delete(c.active, key)
	delete(c.fileChanges, key)
	c.mu.Unlock()
}

func (c *codexClient) streamTurn(ctx context.Context, key codexTurnKey, settings TurnSettings, events <-chan codexStreamEvent, first []Event, out chan<- Event) {
	started := time.Now()
	track := codexNativeAgentTrack{nativeAgentTasks: map[string]NativeAgentActivity{}}
	// Reasoning arrives twice over this protocol: as deltas while the turn runs,
	// then again whole at turn/completed. These track the delta stream so the
	// duplicate can be dropped and item boundaries kept.
	var streamedReasoning bool
	var lastReasoningItem string
	// Thread-cumulative token total from the last token-usage notification, used
	// to drop repeats: each notification's "last" node is one request's usage, so
	// billing them again when nothing advanced would double-count the turn.
	var lastTokenTotal int64
	defer close(out)
	defer c.unregisterTurn(key)
	for _, event := range first {
		if !sendAdapterEvent(ctx, out, event) {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			if event.err != nil {
				c.log.Warn("provider turn stream failed", "stage", "stream_turn", "thread", key.threadID, "turn", key.turnID, "error", podiomlog.Redact(event.err.Error()))
				sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: event.err.Error()})
				return
			}
			switch event.method {
			case "item/agentMessage/delta":
				if text, itemID, reasoning := codexDelta(event.params); text != "" {
					kind := EventAssistantDelta
					if reasoning {
						kind = EventReasoningDelta
						// Consecutive reasoning items carry no separator of their own —
						// there is no per-item completion notification — and the joined
						// turn/completed message that used to supply the newlines is
						// suppressed below once deltas have streamed. Insert it here so
						// successive working notes do not run together.
						if streamedReasoning && itemID != lastReasoningItem {
							text = "\n" + text
						}
						streamedReasoning = true
						lastReasoningItem = itemID
					}
					if !sendAdapterEvent(ctx, out, Event{Kind: kind, Content: text}) {
						return
					}
				}
			case "item/started", "item/completed", "thread/started":
				for _, activity := range codexNativeAgentActivities(event.method, event.params) {
					enriched, ok := enrichCodexNativeAgentActivity(settings, &track, activity)
					if !ok {
						continue
					}
					if !sendAdapterEvent(ctx, out, Event{Kind: EventNativeAgentActivity, NativeAgent: enriched}) {
						return
					}
				}
				for _, tu := range c.codexToolUses(event.method, event.params, key) {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventToolUse, ToolUse: tu}) {
						return
					}
				}
				if proposal := codexPlanProposal(event.method, event.params); proposal != nil {
					c.log.Info("provider proposed a plan", "event", "provider", "stage", "plan", "thread", key.threadID, "turn", key.turnID)
					if !sendAdapterEvent(ctx, out, Event{Kind: EventPlanProposed, PlanProposal: proposal}) {
						return
					}
				}
			case "turn/completed":
				for _, activity := range codexNativeAgentActivities(event.method, event.params) {
					enriched, ok := enrichCodexNativeAgentActivity(settings, &track, activity)
					if !ok {
						continue
					}
					if !sendAdapterEvent(ctx, out, Event{Kind: EventNativeAgentActivity, NativeAgent: enriched}) {
						return
					}
				}
				// codexReasoningMessage joins the turn's *entire* reasoning, so
				// emitting it after the deltas already streamed would duplicate every
				// working note. It stays as the backstop for turns that streamed none
				// (reasoning summaries off).
				if !streamedReasoning {
					if text := codexReasoningMessage(event.params); text != "" {
						if !sendAdapterEvent(ctx, out, Event{Kind: EventReasoningMessage, Content: text}) {
							return
						}
					}
				}
				if text := codexFinalMessage(event.params); text != "" {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: text}) {
						return
					}
				}
				c.log.Info("provider turn stream completed", "event", "provider", "stage", "stream_turn", "thread", key.threadID, "turn", key.turnID, podiomlog.DurationMS("duration_ms", time.Since(started)))
				sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
				return
			case "error":
				if codexRateLimited(event.params) {
					c.log.Warn("provider rate limited", "stage", "stream_turn", "thread", key.threadID, "turn", key.turnID, "rate_limited", true, "error", podiomlog.RedactTail(codexErrorMessage(event.params), 4096))
					sendAdapterEvent(ctx, out, Event{Kind: EventRateLimited, Content: codexErrorMessage(event.params)})
					sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
					return
				}
				if codexAuthFailure(event.params) {
					c.log.Warn("provider not signed in", "stage", "stream_turn", "thread", key.threadID, "turn", key.turnID, "error", podiomlog.RedactTail(codexErrorMessage(event.params), 4096))
					sendAdapterEvent(ctx, out, Event{Kind: EventAuthRequired, Content: codexErrorMessage(event.params)})
					sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
					return
				}
				c.log.Warn("provider error notification", "stage", "stream_turn", "thread", key.threadID, "turn", key.turnID, "error", podiomlog.RedactTail(codexErrorMessage(event.params), 4096))
				sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: codexErrorMessage(event.params)})
				sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
				return
			case "account/rateLimits/updated":
				if status, ok := codexRateStatus(event.params); ok {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventRateStatus, RateStatus: &status}) {
						return
					}
				}
			case "thread/tokenUsage/updated":
				// The only place the app-server reports tokens: turn/completed
				// carries the turn's items and status, nothing about usage.
				if status, ok := codexContextStatus(event.params); ok {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventContextStatus, ContextStatus: &status}) {
						return
					}
				}
				total := codexCumulativeTokenTotal(event.params)
				if total > lastTokenTotal {
					lastTokenTotal = total
					if usage, ok := codexTurnUsage(event.params); ok {
						if !sendAdapterEvent(ctx, out, Event{Kind: EventTurnUsage, TurnUsage: &usage}) {
							return
						}
					}
				}
			}
		}
	}
}

func (c *codexClient) handleServerRequest(msg codexRPCMessage) {
	if msg.Method == "currentTime/read" {
		c.respond(msg.ID, map[string]any{"currentTimeAt": time.Now().Unix()})
		return
	}
	if msg.Method == "item/tool/requestUserInput" {
		active, ok := c.waitActiveForRequest(msg.Method, msg.Params, 2*time.Second)
		decision := UserInputDecision{Answers: map[string][]string{}}
		if ok && active.input != nil {
			req := codexUserInputRequest(msg.ID, msg.Params, active)
			timeout := active.timeout
			if req.AutoResolutionMS > 0 {
				timeout = time.Duration(req.AutoResolutionMS) * time.Millisecond
			}
			got, err := active.input.RequestUserInput(active.ctx, req, timeout)
			if err == nil && got.Answers != nil {
				decision = got
			}
		}
		c.respond(msg.ID, codexUserInputResponse(decision))
		return
	}
	if !codexIsApprovalRequest(msg.Method) {
		c.respondError(msg.ID, -32601, fmt.Sprintf("unsupported codex server request %s", msg.Method))
		return
	}
	active, ok := c.waitActiveForRequest(msg.Method, msg.Params, 2*time.Second)
	decision := PermissionDecision{Behavior: "deny"}
	if ok && active.relay != nil {
		req := c.codexPermissionRequest(msg.Method, msg.ID, msg.Params, active)
		got, err := active.relay.RequestPermission(active.ctx, req, active.timeout)
		if err == nil && got.Behavior != "" {
			decision = got
		}
	}
	c.respond(msg.ID, codexApprovalResponse(msg.Method, msg.Params, decision))
}

func (c *codexClient) waitActiveForRequest(method string, params json.RawMessage, timeout time.Duration) (codexActiveTurn, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if active, ok := c.activeForRequest(method, params); ok {
			return active, true
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return codexActiveTurn{}, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *codexClient) activeForRequest(method string, params json.RawMessage) (codexActiveTurn, bool) {
	threadID, turnID := codexRequestThreadTurn(method, params)
	c.mu.Lock()
	defer c.mu.Unlock()
	if turnID != "" {
		active, ok := c.active[codexTurnKey{threadID: threadID, turnID: turnID}]
		return active, ok
	}
	for key, active := range c.active {
		if key.threadID == threadID {
			return active, true
		}
	}
	return codexActiveTurn{}, false
}

func (c *codexClient) recordFileChangePatch(params json.RawMessage) {
	key, itemID, summary := codexFileChangePatchSummary(params)
	if key.threadID == "" || key.turnID == "" || itemID == "" || summary == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fileChanges == nil {
		c.fileChanges = map[codexTurnKey]map[string]string{}
	}
	byItem := c.fileChanges[key]
	if byItem == nil {
		byItem = map[string]string{}
		c.fileChanges[key] = byItem
	}
	byItem[itemID] = summary
}

func (c *codexClient) fileChangeSummary(threadID, turnID, itemID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fileChanges[codexTurnKey{threadID: threadID, turnID: turnID}][itemID]
}

func (c *codexClient) respond(id json.RawMessage, result any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return
	}
	_ = c.writeJSONLocked(map[string]any{
		"id":     json.RawMessage(id),
		"result": result,
	})
}

func (c *codexClient) respondError(id json.RawMessage, code int, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return
	}
	_ = c.writeJSONLocked(map[string]any{
		"id": json.RawMessage(id),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func codexThreadStartParams(req StartRequest) map[string]any {
	params := map[string]any{
		"cwd":                   req.WorkspaceDir,
		"runtimeWorkspaceRoots": codexRuntimeRoots(req.PermissionMode, req.WorkspaceDir, req.ExtraWorkspaceDirs),
		"approvalPolicy":        codexApprovalPolicy(req.PermissionMode),
		"sandbox":               codexSandboxMode(req.PermissionMode),
		"threadSource":          "podiom",
		"serviceName":           "podiom",
	}
	if req.Model != "" {
		params["model"] = req.Model
	}
	if len(req.Instructions) > 0 {
		params["developerInstructions"] = string(req.Instructions)
	}
	return params
}

func codexThreadResumeParams(threadID string, settings TurnSettings) map[string]any {
	params := map[string]any{
		"threadId":       threadID,
		"excludeTurns":   true,
		"approvalPolicy": codexApprovalPolicy(settings.PermissionMode),
		"sandbox":        codexSandboxMode(settings.PermissionMode),
	}
	if settings.WorkspaceDir != "" {
		params["cwd"] = settings.WorkspaceDir
		params["runtimeWorkspaceRoots"] = codexRuntimeRoots(settings.PermissionMode, settings.WorkspaceDir, settings.ExtraWorkspaceDirs)
	}
	if settings.Model != "" {
		params["model"] = settings.Model
	}
	if len(settings.Instructions) > 0 {
		params["developerInstructions"] = string(settings.Instructions)
	}
	return params
}

func instructionHash(instructions []byte) string {
	if len(instructions) == 0 {
		return ""
	}
	sum := sha256.Sum256(instructions)
	return fmt.Sprintf("%x", sum[:])
}

func codexTurnStartParams(threadID, message string, images []ImageInput, settings TurnSettings, collaborationMode map[string]any) map[string]any {
	input := []map[string]any{{
		"type":          "text",
		"text":          message,
		"text_elements": []any{},
	}}
	for _, image := range images {
		input = append(input, map[string]any{"type": "localImage", "path": image.Path})
	}
	// The sandbox policy's writableRoots must agree with the runtime roots:
	// Codex derives writable scope from the latter, so a mismatch would be
	// misleading to anyone reading the wire log.
	roots := codexRuntimeRoots(settings.PermissionMode, settings.WorkspaceDir, settings.ExtraWorkspaceDirs)
	sandbox := codexSandboxPolicy(settings.PermissionMode, roots)
	if settings.PlanMode {
		// Plan mode is behavioral orchestration, not a sandbox boundary — the
		// model declined to write under workspace-write, but Podiom pins
		// read-only so non-mutation is enforced rather than instructed.
		sandbox = map[string]any{"type": "readOnly", "networkAccess": false}
	}
	params := map[string]any{
		"threadId":              threadID,
		"input":                 input,
		"cwd":                   settings.WorkspaceDir,
		"runtimeWorkspaceRoots": roots,
		"approvalPolicy":        codexApprovalPolicy(settings.PermissionMode),
		"sandboxPolicy":         sandbox,
	}
	if collaborationMode != nil {
		params["collaborationMode"] = collaborationMode
	}
	if settings.Model != "" {
		params["model"] = settings.Model
	}
	if settings.Effort != "" {
		params["effort"] = settings.Effort
	}
	return params
}

func workspaceRoots(primary string, extra []string) []string {
	seen := map[string]bool{}
	var roots []string
	for _, dir := range append([]string{primary}, extra...) {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		roots = append(roots, dir)
	}
	return roots
}

func codexApprovalPolicy(mode config.PermissionMode) string {
	if mode == config.PermissionYolo {
		return "never"
	}
	// approve and auto both keep asking; they differ in what the sandbox makes
	// possible without an approval round-trip, not in the approval policy.
	return "on-request"
}

func codexSandboxMode(mode config.PermissionMode) string {
	switch mode {
	case config.PermissionYolo:
		return "danger-full-access"
	case config.PermissionAuto:
		return "workspace-write"
	default:
		return "read-only"
	}
}

func codexSandboxPolicy(mode config.PermissionMode, writableRoots []string) map[string]any {
	switch mode {
	case config.PermissionYolo:
		return map[string]any{"type": "dangerFullAccess"}
	case config.PermissionAuto:
		return map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": writableRoots,
			"networkAccess": false,
		}
	default:
		return map[string]any{"type": "readOnly", "networkAccess": false}
	}
}

// codexRuntimeRoots picks the runtime workspace roots for a mode.
//
// On Codex the *writable* scope is governed by runtimeWorkspaceRoots, not by
// sandboxPolicy.writableRoots — verified against app-server 0.142.4: holding
// writableRoots fixed, a directory listed in runtimeWorkspaceRoots was written
// with no approval request, and the same write was refused once that directory
// was dropped from the list.
//
// So a writable mode must not receive the broad set. ExtraWorkspaceDirs
// includes the projects parent directory, and handing that to a workspace-write
// sandbox would let one session write into every project on disk. auto
// therefore gets the working directory alone. Reads are unaffected — also
// verified: a file outside the narrowed roots was still readable, so agents
// keep access to the shared ledger.
//
// approve (read-only) and yolo (full access) keep the broad set: writes are
// impossible in one and unrestricted by design in the other.
func codexRuntimeRoots(mode config.PermissionMode, primary string, extra []string) []string {
	if mode == config.PermissionAuto {
		return workspaceRoots(primary, nil)
	}
	return workspaceRoots(primary, extra)
}

func codexThreadID(raw json.RawMessage) (string, error) {
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse codex thread response: %w", err)
	}
	if resp.Thread.ID == "" {
		return "", errors.New("codex thread response missing thread.id")
	}
	return resp.Thread.ID, nil
}

func codexTurnID(raw json.RawMessage) (string, error) {
	var resp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse codex turn response: %w", err)
	}
	if resp.Turn.ID == "" {
		return "", errors.New("codex turn response missing turn.id")
	}
	return resp.Turn.ID, nil
}

func codexDoubleLoadGuard(raw json.RawMessage, workspace string) error {
	var resp struct {
		InstructionSources []string `json:"instructionSources"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.InstructionSources) == 0 {
		return nil
	}
	workspaceAgents := filepath.Clean(filepath.Join(workspace, "AGENTS.md"))
	parentAgents := filepath.Clean(filepath.Join(filepath.Dir(workspace), "AGENTS.md"))
	var hasWorkspace, hasParent bool
	for _, src := range resp.InstructionSources {
		clean := filepath.Clean(src)
		hasWorkspace = hasWorkspace || clean == workspaceAgents
		hasParent = hasParent || clean == parentAgents
	}
	if hasWorkspace && hasParent {
		return fmt.Errorf("codex loaded both generated workspace AGENTS.md and parent agent AGENTS.md; refusing duplicated instructions")
	}
	return nil
}

func codexNotificationKey(method string, params json.RawMessage) (codexTurnKey, bool) {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return codexTurnKey{}, false
	}
	turnID := p.TurnID
	if turnID == "" {
		turnID = p.Turn.ID
	}
	if p.ThreadID == "" || turnID == "" {
		return codexTurnKey{}, false
	}
	switch method {
	case "item/agentMessage/delta", "item/started", "item/completed", "turn/completed", "error", "turn/started", "thread/tokenUsage/updated":
		return codexTurnKey{threadID: p.ThreadID, turnID: turnID}, true
	default:
		return codexTurnKey{}, false
	}
}

func codexThreadStartedParent(params json.RawMessage) string {
	var p struct {
		Thread struct {
			ParentThreadID string `json:"parentThreadId"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(params, &p)
	return p.Thread.ParentThreadID
}

func codexNativeAgentActivities(method string, params json.RawMessage) []*NativeAgentActivity {
	switch method {
	case "item/started", "item/completed":
		if activity, ok := codexNativeAgentItemActivity(method, params); ok {
			return []*NativeAgentActivity{activity}
		}
	case "thread/started":
		if activity, ok := codexNativeAgentThreadActivity(params); ok {
			return []*NativeAgentActivity{activity}
		}
	case "turn/completed":
		return codexNativeAgentTurnActivities(params)
	}
	return nil
}

// codexToolItem is the tool-call projection of a Codex turn item. Field names
// are best-effort across app-server protocol revisions; unknown shapes yield an
// empty tool use and are skipped.
type codexToolItem struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Command  string `json:"command"`
	Cmd      string `json:"cmd"`
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	Query    string `json:"query"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// codexToolUses extracts side-effecting tool calls (shell commands, file
// changes, MCP calls, web searches) from a Codex item event into audit tool
// uses. Command/mcp/web items are recorded when they start (intent before
// effect); file changes are recorded on completion, when their patch summary is
// available. It is best-effort: items it does not recognize produce nothing.
func (c *codexClient) codexToolUses(method string, params json.RawMessage, key codexTurnKey) []*ToolUse {
	var p struct {
		Item codexToolItem `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	item := p.Item
	switch item.Type {
	case "commandExecution":
		if method != "item/started" {
			return nil
		}
		cmd := firstNonEmptyString(item.Command, item.Cmd)
		return []*ToolUse{{Provider: config.ProviderCodex, ToolUseID: item.ID, Name: "commandExecution", Input: params, Summary: cmd}}
	case "mcpToolCall":
		if method != "item/started" {
			return nil
		}
		name := "mcpToolCall"
		if item.Server != "" && item.Tool != "" {
			name = "mcp__" + item.Server + "__" + item.Tool
		}
		return []*ToolUse{{Provider: config.ProviderCodex, ToolUseID: item.ID, Name: name, Input: params}}
	case "webSearch":
		if method != "item/started" {
			return nil
		}
		return []*ToolUse{{Provider: config.ProviderCodex, ToolUseID: item.ID, Name: "webSearch", Input: params, Summary: item.Query}}
	case "fileChange":
		if method != "item/completed" {
			return nil
		}
		summary := c.fileChangeSummary(key.threadID, key.turnID, item.ID)
		return []*ToolUse{{Provider: config.ProviderCodex, ToolUseID: item.ID, Name: "fileChange", Input: params, Summary: summary}}
	default:
		return nil
	}
}

func codexNativeAgentItemActivity(method string, params json.RawMessage) (*NativeAgentActivity, bool) {
	var p struct {
		Item codexNativeAgentItem `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	return codexNativeAgentActivityFromItem(method, p.Item)
}

func codexNativeAgentTurnActivities(params json.RawMessage) []*NativeAgentActivity {
	var p struct {
		Turn struct {
			Items []codexNativeAgentItem `json:"items"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	out := make([]*NativeAgentActivity, 0, len(p.Turn.Items))
	for _, item := range p.Turn.Items {
		activity, ok := codexNativeAgentActivityFromItem("turn/completed", item)
		if !ok {
			continue
		}
		out = append(out, activity)
	}
	return out
}

type codexNativeAgentItem struct {
	Type              string                     `json:"type"`
	ID                string                     `json:"id"`
	Tool              string                     `json:"tool"`
	Status            string                     `json:"status"`
	ReceiverThreadIDs []string                   `json:"receiverThreadIds"`
	AgentsStates      map[string]codexAgentState `json:"agentsStates"`
	Kind              string                     `json:"kind"`
	AgentThreadID     string                     `json:"agentThreadId"`
	AgentPath         string                     `json:"agentPath"`
}

type codexAgentState struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func codexNativeAgentActivityFromItem(method string, item codexNativeAgentItem) (*NativeAgentActivity, bool) {
	switch item.Type {
	case "collabAgentToolCall":
		if item.Tool != "spawnAgent" {
			return nil, false
		}
		taskID := firstNonEmptyString(firstCodexReceiverThread(item.ReceiverThreadIDs), item.ID)
		if taskID == "" {
			return nil, false
		}
		return &NativeAgentActivity{
			Provider:  config.ProviderCodex,
			TaskID:    taskID,
			ToolUseID: item.ID,
			Status:    codexCollabAgentStatus(method, item.Status, item.AgentsStates),
		}, true
	case "subAgentActivity":
		status := codexSubAgentActivityStatus(item.Kind)
		if status == "" {
			return nil, false
		}
		taskID := firstNonEmptyString(item.AgentThreadID, item.ID)
		if taskID == "" {
			return nil, false
		}
		return &NativeAgentActivity{
			Provider:          config.ProviderCodex,
			TaskID:            taskID,
			ToolUseID:         item.ID,
			ProviderAgentName: item.AgentPath,
			Status:            status,
		}, true
	default:
		return nil, false
	}
}

func codexNativeAgentThreadActivity(params json.RawMessage) (*NativeAgentActivity, bool) {
	var p struct {
		Thread struct {
			ID             string `json:"id"`
			ParentThreadID string `json:"parentThreadId"`
			AgentNickname  string `json:"agentNickname"`
			AgentRole      string `json:"agentRole"`
			Source         any    `json:"source"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	if p.Thread.ID == "" || p.Thread.ParentThreadID == "" {
		return nil, false
	}
	providerAgent := firstNonEmptyString(p.Thread.AgentRole, p.Thread.AgentNickname, codexThreadSourceAgentPath(p.Thread.Source))
	return &NativeAgentActivity{
		Provider:          config.ProviderCodex,
		TaskID:            p.Thread.ID,
		ProviderAgentName: providerAgent,
		Status:            "started",
	}, true
}

func codexThreadSourceAgentPath(source any) string {
	root, ok := source.(map[string]any)
	if !ok {
		return ""
	}
	subagent, ok := root["subagent"].(map[string]any)
	if !ok {
		return ""
	}
	spawn, ok := subagent["thread_spawn"].(map[string]any)
	if !ok {
		return ""
	}
	agentPath, _ := spawn["agent_path"].(string)
	return agentPath
}

func codexCollabAgentStatus(method, status string, states map[string]codexAgentState) string {
	status = strings.TrimSpace(status)
	switch status {
	case "completed", "failed":
		return status
	case "inProgress":
		if method == "turn/completed" {
			for _, state := range states {
				if terminal := codexAgentStateStatus(state.Status); terminal != "" {
					return terminal
				}
			}
		}
		return "started"
	default:
		if method == "item/completed" || method == "turn/completed" {
			return "completed"
		}
		return "started"
	}
}

func codexAgentStateStatus(status string) string {
	switch status {
	case "completed":
		return "completed"
	case "errored", "notFound":
		return "failed"
	case "interrupted", "shutdown":
		return "cancelled"
	default:
		return ""
	}
}

func codexSubAgentActivityStatus(kind string) string {
	switch kind {
	case "started", "interacted":
		return "started"
	case "interrupted":
		return "cancelled"
	default:
		return ""
	}
}

func enrichCodexNativeAgentActivity(settings TurnSettings, track *codexNativeAgentTrack, activity *NativeAgentActivity) (*NativeAgentActivity, bool) {
	if activity == nil {
		return nil, false
	}
	cp := *activity
	cp.Provider = config.ProviderCodex
	if cp.Status == "" {
		cp.Status = "started"
	}
	if cp.TaskID != "" && track != nil {
		if known, ok := track.nativeAgentTasks[cp.TaskID]; ok {
			cp.ProviderAgentName = firstNonEmptyString(cp.ProviderAgentName, known.ProviderAgentName)
			cp.PodiomAgentName = firstNonEmptyString(cp.PodiomAgentName, known.PodiomAgentName)
			cp.DisplayName = firstNonEmptyString(cp.DisplayName, known.DisplayName)
			cp.ToolUseID = firstNonEmptyString(cp.ToolUseID, known.ToolUseID)
			cp.Description = firstNonEmptyString(cp.Description, known.Description)
		}
	}
	if native, ok := matchCodexNativeAgent(cp.ProviderAgentName, settings.NativeAgents); ok {
		cp.ProviderAgentName = native.Name
		cp.PodiomAgentName = native.PodiomName
		cp.DisplayName = native.PodiomName
	}
	if cp.DisplayName == "" {
		cp.DisplayName = displayNameForNativeAgent(codexNativeAgentDisplayCandidate(cp.ProviderAgentName))
	}
	if cp.TaskID == "" && cp.ToolUseID == "" && cp.ProviderAgentName == "" {
		return nil, false
	}
	if cp.TaskID != "" && track != nil {
		if previous, ok := track.nativeAgentTasks[cp.TaskID]; ok && sameNativeAgentActivity(previous, cp) {
			return nil, false
		}
		track.nativeAgentTasks[cp.TaskID] = cp
	}
	return &cp, true
}

func sameNativeAgentActivity(a, b NativeAgentActivity) bool {
	return a.Provider == b.Provider &&
		a.TaskID == b.TaskID &&
		a.ToolUseID == b.ToolUseID &&
		a.ProviderAgentName == b.ProviderAgentName &&
		a.PodiomAgentName == b.PodiomAgentName &&
		a.DisplayName == b.DisplayName &&
		a.Description == b.Description &&
		a.Status == b.Status
}

func matchCodexNativeAgent(providerAgent string, agents []NativeAgent) (NativeAgent, bool) {
	providerAgent = strings.TrimSpace(providerAgent)
	candidatePath := cleanOptionalPath(providerAgent)
	candidateBase := codexNativeAgentDisplayCandidate(providerAgent)
	for _, native := range agents {
		if native.Name != "" && (providerAgent == native.Name || candidateBase == native.Name) {
			return native, true
		}
		if native.PodiomName != "" && strings.EqualFold(providerAgent, native.PodiomName) {
			return native, true
		}
		if native.ConfigPath != "" && candidatePath != "" && cleanOptionalPath(native.ConfigPath) == candidatePath {
			return native, true
		}
	}
	return NativeAgent{}, false
}

func firstCodexReceiverThread(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func codexNativeAgentDisplayCandidate(providerAgent string) string {
	providerAgent = strings.TrimSpace(providerAgent)
	if providerAgent == "" {
		return "subagent"
	}
	base := filepath.Base(providerAgent)
	if base == "." || base == string(filepath.Separator) {
		base = providerAgent
	}
	if ext := filepath.Ext(base); ext != "" && len(ext) < len(base) {
		base = strings.TrimSuffix(base, ext)
	}
	return firstNonEmptyString(base, providerAgent)
}

// codexDelta returns a streamed delta's text, the id of the item it belongs to,
// and whether the item's phase makes it reasoning rather than the final answer.
func codexDelta(params json.RawMessage) (string, string, bool) {
	var p struct {
		Delta  string `json:"delta"`
		Phase  string `json:"phase"`
		ItemID string `json:"itemId"`
		Item   struct {
			ID    string `json:"id"`
			Phase string `json:"phase"`
		} `json:"item"`
	}
	_ = json.Unmarshal(params, &p)
	phase := firstNonEmptyString(p.Phase, p.Item.Phase)
	itemID := firstNonEmptyString(p.ItemID, p.Item.ID)
	return p.Delta, itemID, phase != "" && phase != "final_answer"
}

func codexFinalMessage(params json.RawMessage) string {
	var p struct {
		Turn struct {
			Items []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"items"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	var finals []string
	for _, item := range p.Turn.Items {
		if item.Type != "agentMessage" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		if item.Phase == "final_answer" {
			finals = append(finals, item.Text)
		}
	}
	return strings.Join(finals, "\n")
}

func codexReasoningMessage(params json.RawMessage) string {
	var p struct {
		Turn struct {
			Items []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"items"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	var reasoning []string
	for _, item := range p.Turn.Items {
		if item.Type != "agentMessage" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		if item.Phase != "" && item.Phase != "final_answer" {
			reasoning = append(reasoning, item.Text)
		}
	}
	return strings.Join(reasoning, "\n")
}

func codexErrorMessage(params json.RawMessage) string {
	var p struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(params, &p); err == nil && p.Error != nil {
		raw, _ := json.Marshal(p.Error)
		return "codex error: " + string(raw)
	}
	return "codex error"
}

func codexReplayMessage(history []store.Message, liveMessage string) string {
	var b strings.Builder
	b.WriteString("Podiom is continuing a durable session in a fresh Codex thread. ")
	b.WriteString("Use this canonical transcript as prior context, then answer the live user turn.\n\n")
	b.WriteString("<podiom_history>\n")
	for _, msg := range history {
		content := historyMessageContent(msg)
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", msg.Role, content)
	}
	b.WriteString("</podiom_history>\n\n")
	b.WriteString("Live user turn:\n")
	b.WriteString(liveMessage)
	return b.String()
}

func codexRateLimited(params json.RawMessage) bool {
	lower := strings.ToLower(string(params))
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "usage_limit") ||
		strings.Contains(lower, "usagelimit") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, `"code":429`)
}

// codexAuthFailure reports whether an app-server error notification means this
// Codex account is not signed in. Phrasings come from the CLI's own strings
// ("Not logged in", "not signed in", "Please sign in again", "re-run `codex
// login`", "authentication required").
//
// A bare "unauthorized"/401 is deliberately not matched: it also appears when a
// tool call hits some unrelated API, and offering to sign in to Codex would be
// the wrong fix.
func codexAuthFailure(params json.RawMessage) bool {
	lower := strings.ToLower(string(params))
	return strings.Contains(lower, "not logged in") ||
		strings.Contains(lower, "not signed in") ||
		strings.Contains(lower, "sign in again") ||
		strings.Contains(lower, "codex login") ||
		strings.Contains(lower, "authentication required") ||
		strings.Contains(lower, "chatgpt auth not available")
}

func codexRateStatus(params json.RawMessage) (RateStatus, bool) {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return RateStatus{}, false
	}
	// UsedPercent stays the max across everything for the ≥80% summary trigger;
	// Windows carries the structured primary/secondary breakdown when present.
	max := maxUsedPercent(value)
	windows := codexParseRateWindows(value)
	if max <= 0 && len(windows) == 0 {
		return RateStatus{}, false
	}
	return RateStatus{UsedPercent: max, Windows: windows}, true
}

// codexParseRateWindows extracts the primary (5-hour) and secondary (weekly)
// rate-limit windows from an account/rateLimits/updated payload. It searches for
// a "rateLimits" node anywhere in the tree so it is resilient to nesting.
func codexParseRateWindows(value any) []RateWindow {
	node := findRateLimitsNode(value)
	if node == nil {
		return nil
	}
	var out []RateWindow
	if w, ok := codexWindowFromNode(node["primary"], "primary"); ok {
		out = append(out, w)
	}
	if w, ok := codexWindowFromNode(node["secondary"], "secondary"); ok {
		out = append(out, w)
	}
	return out
}

func findRateLimitsNode(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if strings.EqualFold(key, "rate_limits") || strings.EqualFold(key, "rateLimits") {
				if m, ok := child.(map[string]any); ok {
					return m
				}
			}
		}
		for _, child := range v {
			if m := findRateLimitsNode(child); m != nil {
				return m
			}
		}
	case []any:
		for _, child := range v {
			if m := findRateLimitsNode(child); m != nil {
				return m
			}
		}
	}
	return nil
}

func codexWindowFromNode(value any, key string) (RateWindow, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return RateWindow{}, false
	}
	percent, ok := numFromMap(m, "used_percent", "usedPercent")
	if !ok {
		return RateWindow{}, false
	}
	w := RateWindow{Key: key, UsedPercent: percent}
	if secs, ok := numFromMap(m, "window_seconds", "limit_window_seconds"); ok {
		w.WindowSeconds = int64(secs)
	} else if mins, ok := numFromMap(m, "window_minutes", "windowDurationMins"); ok {
		w.WindowSeconds = int64(mins) * 60
	}
	if secs, ok := numFromMap(m, "resets_in_seconds"); ok {
		w.ResetsAt = time.Now().Add(time.Duration(secs) * time.Second)
	} else if at, ok := numFromMap(m, "reset_at", "resets_at", "resetsAt"); ok && at > 0 {
		w.ResetsAt = time.Unix(int64(at), 0)
	}
	return w, true
}

// codexTokenUsageNode returns one breakdown out of a thread/tokenUsage/updated
// payload's tokenUsage node: "last" is the most recent request's tokens,
// "total" the thread's running sum since it started.
func codexTokenUsageNode(params json.RawMessage, key string) map[string]any {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return nil
	}
	usage := findMapByKey(value, "tokenUsage", "token_usage")
	if usage == nil {
		return nil
	}
	node, _ := usage[key].(map[string]any)
	return node
}

// codexContextStatus extracts context-window utilization from a
// thread/tokenUsage/updated payload. Codex reports both the tokens the last
// request left in the window and the model's window, so the gauge is fully
// deterministic — no per-model lookup table needed.
func codexContextStatus(params json.RawMessage) (ContextStatus, bool) {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return ContextStatus{}, false
	}
	max, ok := findNumberByKey(value, "model_context_window", "modelContextWindow")
	if !ok || max <= 0 {
		return ContextStatus{}, false
	}
	// "last" is what currently occupies the window; "total" is the thread's
	// lifetime sum and would climb past the window within a few turns.
	used := codexTokenUsageTotal(codexTokenUsageNode(params, "last"))
	if used <= 0 {
		return ContextStatus{}, false
	}
	return ContextStatus{UsedTokens: used, MaxTokens: int64(max)}, true
}

// codexCumulativeTokenTotal reports the thread's running token total, which the
// stream loop uses to tell a genuinely new request from a repeated notification.
func codexCumulativeTokenTotal(params json.RawMessage) int64 {
	return codexTokenUsageTotal(codexTokenUsageNode(params, "total"))
}

// codexTurnUsage extracts the billed-token breakdown for the latest request from
// a thread/tokenUsage/updated payload's "last" node, so it is naturally
// incremental — one emission per request, which core sums into the session's
// lifetime total.
func codexTurnUsage(params json.RawMessage) (TurnUsage, bool) {
	usage := codexTokenUsageNode(params, "last")
	if usage == nil {
		return TurnUsage{}, false
	}
	num := func(keys ...string) int64 {
		if v, ok := numFromMap(usage, keys...); ok {
			return int64(v)
		}
		return 0
	}
	cached := num("cached_input_tokens", "cachedInputTokens")
	tu := TurnUsage{
		// Codex counts cache reads inside inputTokens; the classes are billed
		// separately here, so the cached share is subtracted out rather than
		// counted in both.
		Input:      max(num("input_tokens", "inputTokens")-cached, 0),
		Output:     num("output_tokens", "outputTokens") + num("reasoning_output_tokens", "reasoningOutputTokens"),
		CacheRead:  cached,
		CacheWrite: 0, // Codex does not report a cache-creation class.
	}
	if tu.Total() <= 0 {
		return TurnUsage{}, false
	}
	return tu, true
}

// codexTokenUsageTotal totals a Codex token-usage node: totalTokens when the
// provider supplies it, else the sum of the component token classes.
func codexTokenUsageTotal(m map[string]any) int64 {
	if m == nil {
		return 0
	}
	if total, ok := numFromMap(m, "total_tokens", "totalTokens"); ok && total > 0 {
		return int64(total)
	}
	var sum float64
	for _, keys := range [][]string{
		{"input_tokens", "inputTokens"},
		{"output_tokens", "outputTokens"},
	} {
		if v, ok := numFromMap(m, keys...); ok {
			sum += v
		}
	}
	return int64(sum)
}

// findMapByKey returns the first map value stored under any of the given keys,
// searching the tree recursively (resilient to the payload's nesting).
func findMapByKey(value any, keys ...string) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			for _, want := range keys {
				if strings.EqualFold(k, want) {
					if m, ok := child.(map[string]any); ok {
						return m
					}
				}
			}
		}
		for _, child := range v {
			if m := findMapByKey(child, keys...); m != nil {
				return m
			}
		}
	case []any:
		for _, child := range v {
			if m := findMapByKey(child, keys...); m != nil {
				return m
			}
		}
	}
	return nil
}

// findNumberByKey returns the first numeric value stored under any of the given
// keys, searching the tree recursively.
func findNumberByKey(value any, keys ...string) (float64, bool) {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			for _, want := range keys {
				if strings.EqualFold(k, want) {
					if f, ok := child.(float64); ok {
						return f, true
					}
				}
			}
		}
		for _, child := range v {
			if f, ok := findNumberByKey(child, keys...); ok {
				return f, true
			}
		}
	case []any:
		for _, child := range v {
			if f, ok := findNumberByKey(child, keys...); ok {
				return f, true
			}
		}
	}
	return 0, false
}

func numFromMap(m map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		for mk, mv := range m {
			if strings.EqualFold(mk, key) {
				if f, ok := mv.(float64); ok {
					return f, true
				}
			}
		}
	}
	return 0, false
}

func maxUsedPercent(value any) float64 {
	switch v := value.(type) {
	case map[string]any:
		var max float64
		for key, child := range v {
			if strings.EqualFold(key, "used_percent") || strings.EqualFold(key, "usedPercent") {
				if n, ok := child.(float64); ok && n > max {
					max = n
				}
				continue
			}
			if childMax := maxUsedPercent(child); childMax > max {
				max = childMax
			}
		}
		return max
	case []any:
		var max float64
		for _, child := range v {
			if childMax := maxUsedPercent(child); childMax > max {
				max = childMax
			}
		}
		return max
	default:
		return 0
	}
}

func codexIsApprovalRequest(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/permissions/requestApproval",
		"execCommandApproval",
		"applyPatchApproval":
		return true
	default:
		return false
	}
}

func (c *codexClient) codexPermissionRequest(method string, id, params json.RawMessage, active codexActiveTurn) PermissionRequest {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(params, &fields)
	threadID, codexTurnID := codexRequestThreadTurn(method, params)
	toolUseID := firstRawString(fields, "approvalId", "itemId", "callId")
	if toolUseID == "" {
		toolUseID = codexIDKey(id)
	}
	turnID := active.podiomTurnID
	if turnID == "" {
		turnID = codexTurnID
	}
	return PermissionRequest{
		ID:          "codex-" + sanitizeFilename(codexIDKey(id)) + "-" + sanitizeFilename(toolUseID),
		TurnID:      turnID,
		ToolName:    codexToolName(method),
		ToolUseID:   toolUseID,
		Description: c.codexPermissionDescription(method, fields, threadID, codexTurnID, toolUseID),
		Input:       append(json.RawMessage(nil), params...),
	}
}

func (c *codexClient) codexPermissionDescription(method string, fields map[string]json.RawMessage, threadID, turnID, itemID string) string {
	switch method {
	case "item/fileChange/requestApproval":
		if summary := c.fileChangeSummary(threadID, turnID, itemID); summary != "" {
			return summary
		}
		if root := firstRawString(fields, "grantRoot"); root != "" {
			if reason := firstRawString(fields, "reason", "description"); reason != "" {
				return fmt.Sprintf("Approve file changes under %s: %s", root, reason)
			}
			return "Approve file changes under " + root
		}
		if reason := firstRawString(fields, "reason", "description"); reason != "" {
			return "Approve file changes: " + reason
		}
		if itemID != "" {
			return "Approve file changes from Codex item " + itemID
		}
		return "Approve file changes"
	case "applyPatchApproval":
		if summary := codexLegacyFileChangeSummary(fields); summary != "" {
			return summary
		}
	}
	return firstRawString(fields, "description", "reason")
}

func codexFileChangePatchSummary(params json.RawMessage) (codexTurnKey, string, string) {
	var p struct {
		ThreadID string                       `json:"threadId"`
		TurnID   string                       `json:"turnId"`
		ItemID   string                       `json:"itemId"`
		Changes  []codexFileChangeSummaryItem `json:"changes"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return codexTurnKey{}, "", ""
	}
	return codexTurnKey{threadID: p.ThreadID, turnID: p.TurnID}, p.ItemID, codexFormatFileChanges(p.Changes)
}

func codexLegacyFileChangeSummary(fields map[string]json.RawMessage) string {
	raw := fields["fileChanges"]
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = fields["changes"]
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var changes []codexLegacyFileChange
	if err := json.Unmarshal(raw, &changes); err != nil {
		return ""
	}
	items := make([]codexFileChangeSummaryItem, 0, len(changes))
	for _, change := range changes {
		item := codexFileChangeSummaryItem{Path: change.Path}
		item.Kind.Type = change.Type
		item.Kind.MovePath = change.MovePath
		items = append(items, item)
	}
	return codexFormatFileChanges(items)
}

type codexFileChangeSummaryItem struct {
	Path string `json:"path"`
	Kind struct {
		Type     string `json:"type"`
		MovePath string `json:"move_path"`
	} `json:"kind"`
}

type codexLegacyFileChange struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	MovePath string `json:"move_path"`
}

func codexFormatFileChanges(changes []codexFileChangeSummaryItem) string {
	if len(changes) == 0 {
		return ""
	}
	const maxShown = 5
	parts := make([]string, 0, min(len(changes), maxShown))
	for i, change := range changes {
		if i >= maxShown {
			break
		}
		path := strings.TrimSpace(change.Path)
		kind := strings.TrimSpace(change.Kind.Type)
		if kind == "" {
			kind = "update"
		}
		if path == "" {
			path = "unknown path"
		}
		if kind == "update" && strings.TrimSpace(change.Kind.MovePath) != "" {
			parts = append(parts, fmt.Sprintf("move %s -> %s", path, strings.TrimSpace(change.Kind.MovePath)))
			continue
		}
		parts = append(parts, kind+" "+path)
	}
	if len(changes) > maxShown {
		parts = append(parts, fmt.Sprintf("+ %d more", len(changes)-maxShown))
	}
	return "Approve file changes: " + strings.Join(parts, "; ")
}

func codexRequestThreadTurn(method string, params json.RawMessage) (string, string) {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(params, &fields)
	threadID := firstRawString(fields, "threadId", "conversationId")
	turnID := firstRawString(fields, "turnId")
	return threadID, turnID
}

func codexUserInputRequest(id, params json.RawMessage, active codexActiveTurn) UserInputRequest {
	var p struct {
		ThreadID         string              `json:"threadId"`
		TurnID           string              `json:"turnId"`
		ItemID           string              `json:"itemId"`
		Questions        []UserInputQuestion `json:"questions"`
		AutoResolutionMS *uint64             `json:"autoResolutionMs"`
	}
	_ = json.Unmarshal(params, &p)
	autoMS := int64(0)
	if p.AutoResolutionMS != nil {
		autoMS = int64(*p.AutoResolutionMS)
	}
	turnID := active.podiomTurnID
	if turnID == "" {
		turnID = p.TurnID
	}
	reqID := "codex-" + sanitizeFilename(codexIDKey(id))
	if p.ItemID != "" {
		reqID += "-" + sanitizeFilename(p.ItemID)
	}
	NormalizeUserInputQuestions(p.Questions)
	return UserInputRequest{
		ID:               reqID,
		TurnID:           turnID,
		Provider:         config.ProviderCodex,
		ItemID:           p.ItemID,
		Questions:        p.Questions,
		AutoResolutionMS: autoMS,
	}
}

func codexUserInputResponse(decision UserInputDecision) any {
	answers := map[string]any{}
	for id, values := range decision.Answers {
		answers[id] = map[string]any{"answers": values}
	}
	return map[string]any{"answers": answers}
}

func codexToolName(method string) string {
	switch method {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		return "codex.command"
	case "item/fileChange/requestApproval", "applyPatchApproval":
		return "codex.file_change"
	case "item/permissions/requestApproval":
		return "codex.permissions"
	default:
		return "codex.approval"
	}
}

func codexApprovalResponse(method string, params json.RawMessage, decision PermissionDecision) any {
	allowed := decision.Behavior == "allow"
	switch method {
	case "item/commandExecution/requestApproval":
		if allowed {
			return map[string]any{"decision": "accept"}
		}
		return map[string]any{"decision": "decline"}
	case "item/fileChange/requestApproval":
		if allowed {
			return map[string]any{"decision": "accept"}
		}
		return map[string]any{"decision": "decline"}
	case "item/permissions/requestApproval":
		if !allowed {
			return map[string]any{
				"permissions":      map[string]any{},
				"scope":            "turn",
				"strictAutoReview": true,
			}
		}
		var p struct {
			Permissions json.RawMessage `json:"permissions"`
		}
		granted := map[string]any{}
		if err := json.Unmarshal(params, &p); err == nil && len(p.Permissions) > 0 {
			_ = json.Unmarshal(p.Permissions, &granted)
		}
		return map[string]any{"permissions": granted, "scope": "turn"}
	case "execCommandApproval", "applyPatchApproval":
		if allowed {
			return map[string]any{"decision": "approved"}
		}
		return map[string]any{"decision": "denied"}
	default:
		return map[string]any{}
	}
}

func firstRawString(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw := fields[key]
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return ""
}

func codexIDKey(raw json.RawMessage) string {
	return strings.TrimSpace(string(raw))
}

// codexEnv builds the app-server environment. Note: the server is one
// long-lived process per profile, so per-agent ToolPathDirs (workspace tool
// installs §2.2) cannot be injected here — a documented v1 limitation; tools
// are on disk but not on PATH for Codex-backed turns. User-granted credentials
// (ExtraEnv) are read at app-server spawn, but RefreshCredentials respawns the
// server at the next turn boundary (or immediately when idle) after a credential
// is stored, so it reaches Codex-backed turns without a daemon restart.
func codexEnv(profileDir string) []string {
	info, _ := config.ProviderInfoFor(config.ProviderCodex)
	return podiomexec.ProfileEnv(os.Environ(), info.ProfileEnvVar, profileDir)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
