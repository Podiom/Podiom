// Package core owns Podiom's provider-independent domain behavior: durable
// agents, sessions, history append, and instruction composition.
package core

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

var safeAgentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Options configures a Core service.
type Options struct {
	Paths    config.Paths
	Store    *store.Store
	Adapter  adapter.Adapter
	Global   config.Global
	Voice    config.Voice
	Profiles []config.Profile
	// DaemonAddr is used to expose Podiom-owned MCP helpers to provider
	// processes. Empty disables those helpers, which is useful in unit tests.
	DaemonAddr string
	// Logger receives structured run logging (R11.5). Defaults to slog.Default().
	Logger *slog.Logger
	// DisableBackgroundWork suppresses post-turn helper goroutines in tests that
	// need deterministic teardown of temporary storage.
	DisableBackgroundWork bool
}

// Core coordinates typed persistence, filesystem scaffolding, instruction
// composition, and adapter calls.
type Core struct {
	paths config.Paths
	store *store.Store

	adapter adapter.Adapter
	// mu guards global, voice, and profiles, which the Settings/Profile APIs
	// mutate at runtime after persisting config.yaml.
	mu         sync.RWMutex
	global     config.Global
	voice      config.Voice
	profiles   map[string]config.Profile
	composer   InstructionComposer
	ledger     *projects.Ledger
	log        *slog.Logger
	daemonAddr string
	noBg       bool
	// onRateStatus, when set, receives provider rate-limit updates observed
	// mid-turn (attributed to the session's profile/provider). The usage tracker
	// wires this to IngestPassive; nil disables passive enrichment.
	onRateStatus func(profile string, provider config.Provider, rs adapter.RateStatus)
	// onTurnUsage, when set, receives the billed tokens of a completed turn
	// (attributed to the session's profile/provider) so the token meter can
	// calibrate its token→limit-% estimates. nil disables calibration feeding.
	onTurnUsage func(profile string, provider config.Provider, delta int64)

	// dreamMu guards dreaming map below and serializes dreams per agent so a
	// second concurrent dream for the same agent fails fast (ErrDreamInProgress).
	dreamMu  sync.Mutex
	dreaming map[string]bool
	// activeTurn, when set, reports whether a session currently has an in-flight
	// turn. The dream excludes such sessions so it never contends with live work.
	// nil means "assume no active turns" (e.g. tests without a server).
	activeTurn func(sessionID string) bool

	capMu    sync.Mutex
	capCache map[string]capabilityCacheEntry
}

type capabilityCacheEntry struct {
	caps      capabilities.ProviderCapabilities
	expiresAt time.Time
}

// SetActiveTurnChecker registers a predicate the dream uses to skip sessions that
// have a live turn. Safe to call once during daemon wiring, before turns run.
func (c *Core) SetActiveTurnChecker(fn func(sessionID string) bool) {
	c.activeTurn = fn
}

// SetRateStatusHandler registers a callback for provider rate-limit updates seen
// during a turn. Safe to call once during daemon wiring, before turns run.
func (c *Core) SetRateStatusHandler(fn func(profile string, provider config.Provider, rs adapter.RateStatus)) {
	c.onRateStatus = fn
}

// SetTurnUsageHandler registers a callback for the billed-token total of each
// completed turn. Safe to call once during daemon wiring, before turns run.
func (c *Core) SetTurnUsageHandler(fn func(profile string, provider config.Provider, delta int64)) {
	c.onTurnUsage = fn
}

