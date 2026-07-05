package providercheck

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

func TestCheckFindsFakeClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	writeFakeCLI(t, bin, "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then echo 'claude 1.2.3'; exit 0; fi\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then echo '{\"loggedIn\":true}'; exit 0; fi\nexit 1\n")
	t.Setenv("CLAUDE_BIN", bin)

	status := Check(context.Background(), config.ProviderClaude, Options{
		Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}},
	})
	if !status.Ready || !status.Found {
		t.Fatalf("status = %+v, want found and ready", status)
	}
	if status.Version != "claude 1.2.3" {
		t.Fatalf("version = %q", status.Version)
	}
	if !status.LoginChecked || !status.LoggedIn {
		t.Fatalf("login status = checked:%v logged:%v, want true/true", status.LoginChecked, status.LoggedIn)
	}
}

func TestCheckMissingProviderIncludesInstallHint(t *testing.T) {
	t.Setenv("CODEX_BIN", filepath.Join(t.TempDir(), "missing-codex"))
	status := Check(context.Background(), config.ProviderCodex, Options{
		Discovery: podiomexec.Discovery{ExtraDirs: []string{t.TempDir()}},
	})
	if status.Found {
		t.Fatalf("status = %+v, want missing", status)
	}
	if status.InstallHint == "" {
		t.Fatalf("missing install hint: %+v", status)
	}
}

func TestLoginArgsAreContainerSafe(t *testing.T) {
	cases := []struct {
		provider config.Provider
		want     []string
	}{
		{config.ProviderClaude, []string{"/login"}},
		{config.ProviderCodex, []string{"login", "--device-auth"}},
	}
	for _, tc := range cases {
		got, err := LoginArgs(tc.provider)
		if err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s args = %v, want %v", tc.provider, got, tc.want)
		}
	}
}

func TestCheckClaudeAuthStatusLoggedOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	writeFakeCLI(t, bin, "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then echo 'claude 1.2.3'; exit 0; fi\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then echo 'prefix {\"loggedIn\":false}'; exit 1; fi\nexit 1\n")
	t.Setenv("CLAUDE_BIN", bin)

	status := Check(context.Background(), config.ProviderClaude, Options{Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}}})
	if !status.Found || !status.Ready || !status.LoginChecked || status.LoggedIn {
		t.Fatalf("status = %+v, want found ready checked logged-out", status)
	}
}

func TestCheckClaudeAuthStatusFallbackToDoctor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	writeFakeCLI(t, bin, "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then echo 'claude 1.2.3'; exit 0; fi\nif [ \"$1\" = \"auth\" ]; then echo 'usage: claude'; exit 1; fi\nif [ \"$1\" = \"doctor\" ]; then echo ok; exit 0; fi\nexit 1\n")
	t.Setenv("CLAUDE_BIN", bin)

	status := Check(context.Background(), config.ProviderClaude, Options{Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}}})
	if !status.Found || !status.Ready || status.LoginChecked {
		t.Fatalf("status = %+v, want legacy ready without login check", status)
	}
	if status.Doctor != "ok" {
		t.Fatalf("doctor = %q, want ok", status.Doctor)
	}
}

func TestCheckCodexLoginStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	for _, tc := range []struct {
		name         string
		statusScript string
		wantChecked  bool
		wantLoggedIn bool
	}{
		{"logged-in", "echo 'Logged in using ChatGPT'; exit 0", true, true},
		{"logged-out", "echo 'Not logged in'; exit 1", true, false},
		{"missing-subcommand", "echo 'Usage: codex login'; exit 1", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "codex")
			writeFakeCLI(t, bin, "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex 9.8.7'; exit 0; fi\nif [ \"$1\" = \"login\" ] && [ \"$2\" = \"status\" ]; then "+tc.statusScript+"; fi\nexit 1\n")
			t.Setenv("CODEX_BIN", bin)

			status := Check(context.Background(), config.ProviderCodex, Options{Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}}})
			if !status.Found || !status.Ready || status.LoginChecked != tc.wantChecked || status.LoggedIn != tc.wantLoggedIn {
				t.Fatalf("status = %+v, want ready checked:%v logged:%v", status, tc.wantChecked, tc.wantLoggedIn)
			}
		})
	}
}

func TestInstallPackage(t *testing.T) {
	cases := []struct {
		provider config.Provider
		want     string
	}{
		{config.ProviderClaude, "@anthropic-ai/claude-code"},
		{config.ProviderCodex, "@openai/codex"},
	}
	for _, tc := range cases {
		got, err := InstallPackage(tc.provider)
		if err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		if got != tc.want {
			t.Fatalf("%s package = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

func writeFakeCLI(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
