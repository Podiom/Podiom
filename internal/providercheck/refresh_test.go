package providercheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

// TestRefreshCredentialsRunsDoctorScopedToProfile pins both halves of the
// contract: the command is the CLI's doctor (its status command does not refresh
// an expired token) and it is scoped to the profile's own directory.
func TestRefreshCredentialsRunsDoctorScopedToProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	cases := []struct {
		provider config.Provider
		bin      string
		envVar   string
	}{
		{config.ProviderClaude, "claude", "CLAUDE_CONFIG_DIR"},
		{config.ProviderCodex, "codex", "CODEX_HOME"},
	}
	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "invocation")
			bin := filepath.Join(dir, tc.bin)
			writeFakeCLI(t, bin, "#!/usr/bin/env sh\nprintf '%s|%s' \"$*\" \"$"+tc.envVar+"\" > "+log+"\nexit 0\n")
			t.Setenv(strings.ToUpper(tc.bin)+"_BIN", bin)

			profileDir := t.TempDir()
			err := RefreshCredentials(context.Background(), tc.provider, Options{
				Discovery:  podiomexec.Discovery{ExtraDirs: []string{dir}},
				ProfileDir: profileDir,
			})
			if err != nil {
				t.Fatalf("RefreshCredentials: %v", err)
			}
			raw, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatalf("fake CLI was not invoked: %v", readErr)
			}
			want := "doctor|" + profileDir
			if got := string(raw); got != want {
				t.Fatalf("invocation = %q, want %q", got, want)
			}
		})
	}
}

func TestRefreshCredentialsUnknownProvider(t *testing.T) {
	err := RefreshCredentials(context.Background(), config.Provider("nope"), Options{})
	if err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

// TestRefreshCredentialsReportsCLIFailure keeps the caller able to log why a
// renewal attempt did not land, without it being fatal.
func TestRefreshCredentialsReportsCLIFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	writeFakeCLI(t, bin, "#!/usr/bin/env sh\necho 'doctor exploded' >&2\nexit 3\n")
	t.Setenv("CLAUDE_BIN", bin)

	err := RefreshCredentials(context.Background(), config.ProviderClaude, Options{
		Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}},
	})
	if err == nil {
		t.Fatal("expected the CLI failure to surface")
	}
	if !strings.Contains(err.Error(), "doctor exploded") {
		t.Fatalf("error = %q, want it to carry the CLI output", err)
	}
}
