package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/analyze"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/quality"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// jobTestTracker is a QualityRun that records how the job used it. With
// cancelAfter > 0 it cancels the pass context after that many progress
// calls, as switching the quality setting off does.
type jobTestTracker struct {
	cancelAfter int

	mu       sync.Mutex
	begun    int
	folders  []string // the folder of each pass begun
	progress [][2]int
	ended    int
	rated    int
	err      error
}

func (tr *jobTestTracker) Begin(ctx context.Context, folderID string) (context.Context, func(done, total int), func(rated int, err error)) {
	passCtx, cancel := context.WithCancel(ctx)
	tr.mu.Lock()
	tr.begun++
	tr.folders = append(tr.folders, folderID)
	tr.mu.Unlock()
	progress := func(done, total int) {
		tr.mu.Lock()
		tr.progress = append(tr.progress, [2]int{done, total})
		n := len(tr.progress)
		tr.mu.Unlock()
		if tr.cancelAfter > 0 && n >= tr.cancelAfter {
			cancel()
		}
	}
	end := func(rated int, err error) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		tr.ended++
		tr.rated, tr.err = rated, err
		cancel()
	}
	return passCtx, progress, end
}

// jobTestHarness is a sessionTestHarness plus what a Job needs.
type jobTestHarness struct {
	*sessionTestHarness
	rec      *events.Recorder
	emit     *events.Coalescer
	afterJob atomic.Int64

	mu      sync.Mutex
	reports []events.Progress
}

func newJobTestHarness(t *testing.T, o sessionTestOptions) *jobTestHarness {
	t.Helper()
	rec := &events.Recorder{}
	return &jobTestHarness{
		sessionTestHarness: newSessionTestHarness(t, o),
		rec:                rec,
		emit:               events.NewCoalescer(rec, time.Hour),
	}
}

// job returns a Job over the harness, rating through tracker (may be nil).
func (h *jobTestHarness) job(tracker QualityRun) *Job {
	return NewJob(JobOptions{
		Sessions: h.mgr,
		Settings: h.settings.get,
		Quality:  tracker,
		Emit:     h.emit,
		AfterJob: func(context.Context, *Session) { h.afterJob.Add(1) },
	})
}

func (h *jobTestHarness) report(p events.Progress, _ string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reports = append(h.reports, p)
}

func (h *jobTestHarness) reported() []events.Progress {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.reports)
}

// run runs j on the folder and returns its outcome and message.
func (h *jobTestHarness) run(ctx context.Context, j *Job, id string, kind JobKind) (Outcome, string) {
	return j.Run(ctx, id, kind, h.report)
}

// folderChanged counts the folder:changed events of the folder with reason.
func (h *jobTestHarness) folderChanged(id, reason string) int {
	n := 0
	for _, ev := range h.rec.Events(events.FolderChanged) {
		if e, ok := ev.(events.FolderChangedEvent); ok && e.FolderID == id && e.Reason == reason {
			n++
		}
	}
	return n
}

// folderReasons returns the reasons of the folder's folder:changed events,
// oldest first.
func (h *jobTestHarness) folderReasons(id string) []string {
	var out []string
	for _, ev := range h.rec.Events(events.FolderChanged) {
		if e, ok := ev.(events.FolderChangedEvent); ok && e.FolderID == id {
			out = append(out, e.Reason)
		}
	}
	return out
}

// jobWith returns a Job over the harness, rating through tracker (may be
// nil), whose AfterJob is afterJob instead of the harness's counter.
func (h *jobTestHarness) jobWith(tracker QualityRun, afterJob func(context.Context, *Session)) *Job {
	j := h.job(tracker)
	j.o.AfterJob = afterJob
	return j
}

// jobTestRow is one files row as the job tests look at it.
type jobTestRow struct {
	status, err string
}

