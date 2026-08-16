package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PermissionMode is Podiom's permission posture, in escalating order (§5.5 / D12).
type PermissionMode string

const (
	// PermissionApprove relays each side-effecting action to the user (default,
	// the only full safety boundary).
	PermissionApprove PermissionMode = "approve"
	// PermissionAuto auto-approves edits inside the session's working directory
	// and leaves everything else asking. Providers express this differently:
	// Claude gets --permission-mode acceptEdits plus the relay for non-edit
	// tools, Codex gets a workspace-write sandbox scoped by codexRuntimeRoots.
	PermissionAuto PermissionMode = "auto"
	// PermissionYolo auto-approves everything with whole-machine access (opt-in).
	PermissionYolo PermissionMode = "yolo"
)

// permissionModes is the accepted set, in escalating order. UI pickers and
// error messages derive from it so a new mode is added in exactly one place.
var permissionModes = []PermissionMode{PermissionApprove, PermissionAuto, PermissionYolo}

// PermissionModes returns the accepted permission modes in escalating order.
func PermissionModes() []PermissionMode {
	out := make([]PermissionMode, len(permissionModes))
	copy(out, permissionModes)
	return out
}

// KnownPermission reports whether m is an accepted permission mode.
func KnownPermission(m PermissionMode) bool {
	for _, mode := range permissionModes {
		if mode == m {
			return true
		}
	}
	return false
}

// PermissionModesLabel renders the set as "approve|auto|yolo" for messages.
func PermissionModesLabel() string {
	parts := make([]string, len(permissionModes))
	for i, mode := range permissionModes {
		parts[i] = string(mode)
	}
	return strings.Join(parts, "|")
}

// Provider identifies a backing CLI.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
)

const (
	DefaultGitHubAppSlug     = "podiom"
	DefaultGitHubClientID    = "Iv23liIKvhvRj9FdIaPD"
	DefaultPermissionTimeout = "3m"
	// DefaultDreamTime is the local time the nightly memory dream runs by default.
	DefaultDreamTime = "03:00"
)

// Config is the parsed config.yaml. It does not define schedules (self-describing
// files, §7) or projects (shared ledger, §5.3) — only agents, profiles, defaults,
// and the server bind (R9.2).
type Config struct {
	Global      Global      `yaml:"global"`
	GitHub      GitHub      `yaml:"github"`
	Marketplace Marketplace `yaml:"marketplace"`
	Profiles    []Profile   `yaml:"profiles"`
	Agents      []Agent     `yaml:"agents"`
	Voice       Voice       `yaml:"voice,omitempty"`
	Server      Server      `yaml:"server"`
	Logging     Logging     `yaml:"logging"`
}

// Voice configures voice input (speech-to-text). The OpenAI key is used
// server-side only, for Whisper transcription calls; it is never returned by
// the API or sent to the browser. Unlike other secrets it lives in config.yaml
// so the Settings UI and the file stay in sync — the PODIOM_OPENAI_API_KEY or
// OPENAI_API_KEY environment variables override it for setups that keep
// secrets out of the file.
type Voice struct {
	OpenAIAPIKey string `yaml:"openai_api_key,omitempty"`
}

// Marketplace configures the skill-marketplace search/install feature (Spec 07).
// Everything here is non-secret and safe to keep in config.yaml. The SkillsMP API
// key is NOT here — it is loaded from PODIOM_SKILLSMP_API_KEY or the 0600 file
// ~/.podiom/marketplace/skillsmp.key so a secret never lands in a public struct.
type Marketplace struct {
	// Registries optionally toggles individual sources on/off (SRC-5). An empty
	// list means "all built-in registries enabled".
	Registries []string `yaml:"registries,omitempty"`
	// CuratedOnly restricts installs to Verified sources (anthropics/skills) —
	// the SEC-8 "curated only" posture. Default false.
	CuratedOnly bool `yaml:"curated_only,omitempty"`
	// MaxSkillSizeMB caps a downloaded skill's total size (FR-14). 0 = default 50.
	MaxSkillSizeMB int `yaml:"max_skill_size_mb,omitempty"`
	// SearchCacheMinutes / DetailCacheHours tune the server-side TTL cache (SRC-4).
	// 0 selects the defaults (15 min / 24 h).
	SearchCacheMinutes int `yaml:"search_cache_minutes,omitempty"`
	DetailCacheHours   int `yaml:"detail_cache_hours,omitempty"`
	// GitHubToken is an OPTIONAL public passthrough for raising anonymous GitHub
	// rate limits (API-3). Prefer the connected device-flow token; this exists for
	// headless setups. It is a coarse fallback, not a secret vault entry.
	GitHubToken string `yaml:"github_token,omitempty"`
}

