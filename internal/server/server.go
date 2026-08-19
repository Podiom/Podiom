// Package server is podiomd's HTTP/WebSocket front door. In Phase 0 it serves a
// health endpoint (used by the `podiom` CLI to confirm the daemon is live) and
// the embedded SPA. Later phases add the typed WebSocket contract and REST API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/discovery"
	"github.com/Podiom/Podiom/internal/gateway"
	podiomgithub "github.com/Podiom/Podiom/internal/github"
	"github.com/Podiom/Podiom/internal/marketplace"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/providerlogin"
	"github.com/Podiom/Podiom/internal/schedule"
	"github.com/Podiom/Podiom/internal/tokenmeter"
	"github.com/Podiom/Podiom/internal/usage"
	"nhooyr.io/websocket"
)

// BuildInfo is surfaced on /healthz so clients can see what they're talking to.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpSrv     *http.Server
	addr        string
	lanSrv      *http.Server
	lanAddr     string
	build       BuildInfo
	started     time.Time
	core        *core.Core
	scheduler   *schedule.Scheduler
	usage       *usage.Tracker
	tokenMeter  *tokenmeter.Meter
	github      *podiomgithub.Service
	marketplace *marketplace.Service
	broker      *permissionBroker
	input       *userInputBroker
	interviews  *interviewCoordinator
	fallback    *fallbackBroker
	turns       *activeTurnHub
	// logins drives the provider CLIs' own browser login from Settings, so a
	// profile can be authenticated without a terminal.
	logins *providerlogin.Manager
	// providerStatus caches the per-profile login fan-out; each entry costs a
	// CLI spawn.
	providerStatus providerStatusCache
	paths          config.Paths
	log            *slog.Logger
	notifications  *notify.Engine
	installationID string
	registrar      notify.DeviceRegistrar
	// nativePush is the relay channel, used by the test push to reach the phone
	// directly. Held separately from the engine because a test must not be filtered
	// by a notification preference. Nil when no relay is configured.
	nativePush notify.Channel
	// vapidPublic is the VAPID public key served to browsers so they can create
	// a Web Push subscription bound to this daemon. Empty disables push.
	vapidPublic string
	// tokens is the gateway-token keeper enforcing HA7 on every /api/ request
	// and WebSocket handshake. Nil disables enforcement (bare test servers).
	tokens *gateway.Keeper
	// haMode is true when running as a Home Assistant app: self-update is
	// refused (HA26) and the SPA gets the "ha" deployment hint (HA10).
	haMode bool
	// advertise/bind/port drive the mDNS announcement started in Start (R8).
	advertise bool
	bind      string
	port      int
	// mdns is the live announcement, nil when advertising is off or failed.
	mdns *discovery.Responder
	// transcribeBaseURL overrides the OpenAI Whisper endpoint in tests. Empty
	// means the real API (transcribe.DefaultBaseURL).
	transcribeBaseURL string
	// wsConns tracks live WebSocket connections (with their serialized writers)
	// so a token rotation can force-close them (HA12) and goal events can be
	// broadcast to every open dashboard.
	wsMu    sync.Mutex
	wsConns map[*websocket.Conn]*wsWriter
}

// Options configures the server.
type Options struct {
	Bind string // e.g. "127.0.0.1"
	Port int    // e.g. 8787
	// LANPort enables a second API-only listener for native apps. It is used by
	// the Home Assistant image, whose Supervisor port mapping is disabled by
	// default. Zero disables the listener.
	LANPort   int
	Build     BuildInfo
	Core      *core.Core
	Scheduler *schedule.Scheduler
	Usage     *usage.Tracker
	// TokenMeter estimates a session/goal's share of the 5-hour and weekly limits
	// from its cumulative billed tokens. Optional; nil yields zeroed estimates.
	TokenMeter *tokenmeter.Meter
	Paths      config.Paths
	GitHub     config.GitHub
	Logger     *slog.Logger
	// Notifications turns domain activity into user notifications. Optional; nil
	// disables notifications entirely, which bare test servers rely on.
	Notifications *notify.Engine
	// VAPIDPublicKey is served at GET /api/push/vapid for browser subscription.
	VAPIDPublicKey string
	// DeviceRegistrar mirrors device registrations to the push relay, which needs a
	// routing record before it can resolve a device id. Nil disables mirroring, which is
	// the case when no relay is configured.
	DeviceRegistrar notify.DeviceRegistrar
	// NativePush is the same relay channel the engine delivers through, wired here so
	// the test push can bypass the engine. Nil disables the test push, which is the
	// case when no relay is configured.
	NativePush notify.Channel
	// InstallationID is this installation's stable identity, returned to registering
	// devices so one app can tell several Podioms apart. Independent of address by
	// design, so moving the daemon is not a new installation.
	InstallationID string
	// Tokens enforces the gateway token on the API/WS surface (HA7). Nil
	// disables enforcement.
	Tokens *gateway.Keeper
	// Marketplace configures the skill search/install feature (Spec 07). The
	// SkillsMP API key is not here — it is loaded from env/file by the service.
	Marketplace config.Marketplace
	// HAMode marks a Home Assistant app deployment (see hamode.Detect).
	HAMode bool
	// AllowFrom optionally restricts accepted source addresses (IPs/CIDRs);
	// loopback is always allowed, and HA mode adds the Ingress proxy (HA6).
	AllowFrom []string
	// TerminalProxy, when set (HA app only), is the local ttyd base URL that
	// /terminal/{claude,codex} onboarding sub-paths are reverse-proxied to
	// (HA15/HA22). Empty leaves the sub-paths unrouted as today.
	TerminalProxy string
	// Advertise announces the daemon on the local network over mDNS/DNS-SD so
	// the mobile apps can find it (R8). Ignored when Bind is loopback or in HA
	// mode, where the container cannot advertise Supervisor's selected host port.
	Advertise bool
}

