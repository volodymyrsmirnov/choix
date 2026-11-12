package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// catalogTestTimeout bounds every wait for something that must happen.
const catalogTestTimeout = 10 * time.Second

// catalogTestSettings is a mutable config.Config read through get.
type catalogTestSettings struct {
	mu  sync.Mutex
	cfg config.Config
}

func (s *catalogTestSettings) get() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *catalogTestSettings) set(fn func(*config.Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
}

// catalogTestTimers replaces time.AfterFunc: timers fire only when the test
// fires them.
type catalogTestTimers struct {
	mu     sync.Mutex
	timers []*catalogTestTimer
}

type catalogTestTimer struct {
	owner   *catalogTestTimers
	d       time.Duration
	fn      func()
	stopped bool
	fired   bool
}

func (tt *catalogTestTimers) afterFunc(d time.Duration, fn func()) stopper {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	t := &catalogTestTimer{owner: tt, d: d, fn: fn}
	tt.timers = append(tt.timers, t)
	return t
}

func (t *catalogTestTimer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

// pending returns the timers neither stopped nor fired.
func (tt *catalogTestTimers) pending() []*catalogTestTimer {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	var out []*catalogTestTimer
	for _, t := range tt.timers {
		if !t.stopped && !t.fired {
			out = append(out, t)
		}
	}
	return out
}

// fire fires every pending timer, synchronously and in order, and returns
// how many it fired.
func (tt *catalogTestTimers) fire() int {
	pending := tt.pending()
	tt.mu.Lock()
	for _, t := range pending {
		t.fired = true
	}
	tt.mu.Unlock()
	for _, t := range pending {
		t.fn()
	}
	return len(pending)
}

// catalogTestHarness is a Catalog over a real Library and session Manager
// with the platform fakes.
type catalogTestHarness struct {
	lib      *library.Library
	bm       *platformtest.Bookmarks
	mgr      *session.Manager
	rec      *events.Recorder
	settings *catalogTestSettings
	timers   *catalogTestTimers
	cat      *Catalog
	parent   string
	previews string

	enqMu    sync.Mutex
	enqueued []string

	// stills is the platform fake the sessions and previews use.
	stills *platformtest.Stills
	// events records the catalog's hook events, in order.
	hookMu sync.Mutex
	hooked []string
	hookCh chan struct{}
}

// withStills makes the harness use s (set its fields before passing it).
func withStills(s *platformtest.Stills) func(*catalogTestHarness) {
	return func(h *catalogTestHarness) { h.stills = s }
}

// recordHook records a catalog hook event as "event:folderID".
func (h *catalogTestHarness) recordHook(event, folderID string) {
	h.hookMu.Lock()
	defer h.hookMu.Unlock()
	h.hooked = append(h.hooked, event+":"+folderID)
	if h.hookCh != nil {
		close(h.hookCh)
	}
	h.hookCh = make(chan struct{})
}

// waitHook waits until event has been recorded for folderID n times.
func (h *catalogTestHarness) waitHook(t *testing.T, event, folderID string, n int) {
	t.Helper()
	deadline := time.After(catalogTestTimeout)
	for {
		h.hookMu.Lock()
		count := 0
		for _, e := range h.hooked {
			if e == event+":"+folderID {
				count++
			}
		}
		if h.hookCh == nil {
			h.hookCh = make(chan struct{})
		}
		ch := h.hookCh
		h.hookMu.Unlock()
		if count >= n {
			return
		}
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("hook %q for %s never seen %d times", event, folderID, n)
		}
	}
}

func newCatalogTestHarness(t *testing.T, opts ...func(*catalogTestHarness)) *catalogTestHarness {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previews, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := library.OpenDB(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	h := &catalogTestHarness{
		bm:       &platformtest.Bookmarks{},
		rec:      &events.Recorder{},
		settings: &catalogTestSettings{cfg: config.Defaults()},
		timers:   &catalogTestTimers{},
		parent:   parent,
		previews: previews,
	}
	for _, o := range opts {
		o(h)
	}
	if h.stills == nil {
		h.stills = &platformtest.Stills{}
	}
	stills := h.stills
	vols := &platformtest.Volumes{}
	vols.Add("/", platform.VolumeInfo{UUID: "UUID-TEST", Name: "Test HD"})
	previewsDir := func(id string) (string, error) {
		dir := filepath.Join(previews, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return dir, nil
	}
	h.lib = library.New(library.Options{
		DB: db, Bookmarks: h.bm, Volumes: vols, Emit: h.rec, PreviewsDir: previewsDir,
		// As the runtime wires it: a folder that goes offline is
		// invalidated and closed. Delivered on whichever goroutine drains
		// the Library's notifications — possibly one inside Catalog.Open.
		OnOffline: func(id string) {
			if closeFn := h.cat.Invalidate(id); closeFn != nil {
				closeFn()
			}
		},
	})
	h.mgr = session.NewManager(session.Deps{
		Library:  h.lib,
		Settings: h.settings.get,
		Stills:   stills,
		Video:    &platformtest.Video{},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), catalogTestTimeout)
		defer cancel()
		if n := h.mgr.Shutdown(ctx); n != 0 {
			t.Errorf("cleanup: Shutdown left %d sessions open", n)
		}
	})
	h.cat = New(Deps{
		Sessions: h.mgr,
		Library:  h.lib,
		Settings: h.settings.get,
		EnqueueFront: func(id string) {
			h.enqMu.Lock()
			defer h.enqMu.Unlock()
			h.enqueued = append(h.enqueued, id)
		},
		Emit:         h.rec,
		Stills:       stills,
		PreviewsDir:  previewsDir,
		RefreshDelay: 500 * time.Millisecond,
		Context:      context.Background(),
	})
	h.cat.afterFunc = h.timers.afterFunc
	h.cat.hook = h.recordHook
	t.Cleanup(h.cat.CloseAll)
	return h
}

// folder creates the folder name under the harness's parent, adds it to the
// Library and returns its id and root.
func (h *catalogTestHarness) folder(t *testing.T, name string) (id, root string) {
	t.Helper()
	root = filepath.Join(h.parent, name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := h.lib.Add(root)
	if err != nil {
		t.Fatalf("Add(%s): %v", root, err)
	}
	return res.ID, root
}

// open opens the folder in the catalog, failing the test on error.
func (h *catalogTestHarness) open(t *testing.T, id string) FolderView {
	t.Helper()
	v, err := h.cat.Open(context.Background(), id)
	if err != nil {
		t.Fatalf("Open(%s): %v", id, err)
	}
	return v
}

// session returns the open session of the folder (the reference taken here
// is released at once; the catalog's keeps it open).
func (h *catalogTestHarness) session(t *testing.T, id string) *session.Session {
	t.Helper()
	s, release, ok := h.mgr.AcquireOpen(id)
	if !ok {
		t.Fatalf("folder %s is not open", id)
	}
	release()
	return s
}

// statsEvents returns how many library:changed "stats" events were emitted
// for the folder.
func (h *catalogTestHarness) statsEvents(id string) int {
	n := 0
	for _, e := range h.rec.Events(events.LibraryChanged) {
		if ev, ok := e.(events.LibraryChangedEvent); ok && ev.Reason == "stats" && ev.FolderID == id {
			n++
		}
	}
	return n
}

// catalogTestSeed creates root/.choix/state.db and lets fn fill it before
// any session opens the folder.
func catalogTestSeed(t *testing.T, root string, fn func(st *store.Store)) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, ".choix", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	fn(st)
}

// catalogTestFile inserts an analyzed photo row (and, when data is not nil,
// writes the original under root) and returns its id.
func catalogTestFile(t *testing.T, st *store.Store, root, rel string, data []byte) int64 {
	t.Helper()
	size := int64(1)
	if data != nil {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			t.Fatal(err)
		}
		size = int64(len(data))
	}
	id, err := st.Files().Insert(store.File{
		Path: rel, Size: size, Mtime: 1, ContentHash: "h-" + rel,
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert %s: %v", rel, err)
	}
	return id
}

// catalogTestCluster creates a cluster with the given members.
func catalogTestCluster(t *testing.T, st *store.Store, deviceKey string, bucket int64, fileIDs ...int64) int64 {
	t.Helper()
	cid, err := st.Clusters().InsertCluster(store.Cluster{
		DeviceKey:  deviceKey,
		TimeBucket: sql.NullInt64{Int64: bucket, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fid := range fileIDs {
		if err := st.Clusters().AddMember(cid, fid); err != nil {
			t.Fatal(err)
		}
	}
	return cid
}

// catalogTestCopyTree copies the regular files and directories under src
// into dst.
func catalogTestCopyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p) //nolint:gosec // test fixture
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644) //nolint:gosec // test fixture
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", src, dst, err)
	}
}

