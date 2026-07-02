package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Podiom/Podiom/internal/skills"
	"github.com/spf13/cobra"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Browse the skills available to your agents",
		Long: "Skills are reusable SKILL.md capability folders. Podiom discovers them under\n" +
			"~/.agents/skills (the shared union), ~/.claude/skills, and ~/.codex/skills, and\n" +
			"shows one deduplicated catalogue. Podiom reads skills; it never installs them.",
		Example: "  podiom skills list\n" +
			"  podiom skills list --source claude\n" +
			"  podiom skills show hello-podiom\n" +
			"  podiom skills paths\n" +
			"  podiom skills relink",
	}
	cmd.AddCommand(newSkillsListCmd())
	cmd.AddCommand(newSkillsShowCmd())
	cmd.AddCommand(newSkillsPathsCmd())
	cmd.AddCommand(newSkillsRelinkCmd("scan"))
	cmd.AddCommand(newSkillsRelinkCmd("relink"))
	return cmd
}

func newSkillsListCmd() *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List every skill, deduplicated, with its source badge(s)",
		Long:    "Prints one row per skill name with its one-line description and source badges (agents, claude, codex). Use --source to filter.",
		Example: "  podiom skills list\n  podiom skills list --source codex",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validSource(source); err != nil {
				return err
			}
			all, err := skills.Scan()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSOURCES\tDESCRIPTION")
			shown := 0
			for _, s := range all {
				if source != "" && !hasSource(s, skills.Source(source)) {
					continue
				}
				shown++
				flag := ""
				if s.Conflict {
					flag = "  ⚠ versions differ"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s%s\n", s.Name, badges(s.Sources), oneLine(s.Description), flag)
			}
			tw.Flush()
			if shown == 0 {
				fmt.Println("\nNo skills found. Drop a SKILL.md folder in ~/.agents/skills/ to add one.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "filter by source: agents, claude, or codex")
	return cmd
}

func newSkillsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <name>",
		Short:   "Print a skill's SKILL.md and where it lives",
		Long:    "Prints the SKILL.md body and the source path(s) for a skill. Flags a conflict if same-named skills differ across sources.",
		Example: "  podiom skills show hello-podiom",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			all, err := skills.Scan()
			if err != nil {
				return err
			}
			var sk *skills.Skill
			for i := range all {
				if all[i].Name == name {
					sk = &all[i]
					break
				}
			}
			if sk == nil {
				return fmt.Errorf("no skill named %q (try `podiom skills list`)", name)
			}
			fmt.Printf("%s — %s\n", sk.Name, sk.Description)
			fmt.Printf("sources: %s\n", badges(sk.Sources))
			fmt.Println("\nwhere it lives:")
			for _, loc := range sk.Locations {
				fmt.Printf("  [%s] %s\n", loc.Source, loc.Path)
			}
			if sk.Conflict {
				fmt.Println("\n⚠ conflict: same-named skills differ across sources (shown per source below).")
			}
			for _, c := range sk.Contents {
				fmt.Println(strings.Repeat("─", 60))
				if c.Source != "" {
					fmt.Printf("[%s] SKILL.md\n", c.Source)
				} else {
					fmt.Println("SKILL.md")
				}
				fmt.Println(strings.Repeat("─", 60))
				fmt.Println(strings.TrimRight(c.Body, "\n"))
			}
			return nil
		},
	}
}

func newSkillsPathsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "paths",
		Short:   "Print the canonical dir and the resolved union topology",
		Long:    "Shows the three skill roots and, for each entry in the union, what it resolves to (a debugging aid for the symlink topology).",
		Example: "  podiom skills paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			roots, err := skills.DefaultRoots()
			if err != nil {
				return err
			}
			fmt.Printf("canonical (shared): %s\n", roots.Agents)
			fmt.Printf("claude:             %s\n", roots.Claude)
			fmt.Printf("codex:              %s\n", roots.Codex)
			fmt.Println("\nunion topology:")
			entries, err := os.ReadDir(roots.Agents)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("  (union dir does not exist yet — run `podiom skills relink`)")
					return nil
				}
				return err
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				link := filepath.Join(roots.Agents, e.Name())
				if target, lerr := os.Readlink(link); lerr == nil {
					fmt.Printf("  %s -> %s\n", e.Name(), target)
				} else {
					fmt.Printf("  %s (real folder)\n", e.Name())
				}
			}
			return nil
		},
	}
}

func newSkillsRelinkCmd(use string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   "Rebuild the ~/.agents/skills union links",
		Long:    "Rebuilds the per-skill symlinks under ~/.agents/skills from whatever exists in ~/.claude/skills and ~/.codex/skills. Idempotent and never overwrites a real folder.",
		Example: "  podiom skills " + use,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := skills.Sync()
			if err != nil {
				return err
			}
			linked, conflicts := 0, 0
			for _, a := range rep.Actions {
				switch a.Status {
				case "linked":
					linked++
					fmt.Printf("linked  %s  (%s)\n", a.Name, a.Source)
				case "conflict":
					conflicts++
					fmt.Printf("conflict %s  (%s) — %s\n", a.Name, a.Source, a.Detail)
				case "error":
					fmt.Printf("error   %s  — %s\n", a.Name, a.Detail)
				}
			}
			fmt.Printf("\nunion: %s — %d new link(s), %d conflict(s)\n", rep.Canonical, linked, conflicts)
			return nil
		},
	}
}

// oneLine collapses internal whitespace so a multi-line frontmatter description
// renders as a single tidy row, truncated to keep the table scannable.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 96 {
		s = s[:95] + "…"
	}
	return s
}

func badges(sources []skills.Source) string {
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ",")
}

func hasSource(s skills.Skill, src skills.Source) bool {
	for _, x := range s.Sources {
		if x == src {
			return true
		}
	}
	return false
}

func validSource(src string) error {
	switch src {
	case "", string(skills.SourceAgents), string(skills.SourceClaude), string(skills.SourceCodex):
		return nil
	}
	return fmt.Errorf("invalid --source %q: use agents, claude, or codex", src)
}
