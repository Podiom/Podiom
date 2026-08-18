package notify

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

// newTestStore opens a throwaway database. The engine is tested against the real
// store rather than a fake: its behaviour depends on the guarded updates and the
// listing order that live in SQL, so a fake would only prove the fake works.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testLogger keeps expected channel failures out of the test output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
