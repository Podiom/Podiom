package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
)

func TestClaudeArgsApproveWritesPermissionMCPConfig(t *testing.T) {
	workspace := t.TempDir()
	c := &Claude{
		daemonAddr:        "127.0.0.1:8787",
		podiomHome:        "/data/podiom",
		permissionTimeout: time.Minute,
		mcpCommand:        "/tmp/podiomd",
	}

	args, cleanup, _, err := c.args(TurnRequest{
		SessionID: "session-1",
		Settings: TurnSettings{
			Model:             "sonnet",
			Effort:            "medium",
			PermissionMode:    config.PermissionApprove,
			WorkspaceDir:      workspace,
			InstructionPath:   "/tmp/CLAUDE.md",
			PermissionTurnID:  "turn-1",
			PermissionTimeout: 5 * time.Minute,
		},
	}, true)
	defer cleanup()
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{
		"-p",
		"--input-format stream-json",
		"--output-format stream-json",
		"--model sonnet",
		"--effort medium",
		"--append-system-prompt-file /tmp/CLAUDE.md",
		"--mcp-config",
		"--strict-mcp-config",
		"--permission-prompt-tool mcp__podiom_permission__prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args %q missing %q", got, want)
		}
	}
	configIndex := indexOf(args, "--mcp-config")
	if configIndex == -1 || configIndex+1 >= len(args) {
		t.Fatalf("missing mcp config path in args: %#v", args)
	}
	raw, err := os.ReadFile(args[configIndex+1])
	if err != nil {
		t.Fatalf("read mcp config: %v", err)
	}
	if !strings.Contains(string(raw), "permission-mcp") || !strings.Contains(string(raw), "turn-1") || !strings.Contains(string(raw), "5m0s") {
		t.Fatalf("unexpected mcp config:\n%s", raw)
	}
	var parsed struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse mcp config: %v\n%s", err, raw)
	}
	permission := parsed.MCPServers["podiom_permission"]
	env, ok := permission["env"].(map[string]any)
	if !ok {
		t.Fatalf("permission MCP missing env: %+v", permission)
	}
	if got := env[config.EnvHome]; got != "/data/podiom" {
		t.Fatalf("permission MCP %s env = %v, want /data/podiom", config.EnvHome, got)
	}
}

func TestClaudeArgsIncludesAssignedMCPServersStrictly(t *testing.T) {
	workspace := t.TempDir()
	c := &Claude{}
	args, cleanup, _, err := c.args(TurnRequest{
		SessionID: "session-2",
		Settings: TurnSettings{
			PermissionMode: config.PermissionYolo,
			WorkspaceDir:   workspace,
			MCPServers: []podiommcp.Server{{
				Name:      "filesystem",
				Transport: podiommcp.TransportStdio,
				Command:   "npx",
				Args:      []string{"-y", "@modelcontextprotocol/server-filesystem"},
			}},
		},
	}, true)
	defer cleanup()
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--strict-mcp-config") {
		t.Fatalf("expected strict mcp config in args: %q", got)
	}
	configIndex := indexOf(args, "--mcp-config")
	if configIndex == -1 || configIndex+1 >= len(args) {
		t.Fatalf("missing mcp config path in args: %#v", args)
	}
	raw, err := os.ReadFile(args[configIndex+1])
	if err != nil {
		t.Fatalf("read mcp config: %v", err)
	}
	var parsed struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse generated config: %v\n%s", err, raw)
	}
	if _, ok := parsed.MCPServers["filesystem"]; !ok {
		t.Fatalf("assigned server missing from config: %s", raw)
	}
	if _, ok := parsed.MCPServers["podiom_permission"]; ok {
		t.Fatalf("yolo config should not include permission relay: %s", raw)
	}
}