// catalogTestCopyV1 makes root a folder last scanned by v1, from the session
// package's fixture (internal/app/session/testdata/README.md): six analyzed
// photos in DCIM/100TEST, clusters 5 and 6, two picks exported under
// exports/out, thumbnails cached.
func catalogTestCopyV1(t *testing.T, root string) {
	t.Helper()
	fixture := filepath.Join("..", "session", "testdata")
	catalogTestCopyTree(t, filepath.Join(fixture, "v1-folder"), root)
	if err := os.Mkdir(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	catalogTestCopyTree(t, filepath.Join(fixture, "v1-thumbs"), filepath.Join(root, ".choix", "thumbs"))
	data, err := os.ReadFile(filepath.Join(fixture, "v1-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".choix", "state.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// catalogTestV1ID returns the id of the v1 fixture's DCIM/100TEST/TEST000n.JPG.
func catalogTestV1ID(t *testing.T, st *store.Store, n int) int64 {
	t.Helper()
	f, err := st.Files().GetByPath(fmt.Sprintf("DCIM/100TEST/TEST%04d.JPG", n))
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

// catalogTestCode returns the frontend error code of err.
func catalogTestCode(err error) string {
	if e := apperr.From(err); e != nil {
		return e.Code
	}
	return ""
}

// catalogTestPickState returns the stored state of fileID ("" when unmarked)
// and its exported path.
func catalogTestPickState(t *testing.T, st *store.Store, fileID int64) (string, sql.NullString) {
	t.Helper()
	p, err := st.Picks().Get(fileID)
	if errors.Is(err, store.ErrNotFound) {
		return "", sql.NullString{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return p.State, p.ExportedPath
}

// --- Open ---------------------------------------------------------------------

func TestOpen_ClosesPreviousFolder(t *testing.T) {
	h := newCatalogTestHarness(t)
	a, _ := h.folder(t, "a")
	b, rootB := h.folder(t, "b")

	h.open(t, a)
	if got := h.cat.OpenFolder(); got != a {
		t.Fatalf("OpenFolder = %q; want %q", got, a)
	}
	h.open(t, b)
	if got := h.cat.OpenFolder(); got != b {
		t.Fatalf("OpenFolder = %q; want %q", got, b)
	}
	if _, err := h.cat.Clusters(a); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Clusters(previous folder) = %v; want not_open", err)
	}
	// No job holds A: once its session drains, only B's access is active.
	deadline := time.Now().Add(catalogTestTimeout)
	for !slices.Equal(h.bm.Active(), []string{rootB}) {
		if time.Now().After(deadline) {
			t.Fatalf("active access = %v; want only %s", h.bm.Active(), rootB)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOpen_EnqueuesFront(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, _ := h.folder(t, "f")
	h.open(t, id)
	h.enqMu.Lock()
	defer h.enqMu.Unlock()
	if !slices.Equal(h.enqueued, []string{id}) {
		t.Fatalf("EnqueueFront calls = %v; want [%s]", h.enqueued, id)
	}
}

func TestOpen_ReportsPicksBlocked(t *testing.T) {
	h := newCatalogTestHarness(t)
	h.settings.set(func(c *config.Config) { c.PicksDir = "photos" })
	id, root := h.folder(t, "f")
	var a, b int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "photos/A.JPG", []byte("aaaa"))
		b = catalogTestFile(t, st, root, "photos/B.JPG", []byte("bbbb"))
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a, b)
	})

	v := h.open(t, id)
	if v.PicksBlocked != "has_originals" || v.PicksDir != "photos" {
		t.Fatalf("picks_blocked, picks_dir = %q, %q; want has_originals, photos", v.PicksBlocked, v.PicksDir)
	}
	_, err := h.cat.SetPick(id, a, "pick")
	if e := apperr.From(err); e == nil || e.Code != apperr.CodeInvalid || e.Reason != "has_originals" || e.Field != "picks_dir" {
		t.Fatalf("SetPick(pick) = %+v; want invalid picks_dir has_originals", e)
	}
	res, err := h.cat.SetPick(id, b, "reject")
	if err != nil || res.State != "rejected" {
		t.Fatalf("SetPick(reject) = %+v, %v; want rejected", res, err)
	}
}

func TestOpen_ReportsInvalidPicksDir(t *testing.T) {
	h := newCatalogTestHarness(t)
	h.settings.set(func(c *config.Config) { c.PicksDir = "../outside" })
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("aaaa"))
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a)
	})
	if v := h.open(t, id); v.PicksBlocked != "traversal" {
		t.Fatalf("picks_blocked = %q; want traversal", v.PicksBlocked)
	}
	_, err := h.cat.SetPick(id, a, "pick")
	if e := apperr.From(err); e == nil || e.Code != apperr.CodeInvalid || e.Field != "picks_dir" || e.Reason != "traversal" {
		t.Fatalf("SetPick(pick) = %+v; want invalid picks_dir traversal", e)
	}
}

func TestOpen_LegacyPicksDir(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "v1")
	catalogTestCopyV1(t, root)

	v := h.open(t, id)
	if v.PicksDir != "exports/out" || v.PicksBlocked != "" {
		t.Fatalf("picks_dir, picks_blocked = %q, %q; want exports/out, none", v.PicksDir, v.PicksBlocked)
	}
	st := h.session(t, id).Store
	two, one := catalogTestV1ID(t, st, 2), catalogTestV1ID(t, st, 1)

	if res, err := h.cat.SetPick(id, two, "unpick"); err != nil || res.State != "unmarked" {
		t.Fatalf("unpick = %+v, %v; want unmarked", res, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "exports", "out", "DCIM", "100TEST", "TEST0002.JPG")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unpick left the old copy: %v", err)
	}
	res, err := h.cat.SetPick(id, one, "pick")
	if err != nil || res.State != "picked" || res.ExportedPath != "exports/out/DCIM/100TEST/TEST0001.JPG" {
		t.Fatalf("pick = %+v, %v; want exported under exports/out", res, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "exports", "out", "DCIM", "100TEST", "TEST0001.JPG")); err != nil {
		t.Fatalf("new pick not under exports/out: %v", err)
	}
}

