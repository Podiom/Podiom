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
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/buildinfo"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/creds"
	"github.com/Podiom/Podiom/internal/dream"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/hamode"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/providercheck"
	"github.com/Podiom/Podiom/internal/schedule"
	"github.com/Podiom/Podiom/internal/server"
	"github.com/Podiom/Podiom/internal/skills"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/Podiom/Podiom/internal/tokenmeter"
	podiomtools "github.com/Podiom/Podiom/internal/tools"
	"github.com/Podiom/Podiom/internal/usage"
	"github.com/spf13/cobra"
)

// homeAssistantLANPort is the container-side, API-only listener optionally
// published by the HA Supervisor. The add-on manifest declares the matching
// port disabled by default; standalone installs do not start this listener.
const homeAssistantLANPort = 8100

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
	cmd.AddCommand(newPlanMCPCmd(), newProjectMCPCmd())
	cmd.AddCommand(newManageMCPCmd())
	cmd.AddCommand(newInterviewMCPCmd())
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
	daemonBind, daemonPort, addrSource, err := resolveDaemonAddr(
		cfg.Server.Bind,
		cfg.Server.Port,
		os.Getenv("PODIOM_ADDR"),
		res.CreatedConfig,
	)
	if err != nil {
		return err
	}
	cfg.Server.Bind = daemonBind
	cfg.Server.Port = daemonPort
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
	if res.RefreshedBaseAgents {
		log.Info("refreshed base instructions", "path", paths.BaseAgents)
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
	lanPort := 0
	if haMode {
		log.Info("home assistant mode detected")
		lanPort = homeAssistantLANPort
	}

	callbackAddr := internalCallbackAddr(cfg.Server.Bind, cfg.Server.Port)
	permissionTimeout, err := time.ParseDuration(cfg.Global.PermissionTimeout)
	if err != nil {
		return err
	}
	credsStore := creds.New(paths.CredentialsYAML)
	// One shared PATH prefix for every provider process: the toolset is not
	// agent-scoped, so both the per-turn Claude process and the long-lived
	// Codex app-server can carry it.
	toolsetPathDirs := podiomtools.PathDirs(paths.ToolsetDir)
	adapters := map[config.Provider]adapter.Adapter{}
	claude, err := adapter.NewClaude(adapter.ClaudeOptions{
		DaemonAddr:        callbackAddr,
		PodiomHome:        paths.Home,
		PermissionTimeout: permissionTimeout,
		ExtraEnv:          credsStore.EnvPairs,
		ToolsetPathDirs:   toolsetPathDirs,
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
		ExtraEnv:          credsStore.EnvPairs,
		ToolsetPathDirs:   toolsetPathDirs,
		Logger:            log,
	})
	if err != nil {
		log.Warn("codex adapter unavailable", "error", err)
		adapters[config.ProviderCodex] = adapter.Unavailable{Provider: config.ProviderCodex, Err: err}
	} else {
		adapters[config.ProviderCodex] = codex
	}
	// Notifications: build the delivery channels, then the engine that records
	// notifications and hands them to those channels.
	//
	// Channel setup failing is non-fatal. A Podiom without Web Push keys still
	// records every notification and still shows them in the Notification Center;
	// only out-of-app delivery is lost.
	// The installation's identity, needed before any device can register against it.
	// Failure is non-fatal in the same way missing VAPID keys are: notifications are
	// still recorded and still shown, only the transports that need an identity stop.
	installationID, err := config.LoadOrCreateInstallationID(paths.InstallationID)
	if err != nil {
		log.Warn("installation id unavailable: native push disabled", "error", err)
	}

	var channels []notify.Channel
	var vapidPublic string
	if keys, err := notify.LoadOrCreateVAPIDKeys(paths.PushDir); err != nil {
		log.Warn("web push disabled: vapid keys unavailable", "error", err)
	} else {
		vapidPublic = keys.Public
		channels = append(channels, notify.NewWebPushChannel(db, keys, "", log))
		log.Info("web push enabled", "vapid_dir", paths.PushDir)
	}
	// Native push. The daemon registers itself with the relay lazily — the first time a
	// device is registered — so an installation that never uses the mobile app never
	// contacts Podiom infrastructure at all.
	var relayChannel *notify.RelayChannel
	if installationID != "" {
		relayEndpoint := cfg.Notifications.RelayEndpoint()
		if warning := relayEndpointWarning(relayEndpoint); warning != "" {
			log.Warn(warning, "event", "notification", "relay", relayEndpoint)
		}
		if relay := notify.NewRelayChannel(db, relayEndpoint, paths.RelayState,
			installationID, log); relay != nil {
			relayChannel = relay
			channels = append(channels, relay)
			log.Info("native push enabled", "relay", relayEndpoint)
		}
	}
	deviceRegistrar, nativePush := relayInterfaces(relayChannel)
	notifications := notify.New(notify.Options{
		Store:          db,
		Channels:       channels,
		InstallationID: installationID,
		Logger:         log,
	})
	defer notifications.Close()

	coreSvc, err := core.New(core.Options{
		Paths:         paths,
		Store:         db,
		Adapter:       adapter.NewRouter(adapters),
		Global:        cfg.Global,
		Voice:         cfg.Voice,
		Profiles:      cfg.Profiles,
		DaemonAddr:    callbackAddr,
		Logger:        log,
		Credentials:   credsStore,
		Notifications: notifications,
	})
	if err != nil {
		return err
	}
	if err := syncConfiguredAgents(context.Background(), coreSvc, cfg); err != nil {
		return err
	}
	if blocks, err := coreSvc.ReconcileGoalRateLimits(context.Background()); err != nil {
		log.Warn("goal rate-limit reconciliation failed", "error", err)
	} else if len(blocks) > 0 {
		log.Info("goal rate-limit reconciliation created recovery items", "count", len(blocks))
	}

	scheduler := schedule.New(schedule.Options{
		Dir:           paths.SchedulesDir,
		Core:          coreSvc,
		Store:         db,
		Logger:        log,
		Notifications: notifications,
	})
	scheduler.Start()
	defer scheduler.Stop()
	log.Info("scheduler started", "dir", paths.SchedulesDir)

	// Usage tracker polls provider plan-limit windows per auth profile. It reads
	// credential files read-only and never writes or logs tokens.
	usageTracker := usage.New(usage.Options{
		Profiles: coreSvc.ListProfileDetails,
		Logger:   log,
		// An expired token is refreshed by the provider's own CLI: Podiom asks it
		// to and then re-reads the result. It never performs the token exchange.
		Renew: func(ctx context.Context, provider config.Provider, dir string) error {
			return providercheck.RefreshCredentials(ctx, provider, providercheck.Options{ProfileDir: dir})
		},
	})
	usageTracker.Start()
	defer usageTracker.Stop()
	// Passive enrichment: feed mid-turn provider rate data into the usage cache.
	coreSvc.SetRateStatusHandler(usageTracker.IngestPassive)
	// A completed turn has just refreshed the CLI's token; re-read the usage cache
	// then instead of waiting out the poll interval.
	coreSvc.SetTurnEndHandler(func(string, config.Provider) { usageTracker.Kick() })

	// Token meter estimates each session/goal's share of the 5-hour and weekly
	// limits by calibrating Podiom's token throughput against the tracker's
	// reported %-movement. Feed it each turn's billed tokens.
	tokenMeter := tokenmeter.New(usageTracker.Snapshots)
	coreSvc.SetTurnUsageHandler(func(profile string, provider config.Provider, delta int64) {
		tokenMeter.RecordTokens(provider, profile, delta)
	})

	srv := server.New(server.Options{
		Bind:    cfg.Server.Bind,
		Port:    cfg.Server.Port,
		LANPort: lanPort,
		Build: server.BuildInfo{
			Version: buildinfo.Version,
			Commit:  buildinfo.Commit,
		},
		Core:            coreSvc,
		Scheduler:       scheduler,
		Usage:           usageTracker,
		TokenMeter:      tokenMeter,
		Paths:           paths,
		GitHub:          cfg.GitHub,
		Marketplace:     cfg.Marketplace,
		Logger:          log,
		Notifications:   notifications,
		VAPIDPublicKey:  vapidPublic,
		InstallationID:  installationID,
		DeviceRegistrar: deviceRegistrar,
		NativePush:      nativePush,
		Tokens:          tokens,
		HAMode:          haMode,
		AllowFrom:       cfg.Server.AllowFrom,
		Advertise:       cfg.Server.AdvertiseEnabled(),
		TerminalProxy:   os.Getenv("PODIOM_TERMINAL_PROXY"),
	})

	// The dream runner needs to know which sessions have a live turn so it never
	// consolidates one mid-flight. Wire it now that the server (turn registry)
	// exists, then start the nightly memory-consolidation loop.
	coreSvc.SetActiveTurnChecker(srv.HasActiveTurn)
	// Goal tool_use audit events appended during a turn broadcast live so the
	// goal timeline updates in real time as the agent works.
	coreSvc.SetGoalEventHandler(srv.BroadcastGoalEvent)
	// New and updated notifications broadcast to every live client, which is what
	// keeps the Notification Center and its unread badge in sync across devices.
	notifications.SetBroadcaster(srv.BroadcastNotification, srv.BroadcastNotificationUpdate)
	// Permission prompts and live questions exist only in memory for the duration of
	// a turn, so the engine asks the server whether one is still open before offering
	// its actions.
	notifications.SetPendingCheck(srv.RequestPending)
	dreamRunner := dream.New(dream.Options{Core: coreSvc, Logger: log})
	dreamRunner.Start()
	defer dreamRunner.Stop()
	log.Info("dream runner started", "dream_time", cfg.Global.DreamTime)

	// Serve until a termination signal arrives, then shut down gracefully.
	errc := make(chan error, 1)
	go func() { errc <- srv.Start() }()
	log.Info("podiomd listening", "addr", srv.Addr(), "source", addrSource)

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

func resolveDaemonAddr(configBind string, configPort int, envAddr string, defaultConfig bool) (string, int, string, error) {
	if envAddr == "" {
		source := "config"
		if defaultConfig {
			source = "default"
		}
		return configBind, configPort, source, nil
	}

	bind, portText, err := net.SplitHostPort(envAddr)
	if err != nil {
		return "", 0, "", fmt.Errorf("PODIOM_ADDR must be host:port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, "", fmt.Errorf("PODIOM_ADDR has invalid port %q", portText)
	}
	return bind, port, "env", nil
}

func internalCallbackAddr(bind string, port int) string {
	if bind == "" {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	if ip := net.ParseIP(bind); ip != nil && ip.IsUnspecified() {
		if ip.To4() == nil {
			return net.JoinHostPort("::1", strconv.Itoa(port))
		}
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	return net.JoinHostPort(bind, strconv.Itoa(port))
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

// relayInterfaces widens the relay channel to the interfaces the server takes, keeping a
// missing relay genuinely nil.
//
// Assigning a nil *RelayChannel straight into an interface field would produce a non-nil
// interface holding a nil pointer, so the server's "no relay configured" guards would not
// fire and the first device registration would call a method on a nil receiver. That is a
// reachable state, not a theoretical one: an unreadable installation id disables native
// push by design and leaves this pointer nil.
func relayInterfaces(relay *notify.RelayChannel) (notify.DeviceRegistrar, notify.Channel) {
	if relay == nil {
		return nil, nil
	}
	return relay, relay
}

// relayEndpointWarning reports a relay address that will not work, without refusing to
// start over it.
//
// The relay rejects plaintext outright in production, and a token crossing an unencrypted
// hop is worse than no push at all — but a developer pointing at a local relay over http
// is doing so deliberately, so loopback is left alone.
func relayEndpointWarning(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "notifications.relay_url is not a valid URL; native push will fail"
	}
	if parsed.Scheme == "https" {
		return ""
	}
	if host := parsed.Hostname(); host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return ""
	}
	return "notifications.relay_url is not https; the relay refuses plaintext and the credential would cross the network in the clear"
}
