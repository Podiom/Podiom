package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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

func TestTransactionsAcquireWriteLockBeforeReads(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec(`CREATE TABLE lock_probe (id INTEGER PRIMARY KEY, value INTEGER);
		INSERT INTO lock_probe (id, value) VALUES (1, 0)`); err != nil {
		t.Fatal(err)
	}

	tx, err := s.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value int
	if err := tx.QueryRow(`SELECT value FROM lock_probe WHERE id = 1`).Scan(&value); err != nil {
		t.Fatal(err)
	}

	competingWrite := make(chan error, 1)
	go func() {
		_, err := s.DB().Exec(`UPDATE lock_probe SET value = 2 WHERE id = 1`)
		competingWrite <- err
	}()

	select {
	case err := <-competingWrite:
		t.Fatalf("competing writer bypassed an active transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := tx.Exec(`UPDATE lock_probe SET value = 1 WHERE id = 1`); err != nil {
		t.Fatalf("upgrade transaction to writer: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-competingWrite; err != nil {
		t.Fatalf("competing writer after commit: %v", err)
	}
}