func TestOpen_ReportsMissingPicksWithoutCopying(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "v1")
	catalogTestCopyV1(t, root)
	deleted := filepath.Join(root, "exports", "out", "DCIM", "100TEST", "TEST0005.JPG")
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	v := h.open(t, id)
	if v.PicksMissing != 1 {
		t.Fatalf("picks_missing = %d; want 1", v.PicksMissing)
	}
	if _, err := os.Lstat(deleted); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("opening recreated the deleted copy: %v", err)
	}
	five := catalogTestV1ID(t, h.session(t, id).Store, 5)
	if res, err := h.cat.SetPick(id, five, "unpick"); err != nil || res.State != "unmarked" {
		t.Fatalf("unpick of the photo whose copy is missing = %+v, %v; want unmarked", res, err)
	}
	if _, err := os.Lstat(deleted); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unpick recreated the deleted copy: %v", err)
	}
}

func TestOpen_OfflineFolder(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "gone")
	h.bm.Delete(root)
	if _, err := h.cat.Open(context.Background(), id); catalogTestCode(err) != apperr.CodeOffline {
		t.Fatalf("Open = %v; want offline", err)
	}
	if got := h.cat.OpenFolder(); got != "" {
		t.Fatalf("OpenFolder = %q; want none", got)
	}
}

