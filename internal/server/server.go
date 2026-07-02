// Package server is podiumd's HTTP/WebSocket front door. In Phase 0 it serves a
// health endpoint (used by the `podium` CLI to confirm the daemon is live) and
// the embedded SPA. Later phases add the typed WebSocket contract and REST API.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/mar-schmidt/Podium/internal/config"
	"github.com/mar-schmidt/Podium/internal/core"
	podiumgithub "github.com/mar-schmidt/Podium/internal/github"
	"github.com/mar-schmidt/Podium/internal/notify"
	"github.com/mar-schmidt/Podium/internal/schedule"
	"github.com/mar-schmidt/Podium/internal/usage"
)

// BuildInfo is surfaced on /healthz so clients can see what they're talking to.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpSrv   *http.Server
	addr      string
	build     BuildInfo
	started   time.Time
	core      *core.Core
	scheduler *schedule.Scheduler
	usage     *usage.Tracker
	github    *podiumgithub.Service
	broker    *permissionBroker
	input     *userInputBroker
	turns     *activeTurnHub
	paths     config.Paths
	log       *slog.Logger
	notifier  *notify.Dispatcher
	// vapidPublic is the VAPID public key served to browsers so they can create
	// a Web Push subscription bound to this daemon. Empty disables push.
	vapidPublic string
}

// Options configures the server.
type Options struct {
	Bind      string // e.g. "127.0.0.1"
	Port      int    // e.g. 8787
	Build     BuildInfo
	Core      *core.Core
	Scheduler *schedule.Scheduler
	Usage     *usage.Tracker
	Paths     config.Paths
	GitHub    config.GitHub
	Logger    *slog.Logger
	// Notifier delivers out-of-app (Web Push / future native) attention
	// notifications. Optional; nil disables out-of-app delivery.
	Notifier *notify.Dispatcher
	// VAPIDPublicKey is served at GET /api/push/vapid for browser subscription.
	VAPIDPublicKey string
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
		addr:        addr,
		build:       opts.Build,
		started:     time.Now(),
		core:        opts.Core,
		scheduler:   opts.Scheduler,
		usage:       opts.Usage,
		github:      podiumgithub.New(podiumgithub.Options{Config: opts.GitHub, Home: opts.Paths.Home, Logger: log}),
		broker:      newPermissionBroker(log),
		input:       newUserInputBroker(log),
		turns:       newActiveTurnHub(),
		paths:       opts.Paths,
		log:         log,
		notifier:    opts.Notifier,
		vapidPublic: opts.VAPIDPublicKey,
	}
	// Let the turn hub raise out-of-app notifications when a turn blocks on the
	// user, resolving the session's agent name for the notification text.
	if opts.Core != nil {
		s.turns.attachNotifier(opts.Notifier, func(ctx context.Context, sessionID string) (string, error) {
			sess, err := opts.Core.GetSession(ctx, sessionID)
			if err != nil {
				return "", err
			}
			return sess.AgentName, nil
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgent)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/", s.handleProfile)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSession)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/ws", s.handleWebSocket)
	mux.HandleFunc("/api/schedules", s.handleSchedules)
	mux.HandleFunc("/api/schedules/", s.handleSchedule)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProject)
	mux.HandleFunc("/api/github/status", s.handleGitHubStatus)
	mux.HandleFunc("/api/github/device/start", s.handleGitHubDeviceStart)
	mux.HandleFunc("/api/github/device/poll", s.handleGitHubDevicePoll)
	mux.HandleFunc("/api/github/repos", s.handleGitHubRepos)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/tasks/", s.handleTask)
	mux.HandleFunc("/api/skills", s.handleSkills)
	mux.HandleFunc("/api/skills/relink", s.handleSkillsRelink)
	mux.HandleFunc("/api/mcp", s.handleMCP)
	mux.HandleFunc("/api/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("/api/mcp/servers/", s.handleMCPServer)
	mux.HandleFunc("/api/mcp/assignments", s.handleMCPAssignments)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/logs/follow", s.handleLogsFollow)
	mux.HandleFunc("/api/permission-decisions/", s.handlePermissionDecision)
	mux.HandleFunc("/api/user-input-decisions/", s.handleUserInputDecision)
	mux.HandleFunc("/api/permissions/", s.handlePermissionRequest)
	mux.HandleFunc("/api/update", s.handleUpdate)
	mux.HandleFunc("/api/update/apply", s.handleUpdateApply)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/usage", s.handleUsage)
	mux.HandleFunc("/api/push/vapid", s.handlePushVAPID)
	mux.HandleFunc("/api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("/api/push/unsubscribe", s.handlePushUnsubscribe)
	mux.Handle("/", spaHandler())

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
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
	if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// Health is the /healthz response shape. The CLI's `podium status` parses this.
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
