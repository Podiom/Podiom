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

	"github.com/spf13/cobra"
)

func newPlanMCPCmd() *cobra.Command {
	var addr string
	var sessionID string
	var turnID string
	cmd := &cobra.Command{
		Use:    "plan-mcp",
		Short:  "Run the internal plan submission MCP helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlanMCP(cmd.Context(), addr, sessionID, turnID, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "podiomd API address")
	cmd.Flags().StringVar(&sessionID, "session", "", "Podiom session ID")
	cmd.Flags().StringVar(&turnID, "turn", "", "turn ID")
	return cmd
}

func runPlanMCP(ctx context.Context, addr, sessionID, turnID string, in io.Reader, out io.Writer) error {
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
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue
		}
		resp := handlePlanMCPRequest(ctx, addr, sessionID, turnID, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handlePlanMCPRequest(ctx context.Context, addr, sessionID, turnID string, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "podiom-plan", "version": "0"},
			},
		}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": []map[string]any{{
					"name":        "podiom_submit_plan",
					"description": "Submit a Podiom implementation plan for user approval. The markdown argument must be the full rendered Markdown plan using the required Podiom structure: # Plan:, ## Goal, ## Context, ## Approach, ## Changes, ## Steps, ## Tests, ## Risks And Rollback, and ## Open Questions.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"file_path", "markdown"},
						"properties": map[string]any{
							"file_path": map[string]any{
								"type":        "string",
								"description": "Absolute path to the plan file under the active project's plans directory.",
							},
							"markdown": map[string]any{
								"type":        "string",
								"description": "The full rendered Markdown plan, including # Plan: and all required ## sections.",
							},
						},
					},
				}},
			},
		}
	case "tools/call":
		if err := forwardPlanSubmission(ctx, addr, sessionID, turnID, req.Params); err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32000, "message": err.Error()}}
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]string{{"type": "text", "text": "Plan submitted for user approval."}},
			},
		}
	default:
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}}
	}
}

func forwardPlanSubmission(ctx context.Context, addr, sessionID, turnID string, params json.RawMessage) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return err
	}
	if call.Name != "" && call.Name != "podiom_submit_plan" {
		return fmt.Errorf("unknown tool %q", call.Name)
	}
	var args struct {
		FilePath string `json:"file_path"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{
		"file_path": args.FilePath,
		"markdown":  args.Markdown,
		"turn_id":   turnID,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/plans/"+sessionID+"/submit", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	setGatewayToken(httpReq)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("podiom plan API status %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	return nil
}