// TestOpen_LibraryCallbackMayClose: opening a folder whose bookmark no
// longer grants access makes the Library deliver OnOffline — which closes
// the folder in the catalog — on Open's own goroutine. Open must not hold a
// catalog lock across the session's Acquire.
func TestOpen_LibraryCallbackMayClose(t *testing.T) {
	h := newCatalogTestHarness(t)
	a, _ := h.folder(t, "a")
	b, rootB := h.folder(t, "b")
	h.open(t, a)
	h.bm.DenyAccess(rootB)

	done := make(chan error, 1)
	go func() {
		_, err := h.cat.Open(context.Background(), b)
		done <- err
	}()
	select {
	case err := <-done:
		if catalogTestCode(err) != apperr.CodeAccessLost {
			t.Fatalf("Open = %v; want access_lost", err)
		}
	case <-time.After(catalogTestTimeout):
		t.Fatal("Open deadlocked on the Library's OnOffline callback")
	}
	if got := h.cat.OpenFolder(); got != a {
		t.Fatalf("OpenFolder = %q; want %q still open", got, a)
	}
}

func TestClusters_NotOpen(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, _ := h.folder(t, "f")
	if _, err := h.cat.Clusters(id); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Clusters = %v; want not_open", err)
	}
	if _, err := h.cat.Cluster(id, 1); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Cluster = %v; want not_open", err)
	}
	if _, err := h.cat.SetPick(id, 1, "pick"); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("SetPick = %v; want not_open", err)
	}
	if _, err := h.cat.Undo(id); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Undo = %v; want not_open", err)
	}
	if err := h.cat.Recluster(context.Background(), id); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Recluster = %v; want not_open", err)
	}
	if _, err := h.cat.Ratings(id); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Ratings = %v; want not_open", err)
	}
}

// --- SetPick (ported from internal/server/picks_test.go) -------------------------

func TestSetPick_PickThenUnpick(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "Day1/IMG_0001.JPG", []byte("stub"))
	})
	h.open(t, id)
	res, err := h.cat.SetPick(id, a, "pick")
	if err != nil || res.State != "picked" || res.FileID != a || res.ExportedPath != "picks/Day1/IMG_0001.JPG" {
		t.Fatalf("pick = %+v, %v", res, err)
	}
	res, err = h.cat.SetPick(id, a, "unpick")
	if err != nil || res.State != "unmarked" || res.ExportedPath != "" {
		t.Fatalf("unpick = %+v, %v", res, err)
	}
}

func TestSetPick_RejectThenUnreject(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "Day1/IMG_0002.JPG", []byte("stub"))
	})
	h.open(t, id)
	if res, err := h.cat.SetPick(id, a, "reject"); err != nil || res.State != "rejected" {
		t.Fatalf("reject = %+v, %v", res, err)
	}
	if state, _ := catalogTestPickState(t, h.session(t, id).Store, a); state != "rejected" {
		t.Fatalf("stored state = %q; want rejected", state)
	}
	if res, err := h.cat.SetPick(id, a, "unreject"); err != nil || res.State != "unmarked" {
		t.Fatalf("unreject = %+v, %v", res, err)
	}
}

