package config

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestServerAdvertiseEnabled(t *testing.T) {
	enabled, disabled := true, false
	for name, tt := range map[string]struct {
		value *bool
		want  bool
	}{
		"absent": {nil, true},
		"true":   {&enabled, true},
		"false":  {&disabled, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := (Server{Advertise: tt.value}).AdvertiseEnabled(); got != tt.want {
				t.Fatalf("AdvertiseEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNotificationsRelayEndpoint(t *testing.T) {
	for input, want := range map[string]string{
		"":                        DefaultRelayURL,
		"   ":                     DefaultRelayURL,
		"https://relay.test/path": "https://relay.test/path",
		"  https://relay.test/  ": "https://relay.test/",
	} {
		if got := (Notifications{RelayURL: input}).RelayEndpoint(); got != want {
			t.Errorf("RelayEndpoint() = %q, want %q", got, want)
		}
	}
}

func TestPermissionModes(t *testing.T) {
	want := []PermissionMode{PermissionApprove, PermissionAuto, PermissionYolo}
	modes := PermissionModes()
	if !slices.Equal(want, modes) {
		t.Errorf("Invalid permission mode, want: %v, got: %v", want, modes)
	}
	// Check if a copy
	modes[0] = "x"
	modes = PermissionModes()
	if !slices.Equal(want, modes) {
		t.Errorf("Invalid permission mode as not a copy, want: %v, got: %v", want, modes)
	}
}

func TestLoadDefaultConfigIsValid(t *testing.T) {
	// The shipped default config.yaml must always load and validate — it's what a
	// fresh install runs on.
	dir := t.TempDir()
	p := NewPaths(dir)
	if _, err := Scaffold(p); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	cfg, err := Load(p.ConfigYAML)
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if cfg.Server.Port != 8787 {
		t.Errorf("default port = %d, want 8787", cfg.Server.Port)
	}
	if cfg.Global.PermissionMode != PermissionApprove {
		t.Errorf("default permission mode = %q, want approve", cfg.Global.PermissionMode)
	}
	if cfg.Global.PermissionTimeout != DefaultPermissionTimeout {
		t.Errorf("default permission timeout = %q, want %s", cfg.Global.PermissionTimeout, DefaultPermissionTimeout)
	}
	if cfg.Logging.RetentionDays != 7 {
		t.Errorf("default log retention = %d, want 7", cfg.Logging.RetentionDays)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level = %q, want info", cfg.Logging.Level)
	}
	if _, err := os.Stat(p.LogsDir); err != nil {
		t.Errorf("logs dir not scaffolded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ProjectsDir, "unassigned", "plans")); err != nil {
		t.Errorf("unassigned plans dir not scaffolded: %v", err)
	}
}

func TestValidateRejectsUnknownProfileReference(t *testing.T) {
	c := &Config{
		Global: Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout},
		Agents: []Agent{{Name: "a", Profile: "ghost"}},
		Server: Server{Bind: "127.0.0.1", Port: 8787},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown profile reference, got nil")
	}
}

func TestValidateRejectsProfileProviderMismatch(t *testing.T) {
	c := &Config{
		Global:   Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout},
		Profiles: []Profile{{Name: "codex-main", Provider: ProviderCodex, HomeDir: "/tmp/codex"}},
		Agents:   []Agent{{Name: "a", Provider: ProviderClaude, Profile: "codex-main"}},
		Server:   Server{Bind: "127.0.0.1", Port: 8787},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for provider/profile mismatch, got nil")
	}
}

func TestValidateChecksFallbackEntries(t *testing.T) {
	c := &Config{
		Global:   Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout, Fallback: []string{"default", "ghost"}},
		Profiles: []Profile{{Name: "work", Provider: ProviderClaude, ConfigDir: "/tmp/claude"}},
		Agents:   []Agent{{Name: "a"}},
		Server:   Server{Bind: "127.0.0.1", Port: 8787},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown fallback profile, got nil")
	}
}

func TestValidateAcceptsBareProviderFallback(t *testing.T) {
	// A fallback entry may be a bare provider token (provider with no profile),
	// not only "default" or a named profile.
	c := &Config{
		Global: Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout},
		Agents: []Agent{{Name: "a", Provider: ProviderClaude, Fallback: []string{"codex", "claude"}}},
		Server: Server{Bind: "127.0.0.1", Port: 8787},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected bare provider fallback to validate, got %v", err)
	}
}

func TestValidateRejectsReservedProfileName(t *testing.T) {
	// Provider tokens are reserved profile names so fallback entries stay
	// unambiguous.
	for _, name := range []string{"claude", "codex"} {
		c := &Config{
			Global:   Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout},
			Profiles: []Profile{{Name: name, Provider: ProviderClaude, ConfigDir: "/tmp/claude"}},
			Server:   Server{Bind: "127.0.0.1", Port: 8787},
		}
		if err := c.Validate(); err == nil {
			t.Fatalf("expected reserved profile name %q to be rejected, got nil", name)
		}
	}
}

