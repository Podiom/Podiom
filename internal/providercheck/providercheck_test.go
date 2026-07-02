package providercheck

import (
	"context"
	"os"
	"path/filepath"
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
	script := "#!/usr/bin/env sh\nif [ \"$1\" = \"--version\" ]; then echo 'claude 1.2.3'; exit 0; fi\nif [ \"$1\" = \"doctor\" ]; then echo ok; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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
