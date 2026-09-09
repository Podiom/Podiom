// Command podiom is the thin Podiom CLI client. It always connects to a running
// podiomd (it does not run sessions in-process) so there is a single source of
// runtime truth (R11.1 / D2). In Phase 0 it can report daemon status.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/buildinfo"
	"github.com/Podiom/Podiom/internal/client"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/onboard"
	"github.com/Podiom/Podiom/internal/providercheck"
	"github.com/Podiom/Podiom/internal/updater"
	"github.com/spf13/cobra"
)

func main() {
	root := newRootCmd()
	root.SilenceErrors = true

	if err := root.Execute(); err != nil {
		switch {
		case errors.Is(err, onboard.ErrNoTTY):
			// The command already printed its no-TTY guidance.
		case errors.Is(err, client.ErrDaemonUnreachable):
			flagAddr, _ := root.PersistentFlags().GetString("addr")
			resolved, resolveErr := resolveAddr(flagAddr)
			if resolveErr != nil {
				fmt.Fprintln(os.Stderr, "podiomd is not running.\nStart it with: podiomd")
			} else {
				fmt.Fprintf(os.Stderr, "podiomd is not running (tried %s).\nStart it with: podiomd\n", resolved)
			}
		default:
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var addr string

	root := &cobra.Command{
		Use:   "podiom",
		Short: "Podiom CLI — a thin client for the podiomd daemon",
		Long: "podiom is the command-line client for Podiom. It connects to a running\n" +
			"podiomd daemon, which owns all session, agent, and schedule state. Start the\n" +
			"daemon with `podiomd`.",
		Version:       fmt.Sprintf("%s (%s)", buildinfo.Version, buildinfo.Commit),
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().StringVar(&addr, "addr", "",
		"daemon address host:port (default: from config.yaml, or PODIOM_ADDR)")

	root.AddCommand(newStatusCmd(&addr))
	root.AddCommand(newAgentsCmd(&addr))
	root.AddCommand(newMemoryCmd(&addr))
	root.AddCommand(newProfilesCmd(&addr))
	root.AddCommand(newChatCmd(&addr))
	root.AddCommand(newPlanCmd(&addr))
	root.AddCommand(newSessionsCmd(&addr))
	root.AddCommand(newSchedulesCmd(&addr))
	root.AddCommand(newProjectsCmd(&addr))
	root.AddCommand(newTasksCmd(&addr))
	root.AddCommand(newUsageCmd(&addr))
	root.AddCommand(newSkillsCmd())
	root.AddCommand(newMCPCmd(&addr))
	root.AddCommand(newLogsCmd())
	root.AddCommand(newDoctorCmd(&addr))
	root.AddCommand(newOnboardCmd(&addr))
	root.AddCommand(newUpdateCmd(&addr))
	root.AddCommand(newTokenCmd(&addr))
	return root
}

func newTokenCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Show or rotate the gateway token",
		Long: "The gateway token authenticates every API and WebSocket client of podiomd.\n" +
			"It is auto-generated on first daemon start and stored under $PODIOM_HOME.\n" +
			"Browsers enter it once in the web UI's token screen; the CLI reads it from\n" +
			"disk automatically.",
	}
	cmd.AddCommand(newTokenShowCmd())
	cmd.AddCommand(newTokenRotateCmd(addr))
	return cmd
}

func newTokenShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the current gateway token",
		Long: "Print the gateway token from $PODIOM_HOME/gateway.token. Reads straight from\n" +
			"disk, so it works whether or not the daemon is running.",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.ResolveHome()
			if err != nil {
				return err
			}
			path := config.NewPaths(home).GatewayToken
			token, err := gateway.ReadTokenFile(path)
			if err != nil || token == "" {
				return fmt.Errorf("no gateway token at %s — start podiomd once to generate it", path)
			}
			fmt.Println(token)
			fmt.Fprintln(os.Stderr, "Paste this in the web UI's token screen. Rotate with `podiom token rotate`.")
			return nil
		},
	}
}

func newTokenRotateCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the gateway token",
		Long: "Ask the running daemon to rotate the gateway token. The old token stops\n" +
			"working immediately: web clients are disconnected and must enter the new\n" +
			"value, while CLI clients pick it up from disk automatically.\n" +
			"Rotation goes through the daemon so its in-memory token flips atomically —\n" +
			"start podiomd first.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			token, err := c.RotateToken(cmd.Context())
			if err != nil {
				if errors.Is(err, client.ErrDaemonUnreachable) {
					return fmt.Errorf("%w — rotation needs a running daemon so live clients are cut over atomically", err)
				}
				return err
			}
			fmt.Println(token)
			fmt.Fprintln(os.Stderr, "Token rotated. Web clients must re-enter it; the CLI is already up to date.")
			return nil
		},
	}
}

func newUpdateCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for and apply Podiom releases",
		Long:  "Check GitHub Releases for a newer Podiom build and apply verified updates.",
	}
	cmd.AddCommand(newUpdateCheckCmd())
	cmd.AddCommand(newUpdateApplyCmd(addr))
	cmd.AddCommand(newUpdateHelperCmd())
	return cmd
}

