package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

func newInterviewMCPCmd() *cobra.Command {
	var addr string
	var sessionID string
	cmd := &cobra.Command{
		Use:    "interview-mcp",
		Short:  "Run the internal USER.md interview MCP helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInterviewMCP(cmd.Context(), addr, sessionID, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "podiomd API address")
	cmd.Flags().StringVar(&sessionID, "session", "", "USER.md interview session ID")
	return cmd
}

func runInterviewMCP(ctx context.Context, addr, sessionID string, in io.Reader, out io.Writer) error {
	return serveStdioMCP(ctx, "podiom-interview", interviewMCPTools(addr, sessionID), in, out)
}

func interviewMCPTools(addr, sessionID string) []mcpTool {
	factArray := map[string]any{
		"type":     "array",
		"minItems": 1,
		"maxItems": 5,
		"items": map[string]any{
			"type":     "object",
			"required": []string{"label", "value"},
			"properties": map[string]any{
				"label": map[string]any{"type": "string", "description": "A short field label, such as Name, Role, Tone, or Detail."},
				"value": map[string]any{"type": "string", "description": "A concise fact or directive. Do not refer to the user as they/them."},
			},
		},
	}
	return []mcpTool{
		{
			Name:        "podiom_ask_profile_question",
			Description: "Ask exactly one adaptive USER.md interview question. The first five calls must each use a different required topic. The call blocks until the user answers.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"topic", "header", "question", "options"},
				"properties": map[string]any{
					"topic":        map[string]any{"type": "string", "enum": []string{"identity_context", "communication", "output_preferences", "technical_depth", "collaboration"}},
					"header":       map[string]any{"type": "string", "description": "Short UI label for the question."},
					"question":     map[string]any{"type": "string"},
					"multi_select": map[string]any{"type": "boolean", "default": false},
					"options": map[string]any{
						"type": "array", "minItems": 3, "maxItems": 5,
						"items": map[string]any{
							"type": "object", "required": []string{"label", "description"},
							"properties": map[string]any{
								"label":       map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				return forwardInterviewCall(ctx, addr, sessionID, "questions", args)
			},
		},
		{
			Name:        "podiom_submit_user_profile",
			Description: "Submit structured, labeled USER.md facts after all five required interview topics have been answered. Podiom renders the Markdown and sends it to the user for review; this does not save USER.md.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"identity_context", "communication", "output_preferences", "technical_context", "working_together"},
				"properties": map[string]any{
					"identity_context":   factArray,
					"communication":      factArray,
					"output_preferences": factArray,
					"technical_context":  factArray,
					"working_together":   factArray,
				},
			},
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				return forwardInterviewCall(ctx, addr, sessionID, "draft", args)
			},
		},
	}
}

func forwardInterviewCall(ctx context.Context, addr, sessionID, action string, args json.RawMessage) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("session id is required")
	}
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage(`{}`)
	}
	url := "http://" + addr + "/api/interviews/" + sessionID + "/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(args))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	setGatewayToken(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("podiom interview API status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return "ok", nil
	}
	return string(bytes.TrimSpace(body)), nil
}