func TestSetPick_UnpickLeavesARejectAlone(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
	})
	h.open(t, id)
	if _, err := h.cat.SetPick(id, a, "reject"); err != nil {
		t.Fatal(err)
	}
	if res, err := h.cat.SetPick(id, a, "unpick"); err != nil || res.State != "rejected" {
		t.Fatalf("unpick of a rejected photo = %+v, %v; want it still rejected", res, err)
	}
	if res, err := h.cat.Undo(id); err != nil || res.State != "unmarked" {
		t.Fatalf("Undo = %+v, %v; want the reject undone (the no-op unpick is not on the stack)", res, err)
	}
}

func TestSetPick_UnknownAction(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "Day1/IMG_0003.JPG", nil)
	})
	h.open(t, id)
	_, err := h.cat.SetPick(id, a, "destroy")
	if e := apperr.From(err); e == nil || e.Code != apperr.CodeInvalid || e.Field != "action" {
		t.Fatalf("SetPick(destroy) = %+v; want invalid action", e)
	}
}

func TestSetPick_UnknownFile(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, _ := h.folder(t, "f")
	h.open(t, id)
	if _, err := h.cat.SetPick(id, 999999, "pick"); catalogTestCode(err) != apperr.CodeNotFound {
		t.Fatalf("SetPick(unknown file) = %v; want not_found", err)
	}
}

func TestSetPick_MultipleMembersOfClusterCanBePicked(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var ids []int64
	catalogTestSeed(t, root, func(st *store.Store) {
		for _, n := range []string{"A", "B", "C"} {
			ids = append(ids, catalogTestFile(t, st, root, "Day1/"+n+".JPG", []byte("stub-"+n)))
		}
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, ids...)
	})
	h.open(t, id)
	for _, f := range []int64{ids[0], ids[2]} {
		if _, err := h.cat.SetPick(id, f, "pick"); err != nil {
			t.Fatalf("pick %d: %v", f, err)
		}
	}
	v, err := h.cat.Clusters(id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Picked != 2 {
		t.Fatalf("picked = %d; want 2", v.Picked)
	}
	var picked []int64
	for _, m := range v.Clusters[0].Members {
		if m.Picked {
			picked = append(picked, m.FileID)
		}
	}
	if !slices.Equal(picked, []int64{ids[0], ids[2]}) {
		t.Fatalf("picked members = %v; want %v", picked, []int64{ids[0], ids[2]})
	}
}

func TestSetPick_CopiesIntoPicksDir(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	data := bytes.Repeat([]byte("original bytes "), 5000)
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "Day1/IMG_0001.JPG", data)
	})
	orig := filepath.Join(root, "Day1", "IMG_0001.JPG")
	past := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(orig, past, past); err != nil {
		t.Fatal(err)
	}
	h.open(t, id)

	res, err := h.cat.SetPick(id, a, "pick")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(res.ExportedPath)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("exported copy at %s: %d bytes, %v; want the original's bytes", res.ExportedPath, len(got), err)
	}
	fi, err := os.Stat(orig)
	if err != nil || !fi.ModTime().Equal(past) {
		t.Fatalf("original mtime = %v, %v; want unchanged %v", fi.ModTime(), err, past)
	}
	if now, err := os.ReadFile(orig); err != nil || !bytes.Equal(now, data) {
		t.Fatalf("original bytes changed: %v", err)
	}
}

func TestSetPick_UsesDurableCommit(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
	})
	h.open(t, id)

	var mu sync.Mutex
	var trace []string
	restore := picks.SetTraceForTest(func(event string) {
		mu.Lock()
		defer mu.Unlock()
		trace = append(trace, event)
	})
	_, err := h.cat.SetPick(id, a, "pick")
	restore()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(trace) == 0 || trace[len(trace)-1] != "tx-commit" {
		t.Fatalf("trace = %v; want it to end with tx-commit", trace)
	}
	sync := slices.Index(trace, "dir-sync")
	if sync < 0 || sync > len(trace)-2 || slices.Index(trace, "rename") > sync {
		t.Fatalf("trace = %v; want copy, rename and dir-sync before tx-commit", trace)
	}
}

func TestSetPick_FolderUnplugged(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a, b int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("aaaa"))
		b = catalogTestFile(t, st, root, "B.JPG", []byte("bbbb"))
	})
	h.open(t, id)
	if _, err := h.cat.SetPick(id, b, "reject"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"pick", "reject"} {
		if _, err := h.cat.SetPick(id, a, action); catalogTestCode(err) != apperr.CodeOffline {
			t.Fatalf("SetPick(%s) on an unplugged folder = %v; want offline", action, err)
		}
	}
	if _, err := h.cat.Undo(id); catalogTestCode(err) != apperr.CodeOffline {
		t.Fatalf("Undo on an unplugged folder = %v; want offline", err)
	}
}

