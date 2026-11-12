package session

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/analyze"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/quality"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// sessionTestDay is the synthetic capture day of the pipeline tests.
var sessionTestDay = time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

// sessionTestWriteJPEG writes a small real JPEG at rel inside root.
func sessionTestWriteJPEG(t *testing.T, root, rel string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, platformtest.MakeJPEG(t, 64, 48), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	return abs
}

// sessionTestStillProps is what the fake ImageIO reports for a JPEG taken
// at captured by the synthetic camera.
func sessionTestStillProps(captured time.Time) map[string]any {
	return map[string]any{
		"{Exif}": map[string]any{"DateTimeOriginal": captured.Format("2006:01:02 15:04:05")},
		"{TIFF}": map[string]any{"Make": "TESTCAM", "Model": "D1"},
	}
}

// sessionTestInsertAnalyzed inserts an analyzed photo row captured at
// captured, with emb as its feature print (stamped analyze.PrintID) when not
// nil.
func sessionTestInsertAnalyzed(t *testing.T, st *store.Store, rel string, captured time.Time, emb []float32) int64 {
	t.Helper()
	id, err := st.Files().Insert(store.File{Path: rel, Size: 1, Mtime: 1, ContentHash: "h-" + rel, Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(context.Background(),
		`UPDATE files SET device_key = 'TESTCAM D1#TESTSERIAL01', captured_at = ? WHERE id = ?`,
		captured.Unix(), id); err != nil {
		t.Fatal(err)
	}
	if emb != nil {
		if err := st.AISignals().UpsertPrint(id, emb, analyze.PrintID); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// sessionTestUnit returns a feature-print-long unit vector along axis i.
func sessionTestUnit(i int) []float32 {
	v := make([]float32, platform.FeaturePrintLen)
	v[i] = 1
	return v
}

func sessionTestClusterCount(t *testing.T, st *store.Store) int {
	t.Helper()
	cl, err := st.Clusters().ListAll()
	if err != nil {
		t.Fatal(err)
	}
	return len(cl)
}

func sessionTestStoredParams(t *testing.T, st *store.Store) string {
	t.Helper()
	v, err := st.KV().Get(sessionKVClusterParams)
	if err != nil {
		t.Fatalf("cluster_params: %v", err)
	}
	return v
}

// sessionTestSend hands one value to a fake's Gate, failing when nothing
// takes it.
func sessionTestSend(t *testing.T, gate chan struct{}) {
	t.Helper()
	select {
	case gate <- struct{}{}:
	case <-time.After(sessionTestTimeout):
		t.Fatal("no native call reached the gate")
	}
}

func sessionTestWaitErr(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(sessionTestTimeout):
		t.Fatal("the run did not finish")
		return nil
	}
}

func TestClusterParams(t *testing.T) {
	cfg := config.Defaults()
	if got, want := ClusterParams(cfg), "b=600;t=0.9000;x=false;v=true;m=vnfp-r2/fill/preview/v1+complete"; got != want {
		t.Fatalf("ClusterParams(defaults) = %q; want %q", got, want)
	}
}

// TestEnsureClustered_OldParamsRecluster: a folder clustered by an earlier
// build (its params have no method, and another similarity) re-clusters by
// itself when it is opened, and keeps every pick.
func TestEnsureClustered_OldParamsRecluster(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, _ := h.folder(t, "old")
	s, release := h.acquire(t, id)
	defer release()
	ctx := context.Background()
	var ids []int64
	for i := range 3 {
		ids = append(ids, sessionTestInsertAnalyzed(t, s.Store, string(rune('a'+i))+".jpg",
			sessionTestDay.Add(time.Duration(i)*time.Second), nil))
	}
	picked := []store.Pick{
		{FileID: ids[0], State: "picked", PickedAt: 100, Rating: sql.NullInt64{Int64: 4, Valid: true}},
		{FileID: ids[2], State: "rejected", PickedAt: 200},
	}
	for _, p := range picked {
		if err := s.Store.Picks().Upsert(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.KV().Set(sessionKVClusterParams, "b=600;t=0.9200;x=false;v=true"); err != nil {
		t.Fatal(err)
	}

	reclustered, err := s.EnsureClustered(ctx, h.settings.get)
	if err != nil || !reclustered {
		t.Fatalf("EnsureClustered = %v, %v; want true", reclustered, err)
	}
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("stored params = %q; want %q", got, want)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 1 {
		t.Fatalf("clusters = %d; want 1 (one time bucket, no prints yet)", n)
	}
	for _, want := range picked {
		got, err := s.Store.Picks().Get(want.FileID)
		if err != nil || got != want {
			t.Fatalf("pick of %d = %+v, %v; want %+v", want.FileID, got, err, want)
		}
	}
	if again, err := s.EnsureClustered(ctx, h.settings.get); err != nil || again {
		t.Fatalf("second EnsureClustered = %v, %v; want false", again, err)
	}
}

func TestEnsureClustered_ReclustersOnChange(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, _ := h.folder(t, "change")
	s, release := h.acquire(t, id)
	defer release()
	ctx := context.Background()
	for i := range 3 {
		sessionTestInsertAnalyzed(t, s.Store, filepath.ToSlash(filepath.Join("d", string(rune('a'+i))+".jpg")),
			sessionTestDay.Add(time.Duration(i)*100*time.Second), nil)
	}

	if err := s.Recluster(ctx, h.settings.get); err != nil {
		t.Fatal(err)
	}
	if got := sessionTestStoredParams(t, s.Store); got != ClusterParams(h.settings.get()) {
		t.Fatalf("stored params = %q; want the bucket-600 params", got)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 1 {
		t.Fatalf("clusters with a 600 s bucket = %d; want 1", n)
	}
	if again, err := s.EnsureClustered(ctx, h.settings.get); err != nil || again {
		t.Fatalf("EnsureClustered with unchanged settings = %v, %v; want false", again, err)
	}

	h.settings.set(func(c *config.Config) { c.BucketSizeSec = 60 })
	reclustered, err := s.EnsureClustered(ctx, h.settings.get)
	if err != nil || !reclustered {
		t.Fatalf("EnsureClustered = %v, %v; want true", reclustered, err)
	}
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("stored params = %q; want %q", got, want)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 3 {
		t.Fatalf("clusters with a 60 s bucket = %d; want 3", n)
	}
}

func TestClusterer_UsesSettingsAtClusterTime(t *testing.T) {
	gate := make(chan struct{})
	stills := &platformtest.Stills{ReadPaths: true, Gate: gate, Props: map[string]map[string]any{}}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "timing")
	for i, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		abs := sessionTestWriteJPEG(t, root, name)
		stills.Props[abs] = sessionTestStillProps(sessionTestDay.Add(time.Duration(i) * 100 * time.Second))
	}
	s, release := h.acquire(t, id)
	defer release()

	p, _ := s.NewPipeline(h.settings.get) // built while the bucket is 600 s
	errc := make(chan error, 1)
	go func() { errc <- p.Run(context.Background()) }()
	for range 3 {
		sessionTestSend(t, gate) // the metadata stage: one Properties call per file
	}
	sessionTestSend(t, gate) // a thumb-stage call is held: the thumb stage is running
	h.settings.set(func(c *config.Config) { c.BucketSizeSec = 60 })
	close(gate)
	if err := sessionTestWaitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("stored params = %q; want %q", got, want)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 3 {
		t.Fatalf("clusters = %d; want 3 (a 60 s bucket over shots 100 s apart)", n)
	}
}

func TestClusterer_OlderRunCannotOverwriteNewer(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, _ := h.folder(t, "order")
	s, release := h.acquire(t, id)
	defer release()
	for i := range 3 {
		sessionTestInsertAnalyzed(t, s.Store, string(rune('a'+i))+".jpg",
			sessionTestDay.Add(time.Duration(i)*100*time.Second), nil)
	}

	p, _ := s.NewPipeline(h.settings.get) // built while the bucket is 600 s

	var (
		mu       sync.Mutex
		stored   []string
		locks    atomic.Int64
		waits    atomic.Int64
		locked   = make(chan struct{})
		waiting  = make(chan struct{})
		proceed  = make(chan struct{})
		lockOnce sync.Once
		waitOnce sync.Once
	)
	s.clusterHook = func(event, params string) {
		switch event {
		case sessionClusterEventWait:
			if waits.Add(1) == 2 { // the pipeline's cluster stage
				waitOnce.Do(func() { close(waiting) })
			}
		case sessionClusterEventLocked:
			if locks.Add(1) == 1 { // Recluster holds the lock until released
				lockOnce.Do(func() { close(locked) })
				<-proceed
			}
		case sessionClusterEventStored:
			mu.Lock()
			stored = append(stored, params)
			mu.Unlock()
		}
	}

	h.settings.set(func(c *config.Config) { c.BucketSizeSec = 60 })
	reclusterErr := make(chan error, 1)
	go func() { reclusterErr <- s.Recluster(context.Background(), h.settings.get) }()
	select {
	case <-locked:
	case <-time.After(sessionTestTimeout):
		t.Fatal("Recluster never took the lock")
	}
	runErr := make(chan error, 1)
	go func() { runErr <- p.Resume(context.Background()) }()
	select {
	case <-waiting:
	case <-time.After(sessionTestTimeout):
		t.Fatal("the pipeline's cluster stage never waited for the lock")
	}
	close(proceed)
	if err := sessionTestWaitErr(t, reclusterErr); err != nil {
		t.Fatalf("Recluster: %v", err)
	}
	if err := sessionTestWaitErr(t, runErr); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	want := ClusterParams(h.settings.get())
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(stored, []string{want, want}) {
		t.Fatalf("params stored in order = %q; want %q twice", stored, want)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 3 {
		t.Fatalf("clusters = %d; want 3", n)
	}
}

func TestClusterer_VisualGroupingOffIgnoresPrints(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, _ := h.folder(t, "visual")
	s, release := h.acquire(t, id)
	defer release()
	ctx := context.Background()
	for i := range 3 {
		sessionTestInsertAnalyzed(t, s.Store, string(rune('a'+i))+".jpg",
			sessionTestDay.Add(time.Duration(i)*time.Second), sessionTestUnit(i))
	}
	if err := s.Recluster(ctx, h.settings.get); err != nil {
		t.Fatal(err)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 3 {
		t.Fatalf("visual clusters = %d; want 3 (three unrelated prints)", n)
	}

	p, _ := s.NewPipeline(h.settings.get) // built with visual grouping on
	s.clusterHook = func(event, _ string) {
		if event == sessionClusterEventWait {
			h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
		}
	}
	if err := p.Resume(ctx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 1 {
		t.Fatalf("clusters = %d; want 1 time-only cluster", n)
	}
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("stored params = %q; want %q", got, want)
	}
}

func TestPicksDir_HoldsOriginals(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "shoot")
	sessionTestWriteJPEG(t, root, "shoot1/a.jpg")
	sessionTestWriteJPEG(t, root, "shoot1/b.jpg")
	s, release := h.acquire(t, id)
	defer release()
	ctx := context.Background()

	p, _ := s.NewPipeline(h.settings.get)
	if err := p.Scanner.Walk(ctx); err != nil {
		t.Fatal(err)
	}
	h.settings.set(func(c *config.Config) { c.PicksDir = "shoot1" })
	if _, err := s.PicksDir(h.settings.get()); !errors.Is(err, picks.ErrDirHasOriginals) {
		t.Fatalf("PicksDir(shoot1) err = %v; want picks.ErrDirHasOriginals", err)
	}

	sessionTestWriteJPEG(t, root, "shoot1/c.jpg")
	p2, _ := s.NewPipeline(h.settings.get)
	if err := p2.Scanner.Walk(ctx); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"shoot1/a.jpg", "shoot1/b.jpg", "shoot1/c.jpg"} {
		f, err := s.Store.Files().GetByPath(rel)
		if err != nil || f.ScanStatus != "discovered" {
			t.Fatalf("%s = %+v, %v; want discovered as an original", rel, f, err)
		}
	}
}

func TestPicksDir_OrphanDoesNotBlock(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "orphan")
	sessionTestWriteJPEG(t, root, "a.jpg")
	sessionTestWriteJPEG(t, root, "picks/unknown.jpg")
	s, release := h.acquire(t, id)
	defer release()

	dir, err := s.PicksDir(config.Defaults())
	if err != nil || dir != "picks" {
		t.Fatalf("PicksDir = %q, %v; want picks, nil", dir, err)
	}
	p, _ := s.NewPipeline(h.settings.get)
	if err := p.Scanner.Walk(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Files().GetByPath("a.jpg"); err != nil {
		t.Fatalf("a.jpg not discovered: %v", err)
	}
	if f, err := s.Store.Files().GetByPath("picks/unknown.jpg"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the orphan in picks/ was discovered: %+v, %v", f, err)
	}
}

func TestNewPipeline_VisualGroupingOffComputesNoPrints(t *testing.T) {
	vis := &platformtest.Vision{}
	h := newSessionTestHarness(t, sessionTestOptions{vision: vis})
	id, root := h.folder(t, "noembed")
	sessionTestWriteJPEG(t, root, "a.jpg")
	sessionTestWriteJPEG(t, root, "b.jpg")
	s, release := h.acquire(t, id)
	defer release()
	ctx := context.Background()

	h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
	p, _ := s.NewPipeline(h.settings.get)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(vis.Calls()); n != 0 {
		t.Fatalf("Vision called %d times with visual grouping off; want 0", n)
	}
	if n, err := s.Store.Files().CountByStatus("analyzed"); err != nil || n != 2 {
		t.Fatalf("analyzed = %d, %v; want 2", n, err)
	}
	prints, err := s.Store.AISignals().AllPrints(analyze.PrintID)
	if err != nil || len(prints) != 0 {
		t.Fatalf("prints = %d, %v; want none", len(prints), err)
	}
	if n, err := s.Store.Files().CountAnalyzedMissingPrint(analyze.PrintID); err != nil || n != 2 {
		t.Fatalf("pending for a print = %d, %v; want 2", n, err)
	}

	// The same pipeline prints once the setting is back on.
	h.settings.set(func(c *config.Config) { c.VisualGrouping = true })
	sessionTestWriteJPEG(t, root, "c.jpg")
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(vis.Calls()); n != 1 {
		t.Fatalf("Vision called %d times; want 1 (the new file)", n)
	}
}

func TestNewPipeline_SkipsPicksDir(t *testing.T) {
	// Every spelling of the same directory is normalised once, so the
	// scanner's skip list, the export and PicksDir agree.
	for _, setting := range []string{"exports/out", "./exports/out", "exports/x/../out", "exports//out/", "exports/./out/."} {
		t.Run(setting, func(t *testing.T) {
			h := newSessionTestHarness(t, sessionTestOptions{})
			id, root := h.folder(t, "skip")
			sessionTestWriteJPEG(t, root, "a.jpg")
			sessionTestWriteJPEG(t, root, "exports/out/copy.jpg")
			h.settings.set(func(c *config.Config) { c.PicksDir = setting })
			s, release := h.acquire(t, id)
			defer release()
			ctx := context.Background()

			if dir, err := s.PicksDir(h.settings.get()); err != nil || dir != "exports/out" {
				t.Fatalf("PicksDir = %q, %v; want exports/out", dir, err)
			}
			p, _ := s.NewPipeline(h.settings.get)
			if err := p.Scanner.Walk(ctx); err != nil {
				t.Fatal(err)
			}
			a, err := s.Store.Files().GetByPath("a.jpg")
			if err != nil {
				t.Fatalf("a.jpg not discovered: %v", err)
			}
			if f, err := s.Store.Files().GetByPath("exports/out/copy.jpg"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("a file in the picks dir was discovered: %+v, %v", f, err)
			}

			// A pick lands in the same directory, the recorded export
			// directory then agrees with the setting, and the walk skips it.
			svc, err := s.Picks(h.settings.get())
			if err != nil {
				t.Fatal(err)
			}
			res, err := svc.Set(ctx, a.ID, picks.StatePicked)
			if err != nil || res.ExportedPath != "exports/out/a.jpg" {
				t.Fatalf("pick = %+v, %v; want exported to exports/out/a.jpg", res, err)
			}
			if dirs, err := s.scannerSkipDirs(h.settings.get()); err != nil || !slices.Equal(dirs, []string{"exports/out"}) {
				t.Fatalf("skip dirs = %q, %v; want [exports/out]", dirs, err)
			}
			if err := p.Scanner.Walk(ctx); err != nil {
				t.Fatal(err)
			}
			if f, err := s.Store.Files().GetByPath("exports/out/a.jpg"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the exported copy was discovered: %+v, %v", f, err)
			}
		})
	}
}

func TestPicksDir_RefusesDotAndDotDot(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "dots")
	sessionTestWriteJPEG(t, root, "a.jpg")
	s, release := h.acquire(t, id)
	defer release()

	for _, setting := range []string{".", "./", "x/..", "../outside", "a/../../outside", ".choix/picks", "./.choix"} {
		h.settings.set(func(c *config.Config) { c.PicksDir = setting })
		var de *picks.DirError
		if _, err := s.PicksDir(h.settings.get()); !errors.As(err, &de) {
			t.Fatalf("PicksDir(%q) err = %v; want a *picks.DirError", setting, err)
		}
		dirs, err := s.scannerSkipDirs(h.settings.get())
		if err != nil || len(dirs) != 0 {
			t.Fatalf("skip dirs for %q = %q, %v; want none", setting, dirs, err)
		}
	}
	p, _ := s.NewPipeline(h.settings.get)
	if err := p.Scanner.Walk(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Files().GetByPath("a.jpg"); err != nil {
		t.Fatalf("a.jpg not discovered with an unusable picks dir: %v", err)
	}
}

func TestNewPipeline_MissingPreviewFailsReadably(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, _ := h.folder(t, "nothumb")
	s, release := h.acquire(t, id)
	defer release()
	fid := sessionTestInsertAnalyzed(t, s.Store, "a.jpg", sessionTestDay, nil)
	if err := s.Store.Files().SetScanStatus(fid, "thumbed", ""); err != nil {
		t.Fatal(err)
	}

	p, _ := s.NewPipeline(h.settings.get)
	if err := p.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v (one file without a preview must not stop the job)", err)
	}
	f, err := s.Store.Files().GetByID(fid)
	if err != nil {
		t.Fatal(err)
	}
	const want = "cannot analyze: no cached preview image for this file (not found)"
	if f.ScanStatus != "failed" || f.ErrMsg.String != want {
		t.Fatalf("file = %s %q; want failed %q", f.ScanStatus, f.ErrMsg.String, want)
	}
}

// sessionTestDisconnect moves the folder away, as an unplugged volume makes
// it vanish while its open database stays usable, and returns the func that
// brings it back. Cleanup brings it back too.
func sessionTestDisconnect(t *testing.T, root string) (reconnect func()) {
	t.Helper()
	away := root + ".away"
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	reconnect = func() {
		once.Do(func() {
			if err := os.Rename(away, root); err != nil {
				t.Errorf("reconnect: %v", err)
			}
		})
	}
	t.Cleanup(reconnect)
	return reconnect
}

func TestNewPipeline_FolderGoneLeavesFilesPending(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "away")
	s, release := h.acquire(t, id)
	defer release()
	fid := sessionTestInsertAnalyzed(t, s.Store, "a.jpg", sessionTestDay, nil)
	if err := s.Store.Files().SetScanStatus(fid, "thumbed", ""); err != nil {
		t.Fatal(err)
	}

	reconnect := sessionTestDisconnect(t, root)
	p, _ := s.NewPipeline(h.settings.get)
	if err := p.Resume(context.Background()); !errors.Is(err, pipeline.ErrFolderUnavailable) {
		t.Fatalf("Resume = %v; want pipeline.ErrFolderUnavailable", err)
	}
	reconnect()
	f, err := s.Store.Files().GetByID(fid)
	if err != nil {
		t.Fatal(err)
	}
	if f.ScanStatus != "thumbed" || f.ErrMsg.Valid {
		t.Fatalf("file = %s %q; want still thumbed, no error", f.ScanStatus, f.ErrMsg.String)
	}
}

func TestScorer_FolderGoneLeavesPhotoPending(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "away")
	s, release := h.acquire(t, id)
	defer release()
	fid := sessionTestInsertAnalyzed(t, s.Store, "a.jpg", sessionTestDay, nil)
	rel, err := thumb.WriteCache(root, fid, thumb.TierPreview, platformtest.MakeJPEG(t, 512, 512))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Thumbs().Upsert(store.Thumb{FileID: fid, Tier: thumb.TierPreview, RelPath: rel, Width: 512, Height: 512}); err != nil {
		t.Fatal(err)
	}
	q := s.Scorer()
	items, err := q.ListPending(0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("pending = %+v, %v; want the one photo", items, err)
	}

	reconnect := sessionTestDisconnect(t, root)
	if err := q.Score(context.Background(), items[0]); !errors.Is(err, pipeline.ErrFolderUnavailable) {
		t.Fatalf("Score with the folder gone = %v; want pipeline.ErrFolderUnavailable", err)
	}
	reconnect()
	if n, err := q.CountPending(); err != nil || n != 1 {
		t.Fatalf("pending after reconnection = %d, %v; want the photo still pending", n, err)
	}
}

// The session's scorer is shared, rates the file's preview with the
// built-in formula and stamps the scorer id.
func TestScorer_SharedAndRatesThePreview(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "score")
	s, release := h.acquire(t, id)
	defer release()

	q := s.Scorer()
	if q == nil || s.Scorer() != q {
		t.Fatal("Scorer must return one shared scorer")
	}
	fid := sessionTestInsertAnalyzed(t, s.Store, "a.jpg", sessionTestDay, nil)
	rel, err := thumb.WriteCache(root, fid, thumb.TierPreview, platformtest.MakeJPEG(t, 512, 512))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Thumbs().Upsert(store.Thumb{FileID: fid, Tier: thumb.TierPreview, RelPath: rel, Width: 512, Height: 512}); err != nil {
		t.Fatal(err)
	}
	items, err := q.ListPending(0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("pending = %+v, %v; want the one photo", items, err)
	}
	if err := q.Score(context.Background(), items[0]); err != nil {
		t.Fatalf("Score: %v", err)
	}
	var raw sql.NullFloat64
	var model string
	if err := s.Store.DB().QueryRowContext(context.Background(),
		`SELECT quality_raw, quality_model FROM files WHERE id = ?`, fid).Scan(&raw, &model); err != nil {
		t.Fatal(err)
	}
	if !raw.Valid || model != quality.ScorerID {
		t.Fatalf("stored raw=%+v model=%q; want a score stamped %q", raw, model, quality.ScorerID)
	}
}

