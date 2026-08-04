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
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

const defaultPermissionTimeout = 3 * time.Minute
const claudeStderrTailLimit = 16 * 1024

// ClaudeOptions configures the Claude Code adapter.
type ClaudeOptions struct {
	Discovery         podiomexec.Discovery
	DaemonAddr        string
	PodiomHome        string
	PermissionTimeout time.Duration
	MCPCommand        string
	// ExtraEnv supplies additional NAME=value pairs for the CLI subprocess
	// environment (user-granted credentials). Called at spawn time so values
	// stored mid-session reach the next turn. Nil means none.
	ExtraEnv func() []string
	Logger   *slog.Logger
}

// Claude drives Claude Code as a per-turn process.
type Claude struct {
	bin               string
	daemonAddr        string
	podiomHome        string
	permissionTimeout time.Duration
	mcpCommand        string
	extraEnv          func() []string
	log               *slog.Logger
}

// NewClaude discovers the Claude Code CLI and returns an adapter.
func NewClaude(opts ClaudeOptions) (*Claude, error) {
	found, err := opts.Discovery.Find("claude")
	if err != nil {
		return nil, err
	}
	timeout := opts.PermissionTimeout
	if timeout == 0 {
		timeout = defaultPermissionTimeout
	}
	mcpCommand := opts.MCPCommand
	if mcpCommand == "" {
		if exe, err := os.Executable(); err == nil {
			mcpCommand = exe
		}
	}
	return &Claude{
		bin:               found.Path,
		daemonAddr:        opts.DaemonAddr,
		podiomHome:        opts.PodiomHome,
		permissionTimeout: timeout,
		mcpCommand:        mcpCommand,
		extraEnv:          opts.ExtraEnv,
		log:               loggerOrDefault(opts.Logger),
	}, nil
}

// Start returns the existing Claude handle shape. Claude only yields a real
// session ID after the first turn, so a new session starts with an empty handle.
func (c *Claude) Start(ctx context.Context, req StartRequest) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	return Handle{Provider: config.ProviderClaude}, nil
}

// Resume returns the persisted Claude session ID unchanged.
func (c *Claude) Resume(ctx context.Context, req ResumeRequest) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	return req.Handle, nil
}

// SendTurn launches one `claude -p` process and streams parsed events.
func (c *Claude) SendTurn(ctx context.Context, req TurnRequest) (<-chan Event, error) {
	if req.Settings.WorkspaceDir == "" {
		return nil, errors.New("claude workspace dir is required")
	}
	// Snapshot before the process starts so the window covers the whole turn.
	var plans planSnapshot
	if req.Settings.PlanMode {
		plans = snapshotPlans(claudePlansDir(req.Settings.ProfileDir, req.Settings.WorkspaceDir))
	}

	run, err := c.startProcess(ctx, req, true)
	if err != nil {
		c.providerLog(req).Warn("provider turn setup failed", "stage", "args", "error", err)
		return nil, err
	}

	out := make(chan Event, 32)
	go func() {
		defer close(out)
		c.consumeProcess(ctx, req, run, out, true)
		if !req.Settings.PlanMode {
			return
		}
		// A plan turn need not produce a plan — the model may still be
		// exploring, or may have answered a question instead.
		proposal := detectPlan(plans)
		if proposal == nil {
			c.providerLog(req).Info("provider turn produced no plan", "event", "provider", "stage", "plan", "plans_dir", plans.dir)
			return
		}
		c.providerLog(req).Info("provider proposed a plan", "event", "provider", "stage", "plan", "path", proposal.FilePath)
		out <- Event{Kind: EventPlanProposed, PlanProposal: proposal}
	}()
	return out, nil
}

type claudeProcess struct {
	cmd           *osexec.Cmd
	stdout        io.Reader
	stderr        io.Reader
	cleanup       func()
	startedAt     time.Time
	nativeEnabled bool
}

func (c *Claude) startProcess(ctx context.Context, req TurnRequest, allowNative bool) (claudeProcess, error) {
	args, cleanup, nativeEnabled, err := c.args(req, allowNative)
	if err != nil {
		if allowNative && len(req.Settings.NativeAgents) > 0 {
			c.providerLog(req).Warn("native agent projection failed; retrying without native agents", "stage", "native_agents", "error", err)
			return c.startProcess(ctx, withoutClaudeNativeAgents(req), false)
		}
		return claudeProcess{}, err
	}
	cmd := podiomexec.Command(ctx, c.bin, args...)
	cmd.Dir = req.Settings.WorkspaceDir
	cmd.Env = c.env(req.Settings.ProfileDir, req.Settings.ToolPathDirs)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process pipe failed", "stage", "stdin", "error", err)
		return claudeProcess{}, fmt.Errorf("claude stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process pipe failed", "stage", "stdout", "error", err)
		return claudeProcess{}, fmt.Errorf("claude stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process pipe failed", "stage", "stderr", "error", err)
		return claudeProcess{}, fmt.Errorf("claude stderr: %w", err)
	}
	startedAt := time.Now()
	c.providerLog(req).Info("provider process starting", "event", "provider", "stage", "start", "command", c.bin, "resuming", req.Handle.ID != "", "permission", string(req.Settings.PermissionMode), "mcp_servers", len(req.Settings.MCPServers), "extra_workspaces", len(req.Settings.ExtraWorkspaceDirs), "native_agent", nativeEnabled)
	if err := cmd.Start(); err != nil {
		cleanup()
		if nativeEnabled {
			c.providerLog(req).Warn("native agent projection failed; retrying without native agents", "stage", "native_agents", "error", err)
			return c.startProcess(ctx, withoutClaudeNativeAgents(req), false)
		}
		c.providerLog(req).Warn("provider process start failed", "stage", "start", "error", err)
		return claudeProcess{}, fmt.Errorf("start claude: %w", err)
	}
	if err := writeClaudeInput(stdin, messageWithImages(req.Message, req.Images), req.History, req.Handle.ID != ""); err != nil {
		_ = podiomexec.Kill(cmd)
		cleanup()
		if nativeEnabled {
			c.providerLog(req).Warn("native agent projection failed; retrying without native agents", "stage", "native_agents", "error", err)
			return c.startProcess(ctx, withoutClaudeNativeAgents(req), false)
		}
		c.providerLog(req).Warn("provider stdin write failed", "stage", "write_input", "error", err)
		return claudeProcess{}, err
	}
	c.providerLog(req).Info("provider input written", "event", "provider", "stage", "write_input", "history_messages", len(req.History), "resuming", req.Handle.ID != "")
	return claudeProcess{cmd: cmd, stdout: stdout, stderr: stderr, cleanup: cleanup, startedAt: startedAt, nativeEnabled: nativeEnabled}, nil
}

