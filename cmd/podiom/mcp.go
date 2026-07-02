package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Podiom/Podiom/internal/client"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers and per-agent assignments",
	}
	cmd.AddCommand(newMCPListCmd(addr))
	cmd.AddCommand(newMCPShowCmd(addr))
	cmd.AddCommand(newMCPAddCmd(addr))
	cmd.AddCommand(newMCPRemoveCmd(addr))
	cmd.AddCommand(newMCPAssignCmd(addr, true))
	cmd.AddCommand(newMCPAssignCmd(addr, false))
	cmd.AddCommand(newMCPCheckCmd(addr))
	return cmd
}

func newMCPListCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the MCP catalogue",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := mcpClient(addr)
			if err != nil {
				return err
			}
			snapshot, err := c.MCPSnapshot(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTRANSPORT\tSOURCES\tENV")
			for _, s := range snapshot.Servers {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Transport, sourceList(s.Sources), envList(s.EnvStatus))
			}
			return tw.Flush()
		},
	}
}

func newMCPShowCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show an MCP server and its assignments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := mcpClient(addr)
			if err != nil {
				return err
			}
			snapshot, err := c.MCPSnapshot(cmd.Context())
			if err != nil {
				return err
			}
			name := args[0]
			var server *podiommcp.Server
			for i := range snapshot.Servers {
				if snapshot.Servers[i].Name == name {
					server = &snapshot.Servers[i]
					break
				}
			}
			if server == nil {
				return fmt.Errorf("no mcp server named %q", name)
			}
			fmt.Printf("%s\n", podiommcp.PrettyYAML(*server))
			fmt.Printf("\nsources: %s\n", sourceList(server.Sources))
			fmt.Printf("env:     %s\n", envList(server.EnvStatus))
			var assigned []string
			for agent, servers := range snapshot.Assignments {
				if containsString(servers, name) {
					assigned = append(assigned, agent)
				}
			}
			if len(assigned) == 0 {
				fmt.Println("agents:  (none)")
			} else {
				fmt.Printf("agents:  %s\n", strings.Join(assigned, ", "))
			}
			return nil
		},
	}
}

func newMCPAddCmd(addr *string) *cobra.Command {
	var transport, url, command string
	var args, envs []string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or replace a Podiom-owned MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, pos []string) error {
			server := podiommcp.Server{
				Name:      pos[0],
				Transport: podiommcp.Transport(transport),
				URL:       url,
				Command:   command,
				Args:      args,
				EnvVars:   envs,
			}
			c, err := mcpClient(addr)
			if err != nil {
				return err
			}
			_, err = c.UpsertMCPServer(cmd.Context(), server)
			if err != nil {
				return err
			}
			fmt.Printf("saved mcp server %s\n", server.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&transport, "transport", "stdio", "transport: stdio or http")
	cmd.Flags().StringVar(&url, "url", "", "HTTP MCP URL")
	cmd.Flags().StringVar(&command, "command", "", "stdio command")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "stdio command arg (repeatable)")
	cmd.Flags().StringArrayVar(&envs, "env", nil, "environment variable name to track (repeatable)")
	return cmd
}

func newMCPRemoveCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a Podiom-owned MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := mcpClient(addr)
			if err != nil {
				return err
			}
			if _, err := c.RemoveMCPServer(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("removed mcp server %s\n", args[0])
			return nil
		},
	}
}

func newMCPAssignCmd(addr *string, assigned bool) *cobra.Command {
	use := "assign <server> <agent>"
	short := "Assign an MCP server to an agent"
	if !assigned {
		use = "unassign <server> <agent>"
		short = "Unassign an MCP server from an agent"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := mcpClient(addr)
			if err != nil {
				return err
			}
			_, err = c.SetMCPAssignment(cmd.Context(), client.MCPAssignmentRequest{
				ServerName: args[0],
				AgentName:  args[1],
				Assigned:   assigned,
			})
			if err != nil {
				return err
			}
			if assigned {
				fmt.Printf("assigned %s to %s\n", args[0], args[1])
			} else {
				fmt.Printf("unassigned %s from %s\n", args[0], args[1])
			}
			return nil
		},
	}
}

func newMCPCheckCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "check <agent>",
		Short: "Dry-run MCP projection for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := mcpClient(addr)
			if err != nil {
				return err
			}
			snapshot, err := c.MCPSnapshot(cmd.Context())
			if err != nil {
				return err
			}
			assigned, ok := snapshot.Assignments[args[0]]
			if !ok {
				return fmt.Errorf("unknown agent %q", args[0])
			}
			checks := podiommcp.Checks(podiommcp.Catalogue{Servers: snapshot.Servers}, assigned)
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "SERVER\tCLAUDE\tCODEX\tNOTE")
			for _, ch := range checks {
				if !ch.Assigned {
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", ch.Server, ch.Claude, ch.Codex, ch.Reason)
			}
			return tw.Flush()
		},
	}
}

func mcpClient(addr *string) (*client.Client, error) {
	resolved, err := resolveAddr(*addr)
	if err != nil {
		return nil, err
	}
	return client.New(resolved), nil
}

func sourceList(sources []podiommcp.Source) string {
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ",")
}

func envList(envs []podiommcp.EnvStatus) string {
	if len(envs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(envs))
	for _, e := range envs {
		state := "unset"
		if e.Set {
			state = "set"
		}
		parts = append(parts, e.Name+"="+state)
	}
	return strings.Join(parts, ",")
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
