package thumb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
)

func TestIntegrationScanMetaThumb(t *testing.T) {
	et, ff := requireTools(t)

	fixtures := []struct{ src, rel string }{
		{"jpeg-with-exif.jpg", "Day1/IMG_0001.jpg"},
		{"sample.heic", "Day1/IMG_0002.HEIC"},
		{"tiny.raf", "Day1/IMG_0003.RAF"},
		{"no-exif.jpg", "Day1/IMG_0004.jpg"},
		{"trimmed.mov", "Day1/CLIP_0005.MOV"},
	}

	root := t.TempDir()
	for _, f := range fixtures {
		src := fixturePath(t, f.src)
		copyFile(t, src, filepath.Join(root, f.rel))
	}

	s := newTestStoreInThumb(t)

	// Stage 1: scan.
	sc := scanner.New(root, s)
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatalf("scanner.Scan: %v", err)
	}

	// Stage 2: metadata.
	mb := meta.NewExtractor(et, s, root)
	if _, err := mb.ProcessByStatus(context.Background(), "discovered", 100); err != nil {
		t.Fatalf("meta.ProcessByStatus: %v", err)
	}

	// Stage 3: thumb.
	b := &Builder{ScanRoot: root, ExifTool: et, Ffmpeg: ff, Store: s}
	n, err := b.BuildByStatus(context.Background(), "metadata", 100)
	if err != nil {
		t.Fatalf("thumb.BuildByStatus: %v", err)
	}
	if n != len(fixtures) {
		t.Fatalf("processed = %d, want %d", n, len(fixtures))
	}

	// Assert: every file is thumbed and has its expected caches/rows.
	for _, f := range fixtures {
		row, err := s.Files().GetByPath(f.rel)
		if err != nil {
			t.Fatalf("GetByPath %s: %v", f.rel, err)
		}
		if row.ScanStatus != "thumbed" {
			t.Errorf("%s status = %q, want thumbed (err=%v)", f.rel, row.ScanStatus, row.ErrMsg)
			continue
		}

		// Tier-1 thumbnail always present.
		if _, err := os.Stat(CachePath(root, row.ID, TierThumb)); err != nil {
			t.Errorf("%s: missing tier-1 thumb: %v", f.rel, err)
		}
		if _, err := s.Thumbs().Get(row.ID, TierThumb); err != nil {
			t.Errorf("%s: missing thumbs row tier-1: %v", f.rel, err)
		}

		switch row.Kind {
		case "photo":
			if _, err := os.Stat(CachePath(root, row.ID, TierPreview)); err != nil {
				t.Errorf("%s: missing tier-2 preview: %v", f.rel, err)
			}
			if _, err := s.Thumbs().Get(row.ID, TierPreview); err != nil {
				t.Errorf("%s: missing thumbs row tier-2: %v", f.rel, err)
			}
		case "video":
			entries, err := os.ReadDir(KeyframesDir(root, row.ID))
			if err != nil || len(entries) == 0 {
				t.Errorf("%s: missing keyframes dir or empty: err=%v", f.rel, err)
			}
		}
	}
}
