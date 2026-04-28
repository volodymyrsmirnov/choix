package meta

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
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

func TestExtractorProcess(t *testing.T) {
	exiftoolPath, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}

	s := newTestStore(t)
	abs, err := filepath.Abs("../../testdata/exif/sample.jpg")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	id, err := s.Files().Insert(store.File{
		Path: "sample.jpg", Size: 1, Mtime: time.Now().Unix(),
		ContentHash: "deadbeef", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Empty root: Process is given an absolute path directly, no join needed.
	ext := NewExtractor(New(exiftoolPath), s, "")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ext.Process(ctx, id, abs); err != nil {
		t.Fatalf("Process: %v", err)
	}

	got, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ScanStatus != "metadata" {
		t.Errorf("ScanStatus: got %q want metadata", got.ScanStatus)
	}
	if len(got.RawExif) == 0 {
		t.Error("RawExif: empty")
	}

	// raw_exif is gzipped JSON; verify it decompresses to a non-empty array.
	zr, err := gzip.NewReader(strings.NewReader(string(got.RawExif)))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	var records []map[string]any
	if err := json.Unmarshal(plain, &records); err != nil {
		t.Fatalf("unmarshal raw_exif: %v (payload: %s)", err, plain)
	}
	if len(records) == 0 {
		t.Error("raw_exif decoded to empty array")
	}
}

func TestExtractorProcessMissingFile(t *testing.T) {
	exiftoolPath, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}

	s := newTestStore(t)
	id, err := s.Files().Insert(store.File{
		Path: "ghost.jpg", Size: 1, Mtime: time.Now().Unix(),
		ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	ext := NewExtractor(New(exiftoolPath), s, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = ext.Process(ctx, id, "/nonexistent/path/that/does/not/exist.jpg")
	if err == nil {
		t.Fatal("Process: expected error, got nil")
	}

	got, gerr := s.Files().GetByID(id)
	if gerr != nil {
		t.Fatalf("GetByID: %v", gerr)
	}
	if got.ScanStatus != "failed" {
		t.Errorf("ScanStatus: got %q want failed", got.ScanStatus)
	}
	if !got.ErrMsg.Valid || got.ErrMsg.String == "" {
		t.Errorf("ErrMsg: got %+v want non-empty", got.ErrMsg)
	}
}

// TestRecordFailurePreservesExistingEXIF verifies that calling recordFailure
// on a file that already has valid EXIF metadata does not overwrite those
// columns with NULLs. Only scan_status and error change.
func TestRecordFailurePreservesExistingEXIF(t *testing.T) {
	s := newTestStore(t)

	// Insert a file row with full metadata already persisted (as if a
	// previous successful exiftool run had already updated this row).
	id, err := s.Files().Insert(store.File{
		Path: "Day1/IMG_001.jpg", Size: 100, Mtime: 1000, ContentHash: "abc",
		Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.Files().UpdateMetadataFull(context.Background(), id, store.MetadataUpdate{
		DeviceKey:   "Fujifilm X-T5#A1",
		CapturedAt:  1700000000,
		HasCaptured: true,
		Width:       6240,
		Height:      4160,
		ISO:         400,
		RawExif:     []byte{0x1f, 0x8b, 0x08, 0x00},
		ScanStatus:  "metadata",
	}); err != nil {
		t.Fatalf("UpdateMetadataFull: %v", err)
	}

	// Simulate a transient failure via recordFailure (package-internal).
	ext := &Extractor{store: s}
	if err := ext.recordFailure(context.Background(), id, errors.New("synthetic transient error")); err == nil {
		t.Fatal("recordFailure must return the cause error")
	}

	got, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	// Status and error must change.
	if got.ScanStatus != "failed" {
		t.Errorf("ScanStatus = %q, want failed", got.ScanStatus)
	}
	if !got.ErrMsg.Valid || got.ErrMsg.String == "" {
		t.Errorf("ErrMsg = %+v, want non-empty", got.ErrMsg)
	}
	// All metadata columns must be preserved exactly.
	if !got.DeviceKey.Valid || got.DeviceKey.String != "Fujifilm X-T5#A1" {
		t.Errorf("DeviceKey = %+v, want Fujifilm X-T5#A1", got.DeviceKey)
	}
	if !got.CapturedAt.Valid || got.CapturedAt.Int64 != 1700000000 {
		t.Errorf("CapturedAt = %+v, want 1700000000", got.CapturedAt)
	}
	if !got.Width.Valid || got.Width.Int64 != 6240 {
		t.Errorf("Width = %+v, want 6240", got.Width)
	}
	if len(got.RawExif) != 4 {
		t.Errorf("RawExif len = %d, want 4", len(got.RawExif))
	}
}

func TestExtractorProcessByStatus(t *testing.T) {
	exiftoolPath, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}

	s := newTestStore(t)
	abs, err := filepath.Abs("../../testdata/exif/sample.jpg")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	// Insert three rows; one points at a real file, two at missing paths.
	// We expect 1 success and 2 failures, all to be drained in a single batch.
	_, err = s.Files().Insert(store.File{
		Path: abs, Size: 1, Mtime: time.Now().Unix(),
		ContentHash: "h1", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	for i := 0; i < 2; i++ {
		_, err = s.Files().Insert(store.File{
			Path: "/nonexistent/x" + string(rune('1'+i)) + ".jpg", Size: 1, Mtime: time.Now().Unix(),
			ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// Empty root: this test stores absolute paths in the `path` column, so
	// joining root + path is a no-op (filepath.Join("", abs) == abs).
	ext := NewExtractor(New(exiftoolPath), s, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	n, err := ext.ProcessByStatus(ctx, "discovered", 100)
	if err != nil {
		t.Fatalf("ProcessByStatus: %v", err)
	}
	if n != 1 {
		t.Errorf("succeeded count: got %d want 1", n)
	}

	// All three rows should have left the "discovered" status: the success
	// becomes "metadata", the failures become "failed".
	remaining, err := s.Files().PickByStatus(ctx, "discovered", 100)
	if err != nil {
		t.Fatalf("PickByStatus: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected no rows still 'discovered', got %d", len(remaining))
	}
	failed, err := s.Files().PickByStatus(ctx, "failed", 100)
	if err != nil {
		t.Fatalf("PickByStatus(failed): %v", err)
	}
	if len(failed) != 2 {
		t.Errorf("failed count: got %d want 2", len(failed))
	}
}