func (c *Claude) consumeProcess(ctx context.Context, req TurnRequest, run claudeProcess, out chan<- Event, allowNativeRetry bool) {
	defer run.cleanup()
	parsec := make(chan error, 1)
	trackc := make(chan claudeStreamTrack, 1)
	stderrc := make(chan stderrResult, 1)
	parsed := make(chan Event, 32)
	go func() {
		parsec <- parseClaudeStream(ctx, run.stdout, parsed, claudeStreamOptions{
			SuppressStructuredQuestions: claudeQuestionViaPermissionRelay(req),
		})
		close(parsed)
	}()
	go c.trackClaudeStream(ctx, req, parsed, out, trackc)
	go func() { stderrc <- collectStderr(run.stderr, claudeStderrTailLimit) }()
	// os/exec's Wait closes the stdout/stderr pipes the instant the process
	// exits, so calling it while the readers above are still draining races
	// into "read |0: file already closed" and drops the parsed output. Drain
	// every pipe reader first, then reap. The readers reach EOF when the
	// process closes its fds on exit, so this cannot deadlock against Wait.
	parseErr := <-parsec
	track := <-trackc
	stderrResult := <-stderrc
	waitErr := run.cmd.Wait()
	if ctx.Err() != nil {
		c.providerLog(req).Info("provider process canceled", "event", "provider", "stage", "wait", podiomlog.DurationMS("duration_ms", time.Since(run.startedAt)))
		return
	}
	if parseErr != nil {
		c.providerLog(req).Warn("provider stream parse failed", "stage", "parse_stdout", "error", podiomlog.Redact(parseErr.Error()))
		sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: fmt.Sprintf("claude stream error: %v", parseErr)})
		return
	}
	if stderrResult.err != nil {
		c.providerLog(req).Warn("provider stderr read failed", "stage", "read_stderr", "error", stderrResult.err, "stderr_tail", podiomlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
		sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: fmt.Sprintf("claude stderr error: %v", stderrResult.err)})
		return
	}
	if waitErr != nil {
		if run.nativeEnabled && allowNativeRetry && track.lastMessage == "" {
			c.providerLog(req).Warn("native agent projection failed; retrying without native agents", "stage", "native_agents", "exit_error", waitErr, "stderr_tail", podiomlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
			fallback, err := c.startProcess(ctx, withoutClaudeNativeAgents(req), false)
			if err != nil {
				c.providerLog(req).Warn("provider process fallback start failed", "stage", "native_agents", "error", err)
				sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: fmt.Sprintf("claude exited with native-agent error and fallback failed: %v", err)})
				return
			}
			c.consumeProcess(ctx, withoutClaudeNativeAgents(req), fallback, out, false)
			return
		}
		if event, send := claudeWaitEvent(waitErr, stderrResult.text, track); send && event.Kind == EventRateLimited {
			c.providerLog(req).Warn("provider rate limited", "stage", "wait", "rate_limited", true, "stderr_tail", podiomlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
			sendAdapterEvent(ctx, out, event)
			return
		} else if !send {
			c.providerLog(req).Warn("provider process exited after provider message", "event", "provider", "stage", "wait", "exit_error", waitErr, "provider_message", podiomlog.RedactTail(track.lastMessage, 4096), "stderr_tail", podiomlog.RedactTail(stderrResult.text, claudeStderrTailLimit), podiomlog.DurationMS("duration_ms", time.Since(run.startedAt)))
			return
		} else {
			c.providerLog(req).Warn("provider process exited with error", "stage", "wait", "exit_error", waitErr, "stderr_tail", podiomlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
			sendAdapterEvent(ctx, out, event)
			return
		}
	}
	c.providerLog(req).Info("provider process finished", "event", "provider", "stage", "wait", "status", "success", podiomlog.DurationMS("duration_ms", time.Since(run.startedAt)))
	sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
}

type claudeStreamTrack struct {
	lastMessage      string
	nativeAgentTasks map[string]NativeAgentActivity
}

func (c *Claude) trackClaudeStream(ctx context.Context, req TurnRequest, in <-chan Event, out chan<- Event, done chan<- claudeStreamTrack) {
	track := claudeStreamTrack{nativeAgentTasks: map[string]NativeAgentActivity{}}
	lastHandleID := req.Handle.ID
	for event := range in {
		if event.Kind == EventAssistantMessage && strings.TrimSpace(event.Content) != "" {
			track.lastMessage = event.Content
		}
		if event.Kind == EventHandleUpdated && event.Handle != nil {
			if event.Handle.ID != lastHandleID {
				lastHandleID = event.Handle.ID
				c.providerLog(req).Info("provider handle updated", "event", "provider", "stage", "stream", "provider_handle_set", event.Handle.ID != "")
			}
		}
		// The Claude CLI reports prompt token usage but not the model's window,
		// so stamp the deterministic per-model limit here where the request
		// (and thus the model) is in scope.
		if event.Kind == EventContextStatus && event.ContextStatus != nil && event.ContextStatus.MaxTokens == 0 {
			event.ContextStatus.MaxTokens = claudeContextWindow(req.Settings.Model)
		}
		if event.Kind == EventNativeAgentActivity {
			activity, ok := enrichClaudeNativeAgentActivity(req, &track, event.NativeAgent)
			if !ok {
				continue
			}
			event.NativeAgent = activity
		}
		if !sendAdapterEvent(ctx, out, event) {
			break
		}
	}
	done <- track
}