func TestClaudeArgsYoloBypassesPermissions(t *testing.T) {
	c := &Claude{}
	workspace := t.TempDir()
	args, cleanup, _, err := c.args(TurnRequest{
		Handle: Handle{ID: "claude-session"},
		Settings: TurnSettings{
			PermissionMode: config.PermissionYolo,
			WorkspaceDir:   workspace,
		},
	}, true)
	defer cleanup()
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--permission-mode bypassPermissions") {
		t.Fatalf("expected yolo permissions in args: %q", got)
	}
	if !strings.Contains(got, "--resume claude-session") {
		t.Fatalf("expected resume handle in args: %q", got)
	}
	// Skills exposure: the workspace (holding .claude/skills) is added so Claude
	// discovers the union (S6).
	if !strings.Contains(got, "--add-dir "+workspace) {
		t.Fatalf("expected --add-dir %s in args: %q", workspace, got)
	}
}

func TestClaudeArgsIncludesBestEffortNativeAgents(t *testing.T) {
	c := &Claude{}
	workspace := t.TempDir()
	args, cleanup, native, err := c.args(TurnRequest{
		Settings: TurnSettings{
			PermissionMode:  config.PermissionYolo,
			WorkspaceDir:    workspace,
			NativeAgentName: "podiom-builder-12345678",
			NativeAgents: []NativeAgent{{
				Name:         "podiom-builder-12345678",
				Description:  "Podiom agent builder",
				Instructions: "builder instructions",
				Model:        "sonnet",
				Effort:       "medium",
			}},
		},
	}, true)
	defer cleanup()
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	if !native {
		t.Fatalf("expected native agent projection to be enabled")
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"--agents", "--agent podiom-builder-12345678"} {
		if !strings.Contains(got, want) {
			t.Fatalf("args %q missing %q", got, want)
		}
	}
	idx := indexOf(args, "--agents")
	if idx == -1 || idx+1 >= len(args) {
		t.Fatalf("missing --agents payload: %#v", args)
	}
	if !strings.Contains(args[idx+1], `"podiom-builder-12345678"`) || !strings.Contains(args[idx+1], `"builder instructions"`) {
		t.Fatalf("unexpected native agents JSON: %s", args[idx+1])
	}
}

func TestClaudeNativeAgentFailureRetriesWithoutNativeAgents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake CLI is Unix-only")
	}
	dir := t.TempDir()
	fakeClaude := filepath.Join(dir, "claude")
	if err := os.WriteFile(fakeClaude, []byte(`#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--agent" ]; then
    echo "native agent rejected" >&2
    exit 2
  fi
done
cat >/dev/null
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"fallback ok"}]}}'
`), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	c := &Claude{bin: fakeClaude}
	events, err := c.SendTurn(context.Background(), TurnRequest{
		Message: "hello",
		Settings: TurnSettings{
			PermissionMode:  config.PermissionYolo,
			WorkspaceDir:    dir,
			NativeAgentName: "podiom-builder-12345678",
			NativeAgents: []NativeAgent{{
				Name:         "podiom-builder-12345678",
				Description:  "Podiom agent builder",
				Instructions: "builder instructions",
			}},
		},
	})
	if err != nil {
		t.Fatalf("send turn: %v", err)
	}
	var final string
	for event := range events {
		if event.Kind == EventAssistantMessage {
			final = event.Content
		}
	}
	if final != "fallback ok" {
		t.Fatalf("final message = %q, want fallback ok", final)
	}
}

func TestParseClaudeStream(t *testing.T) {
	input := strings.NewReader(`{"type":"system","session_id":"abc"}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"O"}},"session_id":"abc"}
{"type":"assistant_delta","delta":{"text":"hel"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}
`)
	out := make(chan Event, 8)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	var events []Event
	for event := range out {
		events = append(events, event)
	}
	if len(events) != 4 {
		b, _ := json.Marshal(events)
		t.Fatalf("expected 4 events, got %d: %s", len(events), b)
	}
	if events[0].Kind != EventHandleUpdated || events[0].Handle.ID != "abc" {
		t.Fatalf("bad handle event: %+v", events[0])
	}
	if events[1].Kind != EventAssistantDelta || events[1].Content != "O" {
		t.Fatalf("bad nested delta event: %+v", events[1])
	}
	if events[2].Kind != EventAssistantDelta || events[2].Content != "hel" {
		t.Fatalf("bad delta event: %+v", events[2])
	}
	if events[3].Kind != EventAssistantMessage || events[3].Content != "hello" {
		t.Fatalf("bad assistant event: %+v", events[3])
	}
}

