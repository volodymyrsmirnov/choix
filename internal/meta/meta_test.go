package meta

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// An extraction interrupted by cancellation — the run's context is done, or
// the error wraps a context error — is not a failure of the file: the row
// keeps its status so the next run picks it up again.
func TestRecordFailureSkipsCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name  string
		ctx   context.Context
		cause error
	}{
		{"cancelled ctx", cancelled, errors.New("exiftool: signal: killed")},
		{"wrapped Canceled, live ctx", context.Background(), fmt.Errorf("exiftool: %w", context.Canceled)},
		{"wrapped DeadlineExceeded, live ctx", context.Background(), fmt.Errorf("exiftool: %w", context.DeadlineExceeded)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			id, err := s.Files().Insert(store.File{
				Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
			})
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			ext := &Extractor{store: s}
			if err := ext.recordFailure(tc.ctx, id, tc.cause); !errors.Is(err, tc.cause) {
				t.Fatalf("recordFailure = %v, want the cause", err)
			}
			got, err := s.Files().GetByID(id)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.ScanStatus != "discovered" || got.ErrMsg.Valid {
				t.Fatalf("scan_status=%q error=%+v; want 'discovered' and no error", got.ScanStatus, got.ErrMsg)
			}
		})
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
	s := newTestStore(t)
	abs, err := filepath.Abs("../../testdata/exif/sample.jpg")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	stills := &platformtest.Stills{Props: map[string]map[string]any{abs: loadDict(t, "imageio", "iphone_heic.json")}}

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
	ext := NewExtractor(&NativeReader{Stills: stills, Video: &platformtest.Video{}}, s, "")
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
