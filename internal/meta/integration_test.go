package meta

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestScannerMetadataPipeline(t *testing.T) {
	exiftoolPath, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}

	root, err := filepath.Abs("../../testdata/exif/integration")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	s := newTestStore(t)

	// Phase 3 scanner: discover all media under root, populate files table.
	if err := scanner.New(root, s).Walk(context.Background()); err != nil {
		t.Fatalf("scanner.Walk: %v", err)
	}

	// Phase 4 extractor: process every "discovered" row. Scanner stores
	// paths relative to root; ProcessByStatus joins them with this root
	// before invoking exiftool.
	ext := NewExtractor(New(exiftoolPath), s, root)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	n, err := ext.ProcessByStatus(ctx, "discovered", 100)
	if err != nil {
		t.Fatalf("ProcessByStatus: %v", err)
	}
	if n == 0 {
		t.Fatal("ProcessByStatus: 0 successes; integration corpus likely missing")
	}

	processed, err := s.Files().PickByStatus(ctx, "metadata", 100)
	if err != nil {
		t.Fatalf("PickByStatus: %v", err)
	}
	if len(processed) == 0 {
		t.Fatal("no rows reached 'metadata' status")
	}

	for _, f := range processed {
		if !f.DeviceKey.Valid || f.DeviceKey.String == "" {
			t.Errorf("%s: device_key not populated", f.Path)
		}
		if !f.CapturedAt.Valid || f.CapturedAt.Int64 <= 0 {
			t.Errorf("%s: captured_at not populated (%+v)", f.Path, f.CapturedAt)
		}
		if !f.Width.Valid || f.Width.Int64 <= 0 {
			t.Errorf("%s: width not populated (%+v)", f.Path, f.Width)
		}
		if !f.Height.Valid || f.Height.Int64 <= 0 {
			t.Errorf("%s: height not populated (%+v)", f.Path, f.Height)
		}
		if len(f.RawExif) == 0 {
			t.Errorf("%s: raw_exif empty", f.Path)
		}
	}

	_ = store.File{} // ensure import is used even if struct already referenced
}
