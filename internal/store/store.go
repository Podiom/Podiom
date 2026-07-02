// Package store is Podiom's durable persistence layer: an embedded SQLite
// database (pure-Go modernc.org/sqlite, no cgo) holding the canonical message
// history, rolling summaries, provider handles, and session/agent metadata
// (R11.2 / D6). Phase 0 establishes the connection and a forward-only migration
// framework; later phases add the actual schema.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite connection. It is safe for concurrent use by multiple
// goroutines (database/sql manages the pool).
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, applies the
// pragmas Podiom relies on, and runs any pending migrations. The pure-Go driver
// registers under the name "sqlite".
func Open(path string) (*Store, error) {
	// Busy timeout avoids spurious "database is locked" errors under the parallel
	// runs Podiom allows (no concurrency cap, R11.3); foreign_keys + WAL give us
	// referential integrity and better read/write concurrency.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DB exposes the underlying handle for packages that run their own queries.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
