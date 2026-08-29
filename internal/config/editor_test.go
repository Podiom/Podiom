package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSequenceEqual(t *testing.T) {
	tests := []struct {
		name   string
		node   *yaml.Node
		values []string
		want   bool
	}{
		{name: "non-sequence", node: &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Value: "a"}}}, values: []string{"a"}, want: false},
		{name: "different length", node: &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Value: "a"}}}, values: []string{"a", "b"}, want: false},
		{name: "different value", node: &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Value: "a"}, {Value: "c"}}}, values: []string{"a", "b"}, want: false},
		{name: "matching values", node: &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Value: "a"}, {Value: "b"}}}, values: []string{"a", "b"}, want: true},
		{name: "both empty", node: &yaml.Node{Kind: yaml.SequenceNode}, values: nil, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sequenceEqual(tt.node, tt.values); got != tt.want {
				t.Fatalf("sequenceEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveAgentRemovesOnlyMatchingEntryAndKeepsConfigValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `# keep root comment
global:
  # keep global comment
  provider: claude
  permission_mode: approve
agents:
  - name: atlas
    provider: claude
  # keep neighbor comment
  - name: builder
    provider: codex
server:
  bind: 127.0.0.1
  port: 8787
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAgent(path, "atlas"); err != nil {
		t.Fatalf("remove agent: %v", err)
	}
	edited, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(edited)
	if strings.Contains(text, "name: atlas") {
		t.Fatalf("removed agent still present:\n%s", text)
	}
	if !strings.Contains(text, "name: builder") {
		t.Fatalf("neighbor agent was removed:\n%s", text)
	}
	if !strings.Contains(text, "keep root comment") || !strings.Contains(text, "keep global comment") || !strings.Contains(text, "keep neighbor comment") {
		t.Fatalf("expected comments to survive edit:\n%s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %v, want 0600", got)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load edited config: %v", err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "builder" {
		t.Fatalf("agents after edit = %+v", cfg.Agents)
	}
}

func TestRemoveAgentNoOpsWhenAgentOrAgentsSectionAbsent(t *testing.T) {
	dir := t.TempDir()
	withAgents := filepath.Join(dir, "with-agents.yaml")
	rawWithAgents := "global:\n  provider: claude\n  permission_mode: approve\nagents:\n  - name: atlas\nserver:\n  bind: 127.0.0.1\n  port: 8787\n"
	if err := os.WriteFile(withAgents, []byte(rawWithAgents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAgent(withAgents, "ghost"); err != nil {
		t.Fatalf("remove absent agent: %v", err)
	}
	got, err := os.ReadFile(withAgents)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != rawWithAgents {
		t.Fatalf("absent agent edit should not rewrite file:\n%s", got)
	}

	withoutAgents := filepath.Join(dir, "without-agents.yaml")
	rawWithoutAgents := "global:\n  provider: claude\n  permission_mode: approve\nserver:\n  bind: 127.0.0.1\n  port: 8787\n"
	if err := os.WriteFile(withoutAgents, []byte(rawWithoutAgents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAgent(withoutAgents, "ghost"); err != nil {
		t.Fatalf("remove from config without agents: %v", err)
	}
	got, err = os.ReadFile(withoutAgents)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != rawWithoutAgents {
		t.Fatalf("missing agents edit should not rewrite file:\n%s", got)
	}
}

func TestUpsertAndRemoveProfileKeepConfigValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `global:
  provider: claude
  permission_mode: approve
profiles:
  - name: work
    provider: claude
    config_dir: /tmp/old-claude
server:
  bind: 127.0.0.1
  port: 8787
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertProfile(path, Profile{Name: "work", Provider: ProviderCodex, HomeDir: "/tmp/codex-work"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if err := UpsertProfile(path, Profile{Name: "personal", Provider: ProviderClaude, ConfigDir: "/tmp/claude-personal"}); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load edited config: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("profiles = %+v", cfg.Profiles)
	}
	if cfg.Profiles[0].Provider != ProviderCodex || cfg.Profiles[0].HomeDir != "/tmp/codex-work" || cfg.Profiles[0].ConfigDir != "" {
		t.Fatalf("updated profile = %+v", cfg.Profiles[0])
	}
	if err := RemoveProfile(path, "work"); err != nil {
		t.Fatalf("remove profile: %v", err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("load after remove: %v", err)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Name != "personal" {
		t.Fatalf("profiles after remove = %+v", cfg.Profiles)
	}
}
