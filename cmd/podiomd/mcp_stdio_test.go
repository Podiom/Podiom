package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func decodeResponses(t *testing.T, out string) []map[string]any {
	t.Helper()
	var resps []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), stdioMCPBufferMax)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

func testTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "echo",
			Description: "echo back the text argument",
			InputSchema: objectSchema([]string{"text"}, map[string]any{"text": strProp("text")}),
			Handler: func(_ context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(args, &a)
				return "echo:" + a.Text, nil
			},
		},
		{
			Name:        "boom",
			Description: "always errors",
			InputSchema: objectSchema(nil, nil),
			Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
				return "", fmt.Errorf("kaboom")
			},
		},
	}
}

func runLoop(t *testing.T, input string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := serveStdioMCP(context.Background(), "podiom-test", testTools(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serveStdioMCP: %v", err)
	}
	return decodeResponses(t, out.String())
}

func TestServeStdioMCPInitialize(t *testing.T) {
	resps := runLoop(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	result := resps[0]["result"].(map[string]any)
	if result["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "podiom-test" {
		t.Fatalf("serverInfo.name = %v", info["name"])
	}
}

func TestServeStdioMCPToolsList(t *testing.T) {
	resps := runLoop(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	result := resps[0]["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "echo" || first["inputSchema"] == nil {
		t.Fatalf("unexpected first tool: %#v", first)
	}
}

func TestServeStdioMCPToolsCallDispatch(t *testing.T) {
	resps := runLoop(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`+"\n")
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	if first["text"] != "echo:hi" {
		t.Fatalf("text = %v", first["text"])
	}
}

func TestServeStdioMCPHandlerErrorBecomesRPCError(t *testing.T) {
	resps := runLoop(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom","arguments":{}}}`+"\n")
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got %#v", resps[0])
	}
	if !strings.Contains(errObj["message"].(string), "kaboom") {
		t.Fatalf("message = %v", errObj["message"])
	}
}

func TestServeStdioMCPUnknownTool(t *testing.T) {
	resps := runLoop(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`+"\n")
	errObj := resps[0]["error"].(map[string]any)
	if int(errObj["code"].(float64)) != -32601 {
		t.Fatalf("code = %v", errObj["code"])
	}
}

func TestServeStdioMCPSkipsNotifications(t *testing.T) {
	// A request without an id is a notification and must produce no response.
	input := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":6,"method":"initialize"}` + "\n"
	resps := runLoop(t, input)
	if len(resps) != 1 {
		t.Fatalf("want 1 response (notification skipped), got %d", len(resps))
	}
	if resps[0]["id"] == nil {
		t.Fatalf("response missing id")
	}
}

func TestServeStdioMCPHandlesLargeLine(t *testing.T) {
	big := strings.Repeat("x", 200*1024) // > default 64KB scanner token
	call := map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{"text": big}},
	}
	raw, _ := json.Marshal(call)
	resps := runLoop(t, string(raw)+"\n")
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	if first["text"] != "echo:"+big {
		t.Fatalf("large-line round trip failed (len=%d)", len(first["text"].(string)))
	}
}
