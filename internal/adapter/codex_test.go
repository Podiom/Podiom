package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCodexParamsUseNativePermissionModes(t *testing.T) {
	approveStart := codexThreadStartParams(StartRequest{
		Model:          "gpt-5.5",
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   "/tmp/workspace",
	})
	if approveStart["approvalPolicy"] != "on-request" || approveStart["sandbox"] != "read-only" {
		t.Fatalf("bad approve thread params: %#v", approveStart)
	}
	approveTurn := codexTurnStartParams("thread-1", "hi", TurnSettings{
		Effort:         "high",
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   "/tmp/workspace",
	})
	policy, ok := approveTurn["sandboxPolicy"].(map[string]any)
	if !ok || policy["type"] != "readOnly" || policy["networkAccess"] != false {
		t.Fatalf("bad approve turn sandbox policy: %#v", approveTurn["sandboxPolicy"])
	}
	if approveTurn["effort"] != "high" {
		t.Fatalf("turn effort missing: %#v", approveTurn)
	}

	yoloStart := codexThreadStartParams(StartRequest{
		PermissionMode: config.PermissionYolo,
		WorkspaceDir:   "/tmp/workspace",
	})
	if yoloStart["approvalPolicy"] != "never" || yoloStart["sandbox"] != "danger-full-access" {
		t.Fatalf("bad yolo thread params: %#v", yoloStart)
	}
	yoloTurn := codexTurnStartParams("thread-1", "hi", TurnSettings{
		PermissionMode: config.PermissionYolo,
		WorkspaceDir:   "/tmp/workspace",
	})
	policy, ok = yoloTurn["sandboxPolicy"].(map[string]any)
	if !ok || policy["type"] != "dangerFullAccess" {
		t.Fatalf("bad yolo turn sandbox policy: %#v", yoloTurn["sandboxPolicy"])
	}

	allow := codexApprovalResponse("item/commandExecution/requestApproval", nil, PermissionDecision{Behavior: "allow"}).(map[string]any)
	if allow["decision"] != "accept" {
		t.Fatalf("allow decision did not map to accept: %#v", allow)
	}
	deny := codexApprovalResponse("item/commandExecution/requestApproval", nil, PermissionDecision{Behavior: "deny"}).(map[string]any)
	if deny["decision"] != "decline" {
		t.Fatalf("deny decision did not map to decline: %#v", deny)
	}
}

func TestParseCodexModelList(t *testing.T) {
	page, err := parseCodexModelList(json.RawMessage(`{
		"data": [{
			"id": "model-1",
			"model": "gpt-5.1",
			"displayName": "GPT-5.1",
			"description": "Full model",
			"hidden": false,
			"isDefault": true,
			"defaultReasoningEffort": "medium",
			"supportedReasoningEfforts": [
				{"reasoningEffort": "low", "description": "Fast"},
				{"reasoningEffort": "medium", "description": "Balanced"}
			]
		}],
		"nextCursor": "next"
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if page.nextCursor != "next" || len(page.models) != 1 {
		t.Fatalf("bad page: %+v", page)
	}
	model := page.models[0]
	if model.Model != "gpt-5.1" || model.DisplayName != "GPT-5.1" || !model.IsDefault {
		t.Fatalf("bad model: %+v", model)
	}
	if len(model.SupportedEfforts) != 2 || model.SupportedEfforts[1].Effort != "medium" {
		t.Fatalf("bad efforts: %+v", model.SupportedEfforts)
	}
}

func TestCodexReplayMessageIncludesHistoryAndLiveTurn(t *testing.T) {
	got := codexReplayMessage([]store.Message{
		{Role: store.RoleUser, Content: "remember alpha"},
		{Role: store.RoleAssistant, Content: "alpha remembered"},
	}, "continue")
	for _, want := range []string{"<podiom_history>", "user: remember alpha", "assistant: alpha remembered", "Live user turn:\ncontinue"} {
		if !strings.Contains(got, want) {
			t.Fatalf("replay message missing %q:\n%s", want, got)
		}
	}
}

func TestCodexFileChangeApprovalUsesPatchSummary(t *testing.T) {
	client := newCodexClient("codex", "", "", "", "", slogDiscard())
	params := json.RawMessage(`{
		"threadId": "thread-1",
		"turnId": "turn-1",
		"itemId": "call-1",
		"changes": [
			{"path": "web/src/pages/Chat.svelte", "kind": {"type": "update"}, "diff": "@@"},
			{"path": "web/src/lib/plan.ts", "kind": {"type": "add"}, "diff": "@@"}
		]
	}`)
	client.recordFileChangePatch(params)

	req := client.codexPermissionRequest(
		"item/fileChange/requestApproval",
		json.RawMessage("7"),
		json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"call-1","startedAtMs":1,"reason":null,"grantRoot":null}`),
		codexActiveTurn{podiomTurnID: "podiom-turn"},
	)
	if req.Description != "Approve file changes: update web/src/pages/Chat.svelte; add web/src/lib/plan.ts" {
		t.Fatalf("description = %q", req.Description)
	}
	if req.TurnID != "podiom-turn" || req.ToolUseID != "call-1" {
		t.Fatalf("bad request metadata: %+v", req)
	}
}