func enrichClaudeNativeAgentActivity(req TurnRequest, track *claudeStreamTrack, activity *NativeAgentActivity) (*NativeAgentActivity, bool) {
	if activity == nil {
		return nil, false
	}
	cp := *activity
	cp.Provider = config.ProviderClaude
	if cp.Status == "" {
		cp.Status = "started"
	}
	if cp.ProviderAgentName == "" && cp.TaskID != "" && track != nil {
		if known, ok := track.nativeAgentTasks[cp.TaskID]; ok {
			cp.ProviderAgentName = known.ProviderAgentName
			cp.PodiomAgentName = known.PodiomAgentName
			cp.DisplayName = known.DisplayName
			cp.ToolUseID = firstNonEmptyString(cp.ToolUseID, known.ToolUseID)
			cp.Description = firstNonEmptyString(cp.Description, known.Description)
		}
	}
	if cp.ProviderAgentName == "" && cp.TaskID == "" {
		return nil, false
	}
	for _, native := range req.Settings.NativeAgents {
		if native.Name != "" && native.Name == cp.ProviderAgentName {
			cp.PodiomAgentName = native.PodiomName
			cp.DisplayName = native.PodiomName
			break
		}
	}
	if cp.DisplayName == "" {
		cp.DisplayName = displayNameForNativeAgent(cp.ProviderAgentName)
	}
	if cp.Status == "started" && cp.TaskID != "" && track != nil {
		track.nativeAgentTasks[cp.TaskID] = cp
	}
	if cp.ProviderAgentName == "" {
		// Completion-only task notifications are useful only when they can be
		// joined to a known subagent start.
		return nil, false
	}
	return &cp, true
}

func displayNameForNativeAgent(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "subagent"
	}
	name = strings.TrimPrefix(name, "podiom-")
	name = strings.TrimPrefix(name, "podiom_")
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return name
	}
	// Drop the projection hash from unknown Podiom native names. Known names are
	// mapped to PodiomName before this fallback runs.
	if len(parts) > 1 && len(parts[len(parts)-1]) == 8 && isLowerHex(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return value != ""
}

func claudeWaitEvent(waitErr error, stderrText string, track claudeStreamTrack) (Event, bool) {
	if waitErr == nil {
		return Event{}, false
	}
	if claudeRateLimitedText(stderrText) {
		return Event{Kind: EventRateLimited, Content: stderrText}, true
	}
	if track.lastMessage != "" {
		return Event{}, false
	}
	message := fmt.Sprintf("claude exited with error: %v", waitErr)
	if stderrText != "" {
		message += ": " + stderrText
	}
	return Event{Kind: EventAssistantMessage, Content: message}, true
}

func (c *Claude) providerLog(req TurnRequest) *slog.Logger {
	return loggerOrDefault(c.log).With(
		"provider", string(config.ProviderClaude),
		"profile", req.Settings.Profile,
		"session", req.SessionID,
		"agent", req.Settings.AgentName,
	)
}

// Teardown has no persistent Claude process to stop.
func (c *Claude) Teardown(ctx context.Context, handle Handle) error {
	return ctx.Err()
}

// Capabilities returns Claude Code's current effort values when the CLI exposes
// them in help output, plus Podiom's bundled model-alias fallback registry.
func (c *Claude) Capabilities(ctx context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	caps := capabilities.Fallback(config.ProviderClaude, req.Profile)
	caps.Source = "claude-help+fallback"

	// Efforts come from the CLI help text; the CLI exposes no model listing.
	helpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := podiomexec.Command(helpCtx, c.bin, "--help")
	cmd.Env = c.env(req.ProfileDir, nil)
	raw, err := cmd.CombinedOutput()
	if helpCtx.Err() != nil {
		err = helpCtx.Err()
	}
	if err != nil {
		return capabilities.WithError(caps, fmt.Errorf("read claude help: %w", err)), nil
	}
	efforts := capabilities.ParseEffortsFromHelp(string(raw))

	// Models are fetched dynamically from the Anthropic REST API (OAuth token).
	// Any failure keeps the bundled catalogue so the picker stays usable; the
	// error is recorded on the snapshot but never surfaced as a hard error.
	modelsCtx, cancelModels := context.WithTimeout(ctx, 5*time.Second)
	defer cancelModels()
	if models, merr := fetchClaudeModels(modelsCtx, req.ProfileDir); merr == nil {
		caps.Models = models
		caps.Source = "anthropic:/v1/models"
		caps.Stale = false
	} else {
		caps = capabilities.WithError(caps, merr)
	}

	if len(efforts) > 0 {
		caps = capabilities.MergeEfforts(caps, efforts)
	}
	return caps, nil
}

