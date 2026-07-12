package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
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

func TestSetGatewayTokenReadsCurrentTokenEachRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	path := config.NewPaths(home).GatewayToken
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write old token: %v", err)
	}
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	setGatewayToken(req1)
	if got := req1.Header.Get(gateway.Header); got != "old" {
		t.Fatalf("first token header = %q, want old", got)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("write new token: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	setGatewayToken(req2)
	if got := req2.Header.Get(gateway.Header); got != "new" {
		t.Fatalf("second token header = %q, want new", got)
	}
}
