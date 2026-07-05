package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	podiommcp "github.com/Podiom/Podiom/internal/mcp"
)

func TestMCPServerTestEndpointFindsSavedServerAndDoesNotRewriteYAML(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-1")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`))
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"ping"}]}}`))
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()
	if err := podiommcp.SaveUserFile(paths.MCPYAML, []podiommcp.Server{{
		Name:      "probe",
		Transport: podiommcp.TransportHTTP,
		URL:       upstream.URL,
	}}); err != nil {
		t.Fatalf("save mcp yaml: %v", err)
	}
	before, err := os.ReadFile(paths.MCPYAML)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/probe/test", nil)
	rr := httptest.NewRecorder()
	srv.handleMCPServer(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var result podiommcp.TestResult
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.OK || result.ToolCount != 1 {
		t.Fatalf("bad result: %+v", result)
	}
	after, err := os.ReadFile(paths.MCPYAML)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("mcp yaml was rewritten\nbefore=%s\nafter=%s", before, after)
	}
}

func TestMCPServerTestEndpointUnknownServer404(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/nope/test", nil)
	rr := httptest.NewRecorder()
	srv.handleMCPServer(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not found") {
		t.Fatalf("body = %q", rr.Body.String())
	}
}