func TestParseClaudeToolUse(t *testing.T) {
	// A complete assistant message with mixed text + tool_use blocks must yield
	// the assistant text once and one EventToolUse per tool_use block, while the
	// streaming partial (content_block_start) emits no tool_use.
	input := strings.NewReader(`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_partial","name":"Bash","input":{}}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"running it"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"npm install left-pad"}},{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"/repo/main.go"}}]}}
`)
	out := make(chan Event, 16)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	var toolUses []ToolUse
	assistantCount := 0
	for event := range out {
		switch event.Kind {
		case EventToolUse:
			if event.ToolUse != nil {
				toolUses = append(toolUses, *event.ToolUse)
			}
		case EventAssistantMessage:
			assistantCount++
		}
	}
	if assistantCount != 1 {
		t.Fatalf("assistant messages = %d, want 1", assistantCount)
	}
	if len(toolUses) != 2 {
		t.Fatalf("tool uses = %+v, want 2 (partial must not emit)", toolUses)
	}
	if got := toolUses[0]; got.Name != "Bash" || got.ToolUseID != "toolu_1" || got.Summary != "npm install left-pad" {
		t.Fatalf("bad bash tool use: %+v", got)
	}
	if got := toolUses[1]; got.Name != "Read" || got.Summary != "/repo/main.go" {
		t.Fatalf("bad read tool use: %+v", got)
	}
}

func TestParseClaudeNativeAgentActivity(t *testing.T) {
	input := strings.NewReader(`{"type":"system","subtype":"task_started","task_id":"agent-1","tool_use_id":"toolu_1","description":"Review changes","subagent_type":"podiom-reviewer-12345678","task_type":"local_agent","session_id":"abc"}
{"type":"system","subtype":"task_notification","task_id":"agent-1","tool_use_id":"toolu_1","status":"completed","summary":"done","session_id":"abc"}
`)
	out := make(chan Event, 8)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	var activities []NativeAgentActivity
	for event := range out {
		if event.Kind == EventNativeAgentActivity && event.NativeAgent != nil {
			activities = append(activities, *event.NativeAgent)
		}
	}
	if len(activities) != 2 {
		t.Fatalf("activities = %+v, want start and completion", activities)
	}
	if got := activities[0]; got.Status != "started" || got.TaskID != "agent-1" || got.ProviderAgentName != "podiom-reviewer-12345678" || got.ToolUseID != "toolu_1" {
		t.Fatalf("bad start activity: %+v", got)
	}
	if got := activities[1]; got.Status != "completed" || got.TaskID != "agent-1" || got.ProviderAgentName != "" {
		t.Fatalf("bad completion activity: %+v", got)
	}
}

func TestClaudeNativeAgentActivityEnrichment(t *testing.T) {
	req := TurnRequest{Settings: TurnSettings{NativeAgents: []NativeAgent{{
		PodiomName: "Researcher",
		Name:       "podiom-researcher-12345678",
	}}}}
	track := claudeStreamTrack{nativeAgentTasks: map[string]NativeAgentActivity{}}
	start, ok := enrichClaudeNativeAgentActivity(req, &track, &NativeAgentActivity{
		TaskID:            "agent-1",
		ProviderAgentName: "podiom-researcher-12345678",
		Status:            "started",
	})
	if !ok {
		t.Fatal("start activity was dropped")
	}
	if start.PodiomAgentName != "Researcher" || start.DisplayName != "Researcher" {
		t.Fatalf("bad start enrichment: %+v", start)
	}
	done, ok := enrichClaudeNativeAgentActivity(req, &track, &NativeAgentActivity{
		TaskID: "agent-1",
		Status: "completed",
	})
	if !ok {
		t.Fatal("completion activity was dropped")
	}
	if done.ProviderAgentName != "podiom-researcher-12345678" || done.DisplayName != "Researcher" || done.Status != "completed" {
		t.Fatalf("bad completion enrichment: %+v", done)
	}
}

