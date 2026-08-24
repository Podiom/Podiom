package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/Podiom/Podiom/internal/store"
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
	cmd.AddCommand(newUsageTokensCmd(addr))
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

func newUsageTokensCmd(addr *string) *cobra.Command {
	var agent string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "tokens",
		Short: "Show token usage across sessions",
		Long:  "Aggregates and displays token usage (input, output, cache) from all sessions, optionally filtered by agent.",
		Example: "  podiom usage tokens\n" +
			"  podiom usage tokens --agent jared\n" +
			"  podiom usage tokens --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			sessions, err := c.ListSessions(cmd.Context())
			if err != nil {
				return err
			}

			// Filter by agent if specified
			if agent != "" {
				var filtered []store.Session
				for _, s := range sessions {
					if s.AgentName == agent {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Aggregate by agent
			stats := aggregateTokenUsage(sessions)

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(stats)
			}

			if agent != "" && len(stats.ByAgent) == 1 {
				// Single agent detail view
				formatTokensDetail(os.Stdout, stats.ByAgent[0], aggregateByModel(sessions))
			} else {
				// Summary table view
				formatTokensTable(os.Stdout, stats)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "filter by agent name")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")
	return cmd
}

// TokenStats holds aggregated token usage.
type TokenStats struct {
	TotalSessions int              `json:"total_sessions"`
	Total         store.SessionUsage `json:"total"`
	ByAgent       []AgentTokenStats  `json:"by_agent"`
}

// AgentTokenStats holds per-agent token usage.
type AgentTokenStats struct {
	Agent    string             `json:"agent"`
	Sessions int                `json:"sessions"`
	Usage    store.SessionUsage `json:"usage"`
}

// ModelTokenStats holds per-model token usage.
type ModelTokenStats struct {
	Model string `json:"model"`
	Total int64  `json:"total"`
}

func aggregateTokenUsage(sessions []store.Session) TokenStats {
	byAgent := make(map[string]*AgentTokenStats)
	var total store.SessionUsage

	for _, s := range sessions {
		total = total.Add(s.Usage)

		agent := s.AgentName
		if agent == "" {
			agent = "(none)"
		}
		if _, ok := byAgent[agent]; !ok {
			byAgent[agent] = &AgentTokenStats{Agent: agent}
		}
		byAgent[agent].Sessions++
		byAgent[agent].Usage = byAgent[agent].Usage.Add(s.Usage)
	}

	// Convert map to sorted slice
	var agents []AgentTokenStats
	for _, a := range byAgent {
		agents = append(agents, *a)
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Usage.Total() > agents[j].Usage.Total()
	})

	return TokenStats{
		TotalSessions: len(sessions),
		Total:         total,
		ByAgent:       agents,
	}
}

func aggregateByModel(sessions []store.Session) []ModelTokenStats {
	byModel := make(map[string]int64)
	for _, s := range sessions {
		model := s.Model
		if model == "" {
			model = "(default)"
		}
		byModel[model] += s.Usage.Total()
	}

	var models []ModelTokenStats
	for m, t := range byModel {
		models = append(models, ModelTokenStats{Model: m, Total: t})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].Total > models[j].Total
	})
	return models
}

func formatTokensTable(w io.Writer, stats TokenStats) {
	if len(stats.ByAgent) == 0 {
		fmt.Fprintln(w, "no token usage data")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tSESSIONS\tINPUT\tOUTPUT\tCACHE_R\tCACHE_W\tTOTAL")
	for _, a := range stats.ByAgent {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			a.Agent,
			a.Sessions,
			formatTokenCount(a.Usage.InputTokens),
			formatTokenCount(a.Usage.OutputTokens),
			formatTokenCount(a.Usage.CacheReadTokens),
			formatTokenCount(a.Usage.CacheWriteTokens),
			formatTokenCount(a.Usage.Total()),
		)
	}
	fmt.Fprintln(tw, "────────\t────────\t────────\t────────\t────────\t────────\t────────")
	fmt.Fprintf(tw, "TOTAL\t%d\t%s\t%s\t%s\t%s\t%s\n",
		stats.TotalSessions,
		formatTokenCount(stats.Total.InputTokens),
		formatTokenCount(stats.Total.OutputTokens),
		formatTokenCount(stats.Total.CacheReadTokens),
		formatTokenCount(stats.Total.CacheWriteTokens),
		formatTokenCount(stats.Total.Total()),
	)
	tw.Flush()
}

func formatTokensDetail(w io.Writer, agent AgentTokenStats, models []ModelTokenStats) {
	fmt.Fprintf(w, "Agent: %s\n", agent.Agent)
	fmt.Fprintf(w, "Sessions: %d\n\n", agent.Sessions)

	fmt.Fprintln(w, "Token breakdown:")
	fmt.Fprintf(w, "  Input:       %s\n", formatTokenCount(agent.Usage.InputTokens))
	fmt.Fprintf(w, "  Output:      %s\n", formatTokenCount(agent.Usage.OutputTokens))
	fmt.Fprintf(w, "  Cache read:  %s\n", formatTokenCount(agent.Usage.CacheReadTokens))
	fmt.Fprintf(w, "  Cache write: %s\n", formatTokenCount(agent.Usage.CacheWriteTokens))
	fmt.Fprintln(w, "  ─────────────────────")
	fmt.Fprintf(w, "  Total:       %s\n", formatTokenCount(agent.Usage.Total()))

	if len(models) > 0 {
		fmt.Fprintln(w, "\nBy model:")
		for _, m := range models {
			fmt.Fprintf(w, "  %s: %s\n", m.Model, formatTokenCount(m.Total))
		}
	}
}

func formatTokenCount(n int64) string {
	if n == 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