func TestCodexFileChangeApprovalFallbackIsReadable(t *testing.T) {
	client := newCodexClient("codex", "", "", "", "", slogDiscard())
	req := client.codexPermissionRequest(
		"item/fileChange/requestApproval",
		json.RawMessage("8"),
		json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"call-2","startedAtMs":1,"reason":null,"grantRoot":null}`),
		codexActiveTurn{},
	)
	if req.Description != "Approve file changes from Codex item call-2" {
		t.Fatalf("description = %q", req.Description)
	}
}

func TestCodexRateStatusAndLimitParsing(t *testing.T) {
	status, ok := codexRateStatus(json.RawMessage(`{"rate_limits":{"primary":{"used_percent":82.5,"window_minutes":300,"resets_in_seconds":3600},"secondary":{"used_percent":20,"window_minutes":10080}}}`))
	if !ok || status.UsedPercent != 82.5 {
		t.Fatalf("bad rate status: %+v ok=%v", status, ok)
	}
	if len(status.Windows) != 2 {
		t.Fatalf("expected 2 structured windows, got %+v", status.Windows)
	}
	if status.Windows[0].Key != "primary" || status.Windows[0].UsedPercent != 82.5 {
		t.Errorf("primary window = %+v", status.Windows[0])
	}
	if status.Windows[0].WindowSeconds != 300*60 {
		t.Errorf("primary window seconds = %d", status.Windows[0].WindowSeconds)
	}
	if status.Windows[0].ResetsAt.IsZero() {
		t.Errorf("primary resets_at should be derived from resets_in_seconds")
	}
	if status.Windows[1].Key != "secondary" || status.Windows[1].WindowSeconds != 10080*60 {
		t.Errorf("secondary window = %+v", status.Windows[1])
	}
	if !codexRateLimited(json.RawMessage(`{"error":{"message":"usage_limit_exceeded"}}`)) {
		t.Fatal("expected usage limit to be detected")
	}
}

func TestCodexStreamsTurnAndRelaysApproval(t *testing.T) {
	t.Setenv("PODIOM_CODEX_FAKE_MODE", "approval")
	codex := newTestCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}

	handle, err := codex.Start(ctx, StartRequest{
		SessionID:      "session-1",
		Provider:       config.ProviderCodex,
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   workspace,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	relay := &recordingRelay{behavior: "allow", requests: make(chan PermissionRequest, 1), timeouts: make(chan time.Duration, 1)}
	events, err := codex.SendTurn(ctx, TurnRequest{
		SessionID: "session-1",
		Handle:    handle,
		Message:   "run a command",
		Settings: TurnSettings{
			PermissionMode:    config.PermissionApprove,
			WorkspaceDir:      workspace,
			PermissionTurnID:  "podiom-turn-1",
			PermissionTimeout: 5 * time.Minute,
		},
		Relay: relay,
	})
	if err != nil {
		t.Fatalf("send turn: %v", err)
	}

	text := collectCodexText(t, events)
	if text != "approved" {
		t.Fatalf("unexpected assistant text %q", text)
	}
	req := <-relay.requests
	if req.TurnID != "podiom-turn-1" || req.ToolName != "codex.command" || req.ToolUseID != "item-1" {
		t.Fatalf("bad permission request: %+v", req)
	}
	if req.Description != "Run echo ok" {
		t.Fatalf("bad permission request description %q", req.Description)
	}
	if timeout := <-relay.timeouts; timeout != 5*time.Minute {
		t.Fatalf("permission timeout = %v, want 5m", timeout)
	}
}

func TestCodexStreamsTurnAndRelaysUserInput(t *testing.T) {
	t.Setenv("PODIOM_CODEX_FAKE_MODE", "user_input")
	codex := newTestCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}

	handle, err := codex.Start(ctx, StartRequest{
		SessionID:      "session-1",
		Provider:       config.ProviderCodex,
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   workspace,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	input := &recordingInputRelay{
		answers:  map[string][]string{"intent": []string{"Draft a testing roadmap"}},
		requests: make(chan UserInputRequest, 1),
	}
	events, err := codex.SendTurn(ctx, TurnRequest{
		SessionID: "session-1",
		Handle:    handle,
		Message:   "testing roadmap",
		Settings: TurnSettings{
			PermissionMode:   config.PermissionApprove,
			WorkspaceDir:     workspace,
			PermissionTurnID: "podiom-turn-1",
		},
		Input: input,
	})
	if err != nil {
		t.Fatalf("send turn: %v", err)
	}

	text := collectCodexText(t, events)
	if text != "Draft a testing roadmap" {
		t.Fatalf("unexpected assistant text %q", text)
	}
	req := <-input.requests
	if req.TurnID != "podiom-turn-1" || req.Provider != config.ProviderCodex || req.ItemID != "item-question" {
		t.Fatalf("bad input request: %+v", req)
	}
	if len(req.Questions) != 1 || req.Questions[0].ID != "intent" || req.Questions[0].MultiSelect {
		t.Fatalf("bad input question: %+v", req.Questions)
	}
}

func TestCodexResumesThreadAfterAppServerRestart(t *testing.T) {
	t.Setenv("PODIOM_CODEX_FAKE_MODE", "normal")
	codex := newTestCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}

	handle, err := codex.Start(ctx, StartRequest{
		SessionID:      "session-1",
		Provider:       config.ProviderCodex,
		PermissionMode: config.PermissionYolo,
		WorkspaceDir:   workspace,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	codex.client("", "", "", "").reset()

	events, err := codex.SendTurn(ctx, TurnRequest{
		SessionID: "session-1",
		Handle:    handle,
		Message:   "hello after restart",
		Settings: TurnSettings{
			PermissionMode: config.PermissionYolo,
			WorkspaceDir:   workspace,
		},
	})
	if err != nil {
		t.Fatalf("send turn after restart: %v", err)
	}
	if text := collectCodexText(t, events); text != "resumed" {
		t.Fatalf("unexpected assistant text after restart %q", text)
	}
}

func TestCodexAppServerLaunchUsesRootProfile(t *testing.T) {
	t.Setenv("PODIOM_CODEX_FAKE_MODE", "normal")
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("PODIOM_CODEX_ARGV_FILE", argvFile)
	codex := newTestCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspace := t.TempDir()
	profileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}
	if _, err := codex.Start(ctx, StartRequest{
		SessionID:      "session-profile",
		AgentName:      "atlas",
		Provider:       config.ProviderCodex,
		ProfileDir:     profileDir,
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   workspace,
		MCPServers: []podiommcp.Server{{
			Name:      "filesystem",
			Transport: podiommcp.TransportStdio,
			Command:   "npx",
			Args:      []string{"-y", "@modelcontextprotocol/server-filesystem"},
		}},
		MCPAllServers: []podiommcp.Server{{
			Name:      "filesystem",
			Transport: podiommcp.TransportStdio,
			Command:   "npx",
		}},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	text := string(got)
	for _, want := range []string{"app-server", "-c", "mcp_servers.filesystem.command=\"npx\"", "--listen", "stdio://"} {
		if !strings.Contains(text, want) {
			t.Fatalf("argv missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "--profile") {
		t.Fatalf("app-server argv should use config overrides, not --profile: %s", text)
	}
}

func TestCodexAppServerStartupErrorIncludesStderr(t *testing.T) {
	t.Setenv("PODIOM_CODEX_FAKE_MODE", "stderr_exit")
	codex := newTestCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := codex.Start(ctx, StartRequest{
		SessionID:      "session-stderr",
		AgentName:      "atlas",
		Provider:       config.ProviderCodex,
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected startup error")
	}
	if !strings.Contains(err.Error(), "invalid type: boolean") {
		t.Fatalf("startup error should include stderr tail, got: %v", err)
	}
}

func TestCodexHelperProcess(t *testing.T) {
	if os.Getenv("PODIOM_CODEX_HELPER") != "1" {
		return
	}
	if path := os.Getenv("PODIOM_CODEX_ARGV_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(os.Args, "\n")), 0o600)
	}
	runFakeCodexAppServer()
	os.Exit(0)
}

type recordingRelay struct {
	behavior string
	requests chan PermissionRequest
	timeouts chan time.Duration
}

func (r *recordingRelay) RequestPermission(ctx context.Context, req PermissionRequest, timeout time.Duration) (PermissionDecision, error) {
	select {
	case r.requests <- req:
	case <-ctx.Done():
		return PermissionDecision{Behavior: "deny"}, ctx.Err()
	}
	if r.timeouts != nil {
		select {
		case r.timeouts <- timeout:
		default:
		}
	}
	return PermissionDecision{Behavior: r.behavior}, nil
}

type recordingInputRelay struct {
	answers  map[string][]string
	requests chan UserInputRequest
}

func (r *recordingInputRelay) RequestUserInput(ctx context.Context, req UserInputRequest, timeout time.Duration) (UserInputDecision, error) {
	select {
	case r.requests <- req:
	case <-ctx.Done():
		return UserInputDecision{}, ctx.Err()
	}
	return UserInputDecision{Answers: r.answers}, nil
}

func newTestCodex(t *testing.T) *Codex {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nexec env PODIOM_CODEX_HELPER=1 " + strconv.Quote(os.Args[0]) + " -test.run=TestCodexHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write codex wrapper: %v", err)
	}
	t.Setenv("CODEX_BIN", wrapper)
	codex, err := NewCodex(CodexOptions{PermissionTimeout: time.Second})
	if err != nil {
		t.Fatalf("new codex: %v", err)
	}
	return codex
}

func collectCodexText(t *testing.T, events <-chan Event) string {
	t.Helper()
	var text strings.Builder
	for event := range events {
		switch event.Kind {
		case EventAssistantDelta:
			text.WriteString(event.Content)
		case EventAssistantMessage:
			text.Reset()
			text.WriteString(event.Content)
		}
	}
	return text.String()
}

func runFakeCodexAppServer() {
	if os.Getenv("PODIOM_CODEX_FAKE_MODE") == "stderr_exit" {
		fmt.Fprintln(os.Stderr, "Error: invalid type: boolean `false`, expected a string")
		time.Sleep(50 * time.Millisecond)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	loaded := map[string]bool{}
	threadID := "thread-1"
	nextTurn := 0
	var pendingApproval struct {
		threadID string
		turnID   string
		active   bool
	}
	var pendingInput struct {
		threadID string
		turnID   string
		active   bool
	}
	for scanner.Scan() {
		var msg codexRPCMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if len(msg.ID) > 0 && msg.Method == "" {
			var resp struct {
				Result struct {
					Decision string `json:"decision"`
				} `json:"result"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &resp)
			if pendingApproval.active {
				final := "denied"
				if resp.Result.Decision == "accept" {
					final = "approved"
				}
				writeFakeDelta(enc, pendingApproval.threadID, pendingApproval.turnID, final)
				writeFakeCompleted(enc, pendingApproval.threadID, pendingApproval.turnID, final)
				pendingApproval.active = false
			}
			if pendingInput.active {
				var inputResp struct {
					Result struct {
						Answers map[string]struct {
							Answers []string `json:"answers"`
						} `json:"answers"`
					} `json:"result"`
				}
				_ = json.Unmarshal(scanner.Bytes(), &inputResp)
				final := strings.Join(inputResp.Result.Answers["intent"].Answers, ", ")
				if final == "" {
					final = "empty"
				}
				writeFakeDelta(enc, pendingInput.threadID, pendingInput.turnID, final)
				writeFakeCompleted(enc, pendingInput.threadID, pendingInput.turnID, final)
				pendingInput.active = false
			}
			continue
		}
		switch msg.Method {
		case "initialize":
			writeFakeResponse(enc, msg.ID, map[string]any{
				"userAgent":      "fake-codex",
				"codexHome":      "/tmp/fake-codex-home",
				"platformFamily": "unix",
				"platformOs":     "test",
			})
		case "initialized":
		case "thread/start":
			var params struct {
				CWD string `json:"cwd"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			loaded[threadID] = true
			writeFakeResponse(enc, msg.ID, fakeThreadResponse(threadID, params.CWD))
		case "thread/resume":
			var params struct {
				ThreadID string `json:"threadId"`
				CWD      string `json:"cwd"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if params.ThreadID == "" {
				params.ThreadID = threadID
			}
			loaded[params.ThreadID] = true
			writeFakeResponse(enc, msg.ID, fakeThreadResponse(params.ThreadID, params.CWD))
		case "turn/start":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if !loaded[params.ThreadID] {
				writeFakeError(enc, msg.ID, "thread not loaded")
				continue
			}
			nextTurn++
			turnID := fmt.Sprintf("turn-%d", nextTurn)
			writeFakeResponse(enc, msg.ID, map[string]any{"turn": map[string]any{"id": turnID}})
			if os.Getenv("PODIOM_CODEX_FAKE_MODE") == "approval" {
				pendingApproval = struct {
					threadID string
					turnID   string
					active   bool
				}{threadID: params.ThreadID, turnID: turnID, active: true}
				writeFakeRequest(enc, json.RawMessage("100"), "item/commandExecution/requestApproval", map[string]any{
					"threadId":    params.ThreadID,
					"turnId":      turnID,
					"itemId":      "item-1",
					"startedAtMs": time.Now().UnixMilli(),
					"description": "Run echo ok",
					"command":     "echo ok",
					"cwd":         "/tmp",
				})
			} else if os.Getenv("PODIOM_CODEX_FAKE_MODE") == "user_input" {
				pendingInput = struct {
					threadID string
					turnID   string
					active   bool
				}{threadID: params.ThreadID, turnID: turnID, active: true}
				writeFakeRequest(enc, json.RawMessage("101"), "item/tool/requestUserInput", map[string]any{
					"threadId":         params.ThreadID,
					"turnId":           turnID,
					"itemId":           "item-question",
					"autoResolutionMs": 60000,
					"questions": []map[string]any{{
						"id":          "intent",
						"header":      "Intent",
						"question":    "What do you want from \"testing roadmap\"?",
						"multiSelect": false,
						"options": []map[string]any{{
							"label":       "Draft a testing roadmap",
							"description": "Create a phased testing plan.",
						}},
					}},
				})
			} else {
				writeFakeDelta(enc, params.ThreadID, turnID, "res")
				writeFakeDelta(enc, params.ThreadID, turnID, "umed")
				writeFakeCompleted(enc, params.ThreadID, turnID, "resumed")
			}
		case "thread/unsubscribe":
			writeFakeResponse(enc, msg.ID, map[string]any{})
		}
	}
}