func (c *Claude) args(req TurnRequest, allowNative bool) ([]string, func(), bool, error) {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--replay-user-messages",
	}
	// Expose the skills union: the agent's workspace contains a .claude/skills
	// link to ~/.agents/skills, and --add-dir brings that scope into Claude's
	// discovery without touching CLAUDE_CONFIG_DIR (S6/S7).
	if req.Settings.WorkspaceDir != "" {
		args = append(args, "--add-dir", req.Settings.WorkspaceDir)
	}
	for _, dir := range req.Settings.ExtraWorkspaceDirs {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	if path := strings.TrimSpace(req.Settings.InstructionPath); path != "" {
		args = append(args, "--append-system-prompt-file", path)
	}
	if req.Settings.Model != "" {
		args = append(args, "--model", req.Settings.Model)
	}
	if req.Settings.Effort != "" {
		args = append(args, "--effort", req.Settings.Effort)
	}
	nativeEnabled := false
	if allowNative && len(req.Settings.NativeAgents) > 0 {
		raw, err := claudeNativeAgentsJSON(req.Settings.NativeAgents)
		if err != nil {
			return nil, func() {}, false, err
		}
		args = append(args, "--agents", raw)
		if req.Settings.NativeAgentName != "" {
			args = append(args, "--agent", req.Settings.NativeAgentName)
		}
		nativeEnabled = true
	}
	if req.Handle.ID != "" {
		args = append(args, "--resume", req.Handle.ID)
	}
	cleanup := func() {}
	needsPermissionMCP := false
	if req.Settings.PlanMode {
		// Claude enforces read-only itself in plan mode, and its own phased
		// workflow (Explore/Plan subagents) is what produces the plan. Podiom
		// adds nothing here but the flag.
		args = append(args, "--permission-mode", "plan")
		configPath, err := c.writeMCPConfig(req)
		if err != nil {
			return nil, cleanup, nativeEnabled, err
		}
		cleanup = func() { _ = os.Remove(configPath) }
		args = append(args, "--mcp-config", configPath, "--strict-mcp-config")
		return args, cleanup, nativeEnabled, nil
	}
	switch req.Settings.PermissionMode {
	case config.PermissionYolo:
		args = append(args, "--permission-mode", "bypassPermissions")
	default:
		if req.Settings.PermissionMode == config.PermissionAuto {
			// acceptEdits auto-approves file edits; every other tool still goes
			// through the relay configured below. Claude's own richer "auto"
			// mode is deliberately not used: measured against 2.1.220,
			// `--permission-mode auto` is silently downgraded to "default" in
			// headless -p runs (as is "manual"), so acceptEdits is the only
			// setting that actually takes effect here.
			args = append(args, "--permission-mode", "acceptEdits")
		}
		// Unattended (scheduled) preapproved run: there is no human to answer a
		// prompt, so use Claude's native allow-list and rely on `claude -p`
		// auto-denying anything not pre-approved — no permission MCP relay (§7.7).
		if req.Settings.Unattended {
			if allowed := nonEmptyTools(req.Settings.AllowedTools); len(allowed) > 0 {
				args = append(args, "--allowedTools", strings.Join(allowed, ","))
			}
		} else {
			needsPermissionMCP = true
		}
	}
	if needsPermissionMCP && (c.daemonAddr == "" || c.mcpCommand == "") {
		return nil, cleanup, nativeEnabled, errors.New("claude approve mode needs daemon address and MCP command")
	}
	configPath, err := c.writeMCPConfig(req)
	if err != nil {
		return nil, cleanup, nativeEnabled, err
	}
	cleanup = func() { _ = os.Remove(configPath) }
	args = append(args, "--mcp-config", configPath, "--strict-mcp-config")
	if needsPermissionMCP {
		args = append(args, "--permission-prompt-tool", "mcp__podiom_permission__prompt")
	}
	return args, cleanup, nativeEnabled, nil
}

func claudeNativeAgentsJSON(agents []NativeAgent) (string, error) {
	payload := map[string]map[string]any{}
	for _, agent := range agents {
		if strings.TrimSpace(agent.Name) == "" || strings.TrimSpace(agent.Description) == "" || strings.TrimSpace(agent.Instructions) == "" {
			continue
		}
		entry := map[string]any{
			"description": agent.Description,
			"prompt":      agent.Instructions,
		}
		if agent.Model != "" {
			entry["model"] = agent.Model
		}
		if agent.Effort != "" {
			entry["effort"] = agent.Effort
		}
		payload[agent.Name] = entry
	}
	if len(payload) == 0 {
		return "", errors.New("no valid native Claude agent definitions")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode native Claude agents: %w", err)
	}
	return string(raw), nil
}

func withoutClaudeNativeAgents(req TurnRequest) TurnRequest {
	req.Settings.NativeAgentName = ""
	req.Settings.NativeAgents = nil
	return req
}

func (c *Claude) writeMCPConfig(req TurnRequest) (string, error) {
	if err := os.MkdirAll(filepath.Join(req.Settings.WorkspaceDir, ".podiom"), 0o755); err != nil {
		return "", fmt.Errorf("create claude mcp dir: %w", err)
	}
	turnID := req.Settings.PermissionTurnID
	if turnID == "" {
		turnID = req.SessionID
	}
	var permission map[string]any
	// Plan mode needs no relay: Claude's executor enforces read-only itself, so
	// nothing reaches a permission prompt and spawning the helper would be dead
	// weight.
	if req.Settings.PermissionMode != config.PermissionYolo && !req.Settings.Unattended && !req.Settings.PlanMode {
		timeout := req.Settings.PermissionTimeout
		if timeout <= 0 {
			timeout = c.permissionTimeout
		}
		if timeout <= 0 {
			timeout = defaultPermissionTimeout
		}
		permission = map[string]any{
			"command": c.mcpCommand,
			"args": []string{
				"permission-mcp",
				"--addr", c.daemonAddr,
				"--turn", turnID,
				"--timeout", timeout.String(),
			},
		}
		if c.podiomHome != "" {
			permission["env"] = map[string]string{config.EnvHome: c.podiomHome}
		}
	}
	payload := podiommcp.ClaudeConfig(req.Settings.MCPServers, permission)
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(req.Settings.WorkspaceDir, ".podiom", fmt.Sprintf("claude-mcp-%s.json", sanitizeFilename(turnID)))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write claude mcp config: %w", err)
	}
	return path, nil
}

