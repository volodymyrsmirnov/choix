package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Store wraps a *sql.DB connection to a choix SQLite database.
type Store struct {
	db *sql.DB
}

// connPragmas are applied by the driver to every connection it opens, so a
// connection the pool replaces (see Durable) is configured like the first.
var connPragmas = []string{
	"journal_mode(WAL)",
	"foreign_keys(on)",
	"busy_timeout(10000)",
	"synchronous(NORMAL)",
}

// uriEscaper percent-encodes the characters SQLite's URI parser gives a
// meaning to in the path part. strings.Replacer works in one pass, so '%' is
// in effect encoded first and the escapes it inserts are never re-encoded.
var uriEscaper = strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F")

// FileURI returns the SQLite URI that opens the database file at path:
// "file:" followed by path with every '%', '#' and '?' percent-encoded, then
// "?_pragma=<p>" for each pragma, joined with '&'. SQLite decodes the path
// back to exactly the bytes of path, so a folder named "Day #2" is never cut
// at the '#'. pragmas are code constants such as "busy_timeout(10000)"; they
// are passed through unescaped.
//
// This is the only place a "file:" URI is built: callers pass paths to Open
// (and library.OpenDB), never DSNs.
func FileURI(path string, pragmas ...string) string {
	var b strings.Builder
	b.WriteString("file:")
	b.WriteString(uriEscaper.Replace(path))
	for i, p := range pragmas {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString("_pragma=")
		b.WriteString(p)
	}
	return b.String()
}

// Open opens (or creates) the SQLite database file at path and runs
// migrations to the latest version. path is a filesystem path, never a DSN:
// Open builds the URI with FileURI, carrying WAL, foreign keys, a 10 s busy
// timeout and synchronous=NORMAL for every connection the pool opens.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("open sqlite: empty database path")
	}
	db, err := sql.Open("sqlite", FileURI(path, connPragmas...))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One connection: concurrent writes from the pipeline's workers tripped
	// "database is locked" without it, and Durable relies on holding the
	// only connection.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
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
