package main

import (
	"context"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// newManageMCPCmd builds the hidden `podiomd manage-mcp` subcommand: an internal
// stdio MCP server that lets an agent manage Podiom itself (roadmap items,
// projects, schedules, skills, MCP servers, config, logs, and agent inspection).
// It is injected into every session (see Core.withInternalMCPServers) and
// forwards each tool call to the daemon's own REST API, authenticated with the
// gateway token it inherits via PODIOM_HOME.
func newManageMCPCmd() *cobra.Command {
	var addr string
	var sessionID string
	var agentName string
	cmd := &cobra.Command{
		Use:    "manage-mcp",
		Short:  "Run the internal Podiom self-management MCP helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManageMCP(cmd.Context(), addr, sessionID, agentName, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "podiomd API address")
	cmd.Flags().StringVar(&sessionID, "session", "", "Podiom session ID")
	cmd.Flags().StringVar(&agentName, "agent", "", "Podiom agent name")
	return cmd
}

func runManageMCP(ctx context.Context, addr, sessionID, agentName string, in io.Reader, out io.Writer) error {
	_ = sessionID
	_ = agentName
	c := newManageClient(addr)
	return serveStdioMCP(ctx, "podiom-manage", manageTools(c), in, out)
}