func (c *Claude) env(profileDir string, toolPathDirs []string) []string {
	env := prependPath(os.Environ(), toolPathDirs)
	env = applyExtraEnv(env, c.extraEnv)
	if profileDir == "" {
		return unsetEnv(env, "CLAUDE_CONFIG_DIR")
	}
	return append(unsetEnv(env, "CLAUDE_CONFIG_DIR"), "CLAUDE_CONFIG_DIR="+profileDir)
}

// applyExtraEnv overlays supplier pairs onto env; a stored value replaces an
// inherited variable of the same name (matching MCP resolveEnvPairs
// semantics). Nil supplier or malformed pairs are no-ops.
func applyExtraEnv(env []string, supplier func() []string) []string {
	if supplier == nil {
		return env
	}
	for _, kv := range supplier() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		env = append(unsetEnv(env, name), kv)
	}
	return env
}

// prependPath puts the agent's tool directories ahead of the inherited PATH so
// a workspace-installed tool wins over a same-named host tool for this agent
// only. No-op without dirs.
func prependPath(env, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}
	prefix := strings.Join(dirs, string(os.PathListSeparator))
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out := append([]string(nil), env...)
			out[i] = "PATH=" + prefix + string(os.PathListSeparator) + kv[len("PATH="):]
			return out
		}
	}
	return append(append([]string(nil), env...), "PATH="+prefix)
}

func writeClaudeInput(stdin io.WriteCloser, message string, history []store.Message, resumed bool) error {
	defer stdin.Close()
	enc := json.NewEncoder(stdin)
	if !resumed {
		for _, msg := range history {
			content := historyMessageContent(msg)
			if content == "" {
				continue
			}
			if err := enc.Encode(claudeInputMessage(string(msg.Role), content)); err != nil {
				return fmt.Errorf("write history to claude: %w", err)
			}
		}
	}
	if err := enc.Encode(claudeInputMessage("user", message)); err != nil {
		return fmt.Errorf("write user turn to claude: %w", err)
	}
	return nil
}

func claudeInputMessage(role, text string) map[string]any {
	return map[string]any{
		"type": role,
		"message": map[string]any{
			"role": role,
			"content": []map[string]string{
				{"type": "text", "text": text},
			},
		},
	}
}

type claudeStreamOptions struct {
	// SuppressStructuredQuestions leaves AskUserQuestion delivery to Claude's
	// blocking permission-prompt callback. Text fallbacks remain enabled.
	SuppressStructuredQuestions bool
}

func claudeQuestionViaPermissionRelay(req TurnRequest) bool {
	return !req.Settings.PlanMode &&
		!req.Settings.Unattended &&
		req.Settings.PermissionMode != config.PermissionYolo
}

func parseClaudeStream(ctx context.Context, r io.Reader, out chan<- Event, options ...claudeStreamOptions) error {
	var opts claudeStreamOptions
	if len(options) > 0 {
		opts = options[0]
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lastHandleID := ""
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		events, err := parseClaudeLineWithOptions(line, opts)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Kind == EventHandleUpdated && event.Handle != nil {
				if event.Handle.ID == lastHandleID {
					continue
				}
				lastHandleID = event.Handle.ID
			}
			if !sendAdapterEvent(ctx, out, event) {
				return ctx.Err()
			}
		}
	}
	return scanner.Err()
}

func parseClaudeLine(line []byte) ([]Event, error) {
	return parseClaudeLineWithOptions(line, claudeStreamOptions{})
}

func parseClaudeLineWithOptions(line []byte, opts claudeStreamOptions) ([]Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("parse claude json %q: %w", string(line), err)
	}
	var events []Event
	if id := firstString(raw, "session_id", "sessionId"); id != "" {
		events = append(events, Event{
			Kind:   EventHandleUpdated,
			Handle: &Handle{Provider: config.ProviderClaude, ID: id},
		})
	}
	eventType := firstString(raw, "type", "event")
	switch eventType {
	case "stream_event":
		if nested, ok := raw["event"].(map[string]any); ok {
			if req, ok := claudeStreamUserInputRequest(nested, line, opts); ok {
				events = append(events, Event{Kind: EventUserInputRequest, UserInputRequest: req})
			} else if text, reasoning := extractTextKind(nested); text != "" {
				if reasoning {
					events = append(events, Event{Kind: EventReasoningDelta, Content: text})
				} else {
					events = append(events, Event{Kind: EventAssistantDelta, Content: text})
				}
			}
		}
	case "assistant_delta", "text_delta", "content_block_delta", "thinking_delta", "reasoning_delta":
		if text, reasoning := extractTextKind(raw); text != "" {
			if reasoning {
				events = append(events, Event{Kind: EventReasoningDelta, Content: text})
			} else {
				events = append(events, Event{Kind: EventAssistantDelta, Content: text})
			}
		}
	case "assistant", "message":
		if req, ok := claudeStreamUserInputRequest(raw, line, opts); ok {
			events = append(events, Event{Kind: EventUserInputRequest, UserInputRequest: req})
		} else {
			if text := extractReasoningText(raw); text != "" {
				events = append(events, Event{Kind: EventReasoningMessage, Content: text})
			}
			if text := extractText(raw); text != "" {
				events = append(events, Event{Kind: EventAssistantMessage, Content: text})
			}
		}
		// The complete assistant message carries the turn's tool_use blocks once;
		// the streaming content_block partials above are text-only, so this is the
		// single, non-duplicating source of tool-call audit events.
		for _, tu := range claudeToolUses(raw) {
			events = append(events, Event{Kind: EventToolUse, ToolUse: tu})
		}
		if ctxEvent, ok := claudeContextEvent(raw); ok {
			events = append(events, ctxEvent)
		}
	case "result":
		if claudeResultRateLimited(raw) {
			events = append(events, Event{Kind: EventRateLimited, Content: firstString(raw, "result", "content")})
			return events, nil
		}
		if text := firstString(raw, "result", "content"); text != "" {
			if req, ok := claudeUserInputRequestFromText(text, line); ok {
				events = append(events, Event{Kind: EventUserInputRequest, UserInputRequest: req})
			} else {
				events = append(events, Event{Kind: EventAssistantMessage, Content: text})
			}
		}
		if ctxEvent, ok := claudeContextEvent(raw); ok {
			events = append(events, ctxEvent)
		}
		if usageEvent, ok := claudeTurnUsageEvent(raw); ok {
			events = append(events, usageEvent)
		}
	case "api_retry":
		if claudeRateLimited(raw) {
			events = append(events, Event{Kind: EventRateLimited, Content: "claude rate limited"})
		}
	case "rate_limit_event":
		// The CLI's authoritative account-limit signal: status "allowed" (and
		// warnings) are informational; "rejected"/"exceeded" mean the limit is in
		// effect for this request.
		if info, ok := raw["rate_limit_info"].(map[string]any); ok {
			switch strings.ToLower(firstString(info, "status")) {
			case "rejected", "exceeded":
				events = append(events, Event{Kind: EventRateLimited, Content: "claude rate limited: " + firstString(info, "rateLimitType", "rate_limit_type", "status")})
			}
		}
	case "system":
		if activity, ok := claudeNativeAgentActivity(raw); ok {
			events = append(events, Event{Kind: EventNativeAgentActivity, NativeAgent: activity})
		}
	case "error":
		message := claudeErrorMessage(raw)
		if claudeRateLimitedText(message) {
			events = append(events, Event{Kind: EventRateLimited, Content: message})
		} else if message != "" {
			events = append(events, Event{Kind: EventAssistantMessage, Content: "claude error: " + message})
		}
	}
	return events, nil
}