func newUpdateCheckCmd() *cobra.Command {
	var jsonOut bool
	var version string
	cmd := &cobra.Command{
		Use:     "check",
		Short:   "Check GitHub Releases for an update",
		Example: "  podiom update check\n  podiom update check --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := updater.Check(cmd.Context(), updater.Options{
				CurrentVersion: buildinfo.Version,
				CurrentCommit:  buildinfo.Commit,
				Version:        version,
				Home:           updateHome(),
			})
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(status)
			}
			fmt.Printf("current: %s (%s)\n", status.CurrentVersion, status.CurrentCommit)
			fmt.Printf("latest:  %s\n", status.LatestVersion)
			fmt.Printf("asset:   %s\n", status.AssetName)
			if status.UpdateAvailable {
				fmt.Println("status:  update available")
			} else {
				fmt.Println("status:  up to date")
			}
			if status.BlockingReason != "" {
				fmt.Printf("note:    %s\n", status.BlockingReason)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print machine-readable JSON")
	cmd.Flags().StringVar(&version, "version", "latest", "release tag to check, or latest")
	return cmd
}

func newUpdateApplyCmd(addr *string) *cobra.Command {
	var version, installDir string
	var yes, force bool
	cmd := &cobra.Command{
		Use:     "apply",
		Short:   "Download and apply a verified Podiom update",
		Example: "  podiom update apply --yes\n  podiom update apply --version v0.1.123 --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Println("Updating may restart podiomd and interrupt active turns.")
				fmt.Print("Continue? [y/N] ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				line = strings.TrimSpace(strings.ToLower(line))
				if line != "y" && line != "yes" {
					return nil
				}
			}

			if installDir == "" {
				if resolved, err := resolveAddr(*addr); err == nil {
					c := client.New(resolved, client.WithToken(gatewayTokenFromDisk()))
					if _, err := c.Health(cmd.Context()); err == nil {
						result, err := c.ApplyUpdate(cmd.Context(), client.UpdateApplyRequest{Version: version, Force: force})
						if err == nil {
							fmt.Println(result.Message)
							if result.RestartRequired || result.HelperStarted {
								fmt.Println("podiomd is restarting; reconnect in a moment.")
							}
							return nil
						}
						fmt.Fprintf(os.Stderr, "daemon update failed, trying local update: %v\n", err)
					}
				}
			}

			result, err := updater.Apply(cmd.Context(), updater.Options{
				CurrentVersion: buildinfo.Version,
				CurrentCommit:  buildinfo.Commit,
				Version:        version,
				Force:          force,
				InstallDir:     installDir,
				Home:           updateHome(),
			})
			if err != nil {
				return err
			}
			fmt.Println(result.Message)
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "latest", "release tag to install, or latest")
	cmd.Flags().StringVar(&installDir, "install-dir", "", "override binary install directory")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "allow updating dev, dirty, or same-version builds")
	return cmd
}

func newUpdateHelperCmd() *cobra.Command {
	var stageDir, installDir string
	var parentPID int
	var restartDaemon bool
	cmd := &cobra.Command{
		Use:    "helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return updater.RunHelper(stageDir, installDir, parentPID, restartDaemon)
		},
	}
	cmd.Flags().StringVar(&stageDir, "stage-dir", "", "extracted update directory")
	cmd.Flags().StringVar(&installDir, "install-dir", "", "install directory")
	cmd.Flags().IntVar(&parentPID, "parent-pid", 0, "parent process id to wait for")
	cmd.Flags().BoolVar(&restartDaemon, "restart-daemon", false, "restart podiomd after replacement")
	_ = cmd.MarkFlagRequired("stage-dir")
	_ = cmd.MarkFlagRequired("install-dir")
	return cmd
}

func updateHome() string {
	home, err := config.ResolveHome()
	if err != nil {
		return ""
	}
	return home
}

func newDoctorCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Check Podiom, Claude, and Codex readiness",
		Example: "  podiom doctor",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveAddr(*addr)
			if err != nil {
				return err
			}
			c := client.New(resolved)
			if h, err := c.Health(cmd.Context()); err == nil {
				fmt.Printf("podiomd: ready at %s (%s %s)\n", resolved, h.Version, h.Commit)
			} else {
				fmt.Printf("podiomd: not reachable at %s (%v)\n", resolved, err)
			}
			for _, status := range providercheck.CheckAll(cmd.Context(), providercheck.Options{}) {
				state := "missing"
				if (status.LoginChecked && status.LoggedIn) || (status.Found && !status.LoginChecked && status.Ready) {
					state = "ready"
				} else if status.Found {
					if status.LoginChecked {
						state = "installed, not logged in"
					} else {
						state = "login unknown"
					}
				}
				fmt.Printf("%s: %s\n", status.Provider, state)
				if status.Path != "" {
					fmt.Printf("  path: %s\n", status.Path)
				}
				if status.Version != "" {
					fmt.Printf("  version: %s\n", status.Version)
				}
				if status.LoginChecked {
					if status.LoggedIn {
						fmt.Println("  login: logged in")
					} else {
						fmt.Println("  login: not logged in")
					}
				}
				if !status.Ready && status.Error != "" {
					fmt.Printf("  note: %s\n", status.Error)
				}
				if !status.Found && status.InstallHint != "" {
					fmt.Printf("  install: %s\n", status.InstallHint)
				}
				if status.Found && !status.Ready && status.LoginHint != "" {
					fmt.Printf("  login: %s\n", status.LoginHint)
				}
			}
			return nil
		},
	}
}

func newOnboardCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "onboard",
		Aliases: []string{"setup"},
		Short:   "Run first-run setup and create your first agent",
		Example: "  podiom onboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveAddr(*addr)
			if err != nil {
				return err
			}
			err = onboard.Run(cmd.Context(), onboard.Options{
				Addr: resolved,
				In:   os.Stdin,
				Out:  os.Stdout,
				Err:  os.Stderr,
			})
			// A deliberate Ctrl-C/Esc is a clean cancel, not a failure: the wizard
			// already printed its own message, so exit 0 without cobra noise.
			if errors.Is(err, onboard.ErrAborted) {
				fmt.Fprintln(os.Stderr, "Onboarding cancelled. Run `podiom onboard` to resume.")
				return nil
			}
			// No-tty already printed guidance; silence cobra's duplicate Error line.
			if errors.Is(err, onboard.ErrNoTTY) {
				cmd.SilenceErrors = true
				return err
			}
			return err
		},
	}
}

func newProjectsCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Short:   "Inspect the shared project ledger",
		Long:    "Projects are shared, agent-independent resources tracked in ~/.podiom/projects/projects.yaml.",
		Example: "  podiom projects list",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Short:   "List projects",
		Example: "  podiom projects list",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			list, err := c.ListProjects(cmd.Context())
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("no projects yet")
				return nil
			}
			for _, p := range list {
				fmt.Printf("%s\t%s\tstatus=%s\t%s\n", p.ID, p.Name, p.Status, p.Description)
			}
			return nil
		},
	})
	return cmd
}

func newTasksCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tasks",
		Short:   "Inspect roadmap tasks",
		Long:    "Roadmap tasks are units of work on shared projects, assignable to agents and startable on demand.",
		Example: "  podiom tasks list",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Short:   "List roadmap tasks",
		Example: "  podiom tasks list",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			tasks, err := c.ListTasks(cmd.Context())
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				fmt.Println("no tasks yet")
				return nil
			}
			for _, t := range tasks {
				agent := t.AssignedAgent
				if agent == "" {
					agent = "-"
				}
				fmt.Printf("%s\t[%s]\t%s\tagent=%s\tproject=%s\n",
					t.ID[:8], t.Status, t.Title, agent, t.ProjectID)
			}
			return nil
		},
	})
	cmd.AddCommand(newTasksDeleteCmd(addr))
	return cmd
}

func newTasksDeleteCmd(addr *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a roadmap task",
		Long:    "Removes a roadmap task. Any session started from the task is preserved. A task that is in progress must be moved out of in_progress first.",
		Example: "  podiom tasks delete <id>",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirmDelete(os.Stdin, os.Stderr, fmt.Sprintf("Delete task %s? Its session (if any) is kept.", id), yes) {
				return nil
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			if err := c.DeleteTask(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Printf("deleted task %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newStatusCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Report whether podiomd is running and its version",
		Long:    "Connects to the daemon's health endpoint and prints its status, version, and uptime.",
		Example: "  podiom status\n  podiom --addr 127.0.0.1:8787 status",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveAddr(*addr)
			if err != nil {
				return err
			}
			c := client.New(resolved)
			h, err := c.Health(context.Background())
			if err != nil {
				return err
			}
			fmt.Printf("podiomd is live at %s\n", resolved)
			fmt.Printf("  status:  %s\n", h.Status)
			fmt.Printf("  version: %s (%s)\n", h.Version, h.Commit)
			fmt.Printf("  uptime:  %dms\n", h.UptimeMS)
			return nil
		},
	}
}

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Inspect podiomd logs",
		Long:  "Inspect daemon-owned logs under $PODIOM_HOME/logs (default ~/.podiom/logs).",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "path",
		Short:   "Print the active daemon log path",
		Example: "  podiom logs path",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := activeLogPath()
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	})
	cmd.AddCommand(newLogsFollowCmd())
	return cmd
}

func newLogsFollowCmd() *cobra.Command {
	var lines int
	var noFollow bool
	cmd := &cobra.Command{
		Use:     "follow",
		Aliases: []string{"tail"},
		Short:   "Print and follow the active daemon log",
		Example: "  podiom logs follow\n  podiom logs follow -n 200\n  podiom logs follow --no-follow",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := activeLogPath()
			if err != nil {
				return err
			}
			if noFollow {
				tail, err := podiomlog.Tail(path, lines)
				if err != nil {
					return err
				}
				for _, line := range tail {
					fmt.Println(line)
				}
				return nil
			}
			for event := range podiomlog.Follow(cmd.Context(), path, lines, 0) {
				switch event.Type {
				case "line":
					fmt.Println(event.Line)
				case "reopen":
					fmt.Fprintln(os.Stderr, "[podiom logs reopened]")
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of recent lines to print first")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "print recent lines and exit")
	return cmd
}

func activeLogPath() (string, error) {
	home, err := config.ResolveHome()
	if err != nil {
		return "", err
	}
	return podiomlog.Path(config.NewPaths(home).LogsDir), nil
}

func newAgentsCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage Podiom agents",
		Long:  "List, create, update, and delete durable Podiom agents through the running podiomd daemon.",
		Example: "  podiom agents list\n" +
			"  podiom agents create jared --provider claude --permission approve\n" +
			"  podiom agents update jared --generate-soul\n" +
			"  podiom agents delete jared",
	}
	cmd.AddCommand(newAgentsListCmd(addr))
	cmd.AddCommand(newAgentsCreateCmd(addr))
	cmd.AddCommand(newAgentsUpdateCmd(addr))
	cmd.AddCommand(newAgentsDeleteCmd(addr))
	return cmd
}

func newProfilesCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "Manage Claude and Codex auth profiles",
		Long:  "List, create, update, and delete named auth profiles through the running podiomd daemon.",
		Example: "  podiom profiles list\n" +
			"  podiom profiles create work --provider claude\n" +
			"  podiom profiles update work --config-dir ~/.podiom/profiles/claude-work\n" +
			"  podiom profiles delete work --yes",
	}
	cmd.AddCommand(newProfilesListCmd(addr))
	cmd.AddCommand(newProfilesCreateCmd(addr))
	cmd.AddCommand(newProfilesUpdateCmd(addr))
	cmd.AddCommand(newProfilesDeleteCmd(addr))
	return cmd
}

func newProfilesListCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List auth profiles",
		Example: "  podiom profiles list",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			profiles, err := c.ListProfiles(cmd.Context())
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Println("no profiles configured")
				return nil
			}
			for _, profile := range profiles {
				fmt.Printf("%s\tprovider=%s\tpath=%s\n", profile.Name, profile.Provider, profilePath(profile))
			}
			return nil
		},
	}
}

func newProfilesCreateCmd(addr *string) *cobra.Command {
	var provider, configDir, homeDir string
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create an auth profile",
		Example: "  podiom profiles create work --provider claude\n  podiom profiles create codex-main --provider codex --home-dir ~/.podiom/profiles/codex-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			profile, err := c.CreateProfile(cmd.Context(), client.ProfileRequest{
				Name:      args[0],
				Provider:  config.Provider(provider),
				ConfigDir: configDir,
				HomeDir:   homeDir,
			})
			if err != nil {
				return err
			}
			fmt.Printf("created profile %s (%s)\n", profile.Name, profile.Provider)
			fmt.Printf("path: %s\n", profilePath(profile))
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "claude", "provider: claude or codex")
	cmd.Flags().StringVar(&configDir, "config-dir", "", "Claude config directory (default: ~/.podiom/profiles/claude-<name>)")
	cmd.Flags().StringVar(&homeDir, "home-dir", "", "Codex home directory (default: ~/.podiom/profiles/codex-<name>)")
	return cmd
}

func newProfilesUpdateCmd(addr *string) *cobra.Command {
	var provider, configDir, homeDir string
	cmd := &cobra.Command{
		Use:     "update <name>",
		Short:   "Update an auth profile",
		Example: "  podiom profiles update work --config-dir ~/.podiom/profiles/claude-work\n  podiom profiles update codex-main --provider codex --home-dir ~/.podiom/profiles/codex-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			profile, err := c.UpdateProfile(cmd.Context(), args[0], client.ProfileRequest{
				Provider:  config.Provider(provider),
				ConfigDir: configDir,
				HomeDir:   homeDir,
			})
			if err != nil {
				return err
			}
			fmt.Printf("updated profile %s (%s)\n", profile.Name, profile.Provider)
			fmt.Printf("path: %s\n", profilePath(profile))
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "provider: claude or codex (default: keep current)")
	cmd.Flags().StringVar(&configDir, "config-dir", "", "Claude config directory")
	cmd.Flags().StringVar(&homeDir, "home-dir", "", "Codex home directory")
	return cmd
}

func newProfilesDeleteCmd(addr *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Short:   "Delete an auth profile",
		Example: "  podiom profiles delete work --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !confirmDelete(os.Stdin, os.Stderr, fmt.Sprintf("Delete profile %s from config.yaml?", name), yes) {
				return nil
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			if err := c.DeleteProfile(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Printf("deleted profile %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func profilePath(profile config.Profile) string {
	return profile.Dir()
}

func newAgentsListCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List agents",
		Long:    "Fetches durable agents from podiomd and prints their provider and defaults.",
		Example: "  podiom agents list",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			agents, err := c.ListAgents(cmd.Context())
			if err != nil {
				return err
			}
			if len(agents) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no agents yet")
				return nil
			}
			for _, agent := range agents {
				fmt.Printf("%s\tprovider=%s\tprofile=%s\tmodel=%s\teffort=%s\tpermission=%s\tfallback=%s\n",
					agent.Name, agent.Provider, agent.Profile, agent.Model, agent.Effort, agent.PermissionMode,
					formatFallback(agent.Fallback))
			}
			return nil
		},
	}
}

