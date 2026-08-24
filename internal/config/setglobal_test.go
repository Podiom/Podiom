package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetGlobalUpsertsKeysAndPreservesCommentsAndNeighbors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `# keep root comment
global:
  # keep global comment
  provider: claude
  permission_mode: approve
  permission_timeout: 2m
agents:
  - name: atlas
    provider: claude
server:
  bind: 127.0.0.1
  port: 8787
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	g := Global{
		Provider:          ProviderCodex,
		Model:             "gpt-5.1",
		Effort:            "high",
		PermissionMode:    PermissionYolo,
		PermissionTimeout: "5m",
		Fallback:          []string{"claude"},
		AutoArchiveDays:   30,
	}
	if err := SetGlobal(path, g); err != nil {
		t.Fatalf("set global: %v", err)
	}

	text := mustRead(t, path)
	for _, want := range []string{"keep root comment", "keep global comment", "permission_timeout: 5m", "name: atlas"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q to survive edit:\n%s", want, text)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load edited config: %v", err)
	}
	if cfg.Global.Provider != ProviderCodex || cfg.Global.Model != "gpt-5.1" || cfg.Global.Effort != "high" {
		t.Fatalf("global engine fields not updated: %+v", cfg.Global)
	}
	if cfg.Global.PermissionMode != PermissionYolo {
		t.Fatalf("permission_mode = %q, want yolo", cfg.Global.PermissionMode)
	}
	if cfg.Global.PermissionTimeout != "5m" {
		t.Fatalf("permission_timeout not updated: %q", cfg.Global.PermissionTimeout)
	}
	if len(cfg.Global.Fallback) != 1 || cfg.Global.Fallback[0] != "claude" {
		t.Fatalf("fallback = %+v, want [claude]", cfg.Global.Fallback)
	}
	if cfg.Global.AutoArchiveDays != 30 {
		t.Fatalf("auto_archive_days = %d, want 30", cfg.Global.AutoArchiveDays)
	}
}

func TestSetGlobalCreatesGlobalSectionWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "server:\n  bind: 127.0.0.1\n  port: 8787\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	g := Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m"}
	if err := SetGlobal(path, g); err != nil {
		t.Fatalf("set global: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Global.Provider != ProviderClaude {
		t.Fatalf("global section not created: %+v", cfg.Global)
	}
}

func TestSetGlobalDropsEmptyFallbackAndModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "global:\n  provider: claude\n  model: opus\n  permission_mode: approve\n  fallback:\n    - personal\nserver:\n  bind: 127.0.0.1\n  port: 8787\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	g := Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m"}
	if err := SetGlobal(path, g); err != nil {
		t.Fatalf("set global: %v", err)
	}
	text := mustRead(t, path)
	if strings.Contains(text, "fallback") {
		t.Fatalf("empty fallback should drop the key:\n%s", text)
	}
	if strings.Contains(text, "model:") {
		t.Fatalf("empty model should drop the key:\n%s", text)
	}
}

func TestValidateGlobal(t *testing.T) {
	profiles := map[string]Provider{"personal": ProviderClaude, "codex-main": ProviderCodex}
	cases := []struct {
		name    string
		g       Global
		wantErr bool
	}{
		{"ok minimal", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m"}, false},
		{"ok bare provider fallback", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m", Fallback: []string{"codex"}}, false},
		{"ok named profile fallback", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m", Fallback: []string{"personal"}}, false},
		{"ok default token", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m", Fallback: []string{"default"}}, false},
		{"bad provider", Global{Provider: Provider("gpt"), PermissionMode: PermissionApprove, PermissionTimeout: "3m"}, true},
		{"bad permission", Global{Provider: ProviderClaude, PermissionMode: PermissionMode("preapprove"), PermissionTimeout: "3m"}, true},
		{"missing timeout", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove}, true},
		{"bad timeout", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "soon"}, true},
		{"zero timeout", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "0s"}, true},
		{"negative timeout", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "-1s"}, true},
		{"unknown fallback profile", Global{Provider: ProviderClaude, PermissionMode: PermissionApprove, PermissionTimeout: "3m", Fallback: []string{"ghost"}}, true},
		{"ok default profile matches provider", Global{Provider: ProviderClaude, Profile: "personal", PermissionMode: PermissionApprove, PermissionTimeout: "3m"}, false},
		{"unknown default profile", Global{Provider: ProviderClaude, Profile: "ghost", PermissionMode: PermissionApprove, PermissionTimeout: "3m"}, true},
		{"default profile wrong provider", Global{Provider: ProviderClaude, Profile: "codex-main", PermissionMode: PermissionApprove, PermissionTimeout: "3m"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGlobal(tc.g, profiles)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateGlobal(%+v) err = %v, wantErr = %v", tc.g, err, tc.wantErr)
			}
		})
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