func TestSetPick_ConcurrentMutationsStayConsistent(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	ids := make([]int64, 5)
	data := map[int64][]byte{}
	catalogTestSeed(t, root, func(st *store.Store) {
		for i := range ids {
			b := bytes.Repeat([]byte{byte('a' + i)}, 1000+i)
			ids[i] = catalogTestFile(t, st, root, fmt.Sprintf("Day1/IMG_%04d.JPG", i), b)
			data[ids[i]] = b
		}
	})
	h.open(t, id)

	const goroutines, ops = 50, 6
	actions := []string{"pick", "unpick", "reject", "unreject", "undo"}
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*ops)
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(g), 7)) //nolint:gosec // test randomness
			for range ops {
				action := actions[r.IntN(len(actions))]
				fileID := ids[r.IntN(len(ids))]
				var err error
				if action == "undo" {
					_, err = h.cat.Undo(id)
					if catalogTestCode(err) == apperr.CodeNotFound {
						err = nil
					}
				} else {
					_, err = h.cat.SetPick(id, fileID, action)
				}
				if err != nil {
					errs <- fmt.Errorf("%s %d: %w", action, fileID, err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	st := h.session(t, id).Store
	exported := map[string]bool{}
	for _, f := range ids {
		state, exp := catalogTestPickState(t, st, f)
		if state != "picked" {
			if exp.Valid {
				t.Errorf("file %d is %q with exported_path %q; want NULL", f, state, exp.String)
			}
			continue
		}
		if !exp.Valid {
			t.Errorf("picked file %d has no exported_path", f)
			continue
		}
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(exp.String)))
		if err != nil || !bytes.Equal(got, data[f]) {
			t.Errorf("copy of picked file %d at %s: %v; want the original's bytes", f, exp.String, err)
		}
		exported[strings.ToLower(exp.String)] = true
	}
	err := filepath.WalkDir(filepath.Join(root, "picks"), func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if !exported[strings.ToLower(filepath.ToSlash(rel))] {
			t.Errorf("%s is under the picks folder but no picked row records it", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --- Close ----------------------------------------------------------------------

func TestClose_WaitsForInFlightPick(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
	})
	h.open(t, id)
	s := h.session(t, id)

	entered, unblock := make(chan struct{}), make(chan struct{})
	restore := safefs.SetHooksForTest(safefs.Hooks{BeforeRename: func() {
		close(entered)
		<-unblock
	}})
	defer restore()

	type result struct {
		res PickResult
		err error
	}
	picked := make(chan result, 1)
	go func() {
		res, err := h.cat.SetPick(id, a, "pick")
		picked <- result{res, err}
	}()
	<-entered // the copy is written and held before its rename

	closed := make(chan struct{})
	go func() {
		h.cat.Close(id)
		close(closed)
	}()
	h.waitHook(t, hookCloseWait, id, 1)
	select {
	case <-closed:
		t.Fatal("Close returned while a pick was in flight")
	default:
	}
	var n int
	if err := s.Store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		t.Fatalf("the store was closed while a pick was in flight: %v", err)
	}

	close(unblock)
	var r result
	select {
	case r = <-picked:
	case <-time.After(catalogTestTimeout):
		t.Fatal("the pick never finished")
	}
	if r.err != nil || r.res.State != "picked" {
		t.Fatalf("in-flight pick = %+v, %v; want picked", r.res, r.err)
	}
	select {
	case <-closed:
	case <-time.After(catalogTestTimeout):
		t.Fatal("Close never returned")
	}
	if h.cat.OpenFolder() != "" {
		t.Fatalf("OpenFolder = %q after Close; want none", h.cat.OpenFolder())
	}

	// The pick was committed: a new session reads it.
	s2, release, err := h.mgr.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if state, exp := catalogTestPickState(t, s2.Store, a); state != "picked" || exp.String != "picks/A.JPG" {
		t.Fatalf("after Close the pick is %q at %q; want picked at picks/A.JPG", state, exp.String)
	}
}

// TestDetach_CloseSparesReopenedInstance: Detach lets go of the open folder
// at once; the folder is opened again before the detached instance's close
// runs, and that close leaves the new instance open and usable (its pick
// works, its undo stack is its own). The close releases the detached
// instance's reference, and a CloseAll waits for a close not yet run.
func TestDetach_CloseSparesReopenedInstance(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a)
	})
	if h.cat.Detach(id) != nil {
		t.Fatal("Detach of a folder that is not open returned a close")
	}
	h.open(t, id)

	closeOld := h.cat.Detach(id)
	if closeOld == nil {
		t.Fatal("Detach of the open folder returned no close")
	}
	if got := h.cat.OpenFolder(); got != "" {
		t.Fatalf("OpenFolder after Detach = %q; want none", got)
	}
	if _, err := h.cat.Clusters(id); catalogTestCode(err) != apperr.CodeNotOpen {
		t.Fatalf("Clusters after Detach = %v; want not_open", err)
	}

	h.open(t, id) // reopened before the detached instance's close has run
	closeOld()
	closeOld() // a second call does nothing

	if got := h.cat.OpenFolder(); got != id {
		t.Fatalf("OpenFolder after the old instance closed = %q; want %q", got, id)
	}
	if _, err := h.cat.Clusters(id); err != nil {
		t.Fatalf("Clusters of the reopened folder = %v; want it still open", err)
	}
	if res, err := h.cat.SetPick(id, a, "pick"); err != nil || res.State != "picked" {
		t.Fatalf("SetPick on the reopened folder = %+v, %v; want picked", res, err)
	}
	if res, err := h.cat.Undo(id); err != nil || res.State != "unmarked" {
		t.Fatalf("Undo on the reopened folder = %+v, %v; want unmarked", res, err)
	}

	// A detached close not yet run holds up CloseAll until it runs.
	closeNew := h.cat.Detach(id)
	if closeNew == nil {
		t.Fatal("Detach of the reopened folder returned no close")
	}
	all := make(chan struct{})
	go func() {
		h.cat.CloseAll()
		close(all)
	}()
	h.waitHook(t, hookCloseAllWait, "", 1)
	select {
	case <-all:
		t.Fatal("CloseAll returned before the detached close ran")
	case <-time.After(catalogTestOverlapWindow):
	}
	closeNew()
	select {
	case <-all:
	case <-time.After(catalogTestTimeout):
		t.Fatal("CloseAll never returned")
	}
}

