package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	Logger            *slog.Logger
}

// Codex drives a long-lived `codex app-server --listen stdio://` process.
// Generated MCP profile content is passed as app-server config overrides
// because current Codex app-server does not accept the root --profile flag.
// A separate app-server is maintained for each
// CODEX_HOME plus generated MCP profile hash.
type Codex struct {
	bin               string
	permissionTimeout time.Duration

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
	profileName, profileHash, profileConfig, err := c.ensureMCPProfile(req.ProfileDir, req.AgentName, req.MCPServers, req.MCPAllServers)
	if err != nil {
		return Handle{}, err
	}
	client := c.client(req.ProfileDir, profileName, profileHash, profileConfig)
	result, err := client.call(ctx, "thread/start", codexThreadStartParams(req))
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
	client.markLoaded(threadID)
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
	profileName, profileHash, profileConfig, err := c.ensureMCPProfile(req.Settings.ProfileDir, req.Settings.AgentName, req.Settings.MCPServers, req.Settings.MCPAllServers)
	if err != nil {
		return nil, err
	}
	client := c.client(req.Settings.ProfileDir, profileName, profileHash, profileConfig)
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
		})
		if err != nil {
			c.turnLog(req).Warn("provider thread start failed", "stage", "thread_start", "method", "thread/start", "error", podiomlog.Redact(err.Error()))
			return nil, err
		}
		threadID = handle.ID
		firstEvents = append(firstEvents, Event{Kind: EventHandleUpdated, Handle: &handle})
	} else if err := client.ensureThread(ctx, threadID, req.Settings); err != nil {
		c.turnLog(req).Warn("provider thread resume failed", "stage", "thread_resume", "method", "thread/resume", "error", podiomlog.Redact(err.Error()))
		return nil, err
	} else {
		c.turnLog(req).Info("provider thread resumed", "event", "provider", "stage", "thread_resume", "thread", threadID)
	}

	message := req.Message
	if startedFresh && len(req.History) > 0 {
		message = codexReplayMessage(req.History, req.Message)
	}

	result, err := client.call(ctx, "turn/start", codexTurnStartParams(threadID, message, req.Settings))
	if err != nil && threadID != "" {
		c.turnLog(req).Warn("provider turn start failed; retrying after resume", "stage", "turn_start", "method", "turn/start", "error", podiomlog.Redact(err.Error()))
		client.markUnloaded(threadID)
		if resumeErr := client.ensureThread(ctx, threadID, req.Settings); resumeErr == nil {
			result, err = client.call(ctx, "turn/start", codexTurnStartParams(threadID, message, req.Settings))
		} else {
			c.turnLog(req).Warn("provider retry resume failed", "stage", "thread_resume", "method", "thread/resume", "error", podiomlog.Redact(resumeErr.Error()))
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
	go client.streamTurn(ctx, key, turnEvents, firstEvents, out)
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
		client = newCodexClient(c.bin, profileDir, profileName, profileHash, profileConfig, c.log)
		c.clients[key] = client
	}
	return client
}

func (c *Codex) ensureMCPProfile(profileDir, agentName string, assigned, all []podiommcp.Server) (string, string, string, error) {
	if len(assigned) == 0 && len(all) == 0 {
		return "", "", "", nil
	}
	content, unavailable := podiommcp.CodexProfile(assigned, all)
	if len(unavailable) > 0 {
		return "", "", "", fmt.Errorf("mcp server(s) unavailable on codex: %s", strings.Join(unavailable, ", "))
	}
	name, _, err := podiommcp.WriteCodexProfile(profileDir, agentName, content)
	if err != nil {
		return "", "", "", fmt.Errorf("write codex mcp profile: %w", err)
	}
	return name, podiommcp.ProfileHash(content), content, nil
}

