package local

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
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
	a := NewAnalyzer(s, NewSessions(models, SessionOptions{}))

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
	s, err := store.Open(filepath.Join(dir, "test.db"))
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

	a := NewAnalyzer(s, nil)

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

// A thumbnail that cannot be loaded while the folder is unreachable is not
// the file's failure: the guard keeps the row pending.
func TestAnalyzer_FailureGuard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		guard      func() bool
		wantStatus string
	}{
		{"folder reachable", func() bool { return true }, "failed"},
		{"folder gone", func() bool { return false }, "thumbed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStoreForTest(t)
			fileID := insertFile(t, s, "a.jpg") // 'thumbed'
			a := NewAnalyzer(s, nil)
			a.FailureGuard = tc.guard
			err := a.Analyze(context.Background(), fileID, filepath.Join(t.TempDir(), "nonexistent.jpg"))
			if err == nil {
				t.Fatal("Analyze with a missing thumb must return an error")
			}
			if got, want := errors.Is(err, pipeline.ErrFolderUnavailable), tc.wantStatus == "thumbed"; got != want {
				t.Fatalf("Analyze = %v; ErrFolderUnavailable %v, want %v", err, got, want)
			}
			f, err := s.Files().GetByID(fileID)
			if err != nil {
				t.Fatal(err)
			}
			if f.ScanStatus != tc.wantStatus {
				t.Errorf("scan_status = %q (error %q), want %q", f.ScanStatus, f.ErrMsg.String, tc.wantStatus)
			}
		})
	}
}

// fakeEmbedder returns a fixed embedding or error and counts its calls.
type fakeEmbedder struct {
	emb   []float32
	err   error
	calls int
}

func (f *fakeEmbedder) Embed(image.Image) ([]float32, error) {
	f.calls++
	return f.emb, f.err
}

// assertAnalyzedWithoutEmbedding checks the file reached analyzed with no
// error text and an ai_signals row whose clip_embedding is NULL.
func assertAnalyzedWithoutEmbedding(t *testing.T, s *store.Store, fileID int64) {
	t.Helper()
	row, err := s.AISignals().GetByFileID(fileID)
	if err != nil {
		t.Fatalf("GetByFileID: %v", err)
	}
	if row.ClipEmbedding != nil {
		t.Errorf("clip_embedding = %d bytes, want NULL", len(row.ClipEmbedding))
	}
	f, err := s.Files().GetByID(fileID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if f.ScanStatus != "analyzed" {
		t.Errorf("scan_status = %q, want analyzed", f.ScanStatus)
	}
	if f.ErrMsg.Valid && f.ErrMsg.String != "" {
		t.Errorf("error = %q, want none", f.ErrMsg.String)
	}
}

func TestAnalyzer_NilEmbedderWritesNullEmbedding(t *testing.T) {
	thumbPath := filepath.Join(t.TempDir(), "thumb.jpg")
	writeJPEG(t, thumbPath, color.RGBA{10, 20, 30, 255})
	s := newStoreForTest(t)
	fileID := insertFile(t, s, "Day1/A.jpg")

	if err := NewAnalyzer(s, nil).Analyze(context.Background(), fileID, thumbPath); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	assertAnalyzedWithoutEmbedding(t, s, fileID)
}

func TestAnalyzer_ModelMissingLeavesFileAnalyzed(t *testing.T) {
	thumbPath := filepath.Join(t.TempDir(), "thumb.jpg")
	writeJPEG(t, thumbPath, color.RGBA{10, 20, 30, 255})
	s := newStoreForTest(t)
	fileID := insertFile(t, s, "Day1/B.jpg")

	emb := &fakeEmbedder{err: fmt.Errorf("clip: %w", ErrModelMissing)}
	if err := NewAnalyzer(s, emb).Analyze(context.Background(), fileID, thumbPath); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if emb.calls != 1 {
		t.Fatalf("Embed calls = %d, want 1", emb.calls)
	}
	assertAnalyzedWithoutEmbedding(t, s, fileID)
}

// Any other embedder failure is treated the same way: the file is analyzed
// without an embedding, as in v1.
func TestAnalyzer_EmbedErrorLeavesFileAnalyzed(t *testing.T) {
	thumbPath := filepath.Join(t.TempDir(), "thumb.jpg")
	writeJPEG(t, thumbPath, color.RGBA{10, 20, 30, 255})
	s := newStoreForTest(t)
	fileID := insertFile(t, s, "Day1/C.jpg")

	if err := NewAnalyzer(s, &fakeEmbedder{err: errors.New("inference exploded")}).Analyze(context.Background(), fileID, thumbPath); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	assertAnalyzedWithoutEmbedding(t, s, fileID)
}

func TestAnalyzer_StoresEmbedding(t *testing.T) {
	thumbPath := filepath.Join(t.TempDir(), "thumb.jpg")
	writeJPEG(t, thumbPath, color.RGBA{10, 20, 30, 255})
	s := newStoreForTest(t)
	fileID := insertFile(t, s, "Day1/D.jpg")

	if err := NewAnalyzer(s, &fakeEmbedder{emb: []float32{1, 0}}).Analyze(context.Background(), fileID, thumbPath); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	row, err := s.AISignals().GetByFileID(fileID)
	if err != nil {
		t.Fatalf("GetByFileID: %v", err)
	}
	if want := float32sToBytes([]float32{1, 0}); string(row.ClipEmbedding) != string(want) {
		t.Errorf("clip_embedding = %v, want %v", row.ClipEmbedding, want)
	}
}