// rows opens the folder's session and returns its files rows by path.
func (h *jobTestHarness) rows(t *testing.T, id string) map[string]jobTestRow {
	t.Helper()
	s, release := h.acquire(t, id)
	defer release()
	rows, err := s.Store.DB().QueryContext(context.Background(), `SELECT path, scan_status, COALESCE(error, '') FROM files`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]jobTestRow{}
	for rows.Next() {
		var path string
		var r jobTestRow
		if err := rows.Scan(&path, &r.status, &r.err); err != nil {
			t.Fatal(err)
		}
		out[path] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// countStatus counts rows with status.
func jobTestCountStatus(rows map[string]jobTestRow, status string) int {
	n := 0
	for _, r := range rows {
		if r.status == status {
			n++
		}
	}
	return n
}

// jobTestWriteShots writes the JPEGs names into root, w×h pixels, taken
// gap apart by the synthetic camera, and returns their absolute paths.
func jobTestWriteShots(t *testing.T, stills *platformtest.Stills, root string, w, h int, gap time.Duration, names ...string) []string {
	t.Helper()
	var out []string
	for i, name := range names {
		abs := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, platformtest.MakeJPEG(t, w, h), 0o644); err != nil { //nolint:gosec // test file
			t.Fatal(err)
		}
		if stills.Props != nil {
			stills.Props[abs] = sessionTestStillProps(sessionTestDay.Add(time.Duration(i) * gap))
		}
		out = append(out, abs)
	}
	return out
}

// jobTestTree lists every path under dir, relative and slash-separated.
func jobTestTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// jobTestWaitCalls waits until stills has received n calls of method.
func jobTestWaitCalls(t *testing.T, stills *platformtest.Stills, method string, n int) {
	t.Helper()
	waitFor(t, method+" calls", func() bool {
		count := 0
		for _, c := range stills.Calls() {
			if c.Method == method {
				count++
			}
		}
		return count >= n
	})
}

// jobTestClusterSnapshot returns "id|label" of every cluster, by id.
func jobTestClusterSnapshot(t *testing.T, st *store.Store) []string {
	t.Helper()
	rows, err := st.DB().QueryContext(context.Background(), `SELECT id, COALESCE(label, '') FROM clusters ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id int64
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d|%s", id, label))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestJob_FullRunClusters(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "trip")
	jobTestWriteShots(t, stills, root, 64, 48, 100*time.Second, "a.jpg", "b.jpg", "c.jpg")

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n := sessionTestClusterCount(t, s.Store); n != 1 {
		t.Fatalf("clusters = %d; want 1", n)
	}
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("cluster_params = %q; want %q", got, want)
	}
	if n, err := s.Store.Files().CountByStatus("analyzed"); err != nil || n != 3 {
		t.Fatalf("analyzed = %d, %v; want 3", n, err)
	}
	if !slices.ContainsFunc(h.reported(), func(p events.Progress) bool { return p.Stage == "cluster" }) {
		t.Fatalf("progress reported = %+v; want the stages up to cluster", h.reported())
	}
	if n := h.folderChanged(id, "clusters"); n != 1 {
		t.Fatalf("folder:changed clusters = %d; want 1", n)
	}
	if n := h.afterJob.Load(); n != 1 {
		t.Fatalf("AfterJob called %d times; want 1", n)
	}
}

func TestJob_BrokenFilesDoNotStopIt(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	video := &platformtest.Video{Fail: map[string]error{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, video: video})
	id, root := h.folder(t, "broken")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "good1.jpg", "good2.jpg", "good3.jpg")

	write := func(name string, data []byte) string {
		abs := filepath.Join(root, name)
		if err := os.WriteFile(abs, data, 0o644); err != nil { //nolint:gosec // test file
			t.Fatal(err)
		}
		return abs
	}
	write("empty.jpg", nil)
	write("notes.jpg", []byte("this is a text file, not a JPEG\n"))
	raf, err := os.ReadFile(platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 64, 48)))
	if err != nil {
		t.Fatal(err)
	}
	write("cut.raf", raf[:90])
	mov := write("clip.mov", []byte("not really a movie"))
	video.Fail[mov] = platform.ErrNoVideoTrack

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	rows := h.rows(t, id)
	for _, name := range []string{"empty.jpg", "notes.jpg", "cut.raf", "clip.mov"} {
		r, ok := rows[name]
		if !ok || r.status != "failed" || strings.TrimSpace(r.err) == "" {
			t.Errorf("%s = %+v (present %v); want failed with a reason", name, r, ok)
		}
		t.Logf("%s: %s", name, r.err)
	}
	if n := jobTestCountStatus(rows, "failed"); n != 4 {
		t.Errorf("failed rows = %d; want 4", n)
	}
	if n := jobTestCountStatus(rows, "analyzed"); n != 3 {
		t.Errorf("analyzed rows = %d; want 3: %+v", n, rows)
	}
}

func TestJob_HostileContentCompletes(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "a #1?b%41")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "good.jpg")

	dng, err := os.ReadFile(platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: make([]byte, 64)}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nopreview.dng"), dng, 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	// The picks directory is a symlink to a folder outside, holding a photo.
	outside := filepath.Join(h.parent, "outside")
	jobTestWriteShots(t, stills, outside, 64, 48, time.Second, "elsewhere.jpg")
	if err := os.Symlink(outside, filepath.Join(root, "picks")); err != nil {
		t.Fatal(err)
	}
	before := jobTestTree(t, outside)

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	rows := h.rows(t, id)
	if r := rows["nopreview.dng"]; r.status != "failed" || r.err == "" {
		t.Errorf("nopreview.dng = %+v; want failed with a reason", r)
	}
	if r := rows["good.jpg"]; r.status != "analyzed" {
		t.Errorf("good.jpg = %+v; want analyzed", r)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %+v; want only the folder's two files (nothing through the picks link)", rows)
	}
	if after := jobTestTree(t, outside); !slices.Equal(before, after) {
		t.Errorf("the link target changed: %v → %v", before, after)
	}
	if got := jobTestTree(t, h.parent); slices.ContainsFunc(got, func(p string) bool {
		return !strings.HasPrefix(p, "a #1?b%41") && !strings.HasPrefix(p, "outside") && p != "."
	}) {
		t.Errorf("something was created beside the folder: %v", got)
	}
}

func TestJob_SymlinkedCacheDirIsRefused(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "cache")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg", "b.jpg")
	_, release := h.acquire(t, id) // creates .choix
	release()
	outside := filepath.Join(h.parent, "outside-thumbs")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".choix", "thumbs")); err != nil {
		t.Fatal(err)
	}

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the cache link target holds %v, %v; want nothing written through the link", entries, err)
	}
	for name, r := range h.rows(t, id) {
		if r.status != "failed" || r.err == "" {
			t.Errorf("%s = %+v; want failed with a reason (its thumbnail cannot be written)", name, r)
		}
	}
}

func TestJob_CancelDuringNativeCallReturnsWithinOneSecond(t *testing.T) {
	gate := make(chan struct{})
	stills := &platformtest.Stills{ReadPaths: true, Gate: gate, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "cancel")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg", "b.jpg", "c.jpg")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		o   Outcome
		msg string
	}
	done := make(chan result, 1)
	go func() {
		o, msg := h.run(ctx, h.job(nil), id, JobFull)
		done <- result{o, msg}
	}()
	for range 3 {
		sessionTestSend(t, gate) // the metadata stage
	}
	jobTestWaitCalls(t, stills, "Thumbnail", 1) // a thumbnail call is held at the gate

	start := time.Now()
	cancel()
	var r result
	select {
	case r = <-done:
	case <-time.After(sessionTestTimeout):
		t.Fatal("the job did not return after cancel")
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("the job returned %v after cancel; want under 1 s", elapsed)
	}
	if r.o != Stopped {
		t.Fatalf("Run = %v %q; want Stopped", r.o, r.msg)
	}
	before := jobTestTree(t, root)

	close(gate) // the abandoned native calls return now
	h.events.waitFor(t, id, managerStageClosed, 1)
	for _, p := range jobTestTree(t, root) {
		if !slices.Contains(before, p) {
			t.Errorf("%s appeared after the job stopped", p)
		}
	}
	if n := jobTestCountStatus(h.rows(t, id), "failed"); n != 0 {
		t.Fatalf("failed rows = %d; want none (cancellation is not a file failure)", n)
	}
	// Cancelled before its clusters were final: nothing was published.
	if n := h.afterJob.Load(); n != 0 {
		t.Fatalf("AfterJob called %d times for a job cancelled mid-scan; want 0", n)
	}
	if n := h.folderChanged(id, "clusters"); n != 0 {
		t.Fatalf("folder:changed clusters = %d for a job cancelled mid-scan; want 0", n)
	}
}

func TestJob_FolderVanishesMidScan(t *testing.T) {
	gate := make(chan struct{})
	stills := &platformtest.Stills{ReadPaths: true, Gate: gate, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "unplugged")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg", "b.jpg", "c.jpg")

	type result struct {
		o   Outcome
		msg string
	}
	done := make(chan result, 1)
	go func() {
		o, msg := h.run(context.Background(), h.job(nil), id, JobFull)
		done <- result{o, msg}
	}()
	for range 3 {
		sessionTestSend(t, gate) // the metadata stage
	}
	jobTestWaitCalls(t, stills, "Thumbnail", 1) // the thumb stage is held
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	close(gate)

	var r result
	select {
	case r = <-done:
	case <-time.After(sessionTestTimeout):
		t.Fatal("the job did not return")
	}
	if r.o != Suspended {
		t.Fatalf("Run = %v %q; want Suspended", r.o, r.msg)
	}
	if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the vanished folder was recreated: %v", err)
	}
	if n := h.afterJob.Load(); n != 0 {
		t.Fatalf("AfterJob called %d times for a suspended job; want 0", n)
	}
}

func TestJob_FolderVanishesDuringQualityPass(t *testing.T) {
	// The rating is advisory, so the pass's own errors never end the job;
	// the check between steps still finds the folder gone.
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "unplugged")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	var once sync.Once
	tracker := &jobTestTracker{}
	j := h.job(trackerHook{inner: tracker, onProgress: func() {
		once.Do(func() {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
	}})

	if o, msg := h.run(context.Background(), j, id, JobFull); o != Suspended {
		t.Fatalf("Run = %v %q; want Suspended", o, msg)
	}
	// The clusters were final, and published, before the pass began; the
	// suspension does not publish them again.
	if n := h.afterJob.Load(); n != 1 {
		t.Fatalf("AfterJob called %d times; want 1, before the pass", n)
	}
	if n := h.folderChanged(id, "clusters"); n != 1 {
		t.Fatalf("folder:changed clusters = %d; want 1, before the pass", n)
	}
	// The pass stopped at the next photo instead of stamping the others as
	// never ratable because their previews could not be read.
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.progress) != 1 || tracker.rated != 1 || !errors.Is(tracker.err, context.Canceled) {
		t.Fatalf("progress %v, end(%d, %v); want one photo, then the pass stopped", tracker.progress, tracker.rated, tracker.err)
	}
}

func TestJob_OfflineAtStartIsSuspended(t *testing.T) {
	for name, cut := range map[string]func(h *jobTestHarness, root string){
		"offline":     func(h *jobTestHarness, root string) { h.bm.Delete(root) },
		"access lost": func(h *jobTestHarness, root string) { h.bm.DenyAccess(root) },
		"gone":        func(_ *jobTestHarness, root string) { _ = os.RemoveAll(root) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newJobTestHarness(t, sessionTestOptions{})
			id, root := h.folder(t, "away")
			sessionTestWriteJPEG(t, root, "a.jpg")
			cut(h, root)

			if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Suspended {
				t.Fatalf("Run = %v %q; want Suspended", o, msg)
			}
			h.assertNoAccess(t)
		})
	}
}

// jobTestStaleSession leaves the folder's session draining behind a held
// detached call and moves the folder (recorded by a presence check), with a
// new directory at the old path: the session is stale and cannot close until
// the returned release is called.
func jobTestStaleSession(t *testing.T, h *jobTestHarness, stills *platformtest.Stills, id, root string) (old *Session, release func()) {
	t.Helper()
	s, rel := h.acquire(t, id)
	sessionTestHoldDetachedCall(t, s, stills)
	rel()
	h.events.waitFor(t, id, managerStageWaiting, 1)
	var once sync.Once
	release = func() { once.Do(func() { close(stills.Gate) }) }
	t.Cleanup(func() {
		release()
		sessionTestWaitClosed(t, s)
	})

	moved := filepath.Join(h.parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(root, moved)
	if _, err := h.lib.CheckPresence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o755); err != nil { // the old path exists again
		t.Fatal(err)
	}
	return s, release
}

// The folder's previous session, at a location the folder left, does not
// close within the bound: Acquire answers ErrBusy and the job ends Busy — a
// suspension that keeps it pending — not Failed.
func TestJob_PreviousSessionStillClosingIsBusy(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "moving")
	sessionTestWriteJPEG(t, root, "a.jpg")
	jobTestStaleSession(t, h, stills, id, root)
	h.mgr.staleWait = 20 * time.Millisecond

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Busy {
		t.Fatalf("Run = %v %q; want Busy", o, msg)
	}
}

// jobTestOutcomes wraps a Runner and reports each run's outcome.
type jobTestOutcomes struct {
	inner Runner
	done  chan Outcome
}

func (r jobTestOutcomes) Run(ctx context.Context, id string, kind JobKind, report func(events.Progress, string)) (Outcome, string) {
	o, msg := r.inner.Run(ctx, id, kind, report)
	r.done <- o
	return o, msg
}

// Busy runs are not strikes: the job is deferred, not failed, however many
// bounds the old session outlives; the queue does not re-run it on its own;
// and once the old session has closed the next Resume (the presence pass)
// finishes it.
func TestQueue_BusyJobRecoversAfterTheOldSessionCloses(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "moving")
	sessionTestWriteJPEG(t, root, "a.jpg")
	old, release := jobTestStaleSession(t, h, stills, id, root)
	h.mgr.staleWait = 20 * time.Millisecond

	var suspended atomic.Int64
	runner := jobTestOutcomes{inner: h.job(nil), done: make(chan Outcome, 16)}
	q := NewQueue(QueueOptions{
		Library:     h.lib,
		Runner:      runner,
		Emit:        h.emit,
		OnSuspended: func(string) { suspended.Add(1) },
	})
	q.Start(context.Background())
	t.Cleanup(q.Stop)
	if err := q.Enqueue(id, JobFull); err != nil {
		t.Fatal(err)
	}

	next := func() Outcome {
		t.Helper()
		select {
		case o := <-runner.done:
			return o
		case <-time.After(sessionTestTimeout):
			t.Fatal("the job never ended")
		}
		return 0
	}
	// More busy runs than the suspension limit: each one waited a full bound.
	for i := range maxSuspensions + 2 {
		if o := next(); o != Busy {
			t.Fatalf("run %d = %v; want Busy", i+1, o)
		}
		waitFor(t, "the queue idle after a busy run", q.Idle)
		f, err := h.lib.Folder(id)
		if err != nil {
			t.Fatal(err)
		}
		if !f.JobPending || f.JobError != "" {
			t.Fatalf("after busy run %d: pending %v, error %q; want pending without error", i+1, f.JobPending, f.JobError)
		}
		if i < maxSuspensions+1 {
			q.Resume(id) // the next presence pass
		}
	}
	// Without a Resume the queue ran nothing more on its own.
	if n := len(runner.done); n != 0 || !q.Idle() {
		t.Fatalf("%d more runs (idle %v) without a Resume; want none", n, q.Idle())
	}

	release()
	sessionTestWaitClosed(t, old)
	q.Resume(id)
	if o := next(); o != Done {
		t.Fatalf("run after the old session closed = %v; want Done", o)
	}
	waitFor(t, "the queue idle", q.Idle)
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.JobPending || f.JobError != "" {
		t.Fatalf("after recovery: pending %v, error %q; want done without error", f.JobPending, f.JobError)
	}
	if n := suspended.Load(); n != 0 {
		t.Fatalf("OnSuspended called %d times for busy runs; want none", n)
	}
}

func TestJob_ReadOnlyFolderIsFailed(t *testing.T) {
	h := newJobTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "locked")
	sessionTestWriteJPEG(t, root, "a.jpg")
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	o, msg := h.run(context.Background(), h.job(nil), id, JobFull)
	if o != Failed || msg != ReadOnlyMessage {
		t.Fatalf("Run = %v %q; want Failed %q", o, msg, ReadOnlyMessage)
	}
	h.assertNoAccess(t)
}

func TestJob_SymlinkedDatabaseIsFailed(t *testing.T) {
	h := newJobTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "linked")
	if err := os.Mkdir(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(h.parent, "elsewhere.db")
	if err := os.WriteFile(elsewhere, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, ".choix", "state.db")); err != nil {
		t.Fatal(err)
	}

	o, msg := h.run(context.Background(), h.job(nil), id, JobFull)
	if o != Failed || msg != UnsafeStateMessage {
		t.Fatalf("Run = %v %q; want Failed %q", o, msg, UnsafeStateMessage)
	}
	if got, err := os.ReadFile(elsewhere); err != nil || string(got) != "not a database" { //nolint:gosec // test file
		t.Fatalf("link target = %q, %v; want it untouched", got, err)
	}
	h.assertNoAccess(t)
}

func TestJob_SettingChangedMidRunWins(t *testing.T) {
	gate := make(chan struct{})
	stills := &platformtest.Stills{ReadPaths: true, Gate: gate, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "timing")
	jobTestWriteShots(t, stills, root, 64, 48, 100*time.Second, "a.jpg", "b.jpg", "c.jpg")

	done := make(chan Outcome, 1)
	go func() {
		o, _ := h.run(context.Background(), h.job(nil), id, JobFull) // starts with a 600 s bucket
		done <- o
	}()
	for range 3 {
		sessionTestSend(t, gate) // the metadata stage
	}
	sessionTestSend(t, gate) // a thumb-stage call is held: the thumb stage is running
	h.settings.set(func(c *config.Config) { c.BucketSizeSec = 60 })
	close(gate)
	select {
	case o := <-done:
		if o != Done {
			t.Fatalf("Run = %v; want Done", o)
		}
	case <-time.After(sessionTestTimeout):
		t.Fatal("the job did not finish")
	}

	s, release := h.acquire(t, id)
	defer release()
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("stored params = %q; want %q", got, want)
	}
	if n := sessionTestClusterCount(t, s.Store); n != 3 {
		t.Fatalf("clusters = %d; want 3 (a 60 s bucket over shots 100 s apart)", n)
	}
}

// jobTestPrints returns the folder's prints stamped analyze.PrintID.
func jobTestPrints(t *testing.T, st *store.Store) map[int64][]float32 {
	t.Helper()
	prints, err := st.AISignals().AllPrints(analyze.PrintID)
	if err != nil {
		t.Fatal(err)
	}
	return prints
}

// jobTestPending returns how many analyzed files are pending for a print.
func jobTestPending(t *testing.T, st *store.Store) int {
	t.Helper()
	n, err := st.Files().CountAnalyzedMissingPrint(analyze.PrintID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestJob_BackfillsPendingPrints: a folder analyzed with no prints gets them
// from the next job, which re-clusters; a second job re-analyzes nothing.
func TestJob_BackfillsPendingPrints(t *testing.T) {
	vis := &platformtest.Vision{}
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, vision: vis})
	id, root := h.folder(t, "backfill")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg", "b.jpg", "c.jpg")

	h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("first Run = %v %q; want Done", o, msg)
	}
	func() {
		s, release := h.acquire(t, id)
		defer release()
		if n := len(jobTestPrints(t, s.Store)); n != 0 {
			t.Fatalf("prints with visual grouping off = %d; want none", n)
		}
		if _, err := s.Store.DB().ExecContext(context.Background(), `UPDATE clusters SET label = 'probe'`); err != nil {
			t.Fatal(err)
		}
	}()

	sessionTestWriteJPEG(t, root, "d.jpg") // added between the jobs: a backfill does not walk
	h.settings.set(func(c *config.Config) { c.VisualGrouping = true })
	if o, msg := h.run(context.Background(), h.job(nil), id, JobBackfill); o != Done {
		t.Fatalf("backfill Run = %v %q; want Done", o, msg)
	}

	var before []string
	func() {
		s, release := h.acquire(t, id)
		defer release()
		if n := len(jobTestPrints(t, s.Store)); n != 3 {
			t.Fatalf("prints after the backfill = %d; want 3", n)
		}
		if n := jobTestPending(t, s.Store); n != 0 {
			t.Fatalf("pending for a print = %d; want 0", n)
		}
		if f, err := s.Store.Files().GetByPath("d.jpg"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("d.jpg = %+v, %v; want not discovered (a backfill does not walk)", f, err)
		}
		before = jobTestClusterSnapshot(t, s.Store)
		for _, c := range before {
			if strings.HasSuffix(c, "|probe") {
				t.Fatalf("clusters %v: the backfill did not re-cluster", before)
			}
		}
		if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
			t.Fatalf("stored params = %q; want %q", got, want)
		}
	}()

	calls := len(vis.Calls())
	if o, msg := h.run(context.Background(), h.job(nil), id, JobBackfill); o != Done {
		t.Fatalf("second backfill Run = %v %q; want Done", o, msg)
	}
	if n := len(vis.Calls()); n != calls {
		t.Fatalf("Vision calls %d → %d; want none in the second backfill", calls, n)
	}
	s, release := h.acquire(t, id)
	defer release()
	if after := jobTestClusterSnapshot(t, s.Store); !slices.Equal(before, after) {
		t.Fatalf("clusters %v → %v; want them untouched", before, after)
	}
}

func TestJob_BackfillWithNothingPendingDoesNotRecluster(t *testing.T) {
	vis := &platformtest.Vision{}
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, vision: vis})
	id, root := h.folder(t, "steady")
	jobTestWriteShots(t, stills, root, 64, 48, time.Hour, "a.jpg", "b.jpg", "c.jpg")

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	var before []string
	func() {
		s, release := h.acquire(t, id)
		defer release()
		// A rebuild deletes every cluster row, so a label set here survives
		// only if nothing reclustered.
		if _, err := s.Store.DB().ExecContext(context.Background(), `UPDATE clusters SET label = 'probe'`); err != nil {
			t.Fatal(err)
		}
		before = jobTestClusterSnapshot(t, s.Store)
	}()
	if len(before) != 3 {
		t.Fatalf("clusters = %v; want 3", before)
	}
	calls := len(vis.Calls())

	if o, msg := h.run(context.Background(), h.job(nil), id, JobBackfill); o != Done {
		t.Fatalf("backfill Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	if after := jobTestClusterSnapshot(t, s.Store); !slices.Equal(before, after) {
		t.Fatalf("clusters %v → %v; want them untouched", before, after)
	}
	if n := len(vis.Calls()); n != calls {
		t.Fatalf("Vision calls %d → %d; want none during the backfill", calls, n)
	}
}

// TestJob_VisualGroupingOff: with visual grouping off no print is made, the
// backfill does nothing, and the clusters are the time buckets even when
// prints that would split them are stored.
func TestJob_VisualGroupingOff(t *testing.T) {
	vis := &platformtest.Vision{}
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, vision: vis})
	id, root := h.folder(t, "novisual")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg", "b.jpg", "c.jpg")

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	// Store three unrelated prints, as three different scenes would have:
	// with grouping on they make three clusters.
	func() {
		s, release := h.acquire(t, id)
		defer release()
		for i, rel := range []string{"a.jpg", "b.jpg", "c.jpg"} {
			f, err := s.Store.Files().GetByPath(rel)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Store.AISignals().UpsertPrint(f.ID, sessionTestUnit(i), analyze.PrintID); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Recluster(context.Background(), h.settings.get); err != nil {
			t.Fatal(err)
		}
		if n := sessionTestClusterCount(t, s.Store); n != 3 {
			t.Fatalf("clusters with grouping on = %d; want 3", n)
		}
	}()

	calls := len(vis.Calls())
	h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
	sessionTestWriteJPEG(t, root, "d.jpg")
	stills.Props[filepath.Join(root, "d.jpg")] = sessionTestStillProps(sessionTestDay.Add(3 * time.Second))
	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run with grouping off = %v %q; want Done", o, msg)
	}
	if n := len(vis.Calls()); n != calls {
		t.Fatalf("Vision calls %d → %d; want none with visual grouping off", calls, n)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n := sessionTestClusterCount(t, s.Store); n != 1 {
		t.Fatalf("clusters with grouping off = %d; want 1 (the time bucket)", n)
	}
	if n := jobTestPending(t, s.Store); n != 1 {
		t.Fatalf("pending for a print = %d; want 1 (d.jpg, not rewound)", n)
	}
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("stored params = %q; want %q", got, want)
	}
}

// TestJob_OffDuringAnalysisThenOn: grouping switched off while a job
// analyzes leaves the remaining files analyzed without a print; switching it
// on and running a backfill gives every file one.
func TestJob_OffDuringAnalysisThenOn(t *testing.T) {
	gate := make(chan struct{})
	vis := &platformtest.Vision{Gate: gate}
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, vision: vis})
	id, root := h.folder(t, "switch")
	names := []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg", "e.jpg", "f.jpg", "g.jpg", "h.jpg", "i.jpg", "j.jpg"}
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, names...)

	done := make(chan Outcome, 1)
	go func() {
		o, _ := h.run(context.Background(), h.job(nil), id, JobFull)
		done <- o
	}()
	sessionTestSend(t, gate) // one print is made
	h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
	close(gate) // the prints already asked for are made
	select {
	case o := <-done:
		if o != Done {
			t.Fatalf("Run = %v; want Done", o)
		}
	case <-time.After(sessionTestTimeout):
		t.Fatal("the job did not finish")
	}

	func() {
		s, release := h.acquire(t, id)
		defer release()
		n, err := s.Store.Files().CountByStatus("analyzed")
		if err != nil || n != len(names) {
			t.Fatalf("analyzed = %d, %v; want %d", n, err, len(names))
		}
		printed := len(jobTestPrints(t, s.Store))
		// At most one print per analyze worker was in flight when the
		// setting changed, besides the one let through.
		if printed < 1 || printed > 1+sessionAnalyzeWorkers {
			t.Fatalf("prints = %d; want 1 to %d", printed, 1+sessionAnalyzeWorkers)
		}
		if n := jobTestPending(t, s.Store); n != len(names)-printed {
			t.Fatalf("pending = %d; want %d", n, len(names)-printed)
		}
	}()

	h.settings.set(func(c *config.Config) { c.VisualGrouping = true })
	if o, msg := h.run(context.Background(), h.job(nil), id, JobBackfill); o != Done {
		t.Fatalf("backfill Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n := len(jobTestPrints(t, s.Store)); n != len(names) {
		t.Fatalf("prints after the backfill = %d; want %d", n, len(names))
	}
	if n := jobTestPending(t, s.Store); n != 0 {
		t.Fatalf("pending after the backfill = %d; want 0", n)
	}
}

// TestJob_ChangedFileWhileOffIsReprinted: a file whose content changes while
// visual grouping is off loses its print at the rescan; once grouping is on
// again, the backfill prints the new content.
func TestJob_ChangedFileWhileOffIsReprinted(t *testing.T) {
	vis := &platformtest.Vision{}
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, vision: vis})
	id, root := h.folder(t, "edited")
	abs := jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg")[0]

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	var fileID int64
	var old []float32
	func() {
		s, release := h.acquire(t, id)
		defer release()
		f, err := s.Store.Files().GetByPath("a.jpg")
		if err != nil {
			t.Fatal(err)
		}
		fileID = f.ID
		old = jobTestPrints(t, s.Store)[fileID]
		if old == nil {
			t.Fatal("no print after the first job")
		}
	}()

	h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
	if err := os.WriteFile(abs, platformtest.MakeJPEG(t, 48, 96), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("rescan Run = %v %q; want Done", o, msg)
	}
	func() {
		s, release := h.acquire(t, id)
		defer release()
		if p := jobTestPrints(t, s.Store)[fileID]; p != nil {
			t.Fatal("the old print survived the change of content")
		}
	}()

	h.settings.set(func(c *config.Config) { c.VisualGrouping = true })
	if o, msg := h.run(context.Background(), h.job(nil), id, JobBackfill); o != Done {
		t.Fatalf("backfill Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	got := jobTestPrints(t, s.Store)[fileID]
	preview, err := sessionThumbPath(s.Store, s.Root, fileID, thumb.TierPreview)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(preview) //nolint:gosec // the test folder's own preview
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 48 || b.Dy() != 96 {
		t.Fatalf("preview is %dx%d; want the new content's 48x96", b.Dx(), b.Dy())
	}
	want := platformtest.FeaturePrintOf(img)
	if !slices.Equal(got, want) {
		t.Fatal("the stored print is not the print of the new content")
	}
	if slices.Equal(got, old) {
		t.Fatal("the stored print is still the old one")
	}
}

// The rating pass needs no model: with quality_rating on, the job rates
// every photo with the built-in formula and reports the pass to the tracker.
func TestJob_QualityStepUsesTracker(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "rated")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	tracker := &jobTestTracker{}

	if o, msg := h.run(context.Background(), h.job(tracker), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.begun != 1 || tracker.ended != 1 {
		t.Fatalf("Begin/end = %d/%d; want 1/1", tracker.begun, tracker.ended)
	}
	if n := len(tracker.progress); n == 0 || tracker.progress[n-1] != [2]int{3, 3} {
		t.Fatalf("progress = %v; want it to end at (3, 3)", tracker.progress)
	}
	if tracker.rated != 3 || tracker.err != nil {
		t.Fatalf("end(%d, %v); want end(3, nil)", tracker.rated, tracker.err)
	}
	if n := h.folderChanged(id, "ratings"); n == 0 {
		t.Fatal("no folder:changed ratings event")
	}
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 0 {
		t.Fatalf("photos pending a rating = %d, %v; want 0", n, err)
	}
	var rated int
	if err := s.Store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM files WHERE quality_raw IS NOT NULL AND quality_model = ?`, quality.ScorerID).Scan(&rated); err != nil {
		t.Fatal(err)
	}
	if rated != 3 {
		t.Fatalf("photos rated by %s = %d; want 3", quality.ScorerID, rated)
	}
}

func TestJob_RatingsEventsAreThrottled(t *testing.T) {
	for _, tc := range []struct {
		name string
		step time.Duration // how far the clock moves at each reading
		want int
	}{
		// The first photo's event, then one after the pass for the two
		// held back (no "clusters" event follows the pass to carry them).
		{"same second", 0, 2},
		{"a second apart", time.Second, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
			h := newJobTestHarness(t, sessionTestOptions{stills: stills})
			id, root := h.folder(t, "throttle")
			jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")

			j := h.job(&jobTestTracker{})
			var mu sync.Mutex
			clock := sessionTestDay
			j.now = func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				clock = clock.Add(tc.step)
				return clock
			}
			if o, msg := h.run(context.Background(), j, id, JobFull); o != Done {
				t.Fatalf("Run = %v %q; want Done", o, msg)
			}
			if n := h.folderChanged(id, "ratings"); n != tc.want {
				t.Fatalf("folder:changed ratings = %d; want %d", n, tc.want)
			}
			if got := h.folderReasons(id); got[len(got)-1] != "ratings" {
				t.Fatalf("folder:changed reasons = %v; want the last rating announced last", got)
			}
		})
	}
}

