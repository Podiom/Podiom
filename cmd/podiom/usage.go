package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Podiom/Podiom/internal/usage"
	"github.com/spf13/cobra"
)

func newUsageCmd(addr *string) *cobra.Command {
	var jsonOut, refresh bool
	cmd := &cobra.Command{
		Use:     "usage",
		Short:   "Show provider plan-limit usage per profile",
		Long:    "Reports Claude and Codex plan-limit utilization (5-hour and weekly windows) for each configured auth profile. Data is fetched read-only from provider usage APIs; Podiom never writes provider credentials.",
		Example: "  podiom usage\n  podiom usage --json\n  podiom usage --refresh",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			snaps, err := c.Usage(cmd.Context(), refresh)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(snaps)
			}
			formatUsageTable(os.Stdout, snaps)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print machine-readable JSON")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "force a live re-fetch instead of cached data")
	return cmd
}

// formatUsageTable renders one section per profile. It is pure (writes only to
// w, reads only its args) so it can be table-tested.
func formatUsageTable(w io.Writer, snaps []usage.Snapshot) {
	if len(snaps) == 0 {
		fmt.Fprintln(w, "no usage data yet")
		return
	}
	for i, snap := range snaps {
		if i > 0 {
			fmt.Fprintln(w)
		}
		name := snap.Profile
		if snap.Default {
			name += " (default)"
		}
		plan := snap.Plan
		if plan == "" {
			plan = "-"
		}
		fmt.Fprintf(w, "%s  [%s]  plan=%s  status=%s\n", name, snap.Provider, plan, snap.Status)

		if snap.Status != usage.StatusOK {
			if snap.Error != "" {
				fmt.Fprintf(w, "  %s\n", snap.Error)
			}
			continue
		}
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		for _, win := range snap.Windows {
			fmt.Fprintf(tw, "  %s\t%.0f%%\t%s\n", win.Label, win.UsedPercent, formatResets(win.ResetsAt))
		}
		tw.Flush()
		if snap.Credits != nil && snap.Credits.Enabled {
			fmt.Fprintf(w, "  %s\n", formatCredits(snap.Credits))
		}
	}
}

func formatResets(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Until(t)
	if d <= 0 {
		return "resets now"
	}
	return "resets in " + formatDuration(d)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func formatCredits(c *usage.Credits) string {
	if c.Unlimited {
		return "credits: unlimited"
	}
	if c.MonthlyLimit > 0 {
		return fmt.Sprintf("credits: %.2f/%.2f %s used", c.UsedCredits, c.MonthlyLimit, c.Currency)
	}
	return fmt.Sprintf("credits: %.2f balance", c.Balance)
}