// TestOpen_OfflineBetweenAcquireAndInstall: the folder moves while Open
// holds its session and has not installed it yet; the Library's
// notification (OnOffline, the folder being in use) finds nothing open to
// detach. Open must not install the session it acquired before the move:
// it answers offline and keeps no reference, so the folder's next Open —
// at its new location — opens a fresh session instead of answering busy.
func TestOpen_OfflineBetweenAcquireAndInstall(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "trip")
	moved := filepath.Join(h.parent, "trip moved")
	var once sync.Once
	h.cat.hook = func(event, folderID string) {
		h.recordHook(event, folderID)
		if event != hookOpenAcquired || folderID != id {
			return
		}
		once.Do(func() {
			if err := os.Rename(root, moved); err != nil {
				t.Errorf("move the folder: %v", err)
				return
			}
			h.bm.Move(root, moved)
			if _, err := h.lib.CheckFolder(context.Background(), id); err != nil {
				t.Errorf("CheckFolder: %v", err)
			}
		})
	}

	if _, err := h.cat.Open(context.Background(), id); catalogTestCode(err) != apperr.CodeOffline {
		t.Fatalf("Open across the move = %v; want offline", err)
	}
	if got := h.cat.OpenFolder(); got != "" {
		t.Fatalf("OpenFolder after the refused Open = %q; want none", got)
	}
	// No reference is kept: the session acquired before the move closes and
	// its access is released.
	deadline := time.Now().Add(catalogTestTimeout)
	for len(h.bm.Active()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("access still active after the refused Open: %v", h.bm.Active())
		}
		time.Sleep(time.Millisecond)
	}

	v := h.open(t, id)
	if v.Folder != "trip moved" {
		t.Fatalf("reopened folder name = %q; want the new location's", v.Folder)
	}
	if got := h.bm.Active(); !slices.Equal(got, []string{moved}) {
		t.Fatalf("active access after the reopen = %v; want only %s", got, moved)
	}
}

// catalogTestOverlapWindow is how long a test gives a mutation that must be
// waiting to (wrongly) finish. A correct catalog never finishes it, so the
// window only delays the test; it cannot make it flaky.
const catalogTestOverlapWindow = 100 * time.Millisecond

// TestSetPick_SerialisedAcrossReopen: an unpick of the previous instance of
// a folder, paused after its commit and before it removes the copy, must
// finish before a pick made after Close and Open runs — otherwise the pick
// reuses the identical copy and the unpick then deletes it.
func TestSetPick_SerialisedAcrossReopen(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	data := []byte("the original")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", data)
	})
	h.open(t, id)
	if _, err := h.cat.SetPick(id, a, "pick"); err != nil {
		t.Fatal(err)
	}

	var (
		mu       sync.Mutex
		removing bool // the unpick is held before its removal
		overlap  = make(chan struct{})
		once     sync.Once
	)
	held, unblock := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	t.Cleanup(release)
	restore := picks.SetTraceForTest(func(event string) {
		mu.Lock()
		first := event == "remove" && !removing
		if first {
			removing = true
		} else if removing && event == "tx-commit" {
			once.Do(func() { close(overlap) }) // a commit beside the paused unpick
		}
		mu.Unlock()
		if first {
			close(held)
			<-unblock
		}
	})
	defer restore()

	unpicked := make(chan error, 1)
	go func() {
		_, err := h.cat.SetPick(id, a, "unpick")
		unpicked <- err
	}()
	<-held

	closed := make(chan struct{})
	go func() {
		h.cat.Close(id)
		close(closed)
	}()
	h.waitHook(t, hookCloseWait, id, 1)
	h.open(t, id)

	type result struct {
		res PickResult
		err error
	}
	picked := make(chan result, 1)
	go func() {
		res, err := h.cat.SetPick(id, a, "pick")
		picked <- result{res, err}
	}()
	h.waitHook(t, hookMutateWait, id, 3) // pick, unpick, then this pick
	select {
	case <-overlap:
		t.Fatal("a pick committed while the previous instance's unpick was in flight")
	case <-time.After(catalogTestOverlapWindow):
	}
	release()

	if err := <-unpicked; err != nil {
		t.Fatalf("unpick: %v", err)
	}
	r := <-picked
	if r.err != nil || r.res.State != "picked" {
		t.Fatalf("pick after reopening = %+v, %v; want picked", r.res, r.err)
	}
	<-closed
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(r.res.ExportedPath)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the pick's copy at %s: %v; want the original's bytes", r.res.ExportedPath, err)
	}
	if state, exp := catalogTestPickState(t, h.session(t, id).Store, a); state != "picked" || exp.String != r.res.ExportedPath {
		t.Fatalf("stored pick = %q at %q; want picked at %s", state, exp.String, r.res.ExportedPath)
	}
}

