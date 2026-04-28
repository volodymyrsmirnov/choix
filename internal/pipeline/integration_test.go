package pipeline_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
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

// requireTools skips the test if exiftool is not in $PATH. ffmpeg is
// optional — videos are skipped if ffmpeg is absent, but photos still run.
func requireExiftool(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed — skipping integration test")
	}
	return p
}

// ffmpegPath returns the ffmpeg path if available, or "" if not.
func ffmpegPath() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

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
	dsn := "file:" + filepath.Join(dbDir, "state.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func buildPipeline(t *testing.T, st *store.Store, root, exiftoolBin string) *pipeline.Pipeline {
	t.Helper()

	etool := meta.New(exiftoolBin)
	mx := meta.NewExtractor(etool, st, root)

	var ffRunner *deps.Runner
	if fp := ffmpegPath(); fp != "" {
		ffRunner = deps.NewRunner(fp)
	} else {
		ffRunner = &deps.Runner{Path: "ffmpeg"} // will fail gracefully for video files
	}

	tb := &thumb.Builder{
		ScanRoot: root,
		ExifTool: etool,
		Ffmpeg:   ffRunner,
		Store:    st,
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
	exiftoolBin := requireExiftool(t)

	src := corpusSrc(t)
	root := setupCorpus(t, src, 5)
	st := openTestStore(t, root)

	prog := make(chan pipeline.Progress, 64)
	p := buildPipeline(t, st, root, exiftoolBin)
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
	exiftoolBin := requireExiftool(t)

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

	p := buildPipeline(t, st, root, exiftoolBin)
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