func newAgentsCreateCmd(addr *string) *cobra.Command {
	var provider, profile, model, effort, permission string
	var fallback []string
	var generateSoul bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an agent",
		Long: "Creates a durable agent through podiomd and scaffolds its SOUL.md and workspace/.\n" +
			"The optional per-agent AGENTS.md is left for you to create manually.\n\n" +
			"Each --fallback entry is tried in order when the provider rate-limits: a\n" +
			"profile name, a bare provider (claude|codex, no profile), or \"default\".",
		Example: "  podiom agents create jared\n" +
			"  podiom agents create builder --provider claude --model sonnet --effort medium --permission approve\n" +
			"  podiom agents create builder --provider claude --profile work --fallback work --fallback codex",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			agent, err := c.CreateAgent(cmd.Context(), client.AgentCreateRequest{
				Name:           args[0],
				Provider:       config.Provider(provider),
				Profile:        profile,
				Model:          model,
				Effort:         effort,
				PermissionMode: config.PermissionMode(permission),
				Fallback:       fallback,
			})
			if err != nil {
				return err
			}
			fmt.Printf("created agent %s (%s)\n", agent.Name, agent.Provider)
			if generateSoul {
				result, err := c.GenerateAgentSoul(cmd.Context(), agent.Name, client.AgentGenerateRequest{Save: true})
				if err != nil {
					return fmt.Errorf("generate SOUL.md: %w", err)
				}
				fmt.Printf("generated SOUL.md for %s (%d bytes)\n", result.Agent, len(result.Soul))
			}
			if agent.PermissionMode == config.PermissionYolo {
				fmt.Println("  ⚠ yolo: whole-machine access — every tool call is auto-approved and the")
				fmt.Println("    workspace is NOT a sandbox (R8.31). Use approve mode unless you mean it.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "provider: claude or codex (default from config)")
	cmd.Flags().StringVar(&profile, "profile", "", "auth profile name (must exist and match provider)")
	cmd.Flags().StringVar(&model, "model", "", "default model")
	cmd.Flags().StringVar(&effort, "effort", "", "default reasoning effort (provider-supported level)")
	cmd.Flags().StringVar(&permission, "permission", "", "permission mode: approve, auto, or yolo")
	cmd.Flags().StringArrayVar(&fallback, "fallback", nil, "ordered fallback target (repeatable): profile name, claude|codex, or default")
	cmd.Flags().BoolVar(&generateSoul, "generate-soul", false, "generate and save SOUL.md after creating the agent")
	return cmd
}

func newAgentsUpdateCmd(addr *string) *cobra.Command {
	var provider, profile, model, effort, permission, notes string
	var fallback []string
	var generateSoul, yes bool
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an agent",
		Long: "Updates a durable agent through podiomd. Use --generate-soul to draft a new SOUL.md\n" +
			"through the shared generation endpoint, preview it, then save after confirmation.",
		Example: "  podiom agents update jared --model sonnet\n" +
			"  podiom agents update jared --generate-soul --notes \"make them more direct\"",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if yes && !generateSoul {
				return fmt.Errorf("--yes only applies with --generate-soul")
			}
			if notes != "" && !generateSoul {
				return fmt.Errorf("--notes only applies with --generate-soul")
			}
			req := client.AgentUpdateRequest{}
			changed := false
			if cmd.Flags().Changed("provider") {
				req.Provider = config.Provider(provider)
				changed = true
			}
			if cmd.Flags().Changed("profile") {
				req.Profile = &profile
				changed = true
			}
			if cmd.Flags().Changed("model") {
				req.Model = &model
				changed = true
			}
			if cmd.Flags().Changed("effort") {
				req.Effort = &effort
				changed = true
			}
			if cmd.Flags().Changed("permission") {
				req.PermissionMode = config.PermissionMode(permission)
				changed = true
			}
			if cmd.Flags().Changed("fallback") {
				req.Fallback = &fallback
				changed = true
			}
			if !changed && !generateSoul {
				return fmt.Errorf("nothing to update")
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			name := args[0]
			if changed {
				detail, err := c.UpdateAgent(cmd.Context(), name, req)
				if err != nil {
					return err
				}
				fmt.Printf("updated agent %s\n", detail.Name)
			}
			if !generateSoul {
				return nil
			}
			result, err := c.GenerateAgentSoul(cmd.Context(), name, client.AgentGenerateRequest{Notes: notes})
			if err != nil {
				return fmt.Errorf("generate SOUL.md: %w", err)
			}
			fmt.Print(result.Soul)
			if !yes && !confirmOverwrite(os.Stdin, os.Stderr, fmt.Sprintf("Overwrite %s's SOUL.md?", name)) {
				return nil
			}
			if _, err := c.UpdateAgent(cmd.Context(), name, client.AgentUpdateRequest{Soul: &result.Soul}); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "saved SOUL.md for %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "provider: claude or codex")
	cmd.Flags().StringVar(&profile, "profile", "", "auth profile name")
	cmd.Flags().StringVar(&model, "model", "", "default model")
	cmd.Flags().StringVar(&effort, "effort", "", "default reasoning effort (provider-supported level)")
	cmd.Flags().StringVar(&permission, "permission", "", "permission mode: approve, auto, or yolo")
	cmd.Flags().StringArrayVar(&fallback, "fallback", nil, "replace fallback chain (repeatable): profile name, claude|codex, or default")
	cmd.Flags().BoolVar(&generateSoul, "generate-soul", false, "generate a new SOUL.md and ask before saving")
	cmd.Flags().StringVar(&notes, "notes", "", "extra direction for --generate-soul")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "save generated SOUL.md without prompting")
	return cmd
}

// formatFallback renders an agent's fallback chain for list output.
func formatFallback(chain []string) string {
	if len(chain) == 0 {
		return "-"
	}
	return strings.Join(chain, ",")
}

func newAgentsDeleteCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name>",
		Short:   "Delete an agent",
		Long:    "Archives the agent's sessions into its workspace, deletes the durable agent through podiomd, and removes its config.yaml entry. Agent files under $PODIOM_HOME/agents/<name>/ are preserved.",
		Example: "  podiom agents delete jared",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			confirmation, ok := confirmAgentDeletion(os.Stdin, os.Stderr, name)
			if !ok {
				return nil
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			result, err := c.DeleteAgent(cmd.Context(), name, confirmation)
			if err != nil {
				return err
			}
			fmt.Printf("deleted agent %s\n", name)
			if result.ArchivedSessions > 0 {
				fmt.Printf("archived %d session(s) to %s\n", result.ArchivedSessions, result.ArchivePath)
			}
			return nil
		},
	}
}

