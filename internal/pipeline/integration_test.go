package pipeline_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/avmedia"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/imageio"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// ---------------------------------------------------------------------------
// Adapters bridging real package APIs to pipeline interfaces.
// ---------------------------------------------------------------------------

// storeIDLister wraps *store.Store to satisfy pipeline.IDLister.
// The real FilesRepo.IDsByStatus drops ctx; we add the ctx parameter here.
type storeIDLister struct{ s *store.Store }

func (l *storeIDLister) ListByStatus(ctx context.Context, status string, limit int) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.s.Files().IDsByStatus(status, limit)
}

func (l *storeIDLister) CountByStatus(ctx context.Context, status string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return l.s.Files().CountByStatus(status)
}

// metaProcessorAdapter wraps *meta.Extractor so its three-argument Process
// satisfies the two-argument pipeline.MetaProcessor interface by looking up
// the file path from the store.
type metaProcessorAdapter struct {
	ext  *meta.Extractor
	root string
	s    *store.Store
}

func (a *metaProcessorAdapter) Process(ctx context.Context, fileID int64) error {
	f, err := a.s.Files().GetByID(fileID)
	if err != nil {
		return fmt.Errorf("metaProcessorAdapter: get file %d: %w", fileID, err)
	}
	absPath := filepath.Join(a.root, f.Path)
	return a.ext.Process(ctx, fileID, absPath)
}

// stubAnalyzer is used when CoreML / ONNX models aren't available in the
// test environment. It populates ai_signals with deterministic values so
// the cluster stage has something to chew on.
type stubAnalyzer struct{ s *store.Store }

func (a *stubAnalyzer) Analyze(ctx context.Context, fileID int64) error {
	if err := a.s.AISignals().Upsert(store.AISignals{
		FileID:        fileID,
		ClipEmbedding: make([]byte, 512*4), // zero embedding ok for cluster smoke test
		ComputedAt:    sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	}); err != nil {
		return fmt.Errorf("stub analyze: upsert ai_signals: %w", err)
	}
	return a.s.Files().UpdateStatus(fileID, "analyzed", "")
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// setupCorpus copies up to n regular media files from src into dst,
// preserving filenames in the flat root. Returns the destination root.
func setupCorpus(t *testing.T, src string, n int) string {
	t.Helper()
	root := t.TempDir()
	if err := copyTreeMin(src, root, n); err != nil {
		t.Fatalf("copy corpus: %v", err)
	}
	return root
}

// copyTreeMin copies up to n regular files from src into dst (flat — no
// subdirectory structure). Silently stops after n copies.
func copyTreeMin(src, dst string, n int) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", src, err)
	}
	copied := 0
	for _, e := range entries {
		if copied >= n {
			break
		}
		if e.IsDir() {
			continue
		}
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
		copied++
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is an internal fixture path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // dst is a temp dir path
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func openTestStore(t *testing.T, root string) *store.Store {
	t.Helper()
	dbDir := filepath.Join(root, ".choix")
	if err := os.MkdirAll(dbDir, 0o750); err != nil {
		t.Fatalf("mkdir .choix: %v", err)
	}
	st, err := store.Open(filepath.Join(dbDir, "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func buildPipeline(t *testing.T, st *store.Store, root string) *pipeline.Pipeline {
	t.Helper()

	// Metadata and thumbnails come from ImageIO and AVFoundation in-process.
	mx := meta.NewExtractor(&meta.NativeReader{Stills: imageio.New(), Video: avmedia.New()}, st, root)

	tb := &thumb.Builder{
		ScanRoot: root,
		Store:    st,
		Stills:   imageio.New(),
		Video:    avmedia.New(),
	}

	var an pipeline.AnalyzeProcessor = &stubAnalyzer{s: st}

	grp := group.NewGrouper(st, 600, 0.92, false)

	return &pipeline.Pipeline{
		Store:         &storeIDLister{s: st},
		Scanner:       scanner.New(root, st),
		MetaExtractor: &metaProcessorAdapter{ext: mx, root: root, s: st},
		ThumbBuilder:  tb,
		Analyzer:      an,
		Grouper:       grp,
		Concurrency: map[string]int{
			"metadata": runtime.NumCPU(),
			"thumb":    runtime.NumCPU(),
			"analyze":  1,
		},
		BatchSize: 100,
	}
}

// corpusSrc returns the path to the exif integration fixtures that ship with
// the repo. These are the only guaranteed-present media files.
func corpusSrc(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../internal/pipeline/integration_test.go
	// we want .../testdata/exif/integration
	base := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(base, "testdata", "exif", "integration")
}

// ---------------------------------------------------------------------------
// Tests.
// ---------------------------------------------------------------------------

func TestPipelineEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipped under -short")
	}

	src := corpusSrc(t)
	root := setupCorpus(t, src, 5)
	st := openTestStore(t, root)

	prog := make(chan pipeline.Progress, 64)
	p := buildPipeline(t, st, root)
	p.Reporter = pipeline.NewReporter(prog)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(prog)

	// Every file must have reached scan_status='analyzed' or 'failed'.
	for _, status := range []string{"discovered", "metadata", "thumbed"} {
		ids, err := st.Files().IDsByStatus(status, 1000)
		if err != nil {
			t.Fatalf("IDsByStatus %s: %v", status, err)
		}
		if len(ids) > 0 {
			t.Errorf("files still in status %q after Run: %v", status, ids)
		}
	}

	// At least some files must have been discovered.
	analyzedIDs, err := st.Files().IDsByStatus("analyzed", 1000)
	failedIDs, err2 := st.Files().IDsByStatus("failed", 1000)
	if err != nil {
		t.Fatalf("IDsByStatus analyzed: %v", err)
	}
	if err2 != nil {
		t.Fatalf("IDsByStatus failed: %v", err2)
	}
	if len(analyzedIDs)+len(failedIDs) == 0 {
		t.Error("no files discovered")
	}

	// At least one cluster row must exist (if there are analyzed files).
	if len(analyzedIDs) > 0 {
		clusters, err := st.Clusters().ListAll()
		if err != nil {
			t.Fatalf("Clusters.ListAll: %v", err)
		}
		if len(clusters) == 0 {
			t.Error("no clusters created")
		}
	}

	// Progress channel must have emitted events for every per-file stage
	// plus the cluster stage.
	seen := map[string]int{}
	for ev := range prog {
		seen[ev.Stage]++
	}
	for _, want := range []string{"metadata", "thumb", "analyze", "cluster"} {
		if seen[want] == 0 {
			t.Errorf("no progress events for stage %q", want)
		}
	}
}

func TestPipelineEndToEndCancelLeavesConsistentState(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipped under -short")
	}

	src := corpusSrc(t)
	root := setupCorpus(t, src, 5)
	st := openTestStore(t, root)

	var ticks int32
	prog := make(chan pipeline.Progress, 64)
	go func() {
		for range prog {
			atomic.AddInt32(&ticks, 1)
		}
	}()

	p := buildPipeline(t, st, root)
	p.Reporter = pipeline.NewReporter(prog)
	p.BatchSize = 1

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Let discover finish, then cancel mid-stage.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = p.Run(ctx) // expected to return ctx.Err

	// Resume must complete the rest without panicking.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()
	if err := p.Resume(ctx2); err != nil {
		t.Fatalf("Resume after cancel: %v", err)
	}

	// After resume, all files must be analyzed or failed — no partial status.
	for _, status := range []string{"discovered", "metadata", "thumbed"} {
		ids, err := st.Files().IDsByStatus(status, 1000)
		if err != nil {
			t.Fatalf("IDsByStatus %s: %v", status, err)
		}
		if len(ids) > 0 {
			t.Errorf("after resume, files still in status %q: %v", status, ids)
		}
	}
	close(prog)
}

