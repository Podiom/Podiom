package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetVoiceCreatesBlockAndPreservesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `# keep root comment
global:
  # keep global comment
  provider: claude
  permission_mode: approve
  permission_timeout: 3m
server:
  bind: 127.0.0.1
  port: 8787
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetVoice(path, Voice{OpenAIAPIKey: "sk-test-123"}); err != nil {
		t.Fatalf("set voice: %v", err)
	}

	text := mustRead(t, path)
	for _, want := range []string{"keep root comment", "keep global comment", "openai_api_key: sk-test-123"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q after edit:\n%s", want, text)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load edited config: %v", err)
	}
	if cfg.Voice.OpenAIAPIKey != "sk-test-123" {
		t.Fatalf("voice key not round-tripped: %+v", cfg.Voice)
	}
}

func TestSetVoiceEmptyKeyRemovesBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "global:\n  provider: claude\nvoice:\n  openai_api_key: sk-old\nserver:\n  bind: 127.0.0.1\n  port: 8787\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetVoice(path, Voice{}); err != nil {
		t.Fatalf("clear voice: %v", err)
	}

	text := mustRead(t, path)
	if strings.Contains(text, "openai_api_key") {
		t.Fatalf("cleared key should be removed:\n%s", text)
	}
	if strings.Contains(text, "voice:") {
		t.Fatalf("empty voice block should be dropped:\n%s", text)
	}
	if !strings.Contains(text, "provider: claude") {
		t.Fatalf("neighbors should survive:\n%s", text)
	}
}

func TestSetVoiceClearWhenAbsentIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "global:\n  provider: claude\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetVoice(path, Voice{}); err != nil {
		t.Fatalf("noop clear: %v", err)
	}
	if got := mustRead(t, path); got != raw {
		t.Fatalf("file should be untouched, got:\n%s", got)
	}
}

func TestValidateVoice(t *testing.T) {
	cases := []struct {
		name    string
		v       Voice
		wantErr bool
	}{
		{"empty ok", Voice{}, false},
		{"plain key ok", Voice{OpenAIAPIKey: "sk-proj-abc123"}, false},
		{"embedded space", Voice{OpenAIAPIKey: "sk-proj abc"}, true},
		{"embedded newline", Voice{OpenAIAPIKey: "sk-proj\nabc"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateVoice(tc.v)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateVoice(%+v) err = %v, wantErr = %v", tc.v, err, tc.wantErr)
			}
		})
	}
}
