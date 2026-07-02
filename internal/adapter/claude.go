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
	"strings"
	"time"

	"github.com/mar-schmidt/Podium/internal/capabilities"
	"github.com/mar-schmidt/Podium/internal/config"
	podiumexec "github.com/mar-schmidt/Podium/internal/exec"
	podiumlog "github.com/mar-schmidt/Podium/internal/logging"
	podiummcp "github.com/mar-schmidt/Podium/internal/mcp"
	"github.com/mar-schmidt/Podium/internal/store"
)

const defaultPermissionTimeout = 3 * time.Minute
const claudeStderrTailLimit = 16 * 1024

// ClaudeOptions configures the Claude Code adapter.
type ClaudeOptions struct {
	Discovery         podiumexec.Discovery
	DaemonAddr        string
	PermissionTimeout time.Duration
	MCPCommand        string
	Logger            *slog.Logger
}

// Claude drives Claude Code as a per-turn process.
type Claude struct {
	bin               string
	daemonAddr        string
	permissionTimeout time.Duration
	mcpCommand        string
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
		permissionTimeout: timeout,
		mcpCommand:        mcpCommand,
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
	args, cleanup, err := c.args(req)
	if err != nil {
		c.providerLog(req).Warn("provider turn setup failed", "stage", "args", "error", err)
		return nil, err
	}
	cmd := podiumexec.Command(ctx, c.bin, args...)
	cmd.Dir = req.Settings.WorkspaceDir
	cmd.Env = c.env(req.Settings.ProfileDir)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process pipe failed", "stage", "stdin", "error", err)
		return nil, fmt.Errorf("claude stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process pipe failed", "stage", "stdout", "error", err)
		return nil, fmt.Errorf("claude stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process pipe failed", "stage", "stderr", "error", err)
		return nil, fmt.Errorf("claude stderr: %w", err)
	}
	startedAt := time.Now()
	c.providerLog(req).Info("provider process starting", "event", "provider", "stage", "start", "command", c.bin, "resuming", req.Handle.ID != "", "permission", string(req.Settings.PermissionMode), "mcp_servers", len(req.Settings.MCPServers), "extra_workspaces", len(req.Settings.ExtraWorkspaceDirs))
	if err := cmd.Start(); err != nil {
		cleanup()
		c.providerLog(req).Warn("provider process start failed", "stage", "start", "error", err)
		return nil, fmt.Errorf("start claude: %w", err)
	}

	if err := writeClaudeInput(stdin, req.Message, req.History, req.Handle.ID != ""); err != nil {
		_ = podiumexec.Kill(cmd)
		cleanup()
		c.providerLog(req).Warn("provider stdin write failed", "stage", "write_input", "error", err)
		return nil, err
	}
	c.providerLog(req).Info("provider input written", "event", "provider", "stage", "write_input", "history_messages", len(req.History), "resuming", req.Handle.ID != "")

	out := make(chan Event, 32)
	go func() {
		defer cleanup()
		defer close(out)
		parsec := make(chan error, 1)
		trackc := make(chan claudeStreamTrack, 1)
		stderrc := make(chan stderrResult, 1)
		parsed := make(chan Event, 32)
		go func() {
			parsec <- parseClaudeStream(ctx, stdout, parsed)
			close(parsed)
		}()
		go c.trackClaudeStream(ctx, req, parsed, out, trackc)
		go func() { stderrc <- collectStderr(stderr, claudeStderrTailLimit) }()
		waitErr := cmd.Wait()
		parseErr := <-parsec
		track := <-trackc
		stderrResult := <-stderrc
		if ctx.Err() != nil {
			c.providerLog(req).Info("provider process canceled", "event", "provider", "stage", "wait", podiumlog.DurationMS("duration_ms", time.Since(startedAt)))
			return
		}
		if parseErr != nil {
			c.providerLog(req).Warn("provider stream parse failed", "stage", "parse_stdout", "error", podiumlog.Redact(parseErr.Error()))
			sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: fmt.Sprintf("claude stream error: %v", parseErr)})
			return
		}
		if stderrResult.err != nil {
			c.providerLog(req).Warn("provider stderr read failed", "stage", "read_stderr", "error", stderrResult.err, "stderr_tail", podiumlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
			sendAdapterEvent(ctx, out, Event{Kind: EventAssistantMessage, Content: fmt.Sprintf("claude stderr error: %v", stderrResult.err)})
			return
		}
		if waitErr != nil {
			if event, send := claudeWaitEvent(waitErr, stderrResult.text, track); send && event.Kind == EventRateLimited {
				c.providerLog(req).Warn("provider rate limited", "stage", "wait", "rate_limited", true, "stderr_tail", podiumlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
				sendAdapterEvent(ctx, out, event)
				return
			} else if !send {
				c.providerLog(req).Warn("provider process exited after provider message", "event", "provider", "stage", "wait", "exit_error", waitErr, "provider_message", podiumlog.RedactTail(track.lastMessage, 4096), "stderr_tail", podiumlog.RedactTail(stderrResult.text, claudeStderrTailLimit), podiumlog.DurationMS("duration_ms", time.Since(startedAt)))
				return
			} else {
				c.providerLog(req).Warn("provider process exited with error", "stage", "wait", "exit_error", waitErr, "stderr_tail", podiumlog.RedactTail(stderrResult.text, claudeStderrTailLimit))
				sendAdapterEvent(ctx, out, event)
				return
			}
		}
		c.providerLog(req).Info("provider process finished", "event", "provider", "stage", "wait", "status", "success", podiumlog.DurationMS("duration_ms", time.Since(startedAt)))
		sendAdapterEvent(ctx, out, Event{Kind: EventTurnDone})
	}()
	return out, nil
}