func TestValidateRejectsDuplicateAgentNames(t *testing.T) {
	c := &Config{
		Global: Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout},
		Agents: []Agent{{Name: "dup"}, {Name: "dup"}},
		Server: Server{Bind: "127.0.0.1", Port: 8787},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for duplicate agent names, got nil")
	}
}

func TestValidateChecksLoggingConfig(t *testing.T) {
	c := &Config{
		Global:  Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: DefaultPermissionTimeout},
		Server:  Server{Bind: "127.0.0.1", Port: 8787},
		Logging: Logging{RetentionDays: -1, Level: "info"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid retention to be rejected")
	}
	c.Logging.RetentionDays = 7
	c.Logging.Level = "verbose"
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid log level to be rejected")
	}
	c.Logging.Level = "WARN"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected uppercase log level to validate, got %v", err)
	}
}

func TestLoadRejectsExplicitZeroLogRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("global:\n  provider: claude\n  permission_mode: approve\nlogging:\n  retention_days: 0\nserver:\n  bind: 127.0.0.1\n  port: 8787\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected explicit zero retention to be rejected")
	}
}

func TestLoadDefaultsAndValidatesAutoArchiveDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	base := "global:\n  provider: claude\n  permission_mode: approve\nserver:\n  bind: 127.0.0.1\n  port: 8787\n"
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	if cfg.Global.AutoArchiveDays != DefaultAutoArchiveDays {
		t.Fatalf("auto archive days = %d, want %d", cfg.Global.AutoArchiveDays, DefaultAutoArchiveDays)
	}

	invalid := strings.Replace(base, "  permission_mode: approve\n", "  permission_mode: approve\n  auto_archive_days: 0\n", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "auto_archive_days must be greater than 0") {
		t.Fatalf("load explicit zero err = %v", err)
	}
}

func TestLoadExpandsProfileDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte(`global:
  provider: claude
  permission_mode: approve
profiles:
  - name: personal
    provider: claude
    config_dir: ~/.claude-personal
  - name: codex-main
    provider: codex
    home_dir: ~/.codex-main
server:
  bind: 127.0.0.1
  port: 8787
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles[0].ConfigDir != filepath.Join(home, ".claude-personal") {
		t.Fatalf("config_dir = %q", cfg.Profiles[0].ConfigDir)
	}
	if cfg.Profiles[1].HomeDir != filepath.Join(home, ".codex-main") {
		t.Fatalf("home_dir = %q", cfg.Profiles[1].HomeDir)
	}
}

func TestScaffoldIsIdempotentAndPreservesEdits(t *testing.T) {
	dir := t.TempDir()
	p := NewPaths(dir)

	res, err := Scaffold(p)
	if err != nil {
		t.Fatalf("first scaffold: %v", err)
	}
	if !res.CreatedConfig || !res.CreatedBaseAgents || !res.CreatedProjects {
		t.Fatalf("first scaffold should create seed files, got %+v", res)
	}

	// Simulate a user edit, then re-scaffold; the edit must survive.
	edited := []byte("global:\n  provider: codex\n  permission_mode: approve\nserver:\n  bind: 127.0.0.1\n  port: 9001\n")
	if err := os.WriteFile(p.ConfigYAML, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := Scaffold(p)
	if err != nil {
		t.Fatalf("second scaffold: %v", err)
	}
	if res2.CreatedConfig {
		t.Error("second scaffold should not recreate existing config.yaml")
	}
	cfg, err := Load(p.ConfigYAML)
	if err != nil {
		t.Fatalf("load edited config: %v", err)
	}
	if cfg.Server.Port != 9001 || cfg.Global.Provider != ProviderCodex {
		t.Errorf("user edits not preserved: got port=%d provider=%s", cfg.Server.Port, cfg.Global.Provider)
	}
}

// The base AGENTS.md is the deliberate exception to the rule above: it is
// Podiom-generated, not user-seeded, so every start restores the shipped copy.
// Without this an install keeps whichever template version scaffolded its home,
// and instruction fixes never reach anyone who is already running Podiom.
func TestScaffoldRefreshesBaseAgents(t *testing.T) {
	p := NewPaths(t.TempDir())

	res, err := Scaffold(p)
	if err != nil {
		t.Fatalf("first scaffold: %v", err)
	}
	if !res.CreatedBaseAgents || res.RefreshedBaseAgents {
		t.Fatalf("first scaffold should create, not refresh, got %+v", res)
	}

	// An install running a stale template, which is what an upgrade finds.
	if err := os.WriteFile(p.BaseAgents, []byte("# Operating rules\n\nstale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := Scaffold(p)
	if err != nil {
		t.Fatalf("second scaffold: %v", err)
	}
	if res2.CreatedBaseAgents || !res2.RefreshedBaseAgents {
		t.Errorf("stale base AGENTS.md should be refreshed, got %+v", res2)
	}
	got, err := os.ReadFile(p.BaseAgents)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, baseAgentsMD) {
		t.Errorf("base AGENTS.md is not the shipped copy:\n%s", got)
	}

	// Already current: no write, so mtime stays meaningful and the daemon does
	// not log a refresh on every restart.
	res3, err := Scaffold(p)
	if err != nil {
		t.Fatalf("third scaffold: %v", err)
	}
	if res3.CreatedBaseAgents || res3.RefreshedBaseAgents {
		t.Errorf("unchanged base AGENTS.md should be left alone, got %+v", res3)
	}
}

func TestResolveHomeUsesEnvOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom")
	t.Setenv(EnvHome, want)
	got, err := ResolveHome()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ResolveHome() = %q, want %q", got, want)
	}
}

// A relative PODIOM_HOME must be anchored to an absolute path at resolution
// time so a daemon launched from any cwd resolves the same root (R10.2).
func TestResolveHomeMakesRelativeOverrideAbsolute(t *testing.T) {
	t.Setenv(EnvHome, "podiom-data")
	got, err := ResolveHome()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("ResolveHome() = %q, want an absolute path", got)
	}
}

func TestValidateProfile(t *testing.T) {
	existingMap := map[string]Provider{
		"personal": ProviderClaude,
		"work":     ProviderCodex,
	}

	tests := []struct {
		name        string
		profile     Profile
		existing    map[string]Provider
		wantErrText string
	}{
		{
			name: "empty name",
			profile: Profile{
				Name:      "",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude",
			},
			existing:    nil,
			wantErrText: "name is required",
		},
		{
			name: "reserved name default",
			profile: Profile{
				Name:      "default",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude",
			},
			existing:    nil,
			wantErrText: `profile name "default" is reserved`,
		},
		{
			name: "reserved bare provider claude",
			profile: Profile{
				Name:      "claude",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude",
			},
			existing:    nil,
			wantErrText: `profile name "claude" is reserved`,
		},
		{
			name: "reserved bare provider codex",
			profile: Profile{
				Name:     "codex",
				Provider: ProviderCodex,
				HomeDir:  "/tmp/codex",
			},
			existing:    nil,
			wantErrText: `profile name "codex" is reserved`,
		},
		{
			name: "unreserved prefix name valid",
			profile: Profile{
				Name:      "claude-work",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude-work",
			},
			existing:    nil,
			wantErrText: "",
		},
		{
			name: "nil existing map skips duplicate check",
			profile: Profile{
				Name:      "personal",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude",
			},
			existing:    nil,
			wantErrText: "",
		},
		{
			name: "duplicate profile name with existing map",
			profile: Profile{
				Name:      "personal",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude",
			},
			existing:    existingMap,
			wantErrText: `duplicate profile name "personal"`,
		},
		{
			name: "non-duplicate profile name with existing map",
			profile: Profile{
				Name:      "staging",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/claude-staging",
			},
			existing:    existingMap,
			wantErrText: "",
		},
		{
			name: "unknown provider rejected",
			profile: Profile{
				Name:     "custom",
				Provider: Provider("unknown"),
			},
			existing:    nil,
			wantErrText: `unknown provider "unknown"`,
		},
		{
			name: "claude profile missing config_dir",
			profile: Profile{
				Name:      "valid-claude",
				Provider:  ProviderClaude,
				ConfigDir: "",
			},
			existing:    nil,
			wantErrText: "claude profile needs config_dir",
		},
		{
			name: "codex profile missing home_dir",
			profile: Profile{
				Name:     "valid-codex",
				Provider: ProviderCodex,
				HomeDir:  "",
			},
			existing:    nil,
			wantErrText: "codex profile needs home_dir",
		},
		{
			name: "valid claude profile",
			profile: Profile{
				Name:      "my-claude",
				Provider:  ProviderClaude,
				ConfigDir: "/tmp/my-claude",
			},
			existing:    nil,
			wantErrText: "",
		},
		{
			name: "valid codex profile",
			profile: Profile{
				Name:     "my-codex",
				Provider: ProviderCodex,
				HomeDir:  "/tmp/my-codex",
			},
			existing:    nil,
			wantErrText: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile(tt.profile, tt.existing)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("ValidateProfile() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateProfile() = nil, want error containing %q", tt.wantErrText)
			}
			if !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("ValidateProfile() error = %q, want containing %q", err.Error(), tt.wantErrText)
			}
		})
	}
}

func TestReservedProfileName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"default", true},
		{"claude", true},
		{"codex", true},
		{"claude-work", false},
		{"codex-main", false},
		{"custom", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reservedProfileName(tt.name); got != tt.want {
				t.Errorf("reservedProfileName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestValidateProfileDir(t *testing.T) {
	// Unknown provider has no ProviderInfoFor entry — absence of metadata is permissive (returns nil).
	unregistered := Profile{
		Name:     "unregistered",
		Provider: Provider("unregistered"),
	}
	if err := validateProfileDir(unregistered); err != nil {
		t.Errorf("validateProfileDir(unregistered) = %v, want nil", err)
	}

	// Known provider with empty dir produces error.
	missingDir := Profile{
		Name:      "empty",
		Provider:  ProviderClaude,
		ConfigDir: "",
	}
	if err := validateProfileDir(missingDir); err == nil || !strings.Contains(err.Error(), "claude profile needs config_dir") {
		t.Errorf("validateProfileDir(missingDir) = %v, want error containing %q", err, "claude profile needs config_dir")
	}

	// Known provider with populated dir returns nil.
	validDir := Profile{
		Name:      "valid",
		Provider:  ProviderClaude,
		ConfigDir: "/tmp/claude",
	}
	if err := validateProfileDir(validDir); err != nil {
		t.Errorf("validateProfileDir(validDir) = %v, want nil", err)
	}
}
