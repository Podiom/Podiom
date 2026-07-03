package main

import (
	"context"
	"strings"
	"testing"
)

func TestPlanMCPToolDescribesStructuredMarkdown(t *testing.T) {
	resp := handlePlanMCPRequest(context.Background(), "127.0.0.1:8787", "session-1", "turn-1", rpcRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/list",
	})
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	tools, ok := result["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", result["tools"])
	}
	description, _ := tools[0]["description"].(string)
	for _, want := range []string{"# Plan:", "## Goal", "## Risks And Rollback", "## Open Questions"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q: %s", want, description)
		}
	}
}