// Global holds defaults applied across agents unless overridden per agent.
type Global struct {
	Provider          Provider       `yaml:"provider"`
	Profile           string         `yaml:"profile"`
	Model             string         `yaml:"model"`
	Effort            string         `yaml:"effort"`
	PermissionMode    PermissionMode `yaml:"permission_mode"`
	PermissionTimeout string         `yaml:"permission_timeout"`
	Fallback          []string       `yaml:"fallback"`
	// DreamTime is the local wall-clock time ("HH:MM") at which the nightly
	// memory-consolidation ("dreaming") runner fires. Empty means the default.
	DreamTime string `yaml:"dream_time"`
	// CollapseReasoning makes chat fold a finished thinking/working note down to
	// a single clickable line once the turn's answer arrives. Default false:
	// notes stay expanded.
	CollapseReasoning bool `yaml:"collapse_reasoning,omitempty"`
}

// GitHub is optional. Omitted, it defaults to Podiom's official GitHub App
// (podiom) for repo connect + sync; set it only to use your own
// GitHub App instead. These values are public identifiers, not secrets; do not
// add private keys or client secrets here.
type GitHub struct {
	AppSlug   string `yaml:"app_slug"`
	ClientID  string `yaml:"client_id"`
	WebBase   string `yaml:"web_base,omitempty"`
	APIBase   string `yaml:"api_base,omitempty"`
	LoginBase string `yaml:"login_base,omitempty"`
}

// Profile is an optional named auth context, 1:1 with one underlying account
// (§8.7). Podiom owns only the directory path and name — never credentials.
type Profile struct {
	Name     string   `yaml:"name"`
	Provider Provider `yaml:"provider"`
	// ConfigDir is exported as CLAUDE_CONFIG_DIR (Claude profiles).
	ConfigDir string `yaml:"config_dir,omitempty"`
	// HomeDir is exported as CODEX_HOME (Codex profiles).
	HomeDir string `yaml:"home_dir,omitempty"`
}

// Agent is a named colleague maintained by Podiom (§5.1). Empty optional fields
// inherit from Global.
type Agent struct {
	Name           string         `yaml:"name"`
	Provider       Provider       `yaml:"provider,omitempty"`
	Profile        string         `yaml:"profile,omitempty"`
	Model          string         `yaml:"model,omitempty"`
	Effort         string         `yaml:"effort,omitempty"`
	PermissionMode PermissionMode `yaml:"permission_mode,omitempty"`
	Fallback       []string       `yaml:"fallback,omitempty"`
	MCPServers     []string       `yaml:"mcp_servers,omitempty"`
	MCPConfig      string         `yaml:"mcp_config,omitempty"`
}

// Server is the web UI / API bind address.
type Server struct {
	Bind string `yaml:"bind"`
	Port int    `yaml:"port"`
	// AllowFrom optionally restricts which source IPs/CIDRs may connect at
	// all (useful when binding beyond loopback). Loopback is always allowed;
	// empty means no restriction. In HA-app mode the Ingress proxy address is
	// enforced automatically regardless of this list (HA6).
	AllowFrom []string `yaml:"allow_from,omitempty"`
	// Advertise announces this daemon on the local network over mDNS/DNS-SD so
	// the mobile apps can find it without being told an address (R8). Defaults
	// to true, and is ignored when Bind is loopback (nothing else could reach
	// the advertised address) or in HA-app mode (the container cannot advertise
	// the host port selected in Supervisor).
	Advertise *bool `yaml:"advertise,omitempty"`
}

