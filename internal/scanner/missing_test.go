package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScannerMarksDeletedFilesMissing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("k"))
	mustWrite(t, root, "vanish.raf", []byte("v"))

	s := newScannerStore(t)
	sc := New(root, s)

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if got := statusOf(t, sc, "keep.jpg"); got != "discovered" {
		t.Fatalf("keep status = %q", got)
	}
	if got := statusOf(t, sc, "vanish.raf"); got != "discovered" {
		t.Fatalf("vanish status = %q", got)
	}

	// Delete one file from disk.
	if err := os.Remove(filepath.Join(root, "vanish.raf")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("second walk: %v", err)
	}

	// Row must remain (we never delete files rows automatically), but its status
	// must be 'missing'.
	if got, want := countFiles(t, sc), 2; got != want {
		t.Errorf("count = %d, want %d (rows preserved)", got, want)
	}
	if got := statusOf(t, sc, "keep.jpg"); got != "discovered" {
		t.Errorf("keep status = %q, want 'discovered'", got)
	}
	if got := statusOf(t, sc, "vanish.raf"); got != "missing" {
		t.Errorf("vanish status = %q, want 'missing'", got)
	}
}