func claudeStreamUserInputRequest(raw map[string]any, source []byte, opts claudeStreamOptions) (*UserInputRequest, bool) {
	if !opts.SuppressStructuredQuestions {
		return claudeUserInputRequest(raw, source)
	}
	if text := extractText(raw); text != "" {
		return claudeUserInputRequestFromText(text, source)
	}
	return nil, false
}

// claudeToolUses extracts tool_use content blocks from a complete assistant
// message into audit events. Each block is {type:"tool_use", id, name, input};
// Summary is a best-effort human-readable one-liner per known tool.
func claudeToolUses(raw map[string]any) []*ToolUse {
	content := raw["content"]
	if message, ok := raw["message"].(map[string]any); ok {
		if c, ok := message["content"]; ok {
			content = c
		}
	}
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	var uses []*ToolUse
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok || firstString(block, "type") != "tool_use" {
			continue
		}
		name := firstString(block, "name")
		if name == "" {
			continue
		}
		tu := &ToolUse{
			Provider:  config.ProviderClaude,
			ToolUseID: firstString(block, "id", "tool_use_id", "toolUseID"),
			Name:      name,
		}
		if input, ok := block["input"].(map[string]any); ok {
			if raw, err := json.Marshal(input); err == nil {
				tu.Input = raw
			}
			tu.Summary = claudeToolSummary(name, input)
		}
		uses = append(uses, tu)
	}
	return uses
}

// claudeToolSummary returns a one-line description of a tool call from its input.
func claudeToolSummary(name string, input map[string]any) string {
	switch name {
	case "Bash":
		return firstString(input, "command")
	case "Write", "Edit", "MultiEdit", "NotebookEdit", "Read":
		return firstString(input, "file_path", "notebook_path", "path")
	case "WebFetch":
		return firstString(input, "url")
	case "WebSearch":
		return firstString(input, "query")
	case "Grep":
		return firstString(input, "pattern")
	case "Glob":
		return firstString(input, "pattern", "path")
	case "Task":
		return firstString(input, "description")
	default:
		return ""
	}
}