func confirmAgentDeletion(in io.Reader, out io.Writer, name string) (string, bool) {
	fmt.Fprintf(out, "This archives sessions for %q into its workspace, removes them from active history, and deletes the agent from Podiom and config.yaml. Agent files are preserved.\n", name)
	fmt.Fprintf(out, "Type %s to delete: ", name)
	line, _ := bufio.NewReader(in).ReadString('\n')
	confirmation := strings.TrimSpace(line)
	if confirmation != name {
		fmt.Fprintln(out, "agent deletion aborted")
		return confirmation, false
	}
	return confirmation, true
}

// confirmDelete asks a simple y/N question before a destructive action. It is
// the CLI analogue of the UI's confirmation modal. Passing yes skips the prompt.
func confirmDelete(in io.Reader, out io.Writer, prompt string, yes bool) bool {
	if yes {
		return true
	}
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return true
	}
	fmt.Fprintln(out, "aborted")
	return false
}

func confirmOverwrite(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return true
	}
	fmt.Fprintln(out, "aborted")
	return false
}

func newChatCmd(addr *string) *cobra.Command {
	var agentName, sessionID string
	var planFirst bool
	cmd := &cobra.Command{
		Use:   "chat <message>",
		Short: "Send one chat turn through podiomd",
		Long: "Sends one message to a Podiom session through the daemon. Provide --agent to start\n" +
			"a new CLI-origin session, or --session to continue an existing one.",
		Example: "  podiom chat --agent jared \"Summarise this workspace\"\n" +
			"  podiom chat --session <session-id> \"Continue\"",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (agentName == "") == (sessionID == "") {
				return fmt.Errorf("provide exactly one of --agent or --session")
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			chatReq := client.ChatRequest{
				AgentName:                      agentName,
				SessionID:                      sessionID,
				Message:                        args[0],
				CreatePlanBeforeImplementation: planFirst,
			}
			return runCLIChat(cmd.Context(), c, chatReq)
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name for a new session")
	cmd.Flags().StringVar(&sessionID, "session", "", "existing session ID to continue")
	cmd.Flags().BoolVar(&planFirst, "plan", false, "create a plan before implementation")
	return cmd
}

func runCLIChat(ctx context.Context, c *client.Client, chatReq client.ChatRequest) error {
	for {
		events, errs := c.Chat(ctx, chatReq)
		printedDelta := false
		followup := ""
		for event := range events {
			switch event.Type {
			case "session":
				if event.Session != nil {
					chatReq.SessionID = event.Session.ID
					chatReq.AgentName = ""
					fmt.Fprintf(os.Stderr, "session %s (%s)\n", event.Session.ID, event.Session.AgentName)
				}
			case "delta":
				fmt.Print(event.Delta)
				printedDelta = true
			case "assistant":
				if !printedDelta {
					fmt.Print(event.Delta)
					printedDelta = true
				}
			case "notice":
				fmt.Fprintf(os.Stderr, "%s\n", event.Notice)
			case "permission_request":
				if event.Request != nil {
					if err := promptPermission(ctx, c, *event.Request); err != nil {
						return err
					}
				}
			case "user_input_request":
				if event.Input != nil {
					decision, text, err := promptUserInput(*event.Input)
					if err != nil {
						return err
					}
					if err := c.DecideUserInput(ctx, event.Input.ID, decision); err != nil {
						return err
					}
					// Turn-ending providers need the answer re-sent as a
					// follow-up turn; blocking providers resume on the decision.
					if info, ok := config.ProviderInfoFor(event.Input.Provider); ok && info.QuestionEndsTurn {
						followup = text
					}
				}
			case "error":
				return errors.New(event.Error)
			}
		}
		if err := <-errs; err != nil {
			return err
		}
		if followup == "" {
			fmt.Println()
			return nil
		}
		if chatReq.SessionID == "" {
			return errors.New("cannot answer provider question without a session")
		}
		chatReq.Message = followup
	}
}

func newPlanCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Review and decide implementation plans",
	}
	cmd.AddCommand(newPlanShowCmd(addr))
	cmd.AddCommand(newPlanStatusCmd(addr))
	cmd.AddCommand(newPlanApproveCmd(addr))
	cmd.AddCommand(newPlanFeedbackCmd(addr))
	cmd.AddCommand(newPlanRejectCmd(addr))
	return cmd
}

func newPlanShowCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "show [session]",
		Short: "Print the current plan Markdown",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			id, err := resolvePlanSession(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			status, err := c.GetPlan(cmd.Context(), id)
			if err != nil {
				return err
			}
			if strings.TrimSpace(status.Plan.Markdown) == "" {
				return fmt.Errorf("session %s has no submitted plan", id)
			}
			fmt.Println(status.Plan.Markdown)
			return nil
		},
	}
}

func newPlanStatusCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status [session]",
		Short: "Show whether a session is awaiting plan approval",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			id, err := resolvePlanSession(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			status, err := c.GetPlan(cmd.Context(), id)
			if err != nil {
				return err
			}
			fmt.Printf("%s\tstate=%s\texplicit=%t", status.SessionID, status.State, status.Explicit)
			if status.Plan.FilePath != "" {
				fmt.Printf("\tfile=%s", status.Plan.FilePath)
			}
			if status.Plan.UpdatedAt != "" {
				fmt.Printf("\tupdated=%s", status.Plan.UpdatedAt)
			}
			fmt.Println()
			return nil
		},
	}
}

func newPlanApproveCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <session>",
		Short: "Approve a submitted plan and continue the build",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			decision, err := c.ApprovePlan(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "approved plan for session %s\n", decision.Session.ID)
			if strings.TrimSpace(decision.NextMessage) == "" {
				return nil
			}
			return runCLIChat(cmd.Context(), c, client.ChatRequest{SessionID: decision.Session.ID, Message: decision.NextMessage})
		},
	}
}

func newPlanFeedbackCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "feedback <session> <text>",
		Short: "Send feedback and ask the agent to revise the plan",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			decision, err := c.FeedbackPlan(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "sent plan feedback for session %s\n", decision.Session.ID)
			return runCLIChat(cmd.Context(), c, client.ChatRequest{SessionID: decision.Session.ID, Message: decision.NextMessage})
		},
	}
}

func newPlanRejectCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reject <session>",
		Short: "Reject a submitted plan and leave plan mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			session, err := c.RejectPlan(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("rejected plan for session %s\n", session.ID)
			return nil
		},
	}
}

func resolvePlanSession(ctx context.Context, c *client.Client, args []string) (string, error) {
	if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
		return args[0], nil
	}
	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return "", err
	}
	var awaiting []string
	for _, s := range sessions {
		if string(s.PlanState) == "awaiting_approval" {
			awaiting = append(awaiting, s.ID)
		}
	}
	if len(awaiting) == 1 {
		return awaiting[0], nil
	}
	if len(awaiting) == 0 {
		return "", errors.New("no session is awaiting plan approval")
	}
	return "", fmt.Errorf("multiple sessions are awaiting plan approval: %s", strings.Join(awaiting, ", "))
}

func newSessionsCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sessions",
		Short:   "Inspect and delete chat sessions",
		Long:    "Durable sessions are the chat threads Podiom keeps for each agent, whether started from the web UI, the CLI, a schedule, or a roadmap task.",
		Example: "  podiom sessions list\n  podiom sessions delete <id>",
	}
	cmd.AddCommand(newSessionsListCmd(addr))
	cmd.AddCommand(newSessionsDeleteCmd(addr))
	return cmd
}

func newSessionsListCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List durable sessions",
		Example: "  podiom sessions list",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			sessions, err := c.ListSessions(cmd.Context())
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("no sessions yet")
				return nil
			}
			for _, s := range sessions {
				name := s.Name
				if name == "" {
					name = "-"
				}
				fmt.Printf("%s\tagent=%s\torigin=%s\t%s\n", s.ID, s.AgentName, s.Origin, name)
			}
			return nil
		},
	}
}

func newSessionsDeleteCmd(addr *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a session and its chat history",
		Long:    "Permanently removes a session and its message history. This cannot be undone.",
		Example: "  podiom sessions delete <id>",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirmDelete(os.Stdin, os.Stderr, fmt.Sprintf("Delete session %s and its chat history?", id), yes) {
				return nil
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			if err := c.DeleteSession(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Printf("deleted session %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newSchedulesCmd(addr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedules",
		Short: "Inspect and trigger scheduled routines",
		Long: "Schedules are self-describing markdown files under ~/.podiom/schedules/.\n" +
			"List their next-run times and run history, or trigger a run on demand.",
		Example: "  podiom schedules list\n  podiom schedules run morning-calendar",
	}
	cmd.AddCommand(newSchedulesListCmd(addr))
	cmd.AddCommand(newSchedulesRunCmd(addr))
	cmd.AddCommand(newSchedulesDeleteCmd(addr))
	return cmd
}

func newSchedulesDeleteCmd(addr *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Short:   "Delete a schedule",
		Long:    "Deletes a schedule's markdown file and its run history through podiomd. Sessions produced by past runs are preserved.",
		Example: "  podiom schedules delete morning-calendar",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !confirmDelete(os.Stdin, os.Stderr, fmt.Sprintf("Delete schedule %q and its run history?", name), yes) {
				return nil
			}
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			if err := c.DeleteSchedule(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Printf("deleted schedule %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newSchedulesListCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List schedules with next-run time and recent runs",
		Long:    "Fetches every schedule file's state from podiomd: timing, agent, permission policy, next run, and recent run history.",
		Example: "  podiom schedules list",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			statuses, err := c.ListSchedules(cmd.Context())
			if err != nil {
				return err
			}
			if len(statuses) == 0 {
				fmt.Println("no schedules (drop a *.md file in ~/.podiom/schedules/)")
				return nil
			}
			for _, s := range statuses {
				if s.ParseError != "" {
					fmt.Printf("%s\t[invalid] %s\n", s.Name, s.ParseError)
					continue
				}
				timing := s.Cron
				if s.Every != "" {
					timing = "every " + s.Every
				}
				// A webhook is a trigger, not a cadence, so it is shown alongside
				// one when a schedule has both and on its own when it has none.
				if s.Webhook {
					if timing == "" {
						timing = "webhook"
					} else {
						timing += "+webhook"
					}
				}
				state := "enabled"
				if !s.Enabled {
					state = "disabled"
				}
				next := "-"
				if s.NextRun != nil {
					next = s.NextRun.Local().Format("Mon 15:04")
				}
				fmt.Printf("%s\t%s\tagent=%s\t%s\t%s\tnext=%s\truns=%d\n",
					s.Name, state, s.Agent, timing, s.RunPermission, next, len(s.Runs))
			}
			return nil
		},
	}
}

func newSchedulesRunCmd(addr *string) *cobra.Command {
	return &cobra.Command{
		Use:     "run <name>",
		Short:   "Trigger a schedule immediately",
		Long:    "Runs a schedule on demand through podiomd. The run creates a durable schedule-origin session you can revisit and continue manually.",
		Example: "  podiom schedules run morning-calendar",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(*addr)
			if err != nil {
				return err
			}
			run, err := c.RunSchedule(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("run %s: %s\n", run.ID, run.Status)
			if run.SessionID != "" {
				fmt.Printf("  session: %s\n", run.SessionID)
			}
			if run.Error != "" {
				fmt.Printf("  error: %s\n", run.Error)
			}
			return nil
		},
	}
}

func promptPermission(ctx context.Context, c *client.Client, req adapter.PermissionRequest) error {
	fmt.Fprintf(os.Stderr, "\npermission requested: %s\ninput: %s\nAllow? [y/N] ", req.ToolName, string(req.Input))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	behavior := "deny"
	decision := adapter.PermissionDecision{Behavior: behavior}
	if strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes") {
		behavior = "allow"
		decision = adapter.PermissionDecision{Behavior: behavior, UpdatedInput: req.Input}
	} else {
		decision = adapter.PermissionDecision{Behavior: behavior, Message: "Denied by user"}
	}
	return c.DecidePermission(ctx, req.ID, decision)
}

func promptUserInput(req adapter.UserInputRequest) (adapter.UserInputDecision, string, error) {
	reader := bufio.NewReader(os.Stdin)
	answers := map[string][]string{}
	for _, q := range req.Questions {
		if q.Header != "" {
			fmt.Fprintf(os.Stderr, "\n%s\n", q.Header)
		} else {
			fmt.Fprintln(os.Stderr)
		}
		fmt.Fprintf(os.Stderr, "%s\n", q.Question)
		for i, option := range q.Options {
			if option.Description != "" {
				fmt.Fprintf(os.Stderr, "  %d. %s — %s\n", i+1, option.Label, option.Description)
			} else {
				fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, option.Label)
			}
		}
		if len(q.Options) == 0 {
			fmt.Fprint(os.Stderr, "Answer: ")
			line, _ := reader.ReadString('\n')
			answers[q.ID] = []string{strings.TrimSpace(line)}
			continue
		}
		if q.MultiSelect {
			fmt.Fprint(os.Stderr, "Choose one or more numbers, comma-separated: ")
		} else {
			fmt.Fprint(os.Stderr, "Choose a number: ")
		}
		line, _ := reader.ReadString('\n')
		selected, err := selectedUserInputOptions(q, line)
		if err != nil {
			return adapter.UserInputDecision{}, "", err
		}
		answers[q.ID] = selected
	}
	decision := adapter.UserInputDecision{Answers: answers}
	return decision, formatUserInputFollowup(req, answers), nil
}

