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

// The podiom_project helper gives an agent the project it is working in, and
// the means to follow that project's source-control policy.
//
// It is launched with the session id fixed, and resolves the project from it
// server-side. That is the point: the agent never passes a project id, so it
// cannot address the wrong project, and "which project am I in" stops being
// something it has to infer from the prompt.
func newProjectMCPCmd() *cobra.Command {
	var addr string
	var sessionID string
	cmd := &cobra.Command{
		Use:    "project-mcp",
		Short:  "Run the internal project-context MCP helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectMCP(cmd.Context(), addr, sessionID, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "podiomd API address")
	cmd.Flags().StringVar(&sessionID, "session", "", "Podiom session ID")
	return cmd
}

func runProjectMCP(ctx context.Context, addr, sessionID string, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
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
		if err := enc.Encode(handleProjectMCPRequest(ctx, addr, sessionID, req)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleProjectMCPRequest(ctx context.Context, addr, sessionID string, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "podiom-project", "version": "0"},
			},
		}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": projectMCPTools()},
		}
	case "tools/call":
		text, err := forwardProjectCall(ctx, addr, sessionID, req.Params)
		if err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32000, "message": err.Error()}}
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"content": []map[string]string{{"type": "text", "text": text}}},
		}
	default:
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}}
	}
}

func projectMCPTools() []map[string]any {
	return []map[string]any{
		{
			"name": "podiom_project_context",
			"description": "Get the project this session is working in: its identity, local paths, stack, notes, " +
				"and its source-control policy (whether it uses git, its remote, default branch, branching policy, " +
				"commit policy, current branch, and whether git is ready on this machine). Takes no arguments — " +
				"the project is resolved from the session.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name": "podiom_start_work",
			"description": "Apply the project's branching policy before you start editing. On a branch-per-task " +
				"project this creates and checks out the right branch; on a direct-to-main project it confirms the " +
				"default branch. Safe to call more than once for the same work. Call this before making changes " +
				"rather than running git branch yourself, so the project's policy is actually followed.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"kind", "slug"},
				"properties": map[string]any{
					"kind": map[string]any{
						"type":        "string",
						"description": "The kind of work: feature, bugfix, or chore.",
					},
					"slug": map[string]any{
						"type":        "string",
						"description": "A few words naming the work, e.g. \"widget crash on save\". Podiom turns it into a branch name.",
					},
				},
			},
		},
	}
}

func forwardProjectCall(ctx context.Context, addr, sessionID string, params json.RawMessage) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("session id is required")
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return "", err
	}
	base := "http://" + addr + "/api/session-project/" + sessionID
	switch call.Name {
	case "podiom_project_context":
		return projectHTTP(ctx, http.MethodGet, base+"/context", nil)
	case "podiom_start_work":
		var args struct {
			Kind string `json:"kind"`
			Slug string `json:"slug"`
		}
		if len(call.Arguments) > 0 {
			if err := json.Unmarshal(call.Arguments, &args); err != nil {
				return "", err
			}
		}
		body, _ := json.Marshal(args)
		return projectHTTP(ctx, http.MethodPost, base+"/start-work", body)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

func projectHTTP(ctx context.Context, method, url string, body []byte) (string, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return "", err
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	setGatewayToken(httpReq)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("podiom project API status %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	return string(bytes.TrimSpace(raw)), nil
}