func TestJob_QualityCancelledByTrackerStillFinishes(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "switched-off")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	tracker := &jobTestTracker{cancelAfter: 1}

	if o, msg := h.run(context.Background(), h.job(tracker), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	if n := h.afterJob.Load(); n != 1 {
		t.Fatalf("AfterJob called %d times; want 1", n)
	}
	if n := h.folderChanged(id, "clusters"); n != 1 {
		t.Fatalf("folder:changed clusters = %d; want 1", n)
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.ended != 1 || !errors.Is(tracker.err, context.Canceled) || tracker.rated != 1 {
		t.Fatalf("end(%d, %v) called %d times; want end(1, context.Canceled) once", tracker.rated, tracker.err, tracker.ended)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 2 {
		t.Fatalf("photos pending a rating = %d, %v; want 2 (the pass stopped after one)", n, err)
	}
}

func TestJob_QualitySettingOffStopsThePass(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "off")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	// No tracker cancels the pass: the job itself stops rating once the
	// setting reads off.
	tracker := &jobTestTracker{}
	j := h.job(tracker)
	j.o.Quality = trackerHook{inner: tracker, onProgress: func() {
		h.settings.set(func(c *config.Config) { c.QualityRating = false })
	}}

	if o, msg := h.run(context.Background(), j, id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 2 {
		t.Fatalf("photos pending a rating = %d, %v; want 2 (no photo rated after the switch)", n, err)
	}
}

// trackerHook wraps a QualityRun, calling onProgress after each progress
// and onEnd before end (each when set).
type trackerHook struct {
	inner      QualityRun
	onProgress func()
	onEnd      func()
}

func (t trackerHook) Begin(ctx context.Context, folderID string) (context.Context, func(done, total int), func(rated int, err error)) {
	passCtx, progress, end := t.inner.Begin(ctx, folderID)
	return passCtx, func(done, total int) {
			progress(done, total)
			if t.onProgress != nil {
				t.onProgress()
			}
		}, func(rated int, err error) {
			if t.onEnd != nil {
				t.onEnd()
			}
			end(rated, err)
		}
}

// Review Focus 1: a drive unplugged mid-scan, its database kept. The job is
// suspended without marking any file failed because of the outage; after
// reconnection the next job finishes the interrupted files, and a file that
// is really broken still ends failed.
func TestJob_DisconnectReconnectResumes(t *testing.T) {
	gate := make(chan struct{})
	stills := &platformtest.Stills{ReadPaths: true, Gate: gate, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "external")
	jobTestWriteShots(t, stills, root, 64, 48, time.Second, "a.jpg", "b.jpg", "c.jpg")
	if err := os.WriteFile(filepath.Join(root, "notes.jpg"), []byte("not a JPEG"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}

	type result struct {
		o   Outcome
		msg string
	}
	done := make(chan result, 1)
	go func() {
		o, msg := h.run(context.Background(), h.job(nil), id, JobFull)
		done <- result{o, msg}
	}()
	for range 4 {
		sessionTestSend(t, gate) // the metadata stage
	}
	jobTestWaitCalls(t, stills, "Thumbnail", 1) // the thumb stage is held
	reconnect := sessionTestDisconnect(t, root)
	close(gate) // the held decodes now find the files gone

	var r result
	select {
	case r = <-done:
	case <-time.After(sessionTestTimeout):
		t.Fatal("the job did not return")
	}
	if r.o != Suspended {
		t.Fatalf("Run = %v %q; want Suspended", r.o, r.msg)
	}
	h.events.waitFor(t, id, managerStageClosed, 1)
	reconnect()

	rows := h.rows(t, id)
	if n := jobTestCountStatus(rows, "failed"); n != 0 {
		t.Fatalf("rows after the outage = %+v; want none failed", rows)
	}
	if len(rows) != 4 {
		t.Fatalf("rows after the outage = %+v; want the four files kept", rows)
	}

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run after reconnection = %v %q; want Done", o, msg)
	}
	rows = h.rows(t, id)
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		if rows[name].status != "analyzed" {
			t.Errorf("%s = %+v; want analyzed", name, rows[name])
		}
	}
	if r := rows["notes.jpg"]; r.status != "failed" || r.err == "" {
		t.Errorf("notes.jpg = %+v; want failed with a reason", r)
	}
}

// The folder vanishes after the job's per-photo check but before the scorer
// reads the last photo's preview, and is back before the tracker's end: the
// photo stays pending and the job is suspended — only the scorer's guard saw
// the outage.
func TestJob_FolderVanishesBetweenCheckAndPreviewRead(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "external")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	reconnect := func() {}
	j := h.job(trackerHook{inner: &jobTestTracker{}, onEnd: func() { reconnect() }})
	calls := 0
	j.o.BeforeScore = func() {
		calls++
		if calls == 3 { // the last photo
			reconnect = sessionTestDisconnect(t, root)
		}
	}

	if o, msg := h.run(context.Background(), j, id, JobFull); o != Suspended {
		t.Fatalf("Run = %v %q; want Suspended", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 1 {
		t.Fatalf("photos pending a rating = %d, %v; want 1 (not stamped while the folder was gone)", n, err)
	}
}

// Once a run has seen the folder gone it is suspended, even when the folder
// is back before the job returns.
func TestJob_FolderBackBeforeEndIsStillSuspended(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "flaky")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	var (
		once      sync.Once
		reconnect func()
	)
	j := h.job(trackerHook{
		inner:      &jobTestTracker{},
		onProgress: func() { once.Do(func() { reconnect = sessionTestDisconnect(t, root) }) },
		onEnd:      func() { reconnect() }, // back before the tracker's end returns
	})

	if o, msg := h.run(context.Background(), j, id, JobFull); o != Suspended {
		t.Fatalf("Run = %v %q; want Suspended", o, msg)
	}
	if n := h.afterJob.Load(); n != 1 {
		t.Fatalf("AfterJob called %d times; want 1, before the pass", n)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 2 {
		t.Fatalf("photos pending a rating = %d, %v; want 2", n, err)
	}
}

func TestJob_QualityOffSkipsPass(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "skip")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg")
	h.settings.set(func(c *config.Config) { c.QualityRating = false })
	tracker := &jobTestTracker{}

	if o, msg := h.run(context.Background(), h.job(tracker), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.begun != 0 {
		t.Fatalf("Begin called %d times; want no pass", tracker.begun)
	}
	// Without a pass the job publishes its clusters once, at its end:
	// one refresh, one grid reload, no ratings.
	if n := h.afterJob.Load(); n != 1 {
		t.Fatalf("AfterJob called %d times; want 1", n)
	}
	if got := h.folderReasons(id); !slices.Equal(got, []string{"clusters"}) {
		t.Fatalf("folder:changed reasons = %v; want [clusters]", got)
	}
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 1 {
		t.Fatalf("photos pending a rating = %d, %v; want 1 (none rated)", n, err)
	}
}

// The folder's clusters are final before the quality pass: the job refreshes
// the Library's statistics and previews and then emits folder:changed
// "clusters" before the first photo is rated, so an open folder shows its
// grid while the ratings are computed. The pass ends with a "ratings" event,
// not a second grid reload or refresh.
func TestJob_ClustersPublishedBeforeTheRatingPass(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "early")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")

	var (
		refreshes     atomic.Int64
		eventsAtFirst atomic.Int64 // folder:changed clusters already sent when AfterJob ran
	)
	j := h.jobWith(&jobTestTracker{}, func(context.Context, *Session) {
		if refreshes.Add(1) == 1 {
			eventsAtFirst.Store(int64(h.folderChanged(id, "clusters")))
		}
	})
	// The rating gate: the first photo is held until the test releases it.
	entered, gate := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	defer release()
	var first sync.Once
	j.o.BeforeScore = func() {
		first.Do(func() {
			close(entered)
			<-gate
		})
	}

	done := make(chan Outcome, 1)
	go func() {
		o, _ := h.run(context.Background(), j, id, JobFull)
		done <- o
	}()
	select {
	case <-entered:
	case <-time.After(sessionTestTimeout):
		t.Fatal("the rating pass never reached its first photo")
	}

	// Held at the first photo: the clusters are already published.
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("stats and previews refreshed %d times before the rating gate; want 1", n)
	}
	if n := eventsAtFirst.Load(); n != 0 {
		t.Fatalf("folder:changed clusters sent before the refresh = %d; want the refresh first", n)
	}
	if got := h.folderReasons(id); !slices.Equal(got, []string{"clusters"}) {
		t.Fatalf("folder:changed reasons before the rating gate = %v; want [clusters]", got)
	}

	release()
	select {
	case o := <-done:
		if o != Done {
			t.Fatalf("Run = %v; want Done", o)
		}
	case <-time.After(sessionTestTimeout):
		t.Fatal("the job did not return")
	}
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("stats and previews refreshed %d times; want 1 (the pass changes neither)", n)
	}
	got := h.folderReasons(id)
	if n := h.folderChanged(id, "clusters"); n != 1 {
		t.Fatalf("folder:changed reasons = %v; want one clusters (the pass changes no cluster)", got)
	}
	if len(got) < 2 || got[0] != "clusters" || got[len(got)-1] != "ratings" {
		t.Fatalf("folder:changed reasons = %v; want clusters first and the pass's ratings last", got)
	}
}