// AdvertiseEnabled reports Advertise with its default applied. A pointer is
// used in the struct so an explicit `advertise: false` is distinguishable from
// the field being absent.
func (s Server) AdvertiseEnabled() bool {
	return s.Advertise == nil || *s.Advertise
}

// Logging configures daemon-owned structured log files under Paths.LogsDir.
type Logging struct {
	RetentionDays int    `yaml:"retention_days"`
	Level         string `yaml:"level"`
}

// Load reads and validates config.yaml at the given path. The file is expected
// to exist (Scaffold writes it on first run); a missing file is an error so the
// daemon fails loudly rather than running on invisible defaults.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := rejectExplicitInvalidLogging(raw); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.resolveProfilePaths(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

// applyDefaults fills zero-valued fields with sane defaults so a minimal
// config.yaml still produces a working configuration.
func (c *Config) applyDefaults() {
	if c.Global.Provider == "" {
		c.Global.Provider = providerInfos[0].ID
	}
	if c.Global.Effort == "" {
		c.Global.Effort = "medium"
	}
	if c.Global.PermissionMode == "" {
		c.Global.PermissionMode = PermissionApprove
	}
	if c.Global.PermissionTimeout == "" {
		c.Global.PermissionTimeout = DefaultPermissionTimeout
	}
	if c.Global.DreamTime == "" {
		c.Global.DreamTime = DefaultDreamTime
	}
	if c.GitHub.AppSlug == "" {
		c.GitHub.AppSlug = DefaultGitHubAppSlug
	}
	if c.GitHub.ClientID == "" {
		c.GitHub.ClientID = DefaultGitHubClientID
	}
	if c.GitHub.WebBase == "" {
		c.GitHub.WebBase = "https://github.com"
	}
	if c.GitHub.APIBase == "" {
		c.GitHub.APIBase = "https://api.github.com"
	}
	if c.GitHub.LoginBase == "" {
		c.GitHub.LoginBase = "https://github.com/login"
	}
	if c.Server.Bind == "" {
		c.Server.Bind = "127.0.0.1"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8787
	}
	if c.Logging.RetentionDays == 0 {
		c.Logging.RetentionDays = 7
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
}

func (c *Config) resolveProfilePaths() error {
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if p.ConfigDir != "" {
			resolved, err := resolveConfigPath(p.ConfigDir)
			if err != nil {
				return fmt.Errorf("profiles[%d] (%s).config_dir: %w", i, p.Name, err)
			}
			p.ConfigDir = resolved
		}
		if p.HomeDir != "" {
			resolved, err := resolveConfigPath(p.HomeDir)
			if err != nil {
				return fmt.Errorf("profiles[%d] (%s).home_dir: %w", i, p.Name, err)
			}
			p.HomeDir = resolved
		}
	}
	return nil
}