// cancelThenBuild cancels the run while the thumb stage is working on a
// file, then lets the real builder see the cancelled context.
type cancelThenBuild struct {
	cancel context.CancelFunc
	b      *thumb.Builder
}

func (c *cancelThenBuild) Build(ctx context.Context, fileID int64) error {
	c.cancel()
	return c.b.Build(ctx, fileID)
}

// A file interrupted by cancellation stays at its current status, so the
// next run resumes it; neither the store nor the progress stream reports it
// as failed. No external tool runs: every invocation sees a cancelled ctx.
func TestPipelineCancelMidStageLeavesFilePending(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("not decoded"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	st := openTestStore(t, root)
	id, err := st.Files().Insert(store.File{
		Path: "a.jpg", Size: 11, Mtime: 1, ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "metadata",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prog := make(chan pipeline.Progress, 256)
	p := &pipeline.Pipeline{
		Store:         &storeIDLister{s: st},
		Scanner:       scanner.New(root, st),
		MetaExtractor: &metaProcessorAdapter{ext: meta.NewExtractor(&meta.NativeReader{Stills: imageio.New(), Video: avmedia.New()}, st, root), root: root, s: st},
		ThumbBuilder: &cancelThenBuild{cancel: cancel, b: &thumb.Builder{
			ScanRoot: root, Store: st, Stills: imageio.New(), Video: avmedia.New(),
		}},
		Analyzer:    &stubAnalyzer{s: st},
		Grouper:     group.NewGrouper(st, 600, 0.92, false),
		Reporter:    pipeline.NewReporter(prog),
		Concurrency: map[string]int{"metadata": 1, "thumb": 1, "analyze": 1},
		BatchSize:   100,
	}

	if err := p.Resume(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resume = %v, want context.Canceled", err)
	}
	close(prog)
	for ev := range prog {
		if ev.Failed != 0 {
			t.Errorf("progress event %+v reports a failure", ev)
		}
	}
	row, err := st.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.ScanStatus != "metadata" || row.ErrMsg.Valid {
		t.Fatalf("after cancel: scan_status=%q error=%+v; want 'metadata' and no error (resumable)", row.ScanStatus, row.ErrMsg)
	}
}