// A job cancelled while it publishes its clusters (preemption, quit) stops
// before the rating pass when one is due — the publication is not the end of
// that job — and finishes when none is, as the last step always did. Either
// way the end-of-job path runs once.
func TestJob_CancelledWhilePublishingClusters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		quality bool
		want    Outcome
	}{
		{"a rating pass due", true, Stopped},
		{"no rating pass", false, Done},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
			h := newJobTestHarness(t, sessionTestOptions{stills: stills})
			h.settings.set(func(c *config.Config) { c.QualityRating = tc.quality })
			id, root := h.folder(t, "preempted")
			jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var refreshes atomic.Int64
			tracker := &jobTestTracker{}
			j := h.jobWith(tracker, func(context.Context, *Session) {
				refreshes.Add(1)
				cancel()
			})

			if o, msg := h.run(ctx, j, id, JobFull); o != tc.want {
				t.Fatalf("Run = %v %q; want %v", o, msg, tc.want)
			}
			if n := refreshes.Load(); n != 1 {
				t.Fatalf("AfterJob called %d times; want 1", n)
			}
			if got := h.folderReasons(id); !slices.Equal(got, []string{"clusters"}) {
				t.Fatalf("folder:changed reasons = %v; want [clusters]", got)
			}
			tracker.mu.Lock()
			defer tracker.mu.Unlock()
			if tracker.begun != 0 {
				t.Fatalf("Begin called %d times; want no pass after the cancellation", tracker.begun)
			}
		})
	}
}

