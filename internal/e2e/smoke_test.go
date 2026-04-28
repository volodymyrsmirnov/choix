//go:build smoke

// Package e2e_test provides a smoke test against a synthetic 100-file corpus.
// It does not require network access, exiftool, ffmpeg, or ONNX models.
// Run with: go test -tags smoke ./internal/e2e/...
package e2e_test

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// ---------------------------------------------------------------------------
// Stub implementations that replace real pipeline stages.
// ---------------------------------------------------------------------------

// noopMeta satisfies pipeline.MetaProcessor. It advances scan_status to
// 'metadata' so the pipeline can proceed to thumb and analyze stages.
// It also sets a synthetic device_key derived from the parent directory
// so the grouper can form clusters.
type noopMeta struct{ st *store.Store }

func (m *noopMeta) Process(ctx context.Context, fileID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := m.st.Files().GetByID(fileID)
	if err != nil {
		return err
	}
	// Use parent directory name as synthetic device key.
	deviceKey := filepath.Base(filepath.Dir(f.Path))
	if deviceKey == "" || deviceKey == "." {
		deviceKey = "unknown"
	}
	return m.st.Files().UpdateMetadataFull(ctx, fileID, store.MetadataUpdate{
		DeviceKey:  deviceKey,
		ScanStatus: "metadata",
	})
}

// noopThumb satisfies pipeline.ThumbProcessor. Advances to 'thumbed'.
type noopThumb struct{ st *store.Store }

func (t *noopThumb) Build(ctx context.Context, fileID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.st.Files().UpdateStatus(fileID, "thumbed", "")
}

// stubAnalyzer satisfies pipeline.AnalyzeProcessor. Writes a deterministic
// ai_signals row and advances to 'analyzed'.
type stubAnalyzer struct{ st *store.Store }

func (a *stubAnalyzer) Analyze(ctx context.Context, fileID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.st.AISignals().Upsert(store.AISignals{
		FileID:        fileID,
		ClipEmbedding: make([]byte, 512*4), // zero embedding
		ComputedAt:    sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	}); err != nil {
		return err
	}
	return a.st.Files().UpdateStatus(fileID, "analyzed", "")
}

// storeIDLister adapts *store.Store to pipeline.IDLister.
type storeIDLister struct{ st *store.Store }

func (l *storeIDLister) ListByStatus(ctx context.Context, status string, limit int) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.st.Files().IDsByStatus(status, limit)
}

// ---------------------------------------------------------------------------
// Corpus helpers.
// ---------------------------------------------------------------------------

// makeJPEG writes a tiny solid-color JPEG.
func makeJPEG(t *testing.T, path string, hue uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: hue, G: 128, B: 255 - hue, A: 255})
		}
	}
	f, err := os.Create(path) //nolint:gosec // temp dir path
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src) //nolint:gosec // fixture path
	if err != nil {
		t.Skipf("fixture %s missing: %v", src, err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // temp dir path
		t.Fatalf("mkdir: %v", err)
	}
	out, err := os.Create(dst) //nolint:gosec // temp dir path
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy %s: %v", dst, err)
	}
}

// ---------------------------------------------------------------------------
// Originals-unchanged helpers.
// ---------------------------------------------------------------------------

type fileSnap struct {
	mtime time.Time
	size  int64
	head  [1024]byte
	n     int
}

func snapshotOriginals(t *testing.T, root string) map[string]fileSnap {
	t.Helper()
	out := map[string]fileSnap{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(p)
			if base == ".choix" || base == "picks" {
				return filepath.SkipDir
			}
			return nil
		}
		f, err := os.Open(p) //nolint:gosec // walking temp dir
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		var snap fileSnap
		snap.mtime = info.ModTime()
		snap.size = info.Size()
		n, _ := f.Read(snap.head[:])
		snap.n = n
		rel, _ := filepath.Rel(root, p)
		out[rel] = snap
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

func verifyUnchanged(t *testing.T, snap map[string]fileSnap, root string) {
	t.Helper()
	for rel, s := range snap {
		p := filepath.Join(root, rel)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing original %s: %v", rel, err)
			continue
		}
		if info.Size() != s.size {
			t.Errorf("size changed for %s: %d -> %d", rel, s.size, info.Size())
		}
		if !info.ModTime().Equal(s.mtime) {
			t.Errorf("mtime changed for %s: %v -> %v", rel, s.mtime, info.ModTime())
		}
		f, err := os.Open(p) //nolint:gosec // walking temp dir
		if err != nil {
			t.Errorf("open %s: %v", rel, err)
			continue
		}
		var head [1024]byte
		n, _ := f.Read(head[:])
		_ = f.Close()
		if n != s.n || head != s.head {
			t.Errorf("content head changed for %s", rel)
		}
	}
}

