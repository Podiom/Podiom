package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInterviewMCPExposesQuestionAndSubmitTools(t *testing.T) {
	tools := interviewMCPTools("127.0.0.1:8787", "session-1")
	if len(tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(tools))
	}
	if tools[0].Name != "podiom_ask_profile_question" || tools[1].Name != "podiom_submit_user_profile" {
		t.Fatalf("unexpected tools: %q, %q", tools[0].Name, tools[1].Name)
	}

	byName := map[string]mcpTool{tools[0].Name: tools[0], tools[1].Name: tools[1]}
	resp := dispatchStdioMCP(context.Background(), "podiom-interview", tools, byName, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	for _, want := range []string{"identity_context", "technical_depth", "minItems", "maxItems"} {
		if !json.Valid(raw) || !containsBytes(raw, want) {
			t.Fatalf("tools/list missing %q: %s", want, raw)
		}
	}
}

func containsBytes(raw []byte, want string) bool {
	for i := 0; i+len(want) <= len(raw); i++ {
		if string(raw[i:i+len(want)]) == want {
			return true
		}
	}
	return false
}