// New creates a Core service.
func New(opts Options) (*Core, error) {
	if opts.Store == nil {
		return nil, errors.New("core store is required")
	}
	ad := opts.Adapter
	if ad == nil {
		ad = adapter.NewFake()
	}
	global := opts.Global
	if global.Provider == "" {
		global.Provider = config.ProviderClaude
	}
	if global.Effort == "" {
		global.Effort = "medium"
	}
	if global.PermissionMode == "" {
		global.PermissionMode = config.PermissionApprove
	}
	if global.PermissionTimeout == "" {
		global.PermissionTimeout = config.DefaultPermissionTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	c := &Core{
		paths:      opts.Paths,
		store:      opts.Store,
		adapter:    ad,
		global:     global,
		voice:      opts.Voice,
		profiles:   map[string]config.Profile{},
		composer:   NewFileComposer(opts.Paths),
		ledger:     projects.New(opts.Paths.ProjectsDir),
		log:        logger,
		daemonAddr: opts.DaemonAddr,
		noBg:       opts.DisableBackgroundWork,
		dreaming:   map[string]bool{},
		capCache:   map[string]capabilityCacheEntry{},
	}
	for _, profile := range opts.Profiles {
		c.profiles[profile.Name] = profile
	}
	return c, nil
}

// Store exposes the typed persistence API used by this core.
func (c *Core) Store() *store.Store { return c.store }

// AgentPaths returns the well-known filesystem paths for an agent.
func (c *Core) AgentPaths(name string) AgentPaths {
	return agentPaths(c.paths, name)
}

// AgentPaths is the on-disk layout for one agent.
type AgentPaths struct {
	Root      string
	Soul      string
	Agents    string
	Memory    string
	Workspace string
	// Tools is the per-agent workspace-tool directory (installs, manifest);
	// see docs/requirements/workspace-tool-installs.md.
	Tools string
	// Avatar is the agent's uploaded profile picture (always normalized to PNG),
	// or the path where one would live if none has been uploaded.
	Avatar string
}

func validateAgentName(name string) error {
	if !safeAgentName.MatchString(name) {
		return fmt.Errorf("invalid agent name %q: use letters, numbers, dot, dash, or underscore", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid agent name %q: parent path segments are not allowed", name)
	}
	return nil
}

func (c *Core) profileDir(provider config.Provider, name string) string {
	if name == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	profile, ok := c.profiles[name]
	if !ok || profile.Provider != provider {
		return ""
	}
	switch provider {
	case config.ProviderClaude:
		return profile.ConfigDir
	case config.ProviderCodex:
		return profile.HomeDir
	default:
		return ""
	}
}

// ProfileInfo is the credential-free projection of a configured profile, safe
// to expose over the API: it carries only the name and its provider, never the
// underlying config/home directory.
type ProfileInfo struct {
	Name     string          `json:"Name"`
	Provider config.Provider `json:"Provider"`
}

// GetGlobal returns a copy of the daemon-wide defaults. The Fallback slice is
// deep-copied so callers can't mutate Core's state through the alias.
func (c *Core) GetGlobal() config.Global {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := c.global
	g.Fallback = append([]string(nil), c.global.Fallback...)
	return g
}

// SetGlobal replaces the daemon-wide defaults applied to new agents and runs.
// Persisting to config.yaml is the caller's responsibility (see config.SetGlobal).
func (c *Core) SetGlobal(g config.Global) {
	g.Fallback = append([]string(nil), g.Fallback...)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.global = g
}

// GetVoice returns a copy of the voice-input settings.
func (c *Core) GetVoice() config.Voice {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.voice
}

// SetVoice replaces the voice-input settings. Persisting to config.yaml is the
// caller's responsibility (see config.SetVoice).
func (c *Core) SetVoice(v config.Voice) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.voice = v
}

// ListProfiles returns the configured profiles (name + provider only), sorted
// by name for a stable response.
func (c *Core) ListProfiles() []ProfileInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ProfileInfo, 0, len(c.profiles))
	for _, p := range c.profiles {
		out = append(out, ProfileInfo{Name: p.Name, Provider: p.Provider})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListProfileDetails returns configured profiles including their backing
// directories. Those paths are not credentials and are needed by Settings/CLI
// profile management.
func (c *Core) ListProfileDetails() []config.Profile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]config.Profile, 0, len(c.profiles))
	for _, p := range c.profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SetProfiles replaces the configured profile map. Persisting to config.yaml is
// the caller's responsibility.
func (c *Core) SetProfiles(profiles []config.Profile) {
	next := make(map[string]config.Profile, len(profiles))
	for _, p := range profiles {
		next[p.Name] = p
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profiles = next
}
