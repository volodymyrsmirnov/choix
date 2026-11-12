package scanner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// walkGuard is how long a test waits for a Walk that must not block before
// it reports the walk as stuck. It is a failure deadline, not a sleep: a
// healthy walk returns long before it.
const walkGuard = 10 * time.Second

// walkWithGuard runs sc.Walk on a goroutine and fails the test if it does not
// return within walkGuard. unblock, when non-nil, is called on timeout to
// release whatever the walk is stuck on so the goroutine can end.
func walkWithGuard(t *testing.T, sc *Scanner, unblock func()) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- sc.Walk(context.Background()) }()
	select {
	case err := <-done:
		return err
	case <-time.After(walkGuard):
		if unblock != nil {
			unblock()
		}
		t.Fatalf("Walk did not return within %s: it is blocked on an entry", walkGuard)
		return nil
	}
}

// skipIfRoot skips a test that relies on permission bits: root ignores them.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced for root")
	}
}

// chmodRestore sets path's mode and restores 0o755 at cleanup so
// t.TempDir can remove the tree.
func chmodRestore(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// rowOf returns the files row for rel, or ok=false when there is none.
func rowOf(t *testing.T, s *store.Store, rel string) (store.File, bool) {
	t.Helper()
	f, err := s.Files().GetByPath(rel)
	if errors.Is(err, store.ErrNotFound) {
		return store.File{}, false
	}
	if err != nil {
		t.Fatalf("GetByPath %s: %v", rel, err)
	}
	return f, true
}

func TestWalkSkipsFIFONamedLikeMedia(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "ok.jpg", []byte("ok"))
	fifo := filepath.Join(root, "blocked.jpg")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// Releases a reader blocked in open(2) on the FIFO: opening the write end
	// lets the blocked open return.
	unblock := func() {
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
	}

	s := newScannerStore(t)
	sc := New(root, s)
	if err := walkWithGuard(t, sc, unblock); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := listPaths(t, s); !equalSorted(got, "ok.jpg") {
		t.Fatalf("paths = %v, want only ok.jpg (a FIFO is never taken)", got)
	}
}

func TestWalkSkipsDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "ok.jpg", []byte("ok"))
	if err := os.Symlink(filepath.Join(root, "nowhere.jpg"), filepath.Join(root, "dangling.jpg")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	s := newScannerStore(t)
	sc := New(root, s)
	if err := walkWithGuard(t, sc, nil); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := listPaths(t, s); !equalSorted(got, "ok.jpg") {
		t.Fatalf("paths = %v, want only ok.jpg", got)
	}
	if got := statusOf(t, sc, "ok.jpg"); got != "discovered" {
		t.Fatalf("ok.jpg status = %q, want discovered", got)
	}
}

