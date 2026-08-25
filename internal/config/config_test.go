package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
