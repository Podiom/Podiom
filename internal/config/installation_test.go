package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateInstallationIDIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation.id")

	first, err := LoadOrCreateInstallationID(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first == "" {
		t.Fatal("no installation id was generated")
	}

	// The identity must survive every restart: a new id would make registered
	// devices and existing notifications look like they belong to a stranger.
	second, err := LoadOrCreateInstallationID(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second != first {
		t.Errorf("id changed across reads: %q then %q", first, second)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %v, want 0600", perm)
	}
}

// TestLoadOrCreateInstallationIDReplacesAnEmptyFile covers an interrupted first
// write: a blank file must not become a blank identity.
func TestLoadOrCreateInstallationIDReplacesAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation.id")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id, err := LoadOrCreateInstallationID(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id == "" {
		t.Error("a blank file yielded a blank installation id")
	}
}

// TestInstallationIDIsIndependentOfAddress checks the id derives from nothing about
// how Podiom is reached. Moving the daemon, changing its port, or being fronted by a
// Home Assistant ingress path must not make it a different installation.
func TestInstallationIDIsIndependentOfAddress(t *testing.T) {
	home := t.TempDir()
	paths := NewPaths(home)

	id, err := LoadOrCreateInstallationID(paths.InstallationID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, unrelated := range []string{"127.0.0.1", "localhost", "8787", "/api/hassio_ingress/abc"} {
		if id == unrelated {
			t.Errorf("installation id %q looks derived from %q", id, unrelated)
		}
	}
	// Two installations must never collide, however identical their addresses.
	other, err := LoadOrCreateInstallationID(NewPaths(t.TempDir()).InstallationID)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if other == id {
		t.Error("two installations were given the same id")
	}
}
