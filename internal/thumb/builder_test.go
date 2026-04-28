package thumb

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// copyFile is a tiny helper used by the fixture-based tests below.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create dst: %v", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy: %v", err)
	}
}

func newTestStoreInThumb(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "state.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBuilderBuildPhoto(t *testing.T) {
	et, ff := requireTools(t)
	srcFixture := fixturePath(t, "jpeg-with-exif.jpg")

	root := t.TempDir()
	rel := "Day1/IMG_0001.jpg"
	abs := filepath.Join(root, rel)
	copyFile(t, srcFixture, abs)

	s := newTestStoreInThumb(t)
	id, err := s.Files().Insert(store.File{
		Path:        rel,
		Size:        1,
		Mtime:       time.Now().Unix(),
		ContentHash: "h",
		Kind:        "photo",
		Format:      "jpeg",
		ScanStatus:  "metadata",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	b := &Builder{ScanRoot: root, ExifTool: et, Ffmpeg: ff, Store: s}
	if err := b.Build(context.Background(), id); err != nil {
		t.Fatalf("Build: %v", err)
	}

	row, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.ScanStatus != "thumbed" {
		t.Errorf("scan_status = %q, want %q", row.ScanStatus, "thumbed")
	}

	for _, tier := range []string{TierThumb, TierPreview} {
		path := CachePath(root, id, tier)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing cached %s: %v", tier, err)
		}
		th, err := s.Thumbs().Get(id, tier)
		if err != nil {
			t.Errorf("Thumbs.Get(%s): %v", tier, err)
			continue
		}
		if th.Width <= 0 || th.Height <= 0 {
			t.Errorf("Thumbs(%s) dims = %dx%d, want > 0", tier, th.Width, th.Height)
		}
	}
}

func TestBuilderBuildVideo(t *testing.T) {
	et, ff := requireTools(t)
	srcFixture := fixturePath(t, "trimmed.mov")

	root := t.TempDir()
	rel := "Day1/CLIP.MOV"
	abs := filepath.Join(root, rel)
	copyFile(t, srcFixture, abs)

	s := newTestStoreInThumb(t)
	id, err := s.Files().Insert(store.File{
		Path:        rel,
		Size:        1,
		Mtime:       time.Now().Unix(),
		ContentHash: "h",
		Kind:        "video",
		Format:      "mov",
		ScanStatus:  "metadata",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	b := &Builder{ScanRoot: root, ExifTool: et, Ffmpeg: ff, Store: s}
	if err := b.Build(context.Background(), id); err != nil {
		t.Fatalf("Build: %v", err)
	}

	row, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.ScanStatus != "thumbed" {
		t.Errorf("scan_status = %q, want %q", row.ScanStatus, "thumbed")
	}

	thumbPath := CachePath(root, id, TierThumb)
	if _, err := os.Stat(thumbPath); err != nil {
		t.Errorf("missing tier-1 thumb: %v", err)
	}
	kfDir := KeyframesDir(root, id)
	entries, err := os.ReadDir(kfDir)
	if err != nil {
		t.Fatalf("read keyframes dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no keyframes written")
	}
}

func TestBuilderBuildByStatus(t *testing.T) {
	et, ff := requireTools(t)
	srcFixture := fixturePath(t, "jpeg-with-exif.jpg")

	root := t.TempDir()
	s := newTestStoreInThumb(t)

	var ids []int64
	for i := 1; i <= 3; i++ {
		rel := filepath.Join("Day1", fmt.Sprintf("IMG_%04d.jpg", i))
		copyFile(t, srcFixture, filepath.Join(root, rel))
		id, err := s.Files().Insert(store.File{
			Path:        rel,
			Size:        1,
			Mtime:       time.Now().Unix(),
			ContentHash: "h",
			Kind:        "photo",
			Format:      "jpeg",
			ScanStatus:  "metadata",
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		ids = append(ids, id)
	}

	// One row that should be ignored because it's already analyzed.
	if _, err := s.Files().Insert(store.File{
		Path: "skip.jpg", Size: 1, Mtime: time.Now().Unix(), ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	}); err != nil {
		t.Fatalf("Insert skip: %v", err)
	}

	b := &Builder{ScanRoot: root, ExifTool: et, Ffmpeg: ff, Store: s}
	n, err := b.BuildByStatus(context.Background(), "metadata", 10)
	if err != nil {
		t.Fatalf("BuildByStatus: %v", err)
	}
	if n != 3 {
		t.Errorf("processed = %d, want 3", n)
	}
	for _, id := range ids {
		row, err := s.Files().GetByID(id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if row.ScanStatus != "thumbed" {
			t.Errorf("file %d status = %q, want thumbed", id, row.ScanStatus)
		}
	}
}

func TestBuilderBuildMissingFileMarksFailed(t *testing.T) {
	et, ff := requireTools(t)
	root := t.TempDir()
	s := newTestStoreInThumb(t)
	id, err := s.Files().Insert(store.File{
		Path:        "missing.jpg",
		Size:        0,
		Mtime:       0,
		ContentHash: "h",
		Kind:        "photo",
		Format:      "jpeg",
		ScanStatus:  "metadata",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	b := &Builder{ScanRoot: root, ExifTool: et, Ffmpeg: ff, Store: s}
	if err := b.Build(context.Background(), id); err == nil {
		t.Fatalf("expected error for missing source")
	}
	row, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.ScanStatus != "failed" {
		t.Errorf("scan_status = %q, want %q", row.ScanStatus, "failed")
	}
	if !row.ErrMsg.Valid || row.ErrMsg.String == "" {
		t.Errorf("expected non-empty error message, got %+v", row.ErrMsg)
	}
}