// A preview row whose file is gone does not make the photo unratable when
// the preview is at its standard place (thumb.CachePath): the scorer rates it.
func TestScorer_StalePreviewRowUsesTheStandardCachePath(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "stalescore")
	s, release := h.acquire(t, id)
	defer release()

	fid := sessionTestInsertAnalyzed(t, s.Store, "a.jpg", sessionTestDay, nil)
	if _, err := thumb.WriteCache(root, fid, thumb.TierPreview, platformtest.MakeJPEG(t, 512, 512)); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Thumbs().Upsert(store.Thumb{FileID: fid, Tier: thumb.TierPreview,
		RelPath: ".choix/thumbs/00/gone-preview.jpg", Width: 512, Height: 512}); err != nil {
		t.Fatal(err)
	}
	q := s.Scorer()
	items, err := q.ListPending(0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("pending = %+v, %v; want the one photo", items, err)
	}
	if err := q.Score(context.Background(), items[0]); err != nil {
		t.Fatalf("Score: %v", err)
	}
	var raw sql.NullFloat64
	var model string
	if err := s.Store.DB().QueryRowContext(context.Background(),
		`SELECT quality_raw, quality_model FROM files WHERE id = ?`, fid).Scan(&raw, &model); err != nil {
		t.Fatal(err)
	}
	if !raw.Valid || model != quality.ScorerID {
		t.Fatalf("stored raw=%+v model=%q; want a score stamped %q", raw, model, quality.ScorerID)
	}
}