func TestJob_V1FolderFullRunStampsParams(t *testing.T) {
	h := newJobTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "v1")
	sessionTestCopyV1(t, root)
	func() {
		s, release := h.acquire(t, id)
		defer release()
		if _, err := s.Store.KV().Get(sessionKVClusterParams); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("v1 fixture cluster_params: %v; want none", err)
		}
	}()

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("cluster_params = %q; want %q", got, want)
	}
	if n, err := s.Store.Files().CountByStatus("analyzed"); err != nil || n != 6 {
		t.Fatalf("analyzed = %d, %v; want the six v1 photos left analyzed", n, err)
	}
}

// TestJob_V1FolderBackfillUsesThumbWithoutPreview: v1 made only a thumb tier
// for a video, so the print backfill of a v1 folder reads the thumb tier of a
// file without a preview tier (no row, or a row whose cache file is gone),
// as the media handler does, instead of failing it.
func TestJob_V1FolderBackfillUsesThumbWithoutPreview(t *testing.T) {
	vis := &platformtest.Vision{}
	h := newJobTestHarness(t, sessionTestOptions{vision: vis})
	id, root := h.folder(t, "v1video")
	sessionTestCopyV1(t, root)

	// What v1 left for a video: the original, its row, a thumb row and the
	// thumb cache file, an ai_signals row with no embedding; no preview.
	const videoID = 7
	videoRel := "DCIM/100TEST/TEST0007.MOV"
	videoAbs := filepath.Join(root, filepath.FromSlash(videoRel))
	if err := os.WriteFile(videoAbs, []byte("a v1 video, never decoded by this test\n"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	if err := os.Chtimes(videoAbs, sessionTestV1Mtime, sessionTestV1Mtime); err != nil {
		t.Fatal(err)
	}
	hash, err := scanner.ContentHash(videoAbs)
	if err != nil {
		t.Fatal(err)
	}
	videoThumb := ".choix/thumbs/07/7-thumb.jpg"
	if err := os.MkdirAll(filepath.Join(root, ".choix", "thumbs", "07"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(videoThumb)), platformtest.MakeJPEG(t, 64, 48), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	func() {
		s, release := h.acquire(t, id)
		defer release()
		db := s.Store.DB()
		ctx := context.Background()
		captured := time.Date(2026, 1, 2, 10, 0, 15, 0, time.UTC).Unix()
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO files (id, path, size, mtime, content_hash, kind, format, device_key, captured_at, width, height, scan_status)
			  SELECT ?, ?, ?, ?, ?, 'video', 'mov', device_key, ?, 64, 48, 'analyzed' FROM files WHERE id = 1`,
				[]any{videoID, videoRel, len("a v1 video, never decoded by this test\n"), sessionTestV1Mtime.Unix(), hash, captured}},
			{`INSERT INTO thumbs (file_id, tier, rel_path, width, height) VALUES (?, 'thumb', ?, 64, 48)`, []any{videoID, videoThumb}},
			{`INSERT INTO ai_signals (file_id, clip_embedding, computed_at) VALUES (?, NULL, ?)`, []any{videoID, sessionTestV1Mtime.Unix()}},
			// A photo with only a thumb row, and one whose preview cache
			// file is gone.
			{`DELETE FROM thumbs WHERE file_id = 1 AND tier = 'preview'`, nil},
		} {
			if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
				t.Fatalf("%s: %v", q.sql, err)
			}
		}
	}()
	for _, p := range []string{"01/1-preview.jpg", "04/4-preview.jpg"} {
		if err := os.Remove(filepath.Join(root, ".choix", "thumbs", filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}

	if o, msg := h.run(context.Background(), h.job(nil), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	s, release := h.acquire(t, id)
	defer release()
	prints := jobTestPrints(t, s.Store)
	inCluster := map[int64]bool{}
	rows, err := s.Store.DB().QueryContext(context.Background(), `SELECT file_id FROM cluster_members`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			t.Fatal(err)
		}
		inCluster[fid] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	for _, fid := range []int64{1, 4, videoID} {
		f, err := s.Store.Files().GetByID(fid)
		if err != nil {
			t.Fatal(err)
		}
		if f.ScanStatus != "analyzed" {
			t.Errorf("file %d (%s) is %q (%s); want analyzed", fid, f.Path, f.ScanStatus, f.ErrMsg.String)
		}
		if prints[fid] == nil {
			t.Errorf("file %d (%s) has no print stamped %s", fid, f.Path, analyze.PrintID)
		}
		if !inCluster[fid] {
			t.Errorf("file %d (%s) is in no cluster", fid, f.Path)
		}
	}
	if n := jobTestPending(t, s.Store); n != 0 {
		t.Fatalf("pending for a print = %d; want 0", n)
	}
}

func TestJob_OutcomeClassification(t *testing.T) {
	h := newJobTestHarness(t, sessionTestOptions{})
	j := h.job(nil)
	present := t.TempDir()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	plain := errors.New("disk full")
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		root    string
		err     error
		want    Outcome
		wantMsg string
	}{
		{"cancelled", cancelled, present, plain, Stopped, ""},
		{"shutting down", context.Background(), present, fmt.Errorf("x: %w", ErrShuttingDown), Stopped, ""},
		// Seen gone during the run: suspended even though the root is back.
		{"outage seen by a stage", context.Background(), present, fmt.Errorf("x: %w", pipeline.ErrFolderUnavailable), Suspended, ""},
		{"gone between steps", context.Background(), present, fmt.Errorf("x: %w", errJobFolderGone), Suspended, ""},
		{"offline", context.Background(), "", fmt.Errorf("x: %w", library.ErrOffline), Suspended, ""},
		{"access lost", context.Background(), "", fmt.Errorf("x: %w", library.ErrAccessLost), Suspended, ""},
		{"root gone", context.Background(), filepath.Join(present, "missing"), plain, Suspended, ""},
		{"read-only", context.Background(), present, fmt.Errorf("x: %w", ErrReadOnly), Failed, ReadOnlyMessage},
		{"unsafe", context.Background(), present, fmt.Errorf("x: %w", ErrUnsafeState), Failed, UnsafeStateMessage},
		{"other", context.Background(), present, plain, Failed, "disk full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if o, msg := j.outcome(tc.ctx, "f", tc.root, tc.err); o != tc.want || msg != tc.wantMsg {
				t.Fatalf("outcome = %v %q; want %v %q", o, msg, tc.want, tc.wantMsg)
			}
		})
	}
}

func TestJob_CancelledBeforeStartIsStopped(t *testing.T) {
	h := newJobTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "early")
	sessionTestWriteJPEG(t, root, "a.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if o, msg := h.run(ctx, h.job(nil), id, JobFull); o != Stopped {
		t.Fatalf("Run = %v %q; want Stopped", o, msg)
	}
	if _, err := os.Lstat(filepath.Join(root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a cancelled job opened the folder: %v", err)
	}
}
