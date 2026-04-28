package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store wraps a *sql.DB connection to a choix SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dsn and runs migrations to the latest version.
// dsn is typically "file:.choix/state.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)".
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	// Enforce sane defaults for concurrent worker writes regardless of what
	// the caller put in the DSN: WAL journal, foreign keys on, 10s busy
	// timeout. Repeated PRAGMA execution is a no-op if already set.
	for _, pragma := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 10000`,
		`PRAGMA synchronous = NORMAL`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %s: %w", pragma, err)
		}
	}
	// Cap pool to 1 writer to avoid SQLite "database is locked" under heavy
	// per-file pipeline contention. The driver still allows many readers via
	// a separate read pool.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB for repo packages.
func (s *Store) DB() *sql.DB { return s.db }