// sessionThumbPath stays inside the thumbnail cache: a row naming an
// original, or a path that leaves .choix/thumbs/, is not used, nor is a
// symbolic link at the standard cache path; the row's own file wins over
// the standard path.
func TestSessionThumbPath_StaysInTheCache(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "confined")
	sessionTestWriteJPEG(t, root, "a.jpg")
	s, release := h.acquire(t, id)
	defer release()
	fid := sessionTestInsertAnalyzed(t, s.Store, "a.jpg", sessionTestDay, nil)
	setRow := func(rel string) {
		t.Helper()
		if err := s.Store.Thumbs().Upsert(store.Thumb{FileID: fid, Tier: thumb.TierPreview, RelPath: rel, Width: 8, Height: 8}); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"a.jpg", ".choix/thumbs/../../a.jpg", ".choix/thumbs/../state.db", "/etc/hosts"} {
		setRow(rel)
		if p, err := sessionThumbPath(s.Store, s.Root, fid, thumb.TierPreview); err == nil {
			t.Errorf("row %q resolved to %s; want it refused", rel, p)
		}
	}

	std := thumb.CachePath(s.Root, fid, thumb.TierPreview)
	if err := os.MkdirAll(filepath.Dir(std), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.Root, "a.jpg"), std); err != nil {
		t.Fatal(err)
	}
	if p, err := sessionThumbPath(s.Store, s.Root, fid, thumb.TierPreview); err == nil {
		t.Errorf("a symbolic link at the standard path resolved to %s; want it refused", p)
	}
	if err := os.Remove(std); err != nil {
		t.Fatal(err)
	}

	stdRel, err := thumb.WriteCache(s.Root, fid, thumb.TierPreview, platformtest.MakeJPEG(t, 8, 8))
	if err != nil {
		t.Fatal(err)
	}
	setRow("a.jpg")
	if p, err := sessionThumbPath(s.Store, s.Root, fid, thumb.TierPreview); err != nil || p != std {
		t.Errorf("with a refused row: %s, %v; want the standard path %s", p, err, std)
	}
	other := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(stdRel)), "other-preview.jpg"))
	if err := os.WriteFile(filepath.Join(s.Root, filepath.FromSlash(other)), platformtest.MakeJPEG(t, 8, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	setRow(other)
	if p, err := sessionThumbPath(s.Store, s.Root, fid, thumb.TierPreview); err != nil || p != filepath.Join(s.Root, filepath.FromSlash(other)) {
		t.Errorf("with a usable row: %s, %v; want the row's file, not the standard one", p, err)
	}
}

// The cluster stage reports done of total in files, like the other stages,
// so the Folder page's checklist and the indicators show a real count.
func TestNewPipeline_ClusterStageReportsFiles(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "clusterprogress")
	sessionTestWriteJPEG(t, root, "a.jpg")
	sessionTestWriteJPEG(t, root, "b.jpg")
	sessionTestWriteJPEG(t, root, "c.jpg")
	s, release := h.acquire(t, id)
	defer release()

	p, progress := s.NewPipeline(h.settings.get)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The reporter keeps the newest events when its buffer is full; the
	// cluster stage's are the last ones.
	var cluster []pipeline.Progress
	for drained := false; !drained; {
		select {
		case ev := <-progress:
			if ev.Stage == "cluster" {
				cluster = append(cluster, ev)
			}
		default:
			drained = true
		}
	}
	running := 0
	for _, ev := range cluster {
		if ev.Phase == "running" {
			running++
			if ev.Total != 3 {
				t.Errorf("cluster progress %+v; want a total of 3 files", ev)
			}
		}
	}
	if running == 0 {
		t.Fatalf("cluster events %+v; want running progress with a count", cluster)
	}
	if last := cluster[len(cluster)-1]; last.Phase != "done" || last.Done != 3 || last.Total != 3 {
		t.Errorf("last cluster event %+v; want done at 3 of 3", last)
	}
}
