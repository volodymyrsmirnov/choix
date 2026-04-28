package thumb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/meta"
)

func requireTools(t *testing.T) (*meta.ExifTool, *deps.Runner) {
	t.Helper()
	if _, err := exec.LookPath("exiftool"); err != nil {
		t.Skip("exiftool not on PATH; skipping")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping")
	}
	return meta.New("exiftool"), deps.NewRunner("ffmpeg")
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("fixture %s missing: %v", name, err)
	}
	return abs
}

func TestBuildPhotoTier1JPEG(t *testing.T) {
	et, ff := requireTools(t)
	src := fixturePath(t, "jpeg-with-exif.jpg")
	dst := filepath.Join(t.TempDir(), "out-thumb.jpg")

	w, h, err := BuildPhotoTier1(context.Background(), et, ff, src, dst)
	if err != nil {
		t.Fatalf("BuildPhotoTier1: %v", err)
	}
	if w != WidthThumb {
		t.Errorf("width = %d, want %d", w, WidthThumb)
	}
	if h <= 0 {
		t.Errorf("height = %d, want > 0", h)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("dst is empty")
	}
}

func TestBuildPhotoTier1HEIC(t *testing.T) {
	et, ff := requireTools(t)
	src := fixturePath(t, "sample.heic")
	dst := filepath.Join(t.TempDir(), "out-thumb.jpg")

	w, _, err := BuildPhotoTier1(context.Background(), et, ff, src, dst)
	if err != nil {
		t.Fatalf("BuildPhotoTier1 HEIC: %v", err)
	}
	if w != WidthThumb {
		t.Errorf("width = %d, want %d", w, WidthThumb)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("stat dst: %v", err)
	}
}

func TestBuildPhotoTier2JPEG(t *testing.T) {
	et, ff := requireTools(t)
	src := fixturePath(t, "jpeg-with-exif.jpg")
	dst := filepath.Join(t.TempDir(), "out-preview.jpg")

	w, _, err := BuildPhotoTier2(context.Background(), et, ff, src, dst)
	if err != nil {
		t.Fatalf("BuildPhotoTier2: %v", err)
	}
	if w != WidthPreview {
		t.Errorf("width = %d, want %d", w, WidthPreview)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("stat dst: %v", err)
	}
}

func TestBuildPhotoTier2RAF(t *testing.T) {
	et, ff := requireTools(t)
	src := fixturePath(t, "tiny.raf")
	dst := filepath.Join(t.TempDir(), "out-preview.jpg")

	w, _, err := BuildPhotoTier2(context.Background(), et, ff, src, dst)
	if err != nil {
		t.Fatalf("BuildPhotoTier2 RAF: %v", err)
	}
	if w != WidthPreview {
		t.Errorf("width = %d, want %d", w, WidthPreview)
	}
}
