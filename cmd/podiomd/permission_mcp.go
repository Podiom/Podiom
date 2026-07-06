package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/spf13/cobra"
)

func newPermissionMCPCmd() *cobra.Command {
	var addr string
	var turnID string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:    "permission-mcp",
		Short:  "Run the internal Claude permission MCP helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPermissionMCP(cmd.Context(), addr, turnID, timeout, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "podiomd API address")
	cmd.Flags().StringVar(&turnID, "turn", "", "permission relay turn ID")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "permission request timeout")
	return cmd
}

func runPermissionMCP(ctx context.Context, addr, turnID string, timeout time.Duration, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
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
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue
		}
		resp := handleMCPRequest(ctx, addr, turnID, timeout, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleMCPRequest(ctx context.Context, addr, turnID string, timeout time.Duration, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]string{"name": "podiom-permission", "version": "0"},
			},
		}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": []map[string]any{{
					"name":        "prompt",
					"description": "Ask Podiom whether Claude may run a requested tool action.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tool_name":   map[string]any{"type": "string"},
							"tool_use_id": map[string]any{"type": "string"},
							"description": map[string]any{"type": "string"},
							"input":       map[string]any{"type": "object"},
						},
					},
				}},
			},
		}
	case "tools/call":
		decision, err := forwardPermission(ctx, addr, turnID, timeout, req.Params)
		if err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32000, "message": err.Error()}}
		}
		raw, _ := json.Marshal(decision)
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]string{{"type": "text", "text": string(raw)}},
			},
		}
	default:
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}}
	}
}

func forwardPermission(ctx context.Context, addr, turnID string, timeout time.Duration, params json.RawMessage) (adapter.PermissionDecision, error) {
	var call struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return adapter.PermissionDecision{Behavior: "deny"}, err
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return adapter.PermissionDecision{Behavior: "deny"}, err
	}
	req := adapter.PermissionRequest{
		ID:     uuid.NewString(),
		TurnID: turnID,
		Input:  json.RawMessage(`{}`),
	}
	if raw := args["tool_name"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &req.ToolName)
	}
	if raw := args["tool_use_id"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &req.ToolUseID)
	}
	if raw := args["description"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &req.Description)
	}
	if raw := args["input"]; len(raw) > 0 {
		req.Input = raw
	}
	if req.Description == "" {
		req.Description = permissionInputDescription(req.Input)
	}
	body, _ := json.Marshal(req)
	requestCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	url := "http://" + addr + "/api/permissions/" + turnID + "?timeout=" + timeout.String()
	httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return adapter.PermissionDecision{Behavior: "deny"}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	setGatewayToken(httpReq)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return adapter.PermissionDecision{Behavior: "deny"}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return adapter.PermissionDecision{Behavior: "deny"}, fmt.Errorf("podiom permission API status %d", resp.StatusCode)
	}
	var decision adapter.PermissionDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		return adapter.PermissionDecision{Behavior: "deny"}, err
	}
	if decision.Behavior == "" {
		decision.Behavior = "deny"
	}
	return decision, nil
}

func permissionInputDescription(input json.RawMessage) string {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	raw := fields["description"]
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var desc string
	_ = json.Unmarshal(raw, &desc)
	return desc
}
