package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

func TestConfirmAgentDeletionRequiresExactName(t *testing.T) {
	var out bytes.Buffer
	confirmation, ok := confirmAgentDeletion(strings.NewReader("wrong\n"), &out, "atlas")
	if ok {
		t.Fatal("expected mismatched confirmation to abort")
	}
	if confirmation != "wrong" {
		t.Fatalf("confirmation = %q, want wrong", confirmation)
	}
	if !strings.Contains(out.String(), "agent deletion aborted") {
		t.Fatalf("missing abort message: %q", out.String())
	}

	out.Reset()
	confirmation, ok = confirmAgentDeletion(strings.NewReader("atlas\n"), &out, "atlas")
	if !ok {
		t.Fatal("expected exact confirmation to proceed")
	}
	if confirmation != "atlas" {
		t.Fatalf("confirmation = %q, want atlas", confirmation)
	}
	if strings.Contains(out.String(), "agent deletion aborted") {
		t.Fatalf("unexpected abort message: %q", out.String())
	}
}

func TestParseEnvFlags(t *testing.T) {
	tests := []struct {
		name      string
		input     []string
		wantName  string
		wantValue string
	}{
		{name: "value", input: []string{"NAME=VALUE"}, wantName: "NAME", wantValue: "VALUE"},
		{name: "bare", input: []string{"NAME"}, wantName: "NAME"},
		{name: "trim name", input: []string{" NAME = value "}, wantName: "NAME", wantValue: " value "},
		{name: "first equals", input: []string{"K=a=b"}, wantName: "K", wantValue: "a=b"},
		{name: "explicit empty", input: []string{"NAME="}, wantName: "NAME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEnvFlags(tt.input)
			if len(got) != 1 || got[0].Name != tt.wantName || got[0].Value != tt.wantValue {
				t.Fatalf("parseEnvFlags(%q) = %#v, want name %q value %q", tt.input, got, tt.wantName, tt.wantValue)
			}
		})
	}
	if got := parseEnvFlags(nil); got != nil {
		t.Fatalf("parseEnvFlags(nil) = %#v, want nil", got)
	}
}

func TestConfirmOverwriteDefaultsNo(t *testing.T) {
	var out bytes.Buffer
	if confirmOverwrite(strings.NewReader("\n"), &out, "Overwrite?") {
		t.Fatal("empty answer should abort")
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Fatalf("missing abort message: %q", out.String())
	}

	out.Reset()
	if !confirmOverwrite(strings.NewReader("yes\n"), &out, "Overwrite?") {
		t.Fatal("yes should confirm")
	}
}

func TestAgentsUpdateRejectsYesWithoutGenerateSoul(t *testing.T) {
	addr := "127.0.0.1:1"
	cmd := newAgentsUpdateCmd(&addr)
	cmd.SetArgs([]string{"juno", "--yes", "--model", "sonnet"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected --yes without --generate-soul to fail")
	}
	if !strings.Contains(err.Error(), "--yes only applies with --generate-soul") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentsListPrintsEmptyState(t *testing.T) {
	t.Setenv("PODIOM_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents" {
			t.Fatalf("request path = %q, want /api/agents", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[]")
	}))
	t.Cleanup(srv.Close)

	addr := strings.TrimPrefix(srv.URL, "http://")
	cmd := newAgentsListCmd(&addr)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "no agents yet\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestMCPListPrintsEmptyState(t *testing.T) {
	t.Setenv("PODIOM_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mcp" {
			t.Fatalf("request path = %q, want /api/mcp", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[],"agents":[],"assignments":{}}`)
	}))
	t.Cleanup(srv.Close)

	addr := strings.TrimPrefix(srv.URL, "http://")
	cmd := newMCPListCmd(&addr)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "no MCP servers configured\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestActiveLogPathUsesPodiomHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PODIOM_HOME", home)
	got, err := activeLogPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "logs", "podiomd.log")
	if got != want {
		t.Fatalf("activeLogPath() = %q, want %q", got, want)
	}
}

func TestProfilePath(t *testing.T) {
	tests := []struct {
		name    string
		profile config.Profile
		want    string
	}{
		{
			name:    "Claude config directory",
			profile: config.Profile{Provider: config.ProviderClaude, ConfigDir: "/tmp/claude-test"},
			want:    "/tmp/claude-test",
		},
		{
			name:    "Codex home directory",
			profile: config.Profile{Provider: config.ProviderCodex, HomeDir: "/tmp/codex-test"},
			want:    "/tmp/codex-test",
		},
		{
			name:    "no explicit directory",
			profile: config.Profile{Provider: config.ProviderCodex},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := profilePath(tt.profile); got != tt.want {
				t.Fatalf("profilePath(%#v) = %q, want %q", tt.profile, got, tt.want)
			}
		})
	}
}

func TestResolveAddr(t *testing.T) {
	// No t.Parallel: t.Setenv panics in parallel tests. Every case sets
	// PODIOM_ADDR (even to "") and a fresh PODIOM_HOME so the developer's own
	// environment and config.yaml cannot leak into the result.
	tests := []struct {
		name     string
		flagAddr string
		envAddr  string
		config   string // written to $PODIOM_HOME/config.yaml when non-empty
		want     string
	}{
		{
			name:     "flag wins over env and config",
			flagAddr: "10.0.0.1:1234",
			envAddr:  "127.0.0.1:5555",
			config:   "server:\n  bind: 127.0.0.1\n  port: 9999\n",
			want:     "10.0.0.1:1234",
		},
		{
			name:    "env wins over config",
			envAddr: "127.0.0.1:5555",
			config:  "server:\n  bind: 127.0.0.1\n  port: 9999\n",
			want:    "127.0.0.1:5555",
		},
		{
			name:    "whitespace-only env is returned verbatim, not trimmed",
			envAddr: "   ",
			want:    "   ",
		},
		{
			name:    "env is not validated",
			envAddr: "garbage no port",
			want:    "garbage no port",
		},
		{
			name:   "config bind and port are used",
			config: "server:\n  bind: 0.0.0.0\n  port: 9999\n",
			want:   "0.0.0.0:9999",
		},
		{
			name:   "empty config bind defaults to 127.0.0.1, not an empty host",
			config: "server:\n  bind: \"\"\n  port: 9999\n",
			want:   "127.0.0.1:9999",
		},
		{
			name:   "config port 0 defaults to 8787",
			config: "server:\n  bind: 0.0.0.0\n  port: 0\n",
			want:   "0.0.0.0:8787",
		},
		{
			name:   "IPv6 config bind is bracketed",
			config: "server:\n  bind: \"::1\"\n  port: 9999\n",
			want:   "[::1]:9999",
		},
		{
			name: "no config falls back to the default",
			want: "127.0.0.1:8787",
		},
		{
			name:   "malformed config falls back to the default without an error",
			config: "server: [not a mapping\n",
			want:   "127.0.0.1:8787",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PODIOM_HOME", home)
			t.Setenv("PODIOM_ADDR", tt.envAddr)
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveAddr(tt.flagAddr)
			if err != nil {
				t.Fatalf("resolveAddr(%q) error = %v, want nil", tt.flagAddr, err)
			}
			if got != tt.want {
				t.Fatalf("resolveAddr(%q) = %q, want %q", tt.flagAddr, got, tt.want)
			}
		})
	}
}
