package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPTestStdioSuccess(t *testing.T) {
	result := TestServerWithTimeout(t.Context(), helperServer("stdio-ok"), 2*time.Second)
	if !result.OK {
		t.Fatalf("result failed: %+v", result)
	}
	if result.ToolCount != 1 {
		t.Fatalf("tool count = %d, want 1", result.ToolCount)
	}
	if len(result.Steps) != 6 {
		t.Fatalf("steps = %+v", result.Steps)
	}
}

func TestMCPTestStdioCommandMissing(t *testing.T) {
	result := TestServerWithTimeout(t.Context(), Server{
		Name:      "missing",
		Transport: TransportStdio,
		Command:   filepath.Join(t.TempDir(), "does-not-exist"),
	}, time.Second)
	if result.OK {
		t.Fatalf("result should fail: %+v", result)
	}
	if !strings.Contains(result.Error, "does-not-exist") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestMCPTestStdioTimeoutIncludesStderrTail(t *testing.T) {
	server := helperServer("stdio-silent")
	result := TestServerWithTimeout(t.Context(), server, 150*time.Millisecond)
	if result.OK {
		t.Fatalf("result should fail: %+v", result)
	}
	if !strings.Contains(result.Error, "deadline exceeded") {
		t.Fatalf("error = %q", result.Error)
	}
	if !strings.Contains(result.StderrTail, "booted but quiet") {
		t.Fatalf("stderr tail = %q", result.StderrTail)
	}
}

func TestMCPTestHTTPJSONSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			_ = json.NewEncoder(w).Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(`{"protocolVersion":"2024-11-05"}`)})
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(`{"tools":[{"name":"ping"},{"name":"pong"}]}`)})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer srv.Close()
	result := TestServerWithTimeout(t.Context(), Server{Name: "http-json", Transport: TransportHTTP, URL: srv.URL}, time.Second)
	if !result.OK {
		t.Fatalf("result failed: %+v", result)
	}
	if result.ToolCount != 2 {
		t.Fatalf("tool count = %d, want 2", result.ToolCount)
	}
}

func TestMCPTestHTTPSSESuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Method == "initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var result string
		if req.Method == "initialize" {
			result = `{"protocolVersion":"2024-11-05"}`
		} else {
			result = `{"tools":[{"name":"ping"}]}`
		}
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%v,\"result\":%s}\n\n", req.ID, result)
	}))
	defer srv.Close()
	result := TestServerWithTimeout(t.Context(), Server{Name: "http-sse", Transport: TransportHTTP, URL: srv.URL}, time.Second)
	if !result.OK {
		t.Fatalf("result failed: %+v", result)
	}
	if result.ToolCount != 1 {
		t.Fatalf("tool count = %d, want 1", result.ToolCount)
	}
}

func TestMCPTestRedactsSensitiveValues(t *testing.T) {
	input := `command http://192.168.1.7:9583/private_UsTMdZbcapvngA6IPGcnGw?token=SUPERSECRET Authorization=Bearer abc secret=xyz`
	got := RedactSensitive(input)
	for _, leak := range []string{"private_UsTM", "SUPERSECRET", "Bearer abc", "secret=xyz"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redaction leaked %q in %q", leak, got)
		}
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("redaction did not mark secret: %q", got)
	}
}

func helperServer(mode string) Server {
	return Server{
		Name:      mode,
		Transport: TransportStdio,
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestMCPTestHelper", "--", mode},
	}
}

func TestMCPTestHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "stdio-silent" {
		_, _ = fmt.Fprintln(os.Stderr, "booted but quiet")
		time.Sleep(10 * time.Second)
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(`{"protocolVersion":"2024-11-05"}`)})
		case "tools/list":
			_ = enc.Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(`{"tools":[{"name":"ping"}]}`)})
		default:
			_ = enc.Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Error: mustRaw(`{"code":-32601,"message":"method not found"}`)})
		}
	}
}

func mustRaw(s string) json.RawMessage {
	return json.RawMessage(s)
}
