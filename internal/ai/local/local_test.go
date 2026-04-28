package local

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// writeJPEG writes a 256x256 solid-color JPEG to path.
func writeJPEG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

// When the CLIP model is missing the analyzer must still write an
// ai_signals row (with a NULL embedding) and bump the file to
// scan_status='analyzed'. Otherwise the cluster step never sees the file
// and it stays stuck at 'thumbed'.
func TestAnalyzerWritesRowEvenWithoutCLIPModel(t *testing.T) {
	tmp := t.TempDir()
	thumbPath := filepath.Join(tmp, "thumb.jpg")
	writeJPEG(t, thumbPath, color.RGBA{128, 128, 128, 255})

	s := newStoreForTest(t)
	fileID := insertFile(t, s, "Day1/IMG.jpg")

	models := NewModelStore(filepath.Join(tmp, "models")) // empty -> no models present
	a := NewAnalyzer(s, models)

	if err := a.Analyze(context.Background(), fileID, thumbPath); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	row, err := s.AISignals().GetByFileID(fileID)
	if err != nil {
		t.Fatalf("GetByFileID: %v", err)
	}
	if row.ClipEmbedding != nil {
		t.Errorf("clip_embedding should be NULL when model absent; got %d bytes", len(row.ClipEmbedding))
	}

	f, err := s.Files().GetByID(fileID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if f.ScanStatus != "analyzed" {
		t.Errorf("scan_status=%q, want analyzed", f.ScanStatus)
	}

	if row.ComputedAt.Valid && time.Since(time.Unix(row.ComputedAt.Int64, 0)) > time.Minute {
		t.Errorf("computed_at too old: %v", row.ComputedAt.Int64)
	}
}

func newStoreForTest(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func insertFile(t *testing.T, s *store.Store, p string) int64 {
	t.Helper()
	id, err := s.Files().Insert(store.File{
		Path: p, Size: 1, Mtime: time.Now().Unix(), ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "thumbed",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}
	return id
}

// TestAnalyzeLoadImageFailureMarksFailed verifies that when Analyze cannot
// load the thumbnail (missing file), the file row is transitioned to
// scan_status='failed' so the pipeline does not re-queue it indefinitely.
func TestAnalyzeLoadImageFailureMarksFailed(t *testing.T) {
	tmp := t.TempDir()
	s := newStoreForTest(t)
	fileID := insertFile(t, s, "Day1/IMG_missing.jpg")

	models := NewModelStore(filepath.Join(tmp, "models")) // empty -> no CLIP model
	a := NewAnalyzer(s, models)

	// Pass a non-existent thumb path to force a loadImage failure.
	err := a.Analyze(context.Background(), fileID, filepath.Join(tmp, "nonexistent.jpg"))
	if err == nil {
		t.Fatal("Analyze with missing thumb must return an error")
	}

	f, gerr := s.Files().GetByID(fileID)
	if gerr != nil {
		t.Fatalf("GetByID: %v", gerr)
	}
	if f.ScanStatus != "failed" {
		t.Errorf("scan_status = %q, want failed", f.ScanStatus)
	}
	if !f.ErrMsg.Valid || f.ErrMsg.String == "" {
		t.Errorf("error = %+v, want non-empty", f.ErrMsg)
	}
}