func TestWalkFollowsSymlinkOnlyToARegularFile(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, root, "ok.jpg", []byte("ok"))
	target := filepath.Join(outside, "target.jpg")
	payload := []byte("the target's bytes, longer than the link")
	mustWrite(t, outside, "target.jpg", payload)
	if err := os.Mkdir(filepath.Join(outside, "dir.jpg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, outside, "locked/hidden.jpg", []byte("x"))
	chmodRestore(t, filepath.Join(outside, "locked"), 0)

	for name, to := range map[string]string{
		"link.jpg":     target,                                      // regular file: taken
		"todir.jpg":    filepath.Join(outside, "dir.jpg"),           // a directory: skipped
		"denied.jpg":   filepath.Join(outside, "locked/hidden.jpg"), // stat fails (EACCES): skipped
		"dangling.jpg": filepath.Join(outside, "gone.jpg"),          // dangling: skipped
	} {
		if err := os.Symlink(to, filepath.Join(root, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}

	s := newScannerStore(t)
	sc := New(root, s)
	if err := walkWithGuard(t, sc, nil); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := listPaths(t, s); !equalSorted(got, "link.jpg", "ok.jpg") {
		t.Fatalf("paths = %v, want link.jpg and ok.jpg", got)
	}
	link, _ := rowOf(t, s, "link.jpg")
	want, err := ContentHash(target)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if link.Size != int64(len(payload)) || link.ContentHash != want || link.ScanStatus != "discovered" {
		t.Fatalf("link.jpg = size %d hash %q status %q, want the target's size %d, hash %q, discovered",
			link.Size, link.ContentHash, link.ScanStatus, len(payload), want)
	}
}

func TestWalkSkipsUnreadableSubdirectory(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	mustWrite(t, root, "ok.jpg", []byte("ok"))
	mustWrite(t, root, "locked/inside.jpg", []byte("in"))
	mustWrite(t, root, "later/after.jpg", []byte("after"))

	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	chmodRestore(t, filepath.Join(root, "locked"), 0)

	if err := walkWithGuard(t, sc, nil); err != nil {
		t.Fatalf("Walk with an unreadable sub-directory: %v", err)
	}
	for _, rel := range []string{"ok.jpg", "later/after.jpg"} {
		if got := statusOf(t, sc, rel); got != "discovered" {
			t.Errorf("%s status = %q, want discovered", rel, got)
		}
	}
	// The directory could not be listed, so its file was not seen: it is
	// missing (row and any pick kept) until the directory is readable again.
	if got := statusOf(t, sc, "locked/inside.jpg"); got != "missing" {
		t.Errorf("locked/inside.jpg status = %q, want missing", got)
	}

	if err := os.Chmod(filepath.Join(root, "locked"), 0o755); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("walk after restoring access: %v", err)
	}
	if got := statusOf(t, sc, "locked/inside.jpg"); got != "discovered" {
		t.Errorf("locked/inside.jpg status after access is back = %q, want discovered", got)
	}
}

func TestWalkRecordsUnreadableFileAsFailed(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	mustWrite(t, root, "ok.jpg", []byte("ok"))
	mustWrite(t, root, "noread.jpg", []byte("secret"))
	noread := filepath.Join(root, "noread.jpg")
	chmodRestore(t, noread, 0)

	s := newScannerStore(t)
	sc := New(root, s)
	if err := walkWithGuard(t, sc, nil); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := statusOf(t, sc, "ok.jpg"); got != "discovered" {
		t.Errorf("ok.jpg status = %q, want discovered", got)
	}
	f, ok := rowOf(t, s, "noread.jpg")
	if !ok {
		t.Fatalf("noread.jpg has no row; want a failed row")
	}
	if f.ScanStatus != "failed" || !f.ErrMsg.Valid || !strings.Contains(f.ErrMsg.String, "permission denied") {
		t.Fatalf("noread.jpg = status %q error %q, want failed with the open error", f.ScanStatus, f.ErrMsg.String)
	}
	if f.Kind != "photo" || f.Format != "jpeg" {
		t.Errorf("noread.jpg kind/format = %q/%q, want photo/jpeg", f.Kind, f.Format)
	}

	// Readable again: the next walk takes it like any other file.
	if err := os.Chmod(noread, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("second walk: %v", err)
	}
	f, _ = rowOf(t, s, "noread.jpg")
	want, err := ContentHash(noread)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if f.ScanStatus != "discovered" || f.ErrMsg.Valid || f.ContentHash != want {
		t.Fatalf("noread.jpg after access is back = status %q error %v hash %q, want discovered, no error, hash %q",
			f.ScanStatus, f.ErrMsg, f.ContentHash, want)
	}
}

func TestWalkRootMissingIsFolderUnavailable(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "volume")
	mustWrite(t, root, "a.jpg", []byte("a"))
	mustWrite(t, root, "sub/b.jpg", []byte("b"))

	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove root: %v", err)
	}

	err := sc.Walk(context.Background())
	if !errors.Is(err, pipeline.ErrFolderUnavailable) {
		t.Fatalf("Walk with the root gone = %v, want ErrFolderUnavailable", err)
	}
	for _, rel := range []string{"a.jpg", "sub/b.jpg"} {
		if got := statusOf(t, sc, rel); got != "discovered" {
			t.Errorf("%s status = %q, want discovered (nothing is marked missing during an outage)", rel, got)
		}
	}
}

func TestWalkRootUnreadableIsFolderUnavailable(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	mustWrite(t, root, "a.jpg", []byte("a"))

	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	chmodRestore(t, root, 0)

	err := sc.Walk(context.Background())
	if !errors.Is(err, pipeline.ErrFolderUnavailable) {
		t.Fatalf("Walk with an unreadable root = %v, want ErrFolderUnavailable", err)
	}
	if got := statusOf(t, sc, "a.jpg"); got != "discovered" {
		t.Errorf("a.jpg status = %q, want discovered", got)
	}
}

// ---------------------------------------------------------------------------
// Pipeline harness: the real scanner and grouper, fake per-file stages.
// ---------------------------------------------------------------------------