type claudeStreamTrack struct {
	lastMessage string
}

func (c *Claude) trackClaudeStream(ctx context.Context, req TurnRequest, in <-chan Event, out chan<- Event, done chan<- claudeStreamTrack) {
	var track claudeStreamTrack
	for event := range in {
		if event.Kind == EventAssistantMessage && strings.TrimSpace(event.Content) != "" {
			track.lastMessage = event.Content
		}
		if event.Kind == EventHandleUpdated && event.Handle != nil {
			c.providerLog(req).Info("provider handle updated", "event", "provider", "stage", "stream", "provider_handle_set", event.Handle.ID != "")
		}
		if !sendAdapterEvent(ctx, out, event) {
			break
		}
	}
	done <- track
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
// them in help output, plus Podium's bundled model-alias fallback registry.
func (c *Claude) Capabilities(ctx context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	caps := capabilities.Fallback(config.ProviderClaude, req.Profile)
	caps.Source = "claude-help+fallback"
	helpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := podiumexec.Command(helpCtx, c.bin, "--help")
	cmd.Env = c.env(req.ProfileDir)
	raw, err := cmd.CombinedOutput()
	if helpCtx.Err() != nil {
		err = helpCtx.Err()
	}
	if err != nil {
		return capabilities.WithError(caps, fmt.Errorf("read claude help: %w", err)), nil
	}
	if efforts := capabilities.ParseEffortsFromHelp(string(raw)); len(efforts) > 0 {
		caps = capabilities.MergeEfforts(caps, efforts)
	}
	caps.Stale = false
	return caps, nil
}

func (c *Claude) args(req TurnRequest) ([]string, func(), error) {
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
	if req.Settings.Model != "" {
		args = append(args, "--model", req.Settings.Model)
	}
	if req.Settings.Effort != "" {
		args = append(args, "--effort", req.Settings.Effort)
	}
	if req.Handle.ID != "" {
		args = append(args, "--resume", req.Handle.ID)
	}
	cleanup := func() {}
	needsPermissionMCP := false
	switch req.Settings.PermissionMode {
	case config.PermissionYolo:
		args = append(args, "--permission-mode", "bypassPermissions")
	default:
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
		return nil, cleanup, errors.New("claude approve mode needs daemon address and MCP command")
	}
	configPath, err := c.writeMCPConfig(req)
	if err != nil {
		return nil, cleanup, err
	}
	cleanup = func() { _ = os.Remove(configPath) }
	args = append(args, "--mcp-config", configPath, "--strict-mcp-config")
	if needsPermissionMCP {
		args = append(args, "--permission-prompt-tool", "mcp__podium_permission__prompt")
	}
	return args, cleanup, nil
}

func (c *Claude) writeMCPConfig(req TurnRequest) (string, error) {
	if err := os.MkdirAll(filepath.Join(req.Settings.WorkspaceDir, ".podium"), 0o755); err != nil {
		return "", fmt.Errorf("create claude mcp dir: %w", err)
	}
	turnID := req.Settings.PermissionTurnID
	if turnID == "" {
		turnID = req.SessionID
	}
	var permission map[string]any
	if req.Settings.PermissionMode != config.PermissionYolo && !req.Settings.Unattended {
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
	}
	payload := podiummcp.ClaudeConfig(req.Settings.MCPServers, permission)
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(req.Settings.WorkspaceDir, ".podium", fmt.Sprintf("claude-mcp-%s.json", sanitizeFilename(turnID)))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write claude mcp config: %w", err)
	}
	return path, nil
}

