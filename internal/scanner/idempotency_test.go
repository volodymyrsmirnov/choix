package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func countFiles(t *testing.T, sc *Scanner) int {
	t.Helper()
	var n int
	if err := sc.store.DB().QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func statusOf(t *testing.T, sc *Scanner, rel string) string {
	t.Helper()
	var s string
	if err := sc.store.DB().QueryRow(
		`SELECT scan_status FROM files WHERE path = ?`, filepath.ToSlash(rel)).Scan(&s); err != nil {
		t.Fatalf("status: %v", err)
	}
	return s
}

func hashOf(t *testing.T, sc *Scanner, rel string) string {
	t.Helper()
	var s string
	if err := sc.store.DB().QueryRow(
		`SELECT content_hash FROM files WHERE path = ?`, filepath.ToSlash(rel)).Scan(&s); err != nil {
		t.Fatalf("hash: %v", err)
	}
	return s
}

func setStatus(t *testing.T, sc *Scanner, rel, status string) {
	t.Helper()
	_, err := sc.store.DB().Exec(
		`UPDATE files SET scan_status = ? WHERE path = ?`, status, filepath.ToSlash(rel))
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
}

func TestScannerWalkIsIdempotent(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "a.jpg", []byte("aaa"))
	mustWrite(t, root, "b.raf", []byte("bbb"))

	s := newScannerStore(t)
	sc := New(root, s)

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if got, want := countFiles(t, sc), 2; got != want {
		t.Fatalf("after first walk: %d files, want %d", got, want)
	}

	// Pretend the analyze stage ran on a.jpg.
	setStatus(t, sc, "a.jpg", "analyzed")

	// Second walk: nothing changed on disk → must not duplicate or downgrade status.
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("second walk: %v", err)
	}
	if got, want := countFiles(t, sc), 2; got != want {
		t.Fatalf("after second walk: %d files, want %d (no duplicates)", got, want)
	}
	if got := statusOf(t, sc, "a.jpg"); got != "analyzed" {
		t.Errorf("status of unchanged a.jpg = %q, want preserved 'analyzed'", got)
	}
}

func TestScannerWalkDetectsModifiedFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "a.jpg", []byte("original"))

	s := newScannerStore(t)
	sc := New(root, s)

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	setStatus(t, sc, "a.jpg", "analyzed")
	origHash := hashOf(t, sc, "a.jpg")

	// Modify the file's content (different size + content) and bump its mtime.
	p := filepath.Join(root, "a.jpg")
	if err := os.WriteFile(p, []byte("brand-new-larger-bytes"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("second walk: %v", err)
	}
	if got, want := countFiles(t, sc), 1; got != want {
		t.Fatalf("after modification rescan: %d files, want %d", got, want)
	}
	newHash := hashOf(t, sc, "a.jpg")
	if newHash == origHash {
		t.Errorf("hash unchanged after content change: %q", newHash)
	}
	if got := statusOf(t, sc, "a.jpg"); got != "discovered" {
		t.Errorf("status after modification = %q, want 'discovered' (reset for re-pipeline)", got)
	}
}
