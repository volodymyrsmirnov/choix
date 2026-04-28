package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// NewTestStore returns an in-memory SQLite store for use in tests outside the
// store package. The store is automatically closed when the test completes.
func NewTestStore(t *testing.T) *Store {
	t.Helper()
	return newTestStore(t)
}
