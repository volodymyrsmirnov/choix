package picks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func newTestService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	scanRoot := t.TempDir()
	dsn := "file:" + filepath.Join(scanRoot, ".choix-test.db") + "?_pragma=foreign_keys(on)"
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := New(s, scanRoot, "picks")
	return svc, s, scanRoot
}

func insertFile(t *testing.T, s *store.Store, relPath string) int64 {
	t.Helper()
	id, err := s.Files().Insert(store.File{
		Path:        relPath,
		Size:        4,
		Mtime:       time.Now().Unix(),
		ContentHash: "deadbeef",
		Kind:        "photo",
		Format:      "jpeg",
		ScanStatus:  "analyzed",
	})
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	return id
}

func TestPickRejectTransitions(t *testing.T) {
	svc, s, root := newTestService(t)
	writeFile(t, root, "Day1/IMG_0001.JPG", "pixels")
	id := insertFile(t, s, "Day1/IMG_0001.JPG")

	// Pick → state=picked.
	if err := svc.Pick(id); err != nil {
		t.Fatalf("Pick: %v", err)
	}
	got, err := s.Picks().Get(id)
	if err != nil {
		t.Fatalf("Picks().Get: %v", err)
	}
	if got.State != string(StatePicked) {
		t.Errorf("state=%q want %q", got.State, StatePicked)
	}

	// Pick again → idempotent no-op (no error, state unchanged).
	if err := svc.Pick(id); err != nil {
		t.Fatalf("Pick second call: %v", err)
	}

	// Reject → state=rejected.
	if err := svc.Reject(id); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	got, _ = s.Picks().Get(id)
	if got.State != string(StateRejected) {
		t.Errorf("after Reject state=%q want %q", got.State, StateRejected)
	}

	// Unreject from rejected → row deleted (back to unmarked).
	if err := svc.Unreject(id); err != nil {
		t.Fatalf("Unreject: %v", err)
	}
	if _, err := s.Picks().Get(id); err == nil {
		t.Error("expected ErrNotFound after Unreject")
	}
}

func TestPickMissingFile(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.Pick(99999); err == nil {
		t.Fatal("Pick on nonexistent file id should error")
	}
}