func resolveConfigPath(path string) (string, error) {
	expanded, err := expandTilde(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

// Validate checks structural and referential integrity: known enums, unique
// names, profile/provider consistency, and that agent profile references resolve.
func (c *Config) Validate() error {
	if err := validateProvider(c.Global.Provider); err != nil {
		return fmt.Errorf("global.provider: %w", err)
	}
	if err := validatePermission(c.Global.PermissionMode); err != nil {
		return fmt.Errorf("global.permission_mode: %w", err)
	}
	if err := validatePermissionTimeout(c.Global.PermissionTimeout); err != nil {
		return fmt.Errorf("global.permission_timeout: %w", err)
	}
	if err := ValidateDreamTime(c.Global.DreamTime); err != nil {
		return fmt.Errorf("global.dream_time: %w", err)
	}

	profileNames := map[string]Provider{}
	for i, p := range c.Profiles {
		if p.Name == "" {
			return fmt.Errorf("profiles[%d]: name is required", i)
		}
		if reservedProfileName(p.Name) {
			return fmt.Errorf("profiles[%d]: profile name %q is reserved", i, p.Name)
		}
		if _, dup := profileNames[p.Name]; dup {
			return fmt.Errorf("profiles[%d]: duplicate profile name %q", i, p.Name)
		}
		if err := validateProvider(p.Provider); err != nil {
			return fmt.Errorf("profiles[%d] (%s): %w", i, p.Name, err)
		}
		if err := validateProfileDir(p); err != nil {
			return fmt.Errorf("profiles[%d] (%s): %w", i, p.Name, err)
		}
		profileNames[p.Name] = p.Provider
	}
	for i, entry := range c.Global.Fallback {
		if err := validateFallbackEntry(entry, profileNames); err != nil {
			return fmt.Errorf("global.fallback[%d]: %w", i, err)
		}
	}

	agentNames := map[string]bool{}
	for i, a := range c.Agents {
		if a.Name == "" {
			return fmt.Errorf("agents[%d]: name is required", i)
		}
		if agentNames[a.Name] {
			return fmt.Errorf("agents[%d]: duplicate agent name %q", i, a.Name)
		}
		agentNames[a.Name] = true
		if a.Provider != "" {
			if err := validateProvider(a.Provider); err != nil {
				return fmt.Errorf("agents[%d] (%s): %w", i, a.Name, err)
			}
		}
		effectiveProvider := a.Provider
		if effectiveProvider == "" {
			effectiveProvider = c.Global.Provider
		}
		if a.PermissionMode != "" {
			if err := validatePermission(a.PermissionMode); err != nil {
				return fmt.Errorf("agents[%d] (%s): %w", i, a.Name, err)
			}
		}
		if a.Profile != "" {
			profileProvider, ok := profileNames[a.Profile]
			if !ok {
				return fmt.Errorf("agents[%d] (%s): unknown profile %q", i, a.Name, a.Profile)
			}
			if profileProvider != effectiveProvider {
				return fmt.Errorf("agents[%d] (%s): profile %q belongs to provider %q, not %q", i, a.Name, a.Profile, profileProvider, effectiveProvider)
			}
		}
		for j, entry := range a.Fallback {
			if err := validateFallbackEntry(entry, profileNames); err != nil {
				return fmt.Errorf("agents[%d] (%s).fallback[%d]: %w", i, a.Name, j, err)
			}
		}
		seenMCP := map[string]bool{}
		for j, server := range a.MCPServers {
			server = strings.TrimSpace(server)
			if server == "" {
				return fmt.Errorf("agents[%d] (%s).mcp_servers[%d]: server name is required", i, a.Name, j)
			}
			if seenMCP[server] {
				return fmt.Errorf("agents[%d] (%s).mcp_servers[%d]: duplicate server %q", i, a.Name, j, server)
			}
			seenMCP[server] = true
		}
	}

	if err := ValidateVoice(c.Voice); err != nil {
		return fmt.Errorf("voice: %w", err)
	}

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port out of range: %d", c.Server.Port)
	}
	for i, entry := range c.Server.AllowFrom {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return fmt.Errorf("server.allow_from[%d]: entry is empty", i)
		}
		if _, errPrefix := netip.ParsePrefix(entry); errPrefix != nil {
			if _, errAddr := netip.ParseAddr(entry); errAddr != nil {
				return fmt.Errorf("server.allow_from[%d]: %q is neither an IP nor a CIDR", i, entry)
			}
		}
	}
	if c.Logging.RetentionDays < 0 {
		return fmt.Errorf("logging.retention_days must be greater than 0")
	}
	if level := strings.ToLower(strings.TrimSpace(c.Logging.Level)); level != "" {
		switch level {
		case "debug", "info", "warn", "warning", "error":
		default:
			return fmt.Errorf("logging.level %q is invalid (want debug|info|warn|error)", c.Logging.Level)
		}
	}
	return nil
}

func rejectExplicitInvalidLogging(raw []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	doc := root.Content[0]
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "logging" || doc.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		logging := doc.Content[i+1]
		for j := 0; j+1 < len(logging.Content); j += 2 {
			if logging.Content[j].Value != "retention_days" {
				continue
			}
			var days int
			if err := logging.Content[j+1].Decode(&days); err != nil {
				return fmt.Errorf("logging.retention_days: %w", err)
			}
			if days <= 0 {
				return fmt.Errorf("logging.retention_days must be greater than 0")
			}
		}
	}
	return nil
}

