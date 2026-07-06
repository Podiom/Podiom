package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// rpcRequest / rpcResponse are the minimal JSON-RPC envelopes shared by the
// internal stdio MCP helpers (permission-mcp, plan-mcp, manage-mcp).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// mcpTool is one tool served by an internal stdio MCP server. Handler receives
// the raw `arguments` object from a tools/call and returns the text body of the
// tool result; a returned error becomes a JSON-RPC error.
type mcpTool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// APIRoutes are the server mux patterns this tool exercises (e.g.
	// "/api/tasks", "/api/tasks/"). Metadata only — serveStdioMCP ignores it;
	// the manage-mcp coverage guardrail test uses it to prove every /api route
	// is either wrapped by a tool or explicitly excluded.
	APIRoutes []string
	Handler   func(ctx context.Context, args json.RawMessage) (string, error)
}

// stdioMCPBufferMax bounds a single JSON-RPC line. Management tool calls carry
// schedule bodies and config patches that comfortably exceed bufio.Scanner's
// default 64 KB token limit, so we raise the ceiling well above that.
const stdioMCPBufferMax = 10 * 1024 * 1024

// serveStdioMCP runs the shared initialize / tools/list / tools/call loop for an
// internal stdio MCP server. It reads newline-delimited JSON-RPC requests from
// in and writes responses to out, dispatching tools/call to the matching tool
// handler by name. Notifications (requests without an id) are ignored per the
// JSON-RPC spec.
func serveStdioMCP(ctx context.Context, serverName string, tools []mcpTool, in io.Reader, out io.Writer) error {
	byName := make(map[string]mcpTool, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), stdioMCPBufferMax)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue
		}
		resp := dispatchStdioMCP(ctx, serverName, tools, byName, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func dispatchStdioMCP(ctx context.Context, serverName string, tools []mcpTool, byName map[string]mcpTool, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": serverName, "version": "0"},
			},
		}
	case "tools/list":
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": list}}
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &call); err != nil {
			return rpcError(req.ID, -32602, err.Error())
		}
		tool, ok := byName[call.Name]
		if !ok {
			return rpcError(req.ID, -32601, fmt.Sprintf("unknown tool %q", call.Name))
		}
		text, err := tool.Handler(ctx, call.Arguments)
		if err != nil {
			return rpcError(req.ID, -32000, err.Error())
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]string{{"type": "text", "text": text}},
			},
		}
	default:
		return rpcError(req.ID, -32601, "method not found")
	}
}

func rpcError(id any, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: map[string]any{"code": code, "message": message}}
}
