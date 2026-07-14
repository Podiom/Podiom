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

func TestCodexParamsCarryDeveloperInstructions(t *testing.T) {
	start := codexThreadStartParams(StartRequest{
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   "/tmp/workspace",
		Instructions:   []byte("podiom generated instructions"),
	})
	if start["developerInstructions"] != "podiom generated instructions" {
		t.Fatalf("start developerInstructions = %#v", start["developerInstructions"])
	}
	withoutInstructions := codexThreadStartParams(StartRequest{WorkspaceDir: "/tmp/workspace"})
	if _, ok := withoutInstructions["developerInstructions"]; ok {
		t.Fatalf("empty instructions should be omitted: %#v", withoutInstructions)
	}
	resume := codexThreadResumeParams("thread-1", TurnSettings{
		WorkspaceDir:   "/tmp/workspace",
		Instructions:   []byte("fresh instructions"),
		PermissionMode: config.PermissionApprove,
	})
	if resume["developerInstructions"] != "fresh instructions" {
		t.Fatalf("resume developerInstructions = %#v", resume["developerInstructions"])
	}
}

func TestCodexLoadedThreadTracksInstructionHash(t *testing.T) {
	client := newCodexClient("codex", "", "", "", "", slogDiscard())
	first := instructionHash([]byte("first"))
	second := instructionHash([]byte("second"))
	client.markLoaded("thread-1", first)
	if !client.isLoaded("thread-1", first) {
		t.Fatal("thread should be loaded for matching instruction hash")
	}
	if client.isLoaded("thread-1", second) {
		t.Fatal("thread should not be treated as loaded after instruction hash changes")
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

func TestCodexReasoningPhaseClassification(t *testing.T) {
	text, reasoning := codexDelta(json.RawMessage(`{"delta":"work","phase":"analysis"}`))
	if text != "work" || !reasoning {
		t.Fatalf("phased delta = (%q, %v), want reasoning work", text, reasoning)
	}
	text, reasoning = codexDelta(json.RawMessage(`{"delta":"public"}`))
	if text != "public" || reasoning {
		t.Fatalf("unphased delta = (%q, %v), want visible assistant", text, reasoning)
	}

	completed := json.RawMessage(`{"turn":{"items":[
		{"type":"agentMessage","text":"private","phase":"analysis"},
		{"type":"agentMessage","text":"public","phase":"final_answer"}
	]}}`)
	if got := codexReasoningMessage(completed); got != "private" {
		t.Fatalf("reasoning message = %q", got)
	}
	if got := codexFinalMessage(completed); got != "public" {
		t.Fatalf("final message = %q", got)
	}
	if got := codexFinalMessage(json.RawMessage(`{"turn":{"items":[{"type":"agentMessage","text":"private","phase":"analysis"}]}}`)); got != "" {
		t.Fatalf("non-final item leaked into final message: %q", got)
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

func TestCodexToolUsesFromItems(t *testing.T) {
	client := newCodexClient("codex", "", "", "", "", slogDiscard())
	key := codexTurnKey{threadID: "thread-1", turnID: "turn-1"}

	// commandExecution is recorded when it starts, with the command as summary.
	cmd := client.codexToolUses("item/started", json.RawMessage(`{"item":{"type":"commandExecution","id":"c1","command":"go build ./..."}}`), key)
	if len(cmd) != 1 || cmd[0].Name != "commandExecution" || cmd[0].Summary != "go build ./..." {
		t.Fatalf("commandExecution tool use = %+v", cmd)
	}
	// The same item on completion must not double-record.
	if got := client.codexToolUses("item/completed", json.RawMessage(`{"item":{"type":"commandExecution","id":"c1","command":"go build ./..."}}`), key); len(got) != 0 {
		t.Fatalf("commandExecution should only emit on start, got %+v", got)
	}

	// fileChange is recorded on completion, using the tracked patch summary.
	client.recordFileChangePatch(json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"f1","changes":[{"path":"main.go","kind":{"type":"update"}}]}`))
	fc := client.codexToolUses("item/completed", json.RawMessage(`{"item":{"type":"fileChange","id":"f1"}}`), key)
	if len(fc) != 1 || fc[0].Name != "fileChange" || fc[0].Summary == "" {
		t.Fatalf("fileChange tool use = %+v", fc)
	}

	// Unknown item types produce nothing.
	if got := client.codexToolUses("item/started", json.RawMessage(`{"item":{"type":"reasoning","id":"r1"}}`), key); len(got) != 0 {
		t.Fatalf("unknown item should emit nothing, got %+v", got)
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

func TestCodexContextStatusParsing(t *testing.T) {
	// A token_count payload nests token usage and the window under "info".
	params := json.RawMessage(`{"info":{"total_token_usage":{"total_tokens":150000},"last_token_usage":{"input_tokens":80000,"cached_input_tokens":10000,"output_tokens":2000},"model_context_window":200000}}`)
	status, ok := codexContextStatus(params)
	if !ok {
		t.Fatal("expected context status to parse")
	}
	// Prefers last_token_usage (the tokens occupying the window after the turn).
	if status.UsedTokens != 92000 {
		t.Errorf("used tokens = %d, want 92000", status.UsedTokens)
	}
	if status.MaxTokens != 200000 {
		t.Errorf("max tokens = %d, want 200000", status.MaxTokens)
	}

	// Falls back to total_token_usage.total_tokens when last_token_usage is absent.
	fallback := json.RawMessage(`{"info":{"total_token_usage":{"total_tokens":120000},"model_context_window":272000}}`)
	status, ok = codexContextStatus(fallback)
	if !ok || status.UsedTokens != 120000 || status.MaxTokens != 272000 {
		t.Errorf("fallback status = %+v ok=%v", status, ok)
	}

	// No window reported → no context status (nothing to fill the ring against).
	if _, ok := codexContextStatus(json.RawMessage(`{"info":{"last_token_usage":{"total_tokens":10}}}`)); ok {
		t.Error("expected no context status without model_context_window")
	}
}

func TestCodexTurnUsageParsing(t *testing.T) {
	// last_token_usage is the per-turn breakdown: reasoning output folds into output,
	// cached input maps to cache-read, and Codex reports no cache-write class.
	params := json.RawMessage(`{"info":{"last_token_usage":{"input_tokens":80000,"cached_input_tokens":10000,"output_tokens":2000,"reasoning_output_tokens":500},"model_context_window":200000}}`)
	tu, ok := codexTurnUsage(params)
	if !ok {
		t.Fatal("expected turn usage to parse")
	}
	if tu.Input != 80000 || tu.CacheRead != 10000 || tu.Output != 2500 || tu.CacheWrite != 0 {
		t.Errorf("breakdown = %+v", tu)
	}
	if tu.Total() != 92500 {
		t.Errorf("total = %d, want 92500", tu.Total())
	}

	// No last_token_usage → no event (avoids counting turn/completed twice).
	if _, ok := codexTurnUsage(json.RawMessage(`{"info":{"total_token_usage":{"total_tokens":10},"model_context_window":200000}}`)); ok {
		t.Error("expected no turn usage without last_token_usage")
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

func TestCodexProfileIncludesBestEffortNativeAgents(t *testing.T) {
	profileDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "podiom_builder_12345678.toml")
	codex := &Codex{}
	name, _, content, nativeUsed, err := codex.ensureProfile(profileDir, "builder", nil, nil, []NativeAgent{{
		Name:         "podiom_builder_12345678",
		Description:  "Podiom agent builder",
		Instructions: "builder instructions",
		Model:        "gpt-5",
		Effort:       "high",
		ConfigPath:   configPath,
	}})
	if err != nil {
		t.Fatalf("ensure profile: %v", err)
	}
	if name != "podiom-builder" {
		t.Fatalf("profile name = %q, want podiom-builder", name)
	}
	if !nativeUsed {
		t.Fatalf("expected native agent projection to be used")
	}
	for _, want := range []string{
		"[agents.podiom_builder_12345678]",
		"config_file = " + strconv.Quote(configPath),
		"description = \"Podiom agent builder\"",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("profile missing %q:\n%s", want, content)
		}
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read native config: %v", err)
	}
	nativeConfig := string(raw)
	for _, want := range []string{
		"name = \"podiom_builder_12345678\"",
		"developer_instructions = \"builder instructions\"",
		"model = \"gpt-5\"",
		"model_reasoning_effort = \"high\"",
	} {
		if !strings.Contains(nativeConfig, want) {
			t.Fatalf("native config missing %q:\n%s", want, nativeConfig)
		}
	}
}

func TestCodexNativeAgentActivityParsingAndEnrichment(t *testing.T) {
	settings := TurnSettings{NativeAgents: []NativeAgent{{
		PodiomName: "Researcher",
		Name:       "podiom_researcher_12345678",
		ConfigPath: filepath.Join(t.TempDir(), "podiom_researcher_12345678.toml"),
	}}}
	track := codexNativeAgentTrack{nativeAgentTasks: map[string]NativeAgentActivity{}}

	activities := codexNativeAgentActivities("item/started", json.RawMessage(`{
		"threadId":"thread-1",
		"turnId":"turn-1",
		"item":{
			"type":"collabAgentToolCall",
			"id":"collab-1",
			"tool":"spawnAgent",
			"status":"inProgress",
			"receiverThreadIds":["agent-thread-1"],
			"prompt":"do not leak this prompt"
		}
	}`))
	if len(activities) != 1 {
		t.Fatalf("activities = %+v, want one spawn activity", activities)
	}
	start, ok := enrichCodexNativeAgentActivity(settings, &track, activities[0])
	if !ok {
		t.Fatal("spawn activity was dropped")
	}
	if start.Provider != config.ProviderCodex || start.TaskID != "agent-thread-1" || start.ToolUseID != "collab-1" || start.Status != "started" {
		t.Fatalf("bad spawn activity: %+v", start)
	}
	if start.Description != "" {
		t.Fatalf("spawn activity leaked prompt/description: %+v", start)
	}

	activities = codexNativeAgentActivities("item/started", json.RawMessage(`{
		"threadId":"thread-1",
		"turnId":"turn-1",
		"item":{
			"type":"subAgentActivity",
			"id":"subagent-1",
			"kind":"started",
			"agentThreadId":"agent-thread-1",
			"agentPath":"podiom_researcher_12345678"
		}
	}`))
	if len(activities) != 1 {
		t.Fatalf("subagent activities = %+v, want one", activities)
	}
	named, ok := enrichCodexNativeAgentActivity(settings, &track, activities[0])
	if !ok {
		t.Fatal("named activity was dropped")
	}
	if named.PodiomAgentName != "Researcher" || named.DisplayName != "Researcher" || named.ProviderAgentName != "podiom_researcher_12345678" {
		t.Fatalf("bad named activity: %+v", named)
	}

	activities = codexNativeAgentActivities("item/completed", json.RawMessage(`{
		"threadId":"thread-1",
		"turnId":"turn-1",
		"item":{
			"type":"collabAgentToolCall",
			"id":"collab-1",
			"tool":"spawnAgent",
			"status":"completed",
			"receiverThreadIds":["agent-thread-1"]
		}
	}`))
	done, ok := enrichCodexNativeAgentActivity(settings, &track, activities[0])
	if !ok {
		t.Fatal("completion activity was dropped")
	}
	if done.Status != "completed" || done.DisplayName != "Researcher" || done.ProviderAgentName != "podiom_researcher_12345678" {
		t.Fatalf("bad completion activity: %+v", done)
	}

	threadActivities := codexNativeAgentActivities("thread/started", json.RawMessage(`{
		"thread":{
			"id":"agent-thread-2",
			"parentThreadId":"thread-1",
			"agentRole":"researcher"
		}
	}`))
	if len(threadActivities) != 1 {
		t.Fatalf("thread activities = %+v, want one", threadActivities)
	}
	threadActivity, ok := enrichCodexNativeAgentActivity(settings, &track, threadActivities[0])
	if !ok {
		t.Fatal("thread activity was dropped")
	}
	if threadActivity.DisplayName != "Researcher" || threadActivity.TaskID != "agent-thread-2" {
		t.Fatalf("bad thread activity: %+v", threadActivity)
	}

	fallback := codexNativeAgentActivities("turn/completed", json.RawMessage(`{
		"threadId":"thread-1",
		"turn":{
			"id":"turn-2",
			"items":[{
				"type":"collabAgentToolCall",
				"id":"collab-2",
				"tool":"spawnAgent",
				"status":"completed",
				"receiverThreadIds":["agent-thread-3"],
				"prompt":"do not leak this fallback prompt"
			}]
		}
	}`))
	if len(fallback) != 1 {
		t.Fatalf("fallback activities = %+v, want one", fallback)
	}
	fallbackActivity, ok := enrichCodexNativeAgentActivity(settings, &track, fallback[0])
	if !ok {
		t.Fatal("fallback activity was dropped")
	}
	if fallbackActivity.Status != "completed" || fallbackActivity.TaskID != "agent-thread-3" || fallbackActivity.Description != "" {
		t.Fatalf("bad fallback activity: %+v", fallbackActivity)
	}
}

func TestCodexStreamsNativeAgentActivity(t *testing.T) {
	t.Setenv("PODIOM_CODEX_FAKE_MODE", "native_agent")
	codex := newTestCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}

	handle, err := codex.Start(ctx, StartRequest{
		SessionID:      "session-1",
		AgentName:      "Builder",
		Provider:       config.ProviderCodex,
		PermissionMode: config.PermissionApprove,
		WorkspaceDir:   workspace,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, err := codex.SendTurn(ctx, TurnRequest{
		SessionID: "session-1",
		Handle:    handle,
		Message:   "delegate please",
		Settings: TurnSettings{
			AgentName:      "Builder",
			PermissionMode: config.PermissionApprove,
			WorkspaceDir:   workspace,
			NativeAgents: []NativeAgent{{
				PodiomName: "Researcher",
				Name:       "podiom_researcher_12345678",
			}},
		},
	})
	if err != nil {
		t.Fatalf("send turn: %v", err)
	}

	var activities []NativeAgentActivity
	for _, event := range collectCodexEvents(t, events) {
		if event.Kind == EventNativeAgentActivity && event.NativeAgent != nil {
			activities = append(activities, *event.NativeAgent)
		}
	}
	if len(activities) != 3 {
		t.Fatalf("activities = %+v, want start, name update, completion", activities)
	}
	if got := activities[0]; got.Provider != config.ProviderCodex || got.TaskID != "agent-thread-1" || got.Status != "started" || got.Description != "" {
		t.Fatalf("bad first activity: %+v", got)
	}
	if got := activities[1]; got.DisplayName != "Researcher" || got.PodiomAgentName != "Researcher" || got.ProviderAgentName != "podiom_researcher_12345678" {
		t.Fatalf("bad named activity: %+v", got)
	}
	if got := activities[2]; got.Status != "completed" || got.DisplayName != "Researcher" {
		t.Fatalf("bad completion activity: %+v", got)
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

func collectCodexEvents(t *testing.T, events <-chan Event) []Event {
	t.Helper()
	var out []Event
	for event := range events {
		out = append(out, event)
	}
	return out
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
			} else if os.Getenv("PODIOM_CODEX_FAKE_MODE") == "native_agent" {
				writeFakeNativeAgentStarted(enc, params.ThreadID, turnID)
				writeFakeSubAgentActivity(enc, params.ThreadID, turnID, "started")
				writeFakeNativeAgentCompleted(enc, params.ThreadID, turnID)
				writeFakeCompleted(enc, params.ThreadID, turnID, "delegated")
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

func writeFakeNativeAgentStarted(enc *json.Encoder, threadID, turnID string) {
	_ = enc.Encode(map[string]any{
		"method": "item/started",
		"params": map[string]any{
			"threadId":    threadID,
			"turnId":      turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"type":              "collabAgentToolCall",
				"id":                "collab-1",
				"tool":              "spawnAgent",
				"status":            "inProgress",
				"senderThreadId":    threadID,
				"receiverThreadIds": []string{"agent-thread-1"},
				"prompt":            "do not leak this prompt",
			},
		},
	})
}

func writeFakeSubAgentActivity(enc *json.Encoder, threadID, turnID, kind string) {
	_ = enc.Encode(map[string]any{
		"method": "item/started",
		"params": map[string]any{
			"threadId":    threadID,
			"turnId":      turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"type":          "subAgentActivity",
				"id":            "subagent-1",
				"kind":          kind,
				"agentThreadId": "agent-thread-1",
				"agentPath":     "podiom_researcher_12345678",
			},
		},
	})
}

func writeFakeNativeAgentCompleted(enc *json.Encoder, threadID, turnID string) {
	_ = enc.Encode(map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId":      threadID,
			"turnId":        turnID,
			"completedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"type":              "collabAgentToolCall",
				"id":                "collab-1",
				"tool":              "spawnAgent",
				"status":            "completed",
				"senderThreadId":    threadID,
				"receiverThreadIds": []string{"agent-thread-1"},
			},
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