func claudeNativeAgentActivity(raw map[string]any) (*NativeAgentActivity, bool) {
	switch firstString(raw, "subtype") {
	case "task_started":
		if firstString(raw, "task_type", "taskType") != "local_agent" {
			return nil, false
		}
		agentType := firstString(raw, "subagent_type", "subagentType", "agent_type", "agentType")
		if agentType == "" {
			return nil, false
		}
		return &NativeAgentActivity{
			Provider:          config.ProviderClaude,
			TaskID:            firstString(raw, "task_id", "taskId"),
			ToolUseID:         firstString(raw, "tool_use_id", "toolUseID"),
			ProviderAgentName: agentType,
			Description:       firstString(raw, "description"),
			Status:            "started",
		}, true
	case "task_notification":
		taskID := firstString(raw, "task_id", "taskId")
		if taskID == "" {
			return nil, false
		}
		status := strings.ToLower(firstString(raw, "status"))
		if status == "" {
			status = "completed"
		}
		switch status {
		case "completed", "failed", "cancelled", "canceled":
			return &NativeAgentActivity{
				Provider:  config.ProviderClaude,
				TaskID:    taskID,
				ToolUseID: firstString(raw, "tool_use_id", "toolUseID"),
				Status:    status,
			}, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func claudeUserInputRequest(raw map[string]any, source []byte) (*UserInputRequest, bool) {
	if req, ok := claudeUserInputRequestFromValue(raw, source); ok {
		return req, true
	}
	if text := extractText(raw); text != "" {
		return claudeUserInputRequestFromText(text, source)
	}
	return nil, false
}

func claudeUserInputRequestFromValue(value any, source []byte) (*UserInputRequest, bool) {
	switch v := value.(type) {
	case map[string]any:
		if questions, ok := v["questions"]; ok {
			return claudeUserInputRequestFromPayload(v, questions, source)
		}
		if input, ok := v["input"].(map[string]any); ok {
			if questions, ok := input["questions"]; ok {
				if _, hasID := input["id"]; !hasID {
					input = cloneStringAnyMap(input)
					if id := firstString(v, "id", "tool_use_id", "toolUseID", "item_id", "itemId"); id != "" {
						input["id"] = id
					}
				}
				return claudeUserInputRequestFromPayload(input, questions, source)
			}
		}
		if block, ok := v["content_block"].(map[string]any); ok {
			if req, ok := claudeUserInputRequestFromValue(block, source); ok {
				return req, true
			}
		}
		if nested, ok := v["event"].(map[string]any); ok {
			if req, ok := claudeUserInputRequestFromValue(nested, source); ok {
				return req, true
			}
		}
		if message, ok := v["message"].(map[string]any); ok {
			if req, ok := claudeUserInputRequestFromValue(message, source); ok {
				return req, true
			}
		}
		if content, ok := v["content"]; ok {
			if req, ok := claudeUserInputRequestFromValue(content, source); ok {
				return req, true
			}
		}
	case []any:
		for _, item := range v {
			if req, ok := claudeUserInputRequestFromValue(item, source); ok {
				return req, true
			}
		}
	case string:
		return claudeUserInputRequestFromText(v, source)
	}
	return nil, false
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func claudeUserInputRequestFromPayload(payload map[string]any, questions any, source []byte) (*UserInputRequest, bool) {
	rawQuestions, err := json.Marshal(questions)
	if err != nil {
		return nil, false
	}
	var parsed []UserInputQuestion
	if err := json.Unmarshal(rawQuestions, &parsed); err != nil || len(parsed) == 0 {
		return nil, false
	}
	NormalizeUserInputQuestions(parsed)
	itemID := firstString(payload, "id", "tool_use_id", "toolUseID", "item_id", "itemId")
	autoMS := int64FromAny(firstValue(payload, "autoResolutionMs", "auto_resolution_ms"))
	req := &UserInputRequest{
		ID:               userInputID("claude", source),
		TurnID:           firstString(payload, "session_id", "sessionId"),
		Provider:         config.ProviderClaude,
		ItemID:           itemID,
		Questions:        parsed,
		AutoResolutionMS: autoMS,
		EndsTurn:         true,
	}
	if itemID != "" {
		req.ID = "claude-" + sanitizeFilename(itemID)
	}
	return req, true
}

func claudeUserInputRequestFromText(text string, source []byte) (*UserInputRequest, bool) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "questions:") {
		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "questions:"))
		var questions []UserInputQuestion
		if err := json.Unmarshal([]byte(raw), &questions); err != nil || len(questions) == 0 {
			return nil, false
		}
		NormalizeUserInputQuestions(questions)
		return &UserInputRequest{
			ID:        userInputID("claude", source),
			Provider:  config.ProviderClaude,
			Questions: questions,
			EndsTurn:  true,
		}, true
	}
	if strings.HasPrefix(trimmed, "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return nil, false
		}
		if questions, ok := payload["questions"]; ok {
			return claudeUserInputRequestFromPayload(payload, questions, source)
		}
	}
	if strings.HasPrefix(trimmed, "[") {
		var questions []UserInputQuestion
		if err := json.Unmarshal([]byte(trimmed), &questions); err != nil || len(questions) == 0 {
			return nil, false
		}
		NormalizeUserInputQuestions(questions)
		return &UserInputRequest{
			ID:        userInputID("claude", source),
			Provider:  config.ProviderClaude,
			Questions: questions,
			EndsTurn:  true,
		}, true
	}
	return nil, false
}

func claudeRateLimited(raw map[string]any) bool {
	for _, key := range []string{"status", "status_code", "statusCode"} {
		switch v := raw[key].(type) {
		case float64:
			if int(v) == 429 {
				return true
			}
		case string:
			if v == "429" {
				return true
			}
		}
	}
	return claudeRateLimitedText(claudeErrorMessage(raw))
}

// claudeResultRateLimited reports whether a terminal `result` payload signals
// an account limit. The keyword heuristic is gated on error markers (api_error_status,
// is_error, or the CLI's literal usage-limit banner) so a successful assistant
// reply that merely talks about rate limits is not misclassified.
func claudeResultRateLimited(raw map[string]any) bool {
	if status, ok := raw["api_error_status"].(float64); ok && int(status) == 429 {
		return true
	}
	text := firstString(raw, "result", "content")
	if strings.HasPrefix(strings.ToLower(text), "claude ai usage limit reached") {
		return true
	}
	if isError, ok := raw["is_error"].(bool); ok && isError {
		return claudeRateLimitedText(text)
	}
	return false
}

func claudeRateLimitedText(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "rate limit") ||
		strings.Contains(message, "rate_limit") ||
		strings.Contains(message, "usage limit") ||
		strings.Contains(message, "usage_limit") ||
		strings.Contains(message, "too many requests") ||
		strings.Contains(message, "429")
}

func claudeErrorMessage(raw map[string]any) string {
	if message := firstString(raw, "message", "error", "reason"); message != "" {
		return message
	}
	if errObj, ok := raw["error"].(map[string]any); ok {
		if message := firstString(errObj, "message", "error", "reason", "type", "code"); message != "" {
			return message
		}
	}
	if nested, ok := raw["event"].(map[string]any); ok {
		return claudeErrorMessage(nested)
	}
	return ""
}

func extractText(raw map[string]any) string {
	if text := firstString(raw, "text", "content"); text != "" {
		return text
	}
	if delta, ok := raw["delta"].(map[string]any); ok {
		if text := firstString(delta, "text"); text != "" {
			return text
		}
	}
	if block, ok := raw["content_block"].(map[string]any); ok {
		if text := firstString(block, "text"); text != "" {
			return text
		}
	}
	if message, ok := raw["message"].(map[string]any); ok {
		if text := contentText(message["content"]); text != "" {
			return text
		}
	}
	return contentText(raw["content"])
}

func extractTextKind(raw map[string]any) (string, bool) {
	return extractText(raw), explicitReasoning(raw)
}

