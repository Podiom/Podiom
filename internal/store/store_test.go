package store

import (
	"path/filepath"
	"testing"
)

func TestOpenRunsMigrationsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podiom.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var version int
	if err := s.DB().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if version < 1 {
		t.Errorf("expected at least migration 1 applied, got %d", version)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Re-opening the same DB must not re-run or fail on already-applied migrations.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
}