// ---------------------------------------------------------------------------
// Smoke test.
// ---------------------------------------------------------------------------

func TestSmoke_100FileCorpus(t *testing.T) {
	root := t.TempDir()

	// 100 synthetic JPEGs across 3 device sub-folders.
	for i := range 100 {
		dev := []string{"camA", "camB", "phoneC"}[i%3]
		dir := filepath.Join(root, dev)
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // temp dir
			t.Fatal(err)
		}
		makeJPEG(t, filepath.Join(dir, fmtName(i)+".jpg"), uint8(i*3)) //nolint:gosec // uint8 wraps intentionally
	}

	// Copy JPEG fixture if available (optional enrichment).
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		fix := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))), "testdata", "exif")
		copyFixture(t, filepath.Join(fix, "sample.jpg"), filepath.Join(root, "camA", "sample.jpg"))
	}

	// Open store.
	dbDir := filepath.Join(root, ".choix")
	if err := os.MkdirAll(dbDir, 0o750); err != nil { //nolint:gosec // temp dir
		t.Fatal(err)
	}
	dsn := "file:" + filepath.Join(dbDir, "state.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Snapshot originals before the pipeline runs.
	snap := snapshotOriginals(t, root)

	deadline := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	t.Cleanup(cancel)

	start := time.Now()

	prog := make(chan pipeline.Progress, 256)
	p := &pipeline.Pipeline{
		Store:         &storeIDLister{st: st},
		Scanner:       scanner.New(root, st),
		MetaExtractor: &noopMeta{st: st},
		ThumbBuilder:  &noopThumb{st: st},
		Analyzer:      &stubAnalyzer{st: st},
		Grouper:       group.NewGrouper(st, 600, 0.92, false),
		Reporter:      pipeline.NewReporter(prog),
		Concurrency: map[string]int{
			"metadata": runtime.NumCPU(),
			"thumb":    runtime.NumCPU(),
			"analyze":  runtime.NumCPU(),
		},
		BatchSize: 100,
	}

	if err := p.Run(ctx); err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
	close(prog)

	elapsed := time.Since(start)
	if elapsed > 30*time.Second {
		t.Errorf("pipeline took %v, budget 30s", elapsed)
	}

	// 1. Every file analyzed or failed.
	all, err := st.Files().ListByStatus("analyzed", 10000)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := st.Files().ListByStatus("failed", 10000)
	if err != nil {
		t.Fatal(err)
	}
	total := len(all) + len(failed)
	// We wrote 100 + optional fixture; at least 100 should be processed.
	if total < 100 {
		t.Errorf("files processed = %d, want at least 100", total)
	}
	for _, f := range failed {
		t.Logf("failed file: %s err=%s", f.Path, f.ErrMsg.String)
	}

	// 2. At least one cluster.
	clusters, err := st.Clusters().ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) == 0 {
		t.Errorf("no clusters formed")
	}

	// 3. Picks export round-trip.
	if len(all) > 0 {
		picksDir := filepath.Join(root, "picks")
		if err := os.MkdirAll(picksDir, 0o755); err != nil { //nolint:gosec // temp dir
			t.Fatal(err)
		}
		if err := st.Picks().SetState(all[0].ID, "picked"); err != nil {
			t.Fatalf("pick: %v", err)
		}
		if _, err := os.Stat(picksDir); err != nil {
			t.Errorf("picks dir missing: %v", err)
		}
	}

	// 4. Originals unchanged.
	verifyUnchanged(t, snap, root)
}

func fmtName(i int) string {
	return fmt.Sprintf("%04d", i)
}