// TestCloseAll_WaitsForPendingClose: a shutdown that arrives while a Close
// is waiting for an in-flight pick returns only after that Close has
// finished — the pick committed and the Library statistics refreshed.
func TestCloseAll_WaitsForPendingClose(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
	})
	h.open(t, id)

	entered, unblock := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	t.Cleanup(release)
	restore := safefs.SetHooksForTest(safefs.Hooks{BeforeRename: func() {
		close(entered)
		<-unblock
	}})
	defer restore()

	picked := make(chan error, 1)
	go func() {
		_, err := h.cat.SetPick(id, a, "pick")
		picked <- err
	}()
	<-entered

	closed := make(chan struct{})
	go func() {
		h.cat.Close(id)
		close(closed)
	}()
	h.waitHook(t, hookCloseWait, id, 1)

	all := make(chan struct{})
	go func() {
		h.cat.CloseAll()
		close(all)
	}()
	h.waitHook(t, hookCloseAllWait, "", 1)
	select {
	case <-all:
		t.Fatal("CloseAll returned while a Close was waiting for a pick")
	case <-time.After(catalogTestOverlapWindow):
	}
	release()

	select {
	case <-all:
	case <-time.After(catalogTestTimeout):
		t.Fatal("CloseAll never returned")
	}
	// The pending Close's refresh — run after the pick committed — is
	// stored by the time CloseAll returns.
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stats.Picked != 1 {
		t.Fatalf("library row picked = %d when CloseAll returned; want the Close's refresh stored", f.Stats.Picked)
	}
	if err := <-picked; err != nil {
		t.Fatalf("pick: %v", err)
	}
	<-closed
}

// --- Recluster and Ratings ------------------------------------------------------

func TestRecluster_EmitsFolderChanged(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	catalogTestSeed(t, root, func(st *store.Store) {
		for _, n := range []string{"A", "B"} {
			f := catalogTestFile(t, st, root, n+".JPG", nil)
			if err := st.Files().UpdateMetadata(store.File{
				ID: f, DeviceKey: sql.NullString{String: "X-T5#A1", Valid: true},
				CapturedAt: sql.NullInt64{Int64: 1714185600, Valid: true},
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.Files().UpdateStatus(f, "analyzed", ""); err != nil {
				t.Fatal(err)
			}
		}
	})
	h.open(t, id)
	if err := h.cat.Recluster(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	want := events.FolderChangedEvent{FolderID: id, Reason: "clusters"}
	if got := h.rec.Events(events.FolderChanged); len(got) != 1 || got[0] != want {
		t.Fatalf("folder:changed = %v; want [%+v]", got, want)
	}
	v, err := h.cat.Clusters(id)
	if err != nil || len(v.Clusters) != 1 || len(v.Clusters[0].Members) != 2 {
		t.Fatalf("clusters after Recluster = %+v, %v; want one cluster of two", v.Clusters, err)
	}
}

func TestRatings_OffIsEmpty(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", nil)
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a)
		catalogTestRate(t, st, a, "h-A.JPG")
	})
	h.open(t, id)

	got, err := h.cat.Ratings(id)
	if err != nil || len(got) != 1 {
		t.Fatalf("Ratings (on) = %v, %v; want one rating", got, err)
	}
	if _, ok := got[fmt.Sprint(a)]; !ok {
		t.Fatalf("Ratings (on) = %v; want a key %d", got, a)
	}

	h.settings.set(func(c *config.Config) { c.QualityRating = false })
	got, err = h.cat.Ratings(id)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Ratings (off) = %#v, %v; want an empty map", got, err)
	}
}

// catalogTestRate gives fileID a current quality rating.
func catalogTestRate(t *testing.T, st *store.Store, fileID int64, hash string) {
	t.Helper()
	ok, err := st.Files().SetQuality(fileID, hash, sql.NullFloat64{Float64: 0.6, Valid: true}, local.QualityScorerID)
	if err != nil || !ok {
		t.Fatalf("SetQuality(%d) = %v, %v", fileID, ok, err)
	}
}