type codexClient struct {
	bin           string
	profileDir    string
	profileName   string
	profileHash   string
	profileConfig string
	log           *slog.Logger

	initMu   sync.Mutex
	mu       sync.Mutex
	stderrMu sync.Mutex

	cmd         *osProcess
	stdin       io.WriteCloser
	nextID      int64
	initialized bool

	pending     map[string]chan codexCallResponse
	loaded      map[string]bool
	watchers    map[codexTurnKey]chan codexStreamEvent
	buffered    map[codexTurnKey][]codexStreamEvent
	active      map[codexTurnKey]codexActiveTurn
	fileChanges map[codexTurnKey]map[string]string
	stderrTail  []string
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

func newCodexClient(bin, profileDir, profileName, profileHash, profileConfig string, log *slog.Logger) *codexClient {
	return &codexClient{
		bin:           bin,
		profileDir:    profileDir,
		profileName:   profileName,
		profileHash:   profileHash,
		profileConfig: profileConfig,
		log: loggerOrDefault(log).With(
			"provider", string(config.ProviderCodex),
			"profile_dir_set", profileDir != "",
			"mcp_profile", profileName,
			"mcp_profile_hash", profileHash,
		),
		pending:     map[string]chan codexCallResponse{},
		loaded:      map[string]bool{},
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
	cmd.Env = codexEnv(c.profileDir)
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
	c.loaded = map[string]bool{}
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
	c.loaded = map[string]bool{}
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
	if c.isLoaded(threadID) {
		return nil
	}
	result, err := c.call(ctx, "thread/resume", codexThreadResumeParams(threadID, settings))
	if err != nil {
		return err
	}
	if _, err := codexThreadID(result); err != nil {
		return err
	}
	c.markLoaded(threadID)
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

func (c *codexClient) isLoaded(threadID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loaded[threadID]
}

func (c *codexClient) markLoaded(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded[threadID] = true
}

func (c *codexClient) markUnloaded(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.loaded, threadID)
}

func (c *codexClient) registerTurn(key codexTurnKey, active codexActiveTurn) <-chan codexStreamEvent {
	ch := make(chan codexStreamEvent, 128)
	c.mu.Lock()
	c.watchers[key] = ch
	c.active[key] = active
	buffered := c.buffered[key]
	delete(c.buffered, key)
	c.mu.Unlock()
	go func() {
		for _, event := range buffered {
			ch <- event
		}
	}()
	return ch
}

func (c *codexClient) unregisterTurn(key codexTurnKey) {
	c.mu.Lock()
	delete(c.watchers, key)
	delete(c.active, key)
	delete(c.fileChanges, key)
	c.mu.Unlock()
}

func (c *codexClient) streamTurn(ctx context.Context, key codexTurnKey, events <-chan codexStreamEvent, first []Event, out chan<- Event) {
	started := time.Now()
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
				if text := codexDelta(event.params); text != "" {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventAssistantDelta, Content: text}) {
						return
					}
				}
			case "turn/completed":
				if text := codexFinalMessage(event.params); text != "" {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: text}) {
						return
					}
				}
				if status, ok := codexContextStatus(event.params); ok {
					sendAdapterEvent(ctx, out, Event{Kind: EventContextStatus, ContextStatus: &status})
				}
				if usage, ok := codexTurnUsage(event.params); ok {
					sendAdapterEvent(ctx, out, Event{Kind: EventTurnUsage, TurnUsage: &usage})
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
				c.log.Warn("provider error notification", "stage", "stream_turn", "thread", key.threadID, "turn", key.turnID, "error", podiomlog.RedactTail(codexErrorMessage(event.params), 4096))
				sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: codexErrorMessage(event.params)})
				sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
				return
			case "token_count", "account/updated":
				if status, ok := codexRateStatus(event.params); ok {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventRateStatus, RateStatus: &status}) {
						return
					}
				}
				if status, ok := codexContextStatus(event.params); ok {
					if !sendAdapterEvent(ctx, out, Event{Kind: EventContextStatus, ContextStatus: &status}) {
						return
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
		"runtimeWorkspaceRoots": workspaceRoots(req.WorkspaceDir, req.ExtraWorkspaceDirs),
		"approvalPolicy":        codexApprovalPolicy(req.PermissionMode),
		"sandbox":               codexSandboxMode(req.PermissionMode),
		"threadSource":          "podiom",
		"serviceName":           "podiom",
	}
	if req.Model != "" {
		params["model"] = req.Model
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
		params["runtimeWorkspaceRoots"] = workspaceRoots(settings.WorkspaceDir, settings.ExtraWorkspaceDirs)
	}
	if settings.Model != "" {
		params["model"] = settings.Model
	}
	return params
}

func codexTurnStartParams(threadID, message string, settings TurnSettings) map[string]any {
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          message,
			"text_elements": []any{},
		}},
		"cwd":                   settings.WorkspaceDir,
		"runtimeWorkspaceRoots": workspaceRoots(settings.WorkspaceDir, settings.ExtraWorkspaceDirs),
		"approvalPolicy":        codexApprovalPolicy(settings.PermissionMode),
		"sandboxPolicy":         codexSandboxPolicy(settings.PermissionMode, settings.WorkspaceDir),
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
	return "on-request"
}

func codexSandboxMode(mode config.PermissionMode) string {
	if mode == config.PermissionYolo {
		return "danger-full-access"
	}
	return "read-only"
}