func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestExportCopiesOriginal(t *testing.T) {
	svc, s, root := newTestService(t)
	writeFile(t, root, "Day1/IMG_0001.JPG", "hello")

	id, err := s.Files().Insert(store.File{
		Path: "Day1/IMG_0001.JPG", Size: 5, Mtime: time.Now().Unix(),
		ContentHash: hashOf("hello"), Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	rel, err := svc.Export(id)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if rel != "picks/Day1/IMG_0001.JPG" {
		t.Errorf("rel=%q want picks/Day1/IMG_0001.JPG", rel)
	}
	got, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read exported: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("contents=%q want hello", got)
	}
}

func TestExportIdempotentSameContent(t *testing.T) {
	svc, s, root := newTestService(t)
	writeFile(t, root, "a.jpg", "AAA")
	id, _ := s.Files().Insert(store.File{
		Path: "a.jpg", Size: 3, Mtime: 0, ContentHash: hashOf("AAA"),
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})

	rel1, err := svc.Export(id)
	if err != nil {
		t.Fatalf("Export 1: %v", err)
	}
	rel2, err := svc.Export(id)
	if err != nil {
		t.Fatalf("Export 2: %v", err)
	}
	if rel1 != rel2 {
		t.Errorf("re-export changed path: %q vs %q", rel1, rel2)
	}
}

func TestExportCollisionDifferentContent(t *testing.T) {
	svc, s, root := newTestService(t)
	// Pre-create a file at the target path with different content.
	writeFile(t, root, "picks/a.jpg", "OTHER")
	writeFile(t, root, "a.jpg", "MINE")
	id, _ := s.Files().Insert(store.File{
		Path: "a.jpg", Size: 4, Mtime: 0, ContentHash: hashOf("MINE"),
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})

	rel, err := svc.Export(id)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if rel != "picks/a_2.jpg" {
		t.Errorf("rel=%q want picks/a_2.jpg", rel)
	}
}

func TestExportMissingSource(t *testing.T) {
	svc, s, _ := newTestService(t)
	id, _ := s.Files().Insert(store.File{
		Path: "ghost.jpg", Size: 0, Mtime: 0, ContentHash: "x",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if _, err := svc.Export(id); err == nil {
		t.Fatal("Export of missing source should error")
	}
}

func TestUnpickRemovesExported(t *testing.T) {
	svc, s, root := newTestService(t)
	writeFile(t, root, "x.jpg", "data")
	id, _ := s.Files().Insert(store.File{
		Path: "x.jpg", Size: 4, Mtime: 0, ContentHash: hashOf("data"),
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err := svc.Pick(id); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(root, "picks/x.jpg")
	if _, err := os.Stat(exported); err != nil {
		t.Fatalf("expected file at %s before unpick", exported)
	}
	if err := svc.Unpick(id); err != nil {
		t.Fatalf("Unpick: %v", err)
	}
	if _, err := os.Stat(exported); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ErrNotExist, got %v", err)
	}
}

func TestRejectFromPickedRemovesExported(t *testing.T) {
	svc, s, root := newTestService(t)
	writeFile(t, root, "y.jpg", "data2")
	id, _ := s.Files().Insert(store.File{
		Path: "y.jpg", Size: 5, Mtime: 0, ContentHash: hashOf("data2"),
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	_ = svc.Pick(id)
	if err := svc.Reject(id); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "picks/y.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Error("rejected file should be removed from picks/")
	}
	got, _ := s.Picks().Get(id)
	if got.State != string(StateRejected) {
		t.Errorf("state=%q want rejected", got.State)
	}
}

func TestUnexportIdempotentMissingFile(t *testing.T) {
	svc, s, _ := newTestService(t)
	id, _ := s.Files().Insert(store.File{
		Path: "z.jpg", Size: 0, Mtime: 0, ContentHash: "z",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	// Set exported_path to something that doesn't exist on disk.
	_ = s.Picks().SetState(id, string(StatePicked))
	_ = s.Picks().SetExportedPath(id, "picks/ghost.jpg")
	// Should not error.
	if err := svc.Unexport(id); err != nil {
		t.Fatalf("Unexport with missing target should be no-op: %v", err)
	}
}

func TestPickAutoExports(t *testing.T) {
	svc, s, root := newTestService(t)
	writeFile(t, root, "Day1/IMG_0001.JPG", "PIXELS")
	id, _ := s.Files().Insert(store.File{
		Path: "Day1/IMG_0001.JPG", Size: 6, Mtime: time.Now().Unix(),
		ContentHash: hashOf("PIXELS"), Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})

	if err := svc.Pick(id); err != nil {
		t.Fatalf("Pick: %v", err)
	}

	// File appears in picks/.
	exported := filepath.Join(root, "picks/Day1/IMG_0001.JPG")
	if _, err := os.Stat(exported); err != nil {
		t.Fatalf("exported file missing: %v", err)
	}

	// DB has exported_path set.
	got, err := s.Picks().Get(id)
	if err != nil {
		t.Fatalf("Picks().Get: %v", err)
	}
	if got.ExportedPath.String != "picks/Day1/IMG_0001.JPG" {
		t.Errorf("exported_path=%q want picks/Day1/IMG_0001.JPG", got.ExportedPath.String)
	}
	if got.State != string(StatePicked) {
		t.Errorf("state=%q want picked", got.State)
	}

	// Pick again → still idempotent, no second copy attempt.
	if err := svc.Pick(id); err != nil {
		t.Fatalf("Pick second call: %v", err)
	}
}

func TestExportTraversalPicksDirRejected(t *testing.T) {
	// Build a Service with a traversal-y picksDir and verify Export returns
	// an error without creating anything outside scanRoot.
	scanRoot := t.TempDir()
	dsn := "file:" + filepath.Join(scanRoot, ".choix-trav.db") + "?_pragma=foreign_keys(on)"
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// picksDir is a relative traversal path. New() accepts any string; the
	// containment guard in exportFile is what must stop it.
	svc := New(s, scanRoot, "../escaped")

	writeFile(t, scanRoot, "img.jpg", "data")
	id, err := s.Files().Insert(store.File{
		Path: "img.jpg", Size: 4, Mtime: 0, ContentHash: hashOf("data"),
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, exportErr := svc.Export(id)
	if exportErr == nil {
		t.Fatal("Export with traversal picksDir should return an error")
	}
	// Confirm nothing was created outside scanRoot.
	escaped := filepath.Join(filepath.Dir(scanRoot), "escaped")
	if _, statErr := os.Stat(escaped); statErr == nil {
		t.Errorf("traversal directory %s was created outside scanRoot", escaped)
	}
}

func TestUnexportTraversalPicksDirRejected(t *testing.T) {
	// Build a Service with a traversal-y picksDir; set an exported_path that
	// would resolve outside scanRoot and verify Unexport returns an error
	// without removing the file there.
	scanRoot := t.TempDir()
	dsn := "file:" + filepath.Join(scanRoot, ".choix-trav2.db") + "?_pragma=foreign_keys(on)"
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	svc := New(s, scanRoot, "../escaped2")

	id, err := s.Files().Insert(store.File{
		Path: "z.jpg", Size: 0, Mtime: 0, ContentHash: "z",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Manually record an exported_path that escapes the root.
	_ = s.Picks().SetState(id, string(StatePicked))
	_ = s.Picks().SetExportedPath(id, "../escaped2/z.jpg")

	// Create the "escaped" file so Remove would succeed if the guard is absent.
	escapedDir := filepath.Join(filepath.Dir(scanRoot), "escaped2")
	_ = os.MkdirAll(escapedDir, 0o750)
	escapedFile := filepath.Join(escapedDir, "z.jpg")
	_ = os.WriteFile(escapedFile, []byte("sensitive"), 0o644)
	t.Cleanup(func() { _ = os.RemoveAll(escapedDir) })

	unexportErr := svc.Unexport(id)
	if unexportErr == nil {
		t.Fatal("Unexport with traversal exported_path should return an error")
	}
	// The file outside scanRoot must still exist (not removed).
	if _, statErr := os.Stat(escapedFile); statErr != nil {
		t.Errorf("file outside scanRoot was incorrectly removed: %v", statErr)
	}
}

func hashOf(contents string) string {
	h := xxhash.New()
	_, _ = h.Write([]byte(contents))
	return fmt.Sprintf("%016x", h.Sum64())
}