func TestParseClaudeReasoningStream(t *testing.T) {
	input := strings.NewReader(`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","text":"working"}}}
{"type":"assistant","message":{"content":[{"type":"thinking","text":"private"},{"type":"text","text":"public"}]}}
`)
	out := make(chan Event, 8)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	var events []Event
	for event := range out {
		events = append(events, event)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %+v", events)
	}
	if events[0].Kind != EventReasoningDelta || events[0].Content != "working" {
		t.Fatalf("bad reasoning delta: %+v", events[0])
	}
	if events[1].Kind != EventReasoningMessage || events[1].Content != "private" {
		t.Fatalf("bad reasoning message: %+v", events[1])
	}
	if events[2].Kind != EventAssistantMessage || events[2].Content != "public" {
		t.Fatalf("bad assistant message: %+v", events[2])
	}
}

// Core splits a turn into working notes and an answer at tool-call boundaries,
// which only works if an assistant line emits its text *before* the tool_use it
// carries. This pins that order.
func TestParseClaudeEmitsTextBeforeToolUseInOneMessage(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"thinking","text":"private"},{"type":"text","text":"let me look"},{"type":"tool_use","id":"tu-1","name":"Read","input":{"file_path":"/tmp/a"}}]}}
`)
	out := make(chan Event, 8)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	var kinds []EventKind
	for event := range out {
		kinds = append(kinds, event.Kind)
	}
	want := []EventKind{EventReasoningMessage, EventAssistantMessage, EventToolUse}
	if len(kinds) != len(want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	for i, w := range want {
		if kinds[i] != w {
			t.Fatalf("event[%d] = %q, want %q (full order %v)", i, kinds[i], w, kinds)
		}
	}
}

func TestParseClaudeRateLimitErrorEvent(t *testing.T) {
	input := strings.NewReader(`{"type":"error","error":{"message":"Claude usage limit reached. Try again later."}}
`)
	out := make(chan Event, 1)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	event := <-out
	if event.Kind != EventRateLimited {
		t.Fatalf("expected rate limit event, got %+v", event)
	}
}

func TestParseClaudeResultRateLimited(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		limited bool
	}{
		{"usage limit banner", `{"type":"result","subtype":"success","is_error":false,"result":"Claude AI usage limit reached|1783650000"}`, true},
		{"error result with limit text", `{"type":"result","is_error":true,"result":"API Error: 429 too many requests"}`, true},
		{"api error status 429", `{"type":"result","is_error":true,"api_error_status":429,"result":"request failed"}`, true},
		{"reply that merely mentions rate limits", `{"type":"result","is_error":false,"result":"The rate limit fallback logic looks correct."}`, false},
		{"error without limit text", `{"type":"result","is_error":true,"result":"something else broke"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, err := parseClaudeLine([]byte(tc.line))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %+v", events)
			}
			if limited := events[0].Kind == EventRateLimited; limited != tc.limited {
				t.Fatalf("rate limited = %v, want %v: %+v", limited, tc.limited, events[0])
			}
		})
	}
}

