package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// NewTestStore returns a store backed by a fresh database file in a test
// temporary directory, for use in tests outside the store package. The store
// is automatically closed when the test completes.
func NewTestStore(t *testing.T) *Store {
	t.Helper()
	return newTestStore(t)
}