func fakeThreadResponse(threadID, cwd string) map[string]any {
	return map[string]any{
		"thread": map[string]any{
			"id":  threadID,
			"cwd": cwd,
		},
		"instructionSources": []string{filepath.Join(cwd, "AGENTS.md")},
	}
}

func writeFakeResponse(enc *json.Encoder, id json.RawMessage, result any) {
	_ = enc.Encode(map[string]any{"id": id, "result": result})
}

func writeFakeError(enc *json.Encoder, id json.RawMessage, message string) {
	_ = enc.Encode(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    -32000,
			"message": message,
		},
	})
}

func writeFakeRequest(enc *json.Encoder, id json.RawMessage, method string, params any) {
	_ = enc.Encode(map[string]any{"id": id, "method": method, "params": params})
}

func writeFakeDelta(enc *json.Encoder, threadID, turnID, delta string) {
	_ = enc.Encode(map[string]any{
		"method": "item/agentMessage/delta",
		"params": map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
			"itemId":   "assistant-1",
			"delta":    delta,
		},
	})
}

func writeFakeCompleted(enc *json.Encoder, threadID, turnID, text string) {
	_ = enc.Encode(map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": threadID,
			"turn": map[string]any{
				"id": turnID,
				"items": []map[string]any{{
					"type":  "agentMessage",
					"id":    "assistant-1",
					"text":  text,
					"phase": "final_answer",
				}},
			},
		},
	})
}
