package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// newScannerStore opens a fresh store under t.TempDir() — keeps scanner tests
// independent of the store package's internal helpers.
func newScannerStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "scan.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustWrite(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func listPaths(t *testing.T, s *store.Store) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT path FROM files ORDER BY path`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		paths = append(paths, p)
	}
	return paths
}

func TestScannerWalkDiscoversMediaAndSkipsNonMedia(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "Day1/IMG_0001.JPG", []byte("jpeg-bytes-1"))
	mustWrite(t, root, "Day1/IMG_0002.RAF", []byte("raf-bytes-2"))
	mustWrite(t, root, "Day2/clip.MOV", []byte("mov-bytes-3"))
	mustWrite(t, root, "Day2/notes.txt", []byte("not media"))
	mustWrite(t, root, "README.md", []byte("docs"))

	s := newScannerStore(t)
	sc := New(root, s)

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	got := listPaths(t, s)
	want := []string{
		filepath.Join("Day1", "IMG_0001.JPG"),
		filepath.Join("Day1", "IMG_0002.RAF"),
		filepath.Join("Day2", "clip.MOV"),
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScannerWalkSkipsHiddenAndConventionalDirs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("a"))
	mustWrite(t, root, ".hidden/secret.jpg", []byte("a"))
	mustWrite(t, root, ".choix/state.db", []byte("a"))
	mustWrite(t, root, "picks/already.jpg", []byte("a"))
	mustWrite(t, root, "node_modules/foo.jpg", []byte("a"))
	mustWrite(t, root, "sub/.git/HEAD.jpg", []byte("a"))

	s := newScannerStore(t)
	sc := New(root, s)

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	got := listPaths(t, s)
	if len(got) != 1 || got[0] != "keep.jpg" {
		t.Fatalf("expected only keep.jpg, got %v", got)
	}
}

func TestScannerWalkSkipsConfiguredPicksDir(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("a"))
	mustWrite(t, root, "exported/already.jpg", []byte("a"))
	mustWrite(t, root, "picks/should-be-included.jpg", []byte("a"))

	s := newScannerStore(t)
	sc := New(root, s)
	sc.SetPicksDir("exported")

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	got := listPaths(t, s)
	want := map[string]bool{"keep.jpg": false, "picks/should-be-included.jpg": false}
	for _, g := range got {
		if _, ok := want[g]; ok {
			want[g] = true
			continue
		}
		t.Errorf("unexpected file in scan: %s (should have been skipped or never written)", g)
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("missing %s in scan results: %v", p, got)
		}
	}
}

func TestScannerWalkEvictsRowsAlreadyIndexedFromPicksDir(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("a"))
	mustWrite(t, root, "picks/leftover.jpg", []byte("b"))
	mustWrite(t, root, "picks/sub/leftover.jpg", []byte("c"))

	s := newScannerStore(t)

	// Simulate an earlier build that ingested the picks dir as originals.
	for _, p := range []string{"picks/leftover.jpg", "picks/sub/leftover.jpg"} {
		if _, err := s.Files().Insert(store.File{
			Path:        p,
			Size:        1,
			Mtime:       1,
			ContentHash: p,
			Kind:        "photo",
			Format:      "jpg",
			ScanStatus:  "analyzed",
		}); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	got := listPaths(t, s)
	if len(got) != 1 || got[0] != "keep.jpg" {
		t.Fatalf("expected only keep.jpg after picks eviction, got %v", got)
	}
}

func TestScannerWalkSkipsPicksDirCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("a"))
	mustWrite(t, root, "Picks/exported.jpg", []byte("b"))

	s := newScannerStore(t)
	sc := New(root, s) // default picks_dir = "picks"

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	got := listPaths(t, s)
	if len(got) != 1 || got[0] != "keep.jpg" {
		t.Fatalf("Picks/ should be skipped case-insensitively, got %v", got)
	}
}

func TestScannerWalkSkipsNestedPicksDir(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("a"))
	mustWrite(t, root, "exports/luminar/done.jpg", []byte("a"))
	mustWrite(t, root, "exports/other/keepme.jpg", []byte("a"))

	s := newScannerStore(t)
	sc := New(root, s)
	sc.SetPicksDir("exports/luminar")

	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	got := listPaths(t, s)
	for _, g := range got {
		if g == "exports/luminar/done.jpg" {
			t.Errorf("nested picks dir not skipped, got %v", got)
		}
	}
	hasOther := false
	for _, g := range got {
		if g == "exports/other/keepme.jpg" {
			hasOther = true
		}
	}
	if !hasOther {
		t.Errorf("sibling of nested picks dir was skipped, got %v", got)
	}
}