// ValidateGlobal checks a standalone Global block (provider, permission, and
// fallback chain) against the configured profile names. It mirrors the global
// checks in Validate so the Settings API can validate an edit without
// reconstructing a full Config. profileNames maps profile name -> its provider;
// pass nil when no named profiles are configured.
func ValidateGlobal(g Global, profileNames map[string]Provider) error {
	if err := validateProvider(g.Provider); err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	if err := validatePermission(g.PermissionMode); err != nil {
		return fmt.Errorf("permission_mode: %w", err)
	}
	if err := validatePermissionTimeout(g.PermissionTimeout); err != nil {
		return fmt.Errorf("permission_timeout: %w", err)
	}
	if g.Profile != "" {
		prov, ok := profileNames[g.Profile]
		if !ok {
			return fmt.Errorf("profile: unknown profile %q", g.Profile)
		}
		if prov != g.Provider {
			return fmt.Errorf("profile: %q belongs to provider %q, not the default provider %q", g.Profile, prov, g.Provider)
		}
	}
	for i, entry := range g.Fallback {
		if err := validateFallbackEntry(entry, profileNames); err != nil {
			return fmt.Errorf("fallback[%d]: %w", i, err)
		}
	}
	return nil
}

// ValidateVoice checks a standalone Voice block. Key formats vary across
// OpenAI key generations, so the only check is a paste-error guard: no
// whitespace inside a non-empty key.
func ValidateVoice(v Voice) error {
	if v.OpenAIAPIKey != "" && strings.ContainsAny(v.OpenAIAPIKey, " \t\r\n") {
		return fmt.Errorf("openai_api_key must not contain whitespace")
	}
	return nil
}

// ValidateProfile checks one profile entry in isolation. Existing names may be
// passed when validating creates/renames; the profile's own current name should
// be omitted for ordinary updates.
func ValidateProfile(p Profile, existing map[string]Provider) error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if reservedProfileName(p.Name) {
		return fmt.Errorf("profile name %q is reserved", p.Name)
	}
	if existing != nil {
		if _, dup := existing[p.Name]; dup {
			return fmt.Errorf("duplicate profile name %q", p.Name)
		}
	}
	if err := validateProvider(p.Provider); err != nil {
		return err
	}
	return validateProfileDir(p)
}

// reservedProfileName reports whether name collides with "default" or a bare
// provider token, both of which have fixed meanings in fallback chains.
func reservedProfileName(name string) bool {
	if name == "default" {
		return true
	}
	return KnownProvider(Provider(name))
}

func validateProfileDir(p Profile) error {
	info, ok := ProviderInfoFor(p.Provider)
	if !ok {
		return nil
	}
	if p.Dir() == "" {
		return fmt.Errorf("%s profile needs %s", info.ID, info.ProfileDirKey)
	}
	return nil
}

func validateProvider(p Provider) error {
	if KnownProvider(p) {
		return nil
	}
	return fmt.Errorf("unknown provider %q (want %s)", p, ProviderIDsLabel())
}

func validatePermission(m PermissionMode) error {
	if KnownPermission(m) {
		return nil
	}
	return fmt.Errorf("unknown permission_mode %q (want %s)", m, PermissionModesLabel())
}

func validatePermissionTimeout(raw string) error {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return err
	}
	if d <= 0 {
		return fmt.Errorf("must be greater than 0")
	}
	return nil
}

// ValidateDreamTime checks that a dream time is empty (meaning default) or a
// valid 24-hour "HH:MM" wall-clock time.
func ValidateDreamTime(raw string) error {
	if raw == "" {
		return nil
	}
	if _, err := time.Parse("15:04", raw); err != nil {
		return fmt.Errorf("must be HH:MM (24-hour): %w", err)
	}
	return nil
}

func validateFallbackEntry(entry string, profileNames map[string]Provider) error {
	if entry == "" {
		return fmt.Errorf("entry is required")
	}
	// "default" = agent's own provider; a bare provider token = that provider
	// with no profile. Both resolve without referencing a named profile.
	if entry == "default" || KnownProvider(Provider(entry)) {
		return nil
	}
	if _, ok := profileNames[entry]; !ok {
		return fmt.Errorf("unknown profile %q", entry)
	}
	return nil
}