func TestParseClaudeRateLimitEvent(t *testing.T) {
	events, err := parseClaudeLine([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1783650000,"rateLimitType":"five_hour"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventRateLimited {
		t.Fatalf("expected rate limit event for rejected status, got %+v", events)
	}
	events, err = parseClaudeLine([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1783650000,"rateLimitType":"five_hour"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("allowed status must not emit events, got %+v", events)
	}
}

func TestParseClaudeRawQuestionsText(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"text","text":"questions: [{\"question\":\"What do you want from \\\"testing roadmap\\\"?\",\"header\":\"Intent\",\"options\":[{\"label\":\"Draft a testing roadmap\",\"description\":\"Create a phased plan/document for what testing to build over time.\"},{\"label\":\"Roadmap for a specific project\",\"description\":\"Analyze an existing codebase and produce a tailored testing strategy.\"}],\"multiSelect\":false}]"}]}}
`)
	out := make(chan Event, 2)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	event := <-out
	if event.Kind != EventUserInputRequest || event.UserInputRequest == nil {
		t.Fatalf("expected user input request, got %+v", event)
	}
	req := event.UserInputRequest
	if req.Provider != config.ProviderClaude || len(req.Questions) != 1 {
		t.Fatalf("bad request: %+v", req)
	}
	q := req.Questions[0]
	if q.ID != "q1" || q.Header != "Intent" || q.MultiSelect {
		t.Fatalf("bad question metadata: %+v", q)
	}
	if !req.EndsTurn {
		t.Fatal("raw question text must remain a turn-ending fallback")
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Draft a testing roadmap" {
		t.Fatalf("bad options: %+v", q.Options)
	}
	select {
	case extra, ok := <-out:
		if !ok {
			return
		}
		t.Fatalf("raw question text should be suppressed, got extra event %+v", extra)
	default:
	}
}

func TestParseClaudeToolUseQuestions(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","session_id":"claude-session","message":{"content":[{"type":"tool_use","id":"toolu_question","name":"AskUserQuestion","input":{"questions":[{"id":"intent","question":"Pick one","header":"Intent","multiSelect":true,"options":[{"label":"A","description":"Alpha"},{"label":"B","description":"Beta"}]}],"autoResolutionMs":120000}}]}}
`)
	out := make(chan Event, 3)
	if err := parseClaudeStream(context.Background(), input, out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	<-out // handle update
	event := <-out
	if event.Kind != EventUserInputRequest || event.UserInputRequest == nil {
		t.Fatalf("expected user input request, got %+v", event)
	}
	req := event.UserInputRequest
	if req.ItemID != "toolu_question" || req.AutoResolutionMS != 120000 {
		t.Fatalf("bad request metadata: %+v", req)
	}
	if !req.Questions[0].MultiSelect || req.Questions[0].ID != "intent" {
		t.Fatalf("bad question: %+v", req.Questions[0])
	}
	if !req.EndsTurn {
		t.Fatal("stream-parsed tool question must end the turn")
	}
}

func TestParseClaudeToolUseQuestionSuppressedForPermissionRelay(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","session_id":"claude-session","message":{"content":[{"type":"tool_use","id":"toolu_question","name":"AskUserQuestion","input":{"questions":[{"question":"Pick one","header":"Intent","multiSelect":false,"options":[{"label":"A"},{"label":"B"}]}]}}]}}
`)
	out := make(chan Event, 4)
	if err := parseClaudeStream(context.Background(), input, out, claudeStreamOptions{SuppressStructuredQuestions: true}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	var sawToolUse bool
	for event := range out {
		if event.Kind == EventUserInputRequest {
			t.Fatalf("permission relay mode emitted duplicate question: %+v", event)
		}
		if event.Kind == EventToolUse {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Fatal("question suppression must retain the tool-use audit event")
	}
}

func TestParseClaudeRawQuestionRemainsFallbackWithPermissionRelay(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"text","text":"questions: [{\"question\":\"Pick one\",\"options\":[{\"label\":\"A\"},{\"label\":\"B\"}],\"multiSelect\":false}]"}]}}
`)
	out := make(chan Event, 2)
	if err := parseClaudeStream(context.Background(), input, out, claudeStreamOptions{SuppressStructuredQuestions: true}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	close(out)
	event := <-out
	if event.Kind != EventUserInputRequest || event.UserInputRequest == nil || !event.UserInputRequest.EndsTurn {
		t.Fatalf("raw fallback was not preserved: %+v", event)
	}
}