// statusIDs adapts *store.Store to pipeline.IDLister.
type statusIDs struct{ st *store.Store }

func (l statusIDs) ListByStatus(_ context.Context, status string, limit int) ([]int64, error) {
	return l.st.Files().IDsByStatus(status, limit)
}

func (l statusIDs) CountByStatus(_ context.Context, status string) (int, error) {
	return l.st.Files().CountByStatus(status)
}

// advance is a fake stage that moves a file to the next scan_status.
type advance struct {
	st *store.Store
	to string
}

func (a advance) Process(_ context.Context, id int64) error {
	return a.st.Files().UpdateStatus(id, a.to, "")
}
func (a advance) Build(_ context.Context, id int64) error {
	return a.st.Files().UpdateStatus(id, a.to, "")
}
func (a advance) Analyze(_ context.Context, id int64) error {
	return a.st.Files().UpdateStatus(id, a.to, "")
}

func newTestPipeline(st *store.Store, sc *Scanner) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Store:         statusIDs{st: st},
		Scanner:       sc,
		MetaExtractor: advance{st: st, to: "metadata"},
		ThumbBuilder:  advance{st: st, to: "thumbed"},
		Analyzer:      advance{st: st, to: "analyzed"},
		Grouper:       group.NewGrouper(st, 600, 0.92, false),
		Reporter:      pipeline.NewReporter(make(chan pipeline.Progress, pipeline.ProgressBufferSize)),
		Concurrency:   map[string]int{"metadata": 2, "thumb": 2, "analyze": 1},
	}
}

