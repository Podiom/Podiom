// Command podiomd is the long-running Podiom daemon: it owns all session, agent,
// and schedule state and serves the web UI + API. In Phase 0 it resolves the
// storage root, scaffolds ~/.podiom/ on first run, opens the database, and serves
// a health endpoint and the embedded SPA.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/buildinfo"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/dream"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/hamode"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/schedule"
	"github.com/Podiom/Podiom/internal/server"
	"github.com/Podiom/Podiom/internal/skills"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/Podiom/Podiom/internal/usage"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "podiomd",
		Short: "The Podiom orchestration daemon",
		Long: "podiomd is the long-running Podiom daemon. It owns session, agent, and\n" +
			"schedule state, serves the web UI and API, and runs the embedded scheduler.\n" +
			"All state lives under $PODIOM_HOME (default ~/.podiom/).",
		Version:       fmt.Sprintf("%s (%s)", buildinfo.Version, buildinfo.Commit),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run()
		},
	}
	cmd.AddCommand(newPermissionMCPCmd())
	cmd.AddCommand(newPlanMCPCmd())
	return cmd
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	home, err := config.ResolveHome()
	if err != nil {
		return fmt.Errorf("resolve storage root: %w", err)
	}
	paths := config.NewPaths(home)

	res, err := config.Scaffold(paths)
	if err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}

	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		return err
	}
	fileLog, closer, err := podiomlog.Open(podiomlog.Options{
		Dir:           paths.LogsDir,
		RetentionDays: cfg.Logging.RetentionDays,
		Level:         cfg.Logging.Level,
		Stderr:        os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer closer.Close()
	log = fileLog
	log.Info("storage root", "home", paths.Home)
	if res.CreatedHome {
		log.Info("initialized fresh storage root", "home", paths.Home)
	}
	if res.CreatedConfig {
		log.Info("wrote default config", "path", paths.ConfigYAML)
	}
	log.Info("config loaded",
		"provider", cfg.Global.Provider,
		"agents", len(cfg.Agents),
		"profiles", len(cfg.Profiles),
		"log_dir", paths.LogsDir,
		"log_retention_days", cfg.Logging.RetentionDays,
	)

	// Refresh the skills union on start so the catalogue and provider exposure
	// stay current without a manual `podiom skills relink` (S12). Best-effort:
	// a failure here must not stop the daemon from serving.
	if rep, err := skills.Sync(); err != nil {
		log.Warn("skills union refresh failed", "error", err)
	} else {
		linked := 0
		for _, a := range rep.Actions {
			if a.Status == "linked" {
				linked++
			}
		}
		log.Info("skills union refreshed", "canonical", rep.Canonical, "new_links", linked)
	}

	db, err := store.Open(paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("database open", "path", paths.DB)

	// The gateway token gates every API/WS client (HA7). Auto-generated on
	// first start; retrieved via `podiom token show` (standalone) or the HA
	// Configuration page (HA app). Log events only — never the value (HA21).
	tokens, created, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		return fmt.Errorf("gateway token: %w", err)
	}
	if created {
		log.Info("gateway token generated", "path", paths.GatewayToken)
	} else {
		log.Info("gateway token ready", "path", paths.GatewayToken)
	}

	haMode := hamode.Detect()
	if haMode {
		log.Info("home assistant mode detected")
	}

	addr := net.JoinHostPort(cfg.Server.Bind, strconv.Itoa(cfg.Server.Port))
	permissionTimeout, err := time.ParseDuration(cfg.Global.PermissionTimeout)
	if err != nil {
		return err
	}
	adapters := map[config.Provider]adapter.Adapter{}
	claude, err := adapter.NewClaude(adapter.ClaudeOptions{
		DaemonAddr:        addr,
		PermissionTimeout: permissionTimeout,
		Logger:            log,
	})
	if err != nil {
		log.Warn("claude adapter unavailable", "error", err)
		adapters[config.ProviderClaude] = adapter.Unavailable{Provider: config.ProviderClaude, Err: err}
	} else {
		adapters[config.ProviderClaude] = claude
	}
	codex, err := adapter.NewCodex(adapter.CodexOptions{
		PermissionTimeout: permissionTimeout,
		Logger:            log,
	})
	if err != nil {
		log.Warn("codex adapter unavailable", "error", err)
		adapters[config.ProviderCodex] = adapter.Unavailable{Provider: config.ProviderCodex, Err: err}
	} else {
		adapters[config.ProviderCodex] = codex
	}
	coreSvc, err := core.New(core.Options{
		Paths:      paths,
		Store:      db,
		Adapter:    adapter.NewRouter(adapters),
		Global:     cfg.Global,
		Profiles:   cfg.Profiles,
		DaemonAddr: addr,
		Logger:     log,
	})
	if err != nil {
		return err
	}
	if err := syncConfiguredAgents(context.Background(), coreSvc, cfg); err != nil {
		return err
	}

	scheduler := schedule.New(schedule.Options{
		Dir:    paths.SchedulesDir,
		Core:   coreSvc,
		Store:  db,
		Logger: log,
	})
	scheduler.Start()
	defer scheduler.Stop()
	log.Info("scheduler started", "dir", paths.SchedulesDir)

	// Usage tracker polls provider plan-limit windows per auth profile. It reads
	// credential files read-only and never writes or logs tokens.
	usageTracker := usage.New(usage.Options{
		Profiles: coreSvc.ListProfileDetails,
		Logger:   log,
	})
	usageTracker.Start()
	defer usageTracker.Stop()
	// Passive enrichment: feed mid-turn provider rate data into the usage cache.
	coreSvc.SetRateStatusHandler(usageTracker.IngestPassive)

	// Web Push: load (or first-time generate) the VAPID keypair and build the
	// notification dispatcher. Failure here is non-fatal — the daemon still runs,
	// just without out-of-app push (in-app toasts/red dots are unaffected).
	var notifier *notify.Dispatcher
	var vapidPublic string
	if keys, err := notify.LoadOrCreateVAPIDKeys(paths.PushDir); err != nil {
		log.Warn("web push disabled: vapid keys unavailable", "error", err)
	} else {
		vapidPublic = keys.Public
		notifier = notify.NewDispatcher(log, notify.NewWebPushChannel(db, keys, "", log))
		log.Info("web push enabled", "vapid_dir", paths.PushDir)
	}

	srv := server.New(server.Options{
		Bind: cfg.Server.Bind,
		Port: cfg.Server.Port,
		Build: server.BuildInfo{
			Version: buildinfo.Version,
			Commit:  buildinfo.Commit,
		},
		Core:           coreSvc,
		Scheduler:      scheduler,
		Usage:          usageTracker,
		Paths:          paths,
		GitHub:         cfg.GitHub,
		Marketplace:    cfg.Marketplace,
		Logger:         log,
		Notifier:       notifier,
		VAPIDPublicKey: vapidPublic,
		Tokens:         tokens,
		HAMode:         haMode,
		AllowFrom:      cfg.Server.AllowFrom,
		TerminalProxy:  os.Getenv("PODIOM_TERMINAL_PROXY"),
	})

	// The dream runner needs to know which sessions have a live turn so it never
	// consolidates one mid-flight. Wire it now that the server (turn registry)
	// exists, then start the nightly memory-consolidation loop.
	coreSvc.SetActiveTurnChecker(srv.HasActiveTurn)
	dreamRunner := dream.New(dream.Options{Core: coreSvc, Logger: log})
	dreamRunner.Start()
	defer dreamRunner.Stop()
	log.Info("dream runner started", "dream_time", cfg.Global.DreamTime)

	// Serve until a termination signal arrives, then shut down gracefully.
	errc := make(chan error, 1)
	go func() { errc <- srv.Start() }()
	log.Info("podiomd listening", "addr", srv.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func syncConfiguredAgents(ctx context.Context, coreSvc *core.Core, cfg *config.Config) error {
	for _, a := range cfg.Agents {
		agent, err := coreSvc.GetAgent(ctx, a.Name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				if _, err := coreSvc.CreateAgent(ctx, core.CreateAgentRequest{
					Name:           a.Name,
					Provider:       a.Provider,
					Profile:        a.Profile,
					Model:          a.Model,
					Effort:         a.Effort,
					PermissionMode: a.PermissionMode,
					Fallback:       a.Fallback,
					MCPServers:     a.MCPServers,
					MCPConfig:      a.MCPConfig,
				}); err != nil {
					return err
				}
				continue
			}
			return err
		}
		agent.Provider = a.Provider
		agent.Profile = a.Profile
		agent.Model = a.Model
		agent.Effort = a.Effort
		agent.PermissionMode = a.PermissionMode
		agent.Fallback = a.Fallback
		agent.MCPServers = a.MCPServers
		agent.MCPConfig = a.MCPConfig
		if _, err := coreSvc.UpdateAgent(ctx, agent); err != nil {
			return err
		}
	}
	return nil
}