func TestClaudeQuestionViaPermissionRelayModes(t *testing.T) {
	tests := []struct {
		name     string
		settings TurnSettings
		want     bool
	}{
		{name: "approve", settings: TurnSettings{PermissionMode: config.PermissionApprove}, want: true},
		{name: "auto", settings: TurnSettings{PermissionMode: config.PermissionAuto}, want: true},
		{name: "yolo", settings: TurnSettings{PermissionMode: config.PermissionYolo}, want: false},
		{name: "plan", settings: TurnSettings{PermissionMode: config.PermissionApprove, PlanMode: true}, want: false},
		{name: "unattended", settings: TurnSettings{PermissionMode: config.PermissionApprove, Unattended: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeQuestionViaPermissionRelay(TurnRequest{Settings: tt.settings}); got != tt.want {
				t.Fatalf("relay = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClaudeRateLimitedText(t *testing.T) {
	for _, message := range []string{
		"rate limit exceeded",
		"usage_limit_exceeded",
		"too many requests",
		"HTTP 429 from upstream",
	} {
		if !claudeRateLimitedText(message) {
			t.Fatalf("expected %q to be classified as a rate limit", message)
		}
	}
	if claudeRateLimitedText("authentication failed") {
		t.Fatal("auth failure should not be classified as a rate limit")
	}
}

func TestClaudeWaitErrorKeepsProviderMessage(t *testing.T) {
	event, send := claudeWaitEvent(errors.New("exit status 1"), "", claudeStreamTrack{lastMessage: "claude error: not logged in"})
	if send {
		t.Fatalf("expected provider message to be preserved without generic replacement, got send=%v event=%+v", send, event)
	}
}

func TestClaudeWaitErrorUsesStderrWhenNoProviderMessage(t *testing.T) {
	event, send := claudeWaitEvent(errors.New("exit status 1"), "not logged in", claudeStreamTrack{})
	if !send {
		t.Fatal("expected generic event when no provider message was emitted")
	}
	if event.Kind != EventAssistantMessage || !strings.Contains(event.Content, "not logged in") {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestCollectStderrKeepsTail(t *testing.T) {
	got := collectStderr(strings.NewReader("0123456789abcdef"), 6)
	if got.err != nil {
		t.Fatalf("collect stderr: %v", got.err)
	}
	if got.text != "abcdef" {
		t.Fatalf("expected stderr tail, got %q", got.text)
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func TestClaudeContextEventAndWindow(t *testing.T) {
	// result event: usage lives at the top level.
	var result map[string]any
	if err := json.Unmarshal([]byte(`{"type":"result","result":"done","usage":{"input_tokens":50000,"cache_creation_input_tokens":1000,"cache_read_input_tokens":30000,"output_tokens":400}}`), &result); err != nil {
		t.Fatal(err)
	}
	event, ok := claudeContextEvent(result)
	if !ok || event.Kind != EventContextStatus {
		t.Fatalf("expected context event, got %+v ok=%v", event, ok)
	}
	// Context size is the whole prompt: input + both cache classes (output excluded).
	if event.ContextStatus.UsedTokens != 81000 {
		t.Errorf("used tokens = %d, want 81000", event.ContextStatus.UsedTokens)
	}
	if event.ContextStatus.MaxTokens != 0 {
		t.Errorf("max tokens should be stamped later, got %d", event.ContextStatus.MaxTokens)
	}

	// assistant event: usage is nested under message.usage.
	var assistant map[string]any
	if err := json.Unmarshal([]byte(`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":5}}}`), &assistant); err != nil {
		t.Fatal(err)
	}
	if event, ok := claudeContextEvent(assistant); !ok || event.ContextStatus.UsedTokens != 15 {
		t.Errorf("nested usage event = %+v ok=%v", event, ok)
	}

	// No usage → no event.
	var bare map[string]any
	if err := json.Unmarshal([]byte(`{"type":"result","result":"hi"}`), &bare); err != nil {
		t.Fatal(err)
	}
	if _, ok := claudeContextEvent(bare); ok {
		t.Error("expected no context event without usage")
	}

	if got := claudeContextWindow("claude-sonnet-4-5"); got != claudeDefaultContextWindow {
		t.Errorf("default window = %d, want %d", got, claudeDefaultContextWindow)
	}
	if got := claudeContextWindow("claude-sonnet-4-5[1m]"); got != 1_000_000 {
		t.Errorf("1M window = %d, want 1000000", got)
	}
}

func TestClaudeTurnUsageEvent(t *testing.T) {
	// Billed usage is taken from the terminal result event, including output.
	var result map[string]any
	if err := json.Unmarshal([]byte(`{"type":"result","result":"done","usage":{"input_tokens":50000,"cache_creation_input_tokens":1000,"cache_read_input_tokens":30000,"output_tokens":400}}`), &result); err != nil {
		t.Fatal(err)
	}
	event, ok := claudeTurnUsageEvent(result)
	if !ok || event.Kind != EventTurnUsage || event.TurnUsage == nil {
		t.Fatalf("expected turn usage event, got %+v ok=%v", event, ok)
	}
	tu := *event.TurnUsage
	if tu.Input != 50000 || tu.Output != 400 || tu.CacheWrite != 1000 || tu.CacheRead != 30000 {
		t.Errorf("breakdown = %+v", tu)
	}
	if tu.Total() != 81400 {
		t.Errorf("total = %d, want 81400", tu.Total())
	}

	// The full parse only emits it on the terminal result, not per-message.
	assistantEvents, err := parseClaudeLine([]byte(`{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range assistantEvents {
		if e.Kind == EventTurnUsage {
			t.Error("assistant event must not emit turn usage (would double-count)")
		}
	}
	resultEvents, err := parseClaudeLine([]byte(`{"type":"result","result":"done","usage":{"input_tokens":10,"output_tokens":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	var sawUsage bool
	for _, e := range resultEvents {
		if e.Kind == EventTurnUsage {
			sawUsage = true
		}
	}
	if !sawUsage {
		t.Error("result event should emit a turn usage event")
	}

	// No usage → no event.
	var bare map[string]any
	if err := json.Unmarshal([]byte(`{"type":"result","result":"hi"}`), &bare); err != nil {
		t.Fatal(err)
	}
	if _, ok := claudeTurnUsageEvent(bare); ok {
		t.Error("expected no turn usage event without usage")
	}
}

func TestClaudeEnvAppliesExtraEnv(t *testing.T) {
	t.Setenv("PODIOM_TEST_TOKEN", "inherited")

	// Nil supplier is a no-op.
	c := &Claude{}
	for _, kv := range c.env("", nil) {
		if kv == "PODIOM_TEST_TOKEN=stored" {
			t.Fatal("nil supplier must not inject values")
		}
	}

	// Supplier pairs are injected and a stored value wins over an inherited
	// variable of the same name.
	c = &Claude{extraEnv: func() []string {
		return []string{"PODIOM_TEST_TOKEN=stored", "GITHUB_TOKEN=tok_123", "malformed"}
	}}
	env := c.env("/profile", nil)
	var sawStored, sawInherited, sawNew, sawProfile bool
	for _, kv := range env {
		switch kv {
		case "PODIOM_TEST_TOKEN=stored":
			sawStored = true
		case "PODIOM_TEST_TOKEN=inherited":
			sawInherited = true
		case "GITHUB_TOKEN=tok_123":
			sawNew = true
		case "CLAUDE_CONFIG_DIR=/profile":
			sawProfile = true
		}
	}
	if !sawStored || sawInherited {
		t.Fatalf("stored value should replace inherited: stored=%v inherited=%v", sawStored, sawInherited)
	}
	if !sawNew {
		t.Fatal("supplier pair missing from env")
	}
	if !sawProfile {
		t.Fatal("CLAUDE_CONFIG_DIR handling must survive extra env injection")
	}
	for _, kv := range env {
		if kv == "malformed" {
			t.Fatal("malformed supplier pairs must be dropped")
		}
	}
}