// clustered reports whether the file at rel is a member of a cluster.
func clustered(t *testing.T, st *store.Store, rel string) bool {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM cluster_members cm JOIN files f ON f.id = cm.file_id WHERE f.path = ?`, rel).Scan(&n); err != nil {
		t.Fatalf("cluster membership of %s: %v", rel, err)
	}
	return n > 0
}

func TestPipelineBrokenEntriesDoNotStopTheOtherFiles(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	mustWrite(t, root, "a.jpg", []byte("a"))
	mustWrite(t, root, "day/b.jpg", []byte("b"))
	mustWrite(t, root, "noread.jpg", []byte("n"))
	mustWrite(t, root, "locked/c.jpg", []byte("c"))
	if err := syscall.Mkfifo(filepath.Join(root, "fifo.jpg"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere.jpg"), filepath.Join(root, "dangling.jpg")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	chmodRestore(t, filepath.Join(root, "noread.jpg"), 0)
	chmodRestore(t, filepath.Join(root, "locked"), 0)

	st := newScannerStore(t)
	p := newTestPipeline(st, New(root, st))
	done := make(chan error, 1)
	go func() { done <- p.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(walkGuard):
		if w, err := os.OpenFile(filepath.Join(root, "fifo.jpg"), os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatalf("Run did not return within %s", walkGuard)
	}

	for _, rel := range []string{"a.jpg", "day/b.jpg"} {
		f, ok := rowOf(t, st, rel)
		if !ok || f.ScanStatus != "analyzed" {
			t.Errorf("%s = %+v (row %t), want analyzed", rel, f.ScanStatus, ok)
		}
		if !clustered(t, st, rel) {
			t.Errorf("%s is in no cluster", rel)
		}
	}
	if f, ok := rowOf(t, st, "noread.jpg"); !ok || f.ScanStatus != "failed" {
		t.Errorf("noread.jpg = %q (row %t), want failed", f.ScanStatus, ok)
	}
	if got := listPaths(t, st); !equalSorted(got, "a.jpg", "day/b.jpg", "noread.jpg") {
		t.Errorf("paths = %v, want a.jpg, day/b.jpg, noread.jpg", got)
	}
}

func TestRestoredMissingFileIsAnalysedAgainAndKeepsItsPick(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "keep.jpg", []byte("keep"))
	mustWrite(t, root, "trip/away.jpg", []byte("away and back"))
	away := filepath.Join(root, "trip", "away.jpg")
	info, err := os.Stat(away)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	st := newScannerStore(t)
	p := newTestPipeline(st, New(root, st))
	ctx := context.Background()
	if err := p.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	f, _ := rowOf(t, st, "trip/away.jpg")
	if f.ScanStatus != "analyzed" || !clustered(t, st, "trip/away.jpg") {
		t.Fatalf("after the first run away.jpg = %q, clustered %t; want analyzed and clustered",
			f.ScanStatus, clustered(t, st, "trip/away.jpg"))
	}
	if err := st.Durable(ctx, func(tx *sql.Tx) error {
		return st.Picks().SetTx(tx, f.ID, "picked", "")
	}); err != nil {
		t.Fatalf("pick: %v", err)
	}

	// The sub-folder is moved out: the file is missing and leaves the clusters.
	if err := os.Rename(filepath.Join(root, "trip"), filepath.Join(t.TempDir(), "trip")); err != nil {
		t.Fatalf("move out: %v", err)
	}
	if err := p.Run(ctx); err != nil {
		t.Fatalf("run while away: %v", err)
	}
	if got, _ := rowOf(t, st, "trip/away.jpg"); got.ScanStatus != "missing" {
		t.Fatalf("away.jpg while away = %q, want missing", got.ScanStatus)
	}
	if clustered(t, st, "trip/away.jpg") {
		t.Fatalf("away.jpg is still clustered while missing")
	}

	// It comes back byte for byte with the same modification time.
	mustWrite(t, root, "trip/away.jpg", []byte("away and back"))
	if err := os.Chtimes(away, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := p.Run(ctx); err != nil {
		t.Fatalf("run after restore: %v", err)
	}
	got, _ := rowOf(t, st, "trip/away.jpg")
	if got.ID != f.ID {
		t.Fatalf("away.jpg id = %d, want the same row %d", got.ID, f.ID)
	}
	if got.ScanStatus != "analyzed" {
		t.Errorf("away.jpg after restore = %q, want analyzed", got.ScanStatus)
	}
	if !clustered(t, st, "trip/away.jpg") {
		t.Errorf("away.jpg after restore is in no cluster")
	}
	pick, err := st.Picks().Get(f.ID)
	if err != nil || pick.State != "picked" {
		t.Errorf("pick after restore = %+v, %v; want picked", pick, err)
	}
}

// ---------------------------------------------------------------------------
// Tests that act between readdir and open through the beforeOpen seam.
// ---------------------------------------------------------------------------

func TestWalkSkipsFileRemovedBetweenReaddirAndOpen(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.jpg", "b.jpg", "c.jpg", "known.jpg"} {
		mustWrite(t, root, rel, []byte(rel))
	}
	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "b.jpg")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("second walk: %v", err)
	}
	if got := statusOf(t, sc, "b.jpg"); got != "missing" {
		t.Fatalf("b.jpg = %q, want missing", got)
	}

	// b.jpg is back on disk and a new file appears; they and a known file
	// vanish after the directory was read and before they are opened.
	mustWrite(t, root, "b.jpg", []byte("b.jpg"))
	mustWrite(t, root, "new.jpg", []byte("new"))
	var opened []string
	sc.beforeOpen = func(rel string) {
		opened = append(opened, rel)
		if rel == "b.jpg" || rel == "new.jpg" || rel == "known.jpg" {
			if err := os.Remove(filepath.Join(root, rel)); err != nil {
				t.Errorf("remove %s: %v", rel, err)
			}
		}
	}
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("walk with entries vanishing: %v", err)
	}
	if len(opened) != 5 {
		t.Fatalf("beforeOpen saw %v, want the five media entries", opened)
	}
	for rel, want := range map[string]string{
		"a.jpg": "discovered", "c.jpg": "discovered",
		"b.jpg": "missing", "known.jpg": "missing",
	} {
		if got := statusOf(t, sc, rel); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, ok := rowOf(t, s, "new.jpg"); ok {
		t.Errorf("new.jpg vanished before it was opened but has a row")
	}
}

func TestWalkRootRemovedMidWalkIsFolderUnavailable(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "volume")
	all := []string{"a.jpg", "b.jpg", "day/c.jpg", "day/d.jpg"}
	for _, rel := range all {
		mustWrite(t, root, rel, []byte(rel))
	}
	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	ids, err := s.Files().IDsByStatus("discovered", 10)
	if err != nil || len(ids) != len(all) {
		t.Fatalf("IDsByStatus = %v, %v", ids, err)
	}
	for _, id := range ids {
		if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
	}

	// The volume goes away while the walk is in the middle of the folder.
	calls := 0
	sc.beforeOpen = func(string) {
		calls++
		if calls == 2 {
			if err := os.RemoveAll(root); err != nil {
				t.Errorf("remove root: %v", err)
			}
		}
	}
	err = sc.Walk(context.Background())
	if !errors.Is(err, pipeline.ErrFolderUnavailable) {
		t.Fatalf("Walk = %v, want ErrFolderUnavailable", err)
	}
	for _, rel := range all {
		if got := statusOf(t, sc, rel); got != "analyzed" {
			t.Errorf("%s = %q, want analyzed (an outage marks nothing missing or failed)", rel, got)
		}
	}
}

func TestWalkHonoursCancellationBetweenEntries(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		mustWrite(t, root, rel, []byte(rel))
	}
	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "c.jpg")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := 0
	sc.beforeOpen = func(string) {
		opened++
		cancel()
	}
	err := sc.Walk(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Walk = %v, want context.Canceled", err)
	}
	if opened != 1 {
		t.Errorf("entries opened after cancellation: %d, want 1", opened)
	}
	// A cancelled walk did not see the whole folder: nothing is marked missing.
	if got := statusOf(t, sc, "c.jpg"); got != "discovered" {
		t.Errorf("c.jpg = %q, want discovered", got)
	}
}

// Cancelled while the last entry is taken: no callback follows, but the walk
// must still not reconcile — it returns the context error and marks nothing
// missing.
func TestWalkCancelledOnTheLastEntryMarksNothingMissing(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		mustWrite(t, root, rel, []byte(rel))
	}
	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	// c.jpg leaves: it is the unseen existing row; b.jpg is the last entry.
	if err := os.Remove(filepath.Join(root, "c.jpg")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc.beforeOpen = func(rel string) {
		if rel == "b.jpg" {
			cancel()
		}
	}
	err := sc.Walk(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Walk = %v, want context.Canceled", err)
	}
	if got := statusOf(t, sc, "c.jpg"); got != "discovered" {
		t.Errorf("c.jpg = %q, want discovered (a cancelled walk marks nothing missing)", got)
	}
}

// statusesOf returns every row's path → scan_status.
func statusesOf(t *testing.T, s *store.Store) map[string]string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT path, scan_status FROM files`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var p, st string
		if err := rows.Scan(&p, &st); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[p] = st
	}
	return out
}