func codexSandboxPolicy(mode config.PermissionMode, workspace string) map[string]any {
	if mode == config.PermissionYolo {
		return map[string]any{"type": "dangerFullAccess"}
	}
	return map[string]any{"type": "readOnly", "networkAccess": false}
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
	case "item/agentMessage/delta", "turn/completed", "error", "turn/started", "token_count", "account/updated":
		return codexTurnKey{threadID: p.ThreadID, turnID: turnID}, true
	default:
		return codexTurnKey{}, false
	}
}

func codexDelta(params json.RawMessage) string {
	var p struct {
		Delta string `json:"delta"`
	}
	_ = json.Unmarshal(params, &p)
	return p.Delta
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
	var fallback []string
	for _, item := range p.Turn.Items {
		if item.Type != "agentMessage" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		if item.Phase == "final_answer" {
			finals = append(finals, item.Text)
		} else {
			fallback = append(fallback, item.Text)
		}
	}
	if len(finals) > 0 {
		return strings.Join(finals, "\n")
	}
	return strings.Join(fallback, "\n")
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
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", msg.Role, msg.Content)
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
// rate-limit windows from a token_count/account/updated payload. It searches for
// a "rate_limits" node anywhere in the tree so it is resilient to nesting.
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
	} else if mins, ok := numFromMap(m, "window_minutes"); ok {
		w.WindowSeconds = int64(mins) * 60
	}
	if secs, ok := numFromMap(m, "resets_in_seconds"); ok {
		w.ResetsAt = time.Now().Add(time.Duration(secs) * time.Second)
	} else if at, ok := numFromMap(m, "reset_at", "resets_at"); ok && at > 0 {
		w.ResetsAt = time.Unix(int64(at), 0)
	}
	return w, true
}

// codexContextStatus extracts context-window utilization from a token_count or
// turn/completed payload. Codex reports both the tokens the last exchange left
// in the window (last_token_usage) and the model's window (model_context_window),
// so the gauge is fully deterministic — no per-model lookup table needed.
func codexContextStatus(params json.RawMessage) (ContextStatus, bool) {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return ContextStatus{}, false
	}
	max, ok := findNumberByKey(value, "model_context_window", "modelContextWindow")
	if !ok || max <= 0 {
		return ContextStatus{}, false
	}
	// last_token_usage reflects the tokens occupying the window after the latest
	// turn; total_token_usage (cumulative) is the fallback if it is absent.
	usage := findMapByKey(value, "last_token_usage", "lastTokenUsage")
	if usage == nil {
		usage = findMapByKey(value, "total_token_usage", "totalTokenUsage")
	}
	used := codexTokenUsageTotal(usage)
	if used <= 0 {
		return ContextStatus{}, false
	}
	return ContextStatus{UsedTokens: used, MaxTokens: int64(max)}, true
}

// codexTurnUsage extracts the per-turn billed-token breakdown from a
// turn/completed payload's last_token_usage node (the tokens for the latest
// exchange only, so it is naturally incremental — one emission per turn).
func codexTurnUsage(params json.RawMessage) (TurnUsage, bool) {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return TurnUsage{}, false
	}
	usage := findMapByKey(value, "last_token_usage", "lastTokenUsage")
	if usage == nil {
		return TurnUsage{}, false
	}
	num := func(keys ...string) int64 {
		if v, ok := numFromMap(usage, keys...); ok {
			return int64(v)
		}
		return 0
	}
	tu := TurnUsage{
		Input:      num("input_tokens", "inputTokens"),
		Output:     num("output_tokens", "outputTokens") + num("reasoning_output_tokens", "reasoningOutputTokens"),
		CacheRead:  num("cached_input_tokens", "cachedInputTokens"),
		CacheWrite: 0, // Codex does not report a cache-creation class.
	}
	if tu.Total() <= 0 {
		return TurnUsage{}, false
	}
	return tu, true
}

// codexTokenUsageTotal totals a Codex token-usage node: total_tokens when the
// provider supplies it, else the sum of the component token classes.
func codexTokenUsageTotal(m map[string]any) int64 {
	if m == nil {
		return 0
	}
	if total, ok := numFromMap(m, "total_tokens", "totalTokens"); ok && total > 0 {
		return int64(total)
	}
	var sum float64
	for _, key := range []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens"} {
		if v, ok := numFromMap(m, key); ok {
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
	normalizeUserInputQuestions(p.Questions)
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
// are on disk but not on PATH for Codex-backed turns.
func codexEnv(profileDir string) []string {
	env := os.Environ()
	if profileDir == "" {
		return unsetEnv(env, "CODEX_HOME")
	}
	return append(unsetEnv(env, "CODEX_HOME"), "CODEX_HOME="+profileDir)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