func (c *Claude) env(profileDir string) []string {
	env := os.Environ()
	if profileDir == "" {
		return unsetEnv(env, "CLAUDE_CONFIG_DIR")
	}
	return append(unsetEnv(env, "CLAUDE_CONFIG_DIR"), "CLAUDE_CONFIG_DIR="+profileDir)
}

func writeClaudeInput(stdin io.WriteCloser, message string, history []store.Message, resumed bool) error {
	defer stdin.Close()
	enc := json.NewEncoder(stdin)
	if !resumed {
		for _, msg := range history {
			if msg.Content == "" {
				continue
			}
			if err := enc.Encode(claudeInputMessage(string(msg.Role), msg.Content)); err != nil {
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

func parseClaudeStream(ctx context.Context, r io.Reader, out chan<- Event) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		events, err := parseClaudeLine(line)
		if err != nil {
			return err
		}
		for _, event := range events {
			if !sendAdapterEvent(ctx, out, event) {
				return ctx.Err()
			}
		}
	}
	return scanner.Err()
}

func parseClaudeLine(line []byte) ([]Event, error) {
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
			if req, ok := claudeUserInputRequest(nested, line); ok {
				events = append(events, Event{Kind: EventUserInputRequest, UserInputRequest: req})
			} else if text := extractText(nested); text != "" {
				events = append(events, Event{Kind: EventAssistantDelta, Content: text})
			}
		}
	case "assistant_delta", "text_delta", "content_block_delta":
		if text := extractText(raw); text != "" {
			events = append(events, Event{Kind: EventAssistantDelta, Content: text})
		}
	case "assistant", "message":
		if req, ok := claudeUserInputRequest(raw, line); ok {
			events = append(events, Event{Kind: EventUserInputRequest, UserInputRequest: req})
		} else if text := extractText(raw); text != "" {
			events = append(events, Event{Kind: EventAssistantMessage, Content: text})
		}
	case "result":
		if text := firstString(raw, "result", "content"); text != "" {
			if req, ok := claudeUserInputRequestFromText(text, line); ok {
				events = append(events, Event{Kind: EventUserInputRequest, UserInputRequest: req})
			} else {
				events = append(events, Event{Kind: EventAssistantMessage, Content: text})
			}
		}
	case "api_retry":
		if claudeRateLimited(raw) {
			events = append(events, Event{Kind: EventRateLimited, Content: "claude rate limited"})
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
	normalizeUserInputQuestions(parsed)
	itemID := firstString(payload, "id", "tool_use_id", "toolUseID", "item_id", "itemId")
	autoMS := int64FromAny(firstValue(payload, "autoResolutionMs", "auto_resolution_ms"))
	req := &UserInputRequest{
		ID:               userInputID("claude", source),
		TurnID:           firstString(payload, "session_id", "sessionId"),
		Provider:         config.ProviderClaude,
		ItemID:           itemID,
		Questions:        parsed,
		AutoResolutionMS: autoMS,
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
		normalizeUserInputQuestions(questions)
		return &UserInputRequest{
			ID:        userInputID("claude", source),
			Provider:  config.ProviderClaude,
			Questions: questions,
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
		normalizeUserInputQuestions(questions)
		return &UserInputRequest{
			ID:        userInputID("claude", source),
			Provider:  config.ProviderClaude,
			Questions: questions,
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