// New constructs a Server bound to the given address. It does not start
// listening; call Start.
func New(opts Options) *Server {
	addr := net.JoinHostPort(opts.Bind, fmt.Sprintf("%d", opts.Port))
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		addr:           addr,
		build:          opts.Build,
		started:        time.Now(),
		core:           opts.Core,
		scheduler:      opts.Scheduler,
		usage:          opts.Usage,
		tokenMeter:     opts.TokenMeter,
		github:         podiomgithub.New(podiomgithub.Options{Config: opts.GitHub, Home: opts.Paths.Home, Logger: log}),
		broker:         newPermissionBroker(log),
		input:          newUserInputBroker(log),
		interviews:     newInterviewCoordinator(),
		fallback:       newFallbackBroker(log),
		turns:          newActiveTurnHub(),
		logins:         providerlogin.New(providerlogin.Options{}),
		paths:          opts.Paths,
		log:            log,
		notifications:  opts.Notifications,
		installationID: opts.InstallationID,
		registrar:      opts.DeviceRegistrar,
		nativePush:     opts.NativePush,
		vapidPublic:    opts.VAPIDPublicKey,
		tokens:         opts.Tokens,
		haMode:         opts.HAMode,
		// HA's opt-in LAN endpoint uses a Supervisor-selected host port that the
		// container cannot advertise correctly.
		advertise: opts.Advertise && !opts.HAMode,
		bind:      opts.Bind,
		port:      opts.Port,
	}
	// Skill marketplace (Spec 07). Construction can fail only if the skills root
	// can't be resolved; degrade to a nil service (handlers return 503) rather
	// than refusing to boot the whole daemon.
	ghToken := func() string {
		if t := s.github.AccessToken(); t != "" {
			return t
		}
		return opts.Marketplace.GitHubToken
	}
	if mp, err := marketplace.New(marketplace.Options{
		GitHubToken:    ghToken,
		MarketplaceDir: opts.Paths.MarketplaceDir,
		MaxSkillBytes:  int64(opts.Marketplace.MaxSkillSizeMB) * 1024 * 1024,
		SearchTTL:      time.Duration(opts.Marketplace.SearchCacheMinutes) * time.Minute,
		DetailTTL:      time.Duration(opts.Marketplace.DetailCacheHours) * time.Hour,
		CuratedOnly:    opts.Marketplace.CuratedOnly,
		Registries:     opts.Marketplace.Registries,
		Version:        opts.Build.Version,
		Logger:         log,
	}); err != nil {
		log.Warn("skill marketplace disabled", "error", err)
	} else {
		s.marketplace = mp
	}
	// Let the turn hub raise notifications when a turn blocks on the user. The hub
	// only knows session ids; the engine resolves the agent name from the session
	// itself, so no resolver has to be threaded through here.
	s.turns.attachNotifications(opts.Notifications)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	for _, rt := range s.apiRoutes() {
		mux.HandleFunc(rt.pattern, rt.handler)
	}
	if opts.TerminalProxy != "" {
		if terminal, err := newTerminalProxy(opts.TerminalProxy); err != nil {
			log.Warn("terminal proxy disabled", "error", err)
		} else {
			mux.Handle("/terminal/", terminal)
			log.Info("terminal proxy enabled", "upstream", opts.TerminalProxy)
		}
	}
	mux.Handle("/", s.spaHandler())

	// Middleware order (outermost first): source-IP guard covers everything
	// including static assets (HA6); CORS then answers native preflights before
	// token auth gates the /api/ surface (HA7/HA10).
	handler := withCORS(s.withAuth(mux))
	guarded, err := buildSourceGuard(handler, opts.AllowFrom, opts.HAMode)
	if err != nil {
		// A malformed allow_from entry must fail closed, not open: refuse all
		// non-loopback traffic and surface the error in the log.
		log.Error("invalid allow_from config; restricting to loopback", "error", err)
		guarded, _ = buildSourceGuard(handler, nil, true)
	}
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           guarded,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if opts.LANPort > 0 {
		// Native apps package the SPA themselves, so the LAN listener needs only
		// health plus the existing API/WS contract. Keeping terminal, static
		// assets, and the HA bootstrap off this mux prevents Ingress-authenticated
		// surfaces from becoming reachable merely by enabling a host port.
		lanMux := http.NewServeMux()
		lanMux.HandleFunc("GET /healthz", s.handleHealth)
		for _, rt := range s.apiRoutes() {
			lanMux.HandleFunc(rt.pattern, rt.handler)
		}
		lanHandler := withCORS(s.withStrictAuth(lanMux))
		lanGuarded, err := buildLANSourceGuard(lanHandler, opts.AllowFrom)
		if err != nil {
			// Config validation normally catches this before New. Stay closed if a
			// caller constructs Options directly with a malformed entry.
			log.Error("invalid allow_from config; disabling LAN API", "error", err)
			lanGuarded = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "forbidden source", http.StatusForbidden)
			})
		}
		s.lanAddr = net.JoinHostPort(opts.Bind, fmt.Sprintf("%d", opts.LANPort))
		s.lanSrv = &http.Server{
			Addr:              s.lanAddr,
			Handler:           lanGuarded,
			ReadHeaderTimeout: 10 * time.Second,
		}
	}
	return s
}