// A root that can be listed but not searched (or searched but not listed)
// is not traversable: the walk is an outage, not a list of failed files.
func TestWalkRootNotTraversableIsFolderUnavailable(t *testing.T) {
	skipIfRoot(t)
	for _, mode := range []os.FileMode{0o400, 0o100} {
		t.Run(mode.String(), func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range []string{"a.jpg", "b.jpg", "day/c.jpg"} {
				mustWrite(t, root, rel, []byte(rel))
			}
			s := newScannerStore(t)
			sc := New(root, s)
			if err := sc.Walk(context.Background()); err != nil {
				t.Fatalf("first walk: %v", err)
			}
			if err := os.Remove(filepath.Join(root, "b.jpg")); err != nil {
				t.Fatalf("remove: %v", err)
			}
			before := statusesOf(t, s)
			chmodRestore(t, root, mode)

			err := sc.Walk(context.Background())
			if !errors.Is(err, pipeline.ErrFolderUnavailable) {
				t.Fatalf("Walk with root mode %v = %v, want ErrFolderUnavailable", mode, err)
			}
			if after := statusesOf(t, s); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Fatalf("statuses changed during an outage: %v, want %v", after, before)
			}
		})
	}
}

// The root loses its search permission in the middle of the walk.
func TestWalkRootLosesSearchPermissionMidWalk(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	for _, rel := range []string{"a.jpg", "b.jpg", "c.jpg", "day/d.jpg"} {
		mustWrite(t, root, rel, []byte(rel))
	}
	s := newScannerStore(t)
	sc := New(root, s)
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "c.jpg")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	before := statusesOf(t, s)

	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	calls := 0
	sc.beforeOpen = func(string) {
		calls++
		if calls == 2 {
			if err := os.Chmod(root, 0o400); err != nil {
				t.Errorf("chmod root: %v", err)
			}
		}
	}
	err := sc.Walk(context.Background())
	if !errors.Is(err, pipeline.ErrFolderUnavailable) {
		t.Fatalf("Walk = %v, want ErrFolderUnavailable", err)
	}
	if after := statusesOf(t, s); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("statuses changed during an outage: %v, want %v", after, before)
	}
}