func explicitReasoning(raw map[string]any) bool {
	if isReasoningType(firstString(raw, "type", "event")) {
		return true
	}
	if delta, ok := raw["delta"].(map[string]any); ok && isReasoningType(firstString(delta, "type")) {
		return true
	}
	if block, ok := raw["content_block"].(map[string]any); ok && isReasoningType(firstString(block, "type")) {
		return true
	}
	return false
}

func extractReasoningText(raw map[string]any) string {
	var parts []string
	collectReasoningText(raw, &parts)
	return strings.Join(parts, "")
}

func collectReasoningText(value any, parts *[]string) {
	switch v := value.(type) {
	case map[string]any:
		if explicitReasoning(v) || isReasoningType(firstString(v, "type")) {
			if text := firstString(v, "text", "content"); text != "" {
				*parts = append(*parts, text)
			}
		}
		if delta, ok := v["delta"]; ok {
			collectReasoningText(delta, parts)
		}
		if block, ok := v["content_block"]; ok {
			collectReasoningText(block, parts)
		}
		if message, ok := v["message"].(map[string]any); ok {
			collectReasoningText(message["content"], parts)
		}
		if content, ok := v["content"]; ok {
			collectReasoningText(content, parts)
		}
		if event, ok := v["event"]; ok {
			collectReasoningText(event, parts)
		}
	case []any:
		for _, item := range v {
			collectReasoningText(item, parts)
		}
	}
}

func isReasoningType(raw string) bool {
	raw = strings.ToLower(raw)
	return strings.Contains(raw, "thinking") || strings.Contains(raw, "reasoning")
}

func contentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			switch block := item.(type) {
			case string:
				parts = append(parts, block)
			case map[string]any:
				if isReasoningType(firstString(block, "type")) {
					continue
				}
				if text := firstString(block, "text", "content"); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := raw[key].(string); ok {
			return v
		}
	}
	return ""
}

func firstValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := raw[key]; ok {
			return v
		}
	}
	return nil
}

func int64FromAny(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// claudeDefaultContextWindow is the context window shared by every current
// Claude model. Specific 1M-context model ids override it in claudeContextWindow.
const claudeDefaultContextWindow int64 = 200_000

// claudeContextWindow returns the model's context-window size in tokens. The
// Claude CLI never reports it, so the limit is looked up per model — a
// deterministic table that defaults to the shared 200k window.
func claudeContextWindow(model string) int64 {
	m := strings.ToLower(strings.TrimSpace(model))
	if m != "" {
		// 1M-context models (e.g. the Sonnet long-context tier) advertise the
		// larger window via a "[1m]" suffix on the model id.
		if strings.Contains(m, "[1m]") || strings.Contains(m, "-1m") {
			return 1_000_000
		}
	}
	return claudeDefaultContextWindow
}

// claudeContextEvent extracts a context-window usage event from a Claude
// stream-json line. The token usage lives at top-level `usage` (result events)
// or nested under `message.usage` (assistant/message events). Current context
// size is the last request's whole prompt: input + both cache-token classes.
// MaxTokens is left 0 here and stamped per-model in trackClaudeStream.
func claudeContextEvent(raw map[string]any) (Event, bool) {
	usage := claudeUsageMap(raw)
	if usage == nil {
		return Event{}, false
	}
	used := int64FromAny(firstValue(usage, "input_tokens", "inputTokens")) +
		int64FromAny(firstValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens")) +
		int64FromAny(firstValue(usage, "cache_read_input_tokens", "cacheReadInputTokens"))
	if used <= 0 {
		return Event{}, false
	}
	return Event{Kind: EventContextStatus, ContextStatus: &ContextStatus{UsedTokens: used}}, true
}

// claudeTurnUsageEvent extracts the billed-token breakdown for a completed turn.
// It is emitted only from the terminal `result` event, whose `usage` is the
// turn's cumulative total (input + output + both cache classes) — emitting from
// the per-message events instead would double-count across the tool loop.
func claudeTurnUsageEvent(raw map[string]any) (Event, bool) {
	usage := claudeUsageMap(raw)
	if usage == nil {
		return Event{}, false
	}
	tu := TurnUsage{
		Input:      int64FromAny(firstValue(usage, "input_tokens", "inputTokens")),
		Output:     int64FromAny(firstValue(usage, "output_tokens", "outputTokens")),
		CacheRead:  int64FromAny(firstValue(usage, "cache_read_input_tokens", "cacheReadInputTokens")),
		CacheWrite: int64FromAny(firstValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens")),
	}
	if tu.Total() <= 0 {
		return Event{}, false
	}
	return Event{Kind: EventTurnUsage, TurnUsage: &tu}, true
}

func claudeUsageMap(raw map[string]any) map[string]any {
	if usage, ok := raw["usage"].(map[string]any); ok {
		return usage
	}
	if message, ok := raw["message"].(map[string]any); ok {
		if usage, ok := message["usage"].(map[string]any); ok {
			return usage
		}
	}
	return nil
}

type stderrResult struct {
	text string
	err  error
}

func collectStderr(r io.Reader, limit int) stderrResult {
	tail := &limitedTail{limit: limit}
	_, err := io.Copy(tail, r)
	return stderrResult{text: strings.TrimSpace(tail.String()), err: err}
}

type limitedTail struct {
	limit int
	data  []byte
}

func (b *limitedTail) Write(p []byte) (int, error) {
	written := len(p)
	if b.limit <= 0 {
		return written, nil
	}
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return written, nil
	}
	b.data = append(b.data, p...)
	if overflow := len(b.data) - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:b.limit]
	}
	return written, nil
}

func (b *limitedTail) String() string {
	return string(b.data)
}

func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}

func nonEmptyTools(tools []string) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(name)
}

func sendAdapterEvent(ctx context.Context, out chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}