// HasActiveTurn reports whether a session currently has an in-flight turn. The
// dream runner uses it to avoid consolidating a session mid-conversation.
func (s *Server) HasActiveTurn(sessionID string) bool {
	return s.turns.hasRunning(sessionID)
}

// Addr returns the bound address (host:port).
func (s *Server) Addr() string { return s.addr }

// Start begins serving and blocks until the server stops. It returns nil on a
// graceful shutdown.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.addr, err)
	}
	listeners := []struct {
		name string
		srv  *http.Server
		ln   net.Listener
	}{{name: "web", srv: s.httpSrv, ln: ln}}
	if s.lanSrv != nil {
		lanLn, err := net.Listen("tcp", s.lanAddr)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("listen on LAN API %s: %w", s.lanAddr, err)
		}
		listeners = append(listeners, struct {
			name string
			srv  *http.Server
			ln   net.Listener
		}{name: "LAN API", srv: s.lanSrv, ln: lanLn})
		s.log.Info("home assistant LAN API listening", "addr", s.lanAddr)
	}
	// Announce only once the listener exists, so nothing is ever advertised
	// that is not actually accepting connections.
	if s.advertise {
		s.mdns = discovery.Advertise(s.bind, s.port, s.build.Version, s.log)
	}
	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() {
			err := listener.srv.Serve(listener.ln)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			if err != nil {
				err = fmt.Errorf("serve %s: %w", listener.name, err)
			}
			errCh <- err
		}()
	}

	// The two listeners are one daemon. If either fails, stop its peer so the
	// process can be restarted rather than remaining only partly available.
	err = <-errCh
	if err != nil {
		for _, listener := range listeners {
			_ = listener.srv.Close()
		}
	}
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// In-flight logins hold a child CLI open; they must not outlive the daemon.
	if s.logins != nil {
		s.logins.Shutdown()
	}
	// Withdraw the announcement first so clients stop being offered an instance
	// that is on its way down.
	s.mdns.Shutdown()
	servers := []*http.Server{s.httpSrv}
	if s.lanSrv != nil {
		servers = append(servers, s.lanSrv)
	}
	errCh := make(chan error, len(servers))
	for _, httpSrv := range servers {
		go func() { errCh <- httpSrv.Shutdown(ctx) }()
	}
	var errs []error
	for range servers {
		if err := <-errCh; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Health is the /healthz response shape. The CLI's `podiom status` parses this.
type Health struct {
	Status   string    `json:"status"`
	Version  string    `json:"version"`
	Commit   string    `json:"commit"`
	Started  time.Time `json:"started"`
	UptimeMS int64     `json:"uptime_ms"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	h := Health{
		Status:   "ok",
		Version:  s.build.Version,
		Commit:   s.build.Commit,
		Started:  s.started,
		UptimeMS: time.Since(s.started).Milliseconds(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h)
}