func selectedUserInputOptions(q adapter.UserInputQuestion, line string) ([]string, error) {
	parts := []string{strings.TrimSpace(line)}
	if q.MultiSelect {
		parts = strings.Split(line, ",")
	}
	var selected []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > len(q.Options) {
			return nil, fmt.Errorf("invalid choice %q", part)
		}
		selected = append(selected, q.Options[n-1].Label)
	}
	if len(selected) == 0 {
		return nil, errors.New("at least one answer is required")
	}
	if !q.MultiSelect && len(selected) > 1 {
		return nil, errors.New("choose only one answer")
	}
	return selected, nil
}

func formatUserInputFollowup(req adapter.UserInputRequest, answers map[string][]string) string {
	var b strings.Builder
	if len(req.Questions) == 1 {
		q := req.Questions[0]
		fmt.Fprintf(&b, "Answer to %q: %s", q.Question, strings.Join(answers[q.ID], ", "))
		return b.String()
	}
	b.WriteString("Answers:\n")
	for _, q := range req.Questions {
		fmt.Fprintf(&b, "- %s: %s\n", q.Question, strings.Join(answers[q.ID], ", "))
	}
	return strings.TrimSpace(b.String())
}

func daemonClient(flagAddr string) (*client.Client, error) {
	resolved, err := resolveAddr(flagAddr)
	if err != nil {
		return nil, err
	}
	// Attach the gateway token from disk when present (HA9): same machine,
	// same trust domain, zero friction. Best-effort — a missing file just
	// sends no header, which still works against pre-token daemons.
	return client.New(resolved, client.WithToken(gatewayTokenFromDisk())), nil
}

// gatewayTokenFromDisk reads $PODIOM_HOME/gateway.token, or "" when absent.
func gatewayTokenFromDisk() string {
	home, err := config.ResolveHome()
	if err != nil {
		return ""
	}
	token, err := gateway.ReadTokenFile(config.NewPaths(home).GatewayToken)
	if err != nil {
		return ""
	}
	return token
}

// resolveAddr determines the daemon address with precedence:
//  1. explicit --addr flag,
//  2. PODIOM_ADDR environment variable,
//  3. config.yaml server.bind:server.port under $PODIOM_HOME,
//  4. the built-in default 127.0.0.1:8787.
func resolveAddr(flagAddr string) (string, error) {
	if flagAddr != "" {
		return flagAddr, nil
	}
	if env := os.Getenv("PODIOM_ADDR"); env != "" {
		return env, nil
	}
	home, err := config.ResolveHome()
	if err == nil {
		paths := config.NewPaths(home)
		if cfg, err := config.Load(paths.ConfigYAML); err == nil {
			return net.JoinHostPort(cfg.Server.Bind, strconv.Itoa(cfg.Server.Port)), nil
		}
	}
	return "127.0.0.1:8787", nil
}
