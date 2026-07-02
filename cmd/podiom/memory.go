package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Podiom/Podiom/internal/store"
	"github.com/spf13/cobra"
)

func newMemoryCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "View, edit, and consolidate an agent's memory",
		Long: "Podiom agents keep a self-curated MEMORY.md that grows through nightly\n" +
			"\"dreaming\" — consolidating the day's sessions into durable memory. These\n" +
			"commands let you read, edit, clear, and trigger that memory.",
		Example: "  podiom memory show jared\n" +
			"  podiom memory edit jared\n" +
			"  podiom memory dream jared\n" +
			"  podiom memory status",
	}
	cmd.AddCommand(newMemoryShowCmd(addr))
	cmd.AddCommand(newMemoryEditCmd(addr))
	cmd.AddCommand(newMemoryClearCmd(addr))
	cmd.AddCommand(newMemoryDreamCmd(addr))
	cmd.AddCommand(newMemoryStatusCmd(addr))
	return cmd
}

func newMemoryShowCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "show <agent>",
		Short:   "Print an agent's MEMORY.md",
		Example: "  podiom memory show jared",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			info, err := c.GetMemory(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(info.Memory) == "" {
				fmt.Printf("%s has no memory yet — nothing has been dreamed.\n", args[0])
				return nil
			}
			fmt.Print(info.Memory)
			if !strings.HasSuffix(info.Memory, "\n") {
				fmt.Println()
			}
			return nil
		},
	}
}

func newMemoryEditCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "edit <agent>",
		Short:   "Edit an agent's MEMORY.md in $EDITOR",
		Long:    "Opens the agent's MEMORY.md in $EDITOR (falling back to vi). Your edits are\nauthoritative: anything you remove will not be re-added by the next dream.",
		Example: "  podiom memory edit jared",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			info, err := c.GetMemory(cmd.Context(), name)
			if err != nil {
				return err
			}
			edited, changed, err := editInEditor(info.Memory)
			if err != nil {
				return err
			}
			if !changed {
				fmt.Println("no changes")
				return nil
			}
			if _, err := c.PutMemory(cmd.Context(), name, edited); err != nil {
				return err
			}
			fmt.Printf("saved memory for %s\n", name)
			return nil
		},
	}
}

func newMemoryClearCmd(addr *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "clear <agent>",
		Short:   "Empty an agent's MEMORY.md",
		Example: "  podiom memory clear jared --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !confirmDelete(os.Stdin, os.Stderr, fmt.Sprintf("Clear all memory for %s?", name), yes) {
				return nil
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			if _, err := c.ClearMemory(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Printf("cleared memory for %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newMemoryDreamCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "dream <agent>",
		Short:   "Consolidate un-dreamed sessions into memory now",
		Long:    "Triggers a dream on demand. If there are no un-dreamed sessions it is a no-op.",
		Example: "  podiom memory dream jared",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			res, err := c.Dream(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if res.NoOp || res.Dream == nil {
				fmt.Println("nothing to dream about — no un-dreamed sessions")
				return nil
			}
			d := res.Dream
			fmt.Printf("dreamed over %d session(s) · kept %d · merged %d · pruned %d\n",
				d.SessionCount, d.Kept, d.Merged, d.Pruned)
			if strings.TrimSpace(d.Note) != "" {
				fmt.Printf("  %s\n", d.Note)
			}
			return nil
		},
	}
}

func newMemoryStatusCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "status [<agent>]",
		Short:   "Show last-dream time and pending sessions",
		Example: "  podiom memory status\n  podiom memory status jared",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tLAST DREAM\tPENDING\tLINES")
			if len(args) == 1 {
				info, err := c.GetMemory(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d/%d\n", info.Agent, lastDreamLabel(info.LastDream),
					info.PendingSessions, info.Lines, info.BudgetLines)
				return tw.Flush()
			}
			rows, err := c.MemoryStatus(cmd.Context())
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Println("no agents")
				return nil
			}
			for _, row := range rows {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d/%d\n", row.Agent, lastDreamLabel(row.LastDream),
					row.PendingSessions, row.MemoryLines, row.BudgetLines)
			}
			return tw.Flush()
		},
	}
}

// lastDreamLabel renders a dream's run time as a short relative string.
func lastDreamLabel(d *store.Dream) string {
	if d == nil {
		return "never"
	}
	t, err := time.Parse(time.RFC3339, d.RanAt)
	if err != nil {
		return d.RanAt
	}
	delta := time.Since(t)
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	}
}

// editInEditor writes content to a temp file, opens it in $EDITOR (or vi), and
// returns the edited content and whether it changed.
func editInEditor(content string) (string, bool, error) {
	f, err := os.CreateTemp("", "podiom-memory-*.md")
	if err != nil {
		return "", false, err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}

	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	// Support editors invoked with args, e.g. EDITOR="code --wait".
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("editor exited: %w", err)
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	return string(edited), string(edited) != content, nil
}
