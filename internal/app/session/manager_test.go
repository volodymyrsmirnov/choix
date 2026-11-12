package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// sessionTestTimeout bounds every wait for something that must happen.
const sessionTestTimeout = 10 * time.Second

// sessionTestV1Mtime is the modification time the v1 fixture's generator
// (testdata/_make_v1_state.go, fixedMtime) gave every file of v1-folder.
var sessionTestV1Mtime = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

// sessionTestSettings is a mutable config.Config read through get.
type sessionTestSettings struct {
	mu  sync.Mutex
	cfg config.Config
}

func (s *sessionTestSettings) get() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *sessionTestSettings) set(fn func(*config.Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
}

// sessionTestEmbedder counts its calls and returns a fixed unit vector.
type sessionTestEmbedder struct{ calls atomic.Int64 }

func (e *sessionTestEmbedder) Embed(image.Image) ([]float32, error) {
	e.calls.Add(1)
	v := make([]float32, 512)
	v[0] = 1
	return v, nil
}

// sessionTestEvents records the Manager's drain stages.
type sessionTestEvents struct {
	mu     sync.Mutex
	seen   []string
	notify chan struct{}
}

func (e *sessionTestEvents) record(folderID, stage string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, folderID+":"+stage)
	if e.notify != nil {
		close(e.notify)
	}
	e.notify = make(chan struct{})
}

// waitFor waits until folderID has reached stage at least n times.
func (e *sessionTestEvents) waitFor(t *testing.T, folderID, stage string, n int) {
	t.Helper()
	deadline := time.After(sessionTestTimeout)
	for {
		e.mu.Lock()
		count := 0
		for _, s := range e.seen {
			if s == folderID+":"+stage {
				count++
			}
		}
		if e.notify == nil {
			e.notify = make(chan struct{})
		}
		ch := e.notify
		e.mu.Unlock()
		if count >= n {
			return
		}
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("folder %s never reached %q %d times", folderID, stage, n)
		}
	}
}

// sessionTestHarness is a Manager over a real Library with the platform
// fakes.
type sessionTestHarness struct {
	lib      *library.Library
	bm       *platformtest.Bookmarks
	stills   *platformtest.Stills
	settings *sessionTestSettings
	mgr      *Manager
	events   *sessionTestEvents
	parent   string // a resolved temporary directory holding the folders
}

type sessionTestOptions struct {
	stills   *platformtest.Stills
	video    *platformtest.Video
	embedder local.Embedder
}

func newSessionTestHarness(t *testing.T, o sessionTestOptions) *sessionTestHarness {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := library.OpenDB(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	bm := &platformtest.Bookmarks{}
	vols := &platformtest.Volumes{}
	vols.Add("/", platform.VolumeInfo{UUID: "UUID-TEST", Name: "Test HD"})
	lib := library.New(library.Options{DB: db, Bookmarks: bm, Volumes: vols})

	stills := o.stills
	if stills == nil {
		stills = &platformtest.Stills{ReadPaths: true}
	}
	video := o.video
	if video == nil {
		video = &platformtest.Video{}
	}
	h := &sessionTestHarness{
		lib:      lib,
		bm:       bm,
		stills:   stills,
		settings: &sessionTestSettings{cfg: config.Defaults()},
		events:   &sessionTestEvents{},
		parent:   parent,
	}
	h.mgr = NewManager(Deps{
		Library:  lib,
		Settings: h.settings.get,
		Stills:   stills,
		Video:    video,
		Embedder: o.embedder,
	})
	h.mgr.hook = h.events.record
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), sessionTestTimeout)
		defer cancel()
		if n := h.mgr.Shutdown(ctx); n != 0 {
			t.Errorf("cleanup: Shutdown left %d sessions open", n)
		}
	})
	return h
}

// folder creates the folder name under the harness's parent, adds it to the
// Library and returns its id and root.
func (h *sessionTestHarness) folder(t *testing.T, name string) (id, root string) {
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

// acquire opens the folder's session, failing the test on error.
func (h *sessionTestHarness) acquire(t *testing.T, id string) (*Session, func()) {
	t.Helper()
	s, release, err := h.mgr.Acquire(id)
	if err != nil {
		t.Fatalf("Acquire(%s): %v", id, err)
	}
	return s, release
}

// assertNoAccess fails when any security-scoped access is still active.
func (h *sessionTestHarness) assertNoAccess(t *testing.T) {
	t.Helper()
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("bookmark access still active for %v", got)
	}
}

// sessionTestWaitClosed waits until s has been closed.
func sessionTestWaitClosed(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(sessionTestTimeout):
		t.Fatalf("session %s was never closed", s.FolderID)
	}
}

// sessionTestIsClosed reports whether s has been closed already.
func sessionTestIsClosed(s *Session) bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// sessionTestStoreOpen reports whether the session's store still answers.
func sessionTestStoreOpen(s *Session) bool {
	var n int
	return s.Store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM files`).Scan(&n) == nil
}

// sessionTestHoldDetachedCall starts a Properties call of stills (whose Gate
// must be set) under s.Context, waits until it is counted, then abandons it:
// the Go caller returns while the "native" call stays held by the Gate.
func sessionTestHoldDetachedCall(t *testing.T, s *Session, stills *platformtest.Stills) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := stills.Properties(s.Context(ctx), platform.ImageInput{Path: "/held.jpg"})
		done <- err
	}()
	deadline := time.Now().Add(sessionTestTimeout)
	for s.inflight.Wait(0) {
		if time.Now().After(deadline) {
			t.Fatal("the detached call was never counted")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned call returned %v; want context.Canceled", err)
		}
	case <-time.After(sessionTestTimeout):
		t.Fatal("the abandoned call did not return")
	}
}

// sessionTestCopyTree copies the regular files and directories under src
// into dst.
func sessionTestCopyTree(t *testing.T, src, dst string) {
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

// sessionTestCopyV1 makes root a folder last scanned by v1 (see
// testdata/README.md): the fixture's originals and exported copies, its
// thumbnail cache in .choix/thumbs and its state.db in .choix, every file
// with the modification time v1 left.
func sessionTestCopyV1(t *testing.T, root string) {
	t.Helper()
	sessionTestCopyTree(t, filepath.Join("testdata", "v1-folder"), root)
	if err := os.Mkdir(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	sessionTestCopyTree(t, filepath.Join("testdata", "v1-thumbs"), filepath.Join(root, ".choix", "thumbs"))
	data, err := os.ReadFile(filepath.Join("testdata", "v1-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".choix", "state.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(p, sessionTestV1Mtime, sessionTestV1Mtime)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// sessionTestV1State is what must not change in a v1 folder: its cache
// files (content hash and modification time, by path under .choix/thumbs),
// each file's status and raw_exif, the thumbs rows and the clusters.
type sessionTestV1State struct {
	cache    map[string]string
	files    map[int64]string // id → scan_status + "|" + hex sha256 of raw_exif
	thumbs   []string         // "file_id|tier|rel_path|width|height"
	clusters []string         // "id|device|bucket|members", ordered by id
	members  []string         // the clusters' member lists alone, sorted
}

// sessionTestSnapshotV1 reads s's v1 state, checking that every cached tier
// decodes as a 64×48 JPEG.
func sessionTestSnapshotV1(t *testing.T, s *Session) sessionTestV1State {
	t.Helper()
	ctx := context.Background()
	st := sessionTestV1State{cache: map[string]string{}, files: map[int64]string{}}
	thumbsDir := filepath.Join(s.Root, ".choix", "thumbs")
	err := filepath.WalkDir(thumbsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // test file
		if err != nil {
			return err
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != 64 || cfg.Height != 48 {
			t.Fatalf("cached tier %s unreadable: %v, %v", p, cfg, err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(thumbsDir, p)
		if err != nil {
			return err
		}
		st.cache[filepath.ToSlash(rel)] = fmt.Sprintf("%x|%d", sha256.Sum256(data), fi.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.cache) != 12 {
		t.Fatalf("cache files = %d; want 12 (two tiers of six photos)", len(st.cache))
	}

	query := func(q string, scan func(*sql.Rows) error) {
		t.Helper()
		rows, err := s.Store.DB().QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if err := scan(rows); err != nil {
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	query(`SELECT id, scan_status, raw_exif FROM files`, func(r *sql.Rows) error {
		var (
			id     int64
			status string
			raw    []byte
		)
		if err := r.Scan(&id, &status, &raw); err != nil {
			return err
		}
		st.files[id] = fmt.Sprintf("%s|%x", status, sha256.Sum256(raw))
		return nil
	})
	query(`SELECT file_id, tier, rel_path, width, height FROM thumbs ORDER BY file_id, tier`, func(r *sql.Rows) error {
		var (
			id, w, h  int64
			tier, rel string
		)
		if err := r.Scan(&id, &tier, &rel, &w, &h); err != nil {
			return err
		}
		st.thumbs = append(st.thumbs, fmt.Sprintf("%d|%s|%s|%d|%d", id, tier, rel, w, h))
		return nil
	})
	memberIDs := map[int64][]int64{}
	query(`SELECT cluster_id, file_id FROM cluster_members ORDER BY cluster_id, file_id`, func(r *sql.Rows) error {
		var cid, fid int64
		if err := r.Scan(&cid, &fid); err != nil {
			return err
		}
		memberIDs[cid] = append(memberIDs[cid], fid)
		return nil
	})
	query(`SELECT id, device_key, time_bucket FROM clusters ORDER BY id`, func(r *sql.Rows) error {
		var (
			id, bucket int64
			device     string
		)
		if err := r.Scan(&id, &device, &bucket); err != nil {
			return err
		}
		members := fmt.Sprint(memberIDs[id])
		st.clusters = append(st.clusters, fmt.Sprintf("%d|%s|%d|%s", id, device, bucket, members))
		st.members = append(st.members, members)
		return nil
	})
	slices.Sort(st.members)
	return st
}

// sessionTestAssertSameV1 fails when after differs from before; with
// sameClusterIDs false only the clusters' membership is compared.
func sessionTestAssertSameV1(t *testing.T, when string, before, after sessionTestV1State, sameClusterIDs bool) {
	t.Helper()
	if !maps.Equal(before.cache, after.cache) {
		t.Fatalf("%s: the thumbnail cache changed:\nbefore %v\nafter  %v", when, before.cache, after.cache)
	}
	if !maps.Equal(before.files, after.files) {
		t.Fatalf("%s: file statuses or raw_exif changed:\nbefore %v\nafter  %v", when, before.files, after.files)
	}
	if !slices.Equal(before.thumbs, after.thumbs) {
		t.Fatalf("%s: thumbs rows changed:\nbefore %v\nafter  %v", when, before.thumbs, after.thumbs)
	}
	if sameClusterIDs && !slices.Equal(before.clusters, after.clusters) {
		t.Fatalf("%s: cluster rows changed:\nbefore %v\nafter  %v", when, before.clusters, after.clusters)
	}
	if !slices.Equal(before.members, after.members) {
		t.Fatalf("%s: cluster membership changed:\nbefore %v\nafter  %v", when, before.members, after.members)
	}
}

func TestAcquire_SharesOneSession(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "trip")

	s1, release1 := h.acquire(t, id)
	s2, release2 := h.acquire(t, id)
	if s1 != s2 {
		t.Fatal("two Acquires of one folder returned different sessions")
	}
	if s1.FolderID != id || s1.Root != root || s1.Store == nil {
		t.Fatalf("session = {%q, %q, %v}; want {%q, %q, store}", s1.FolderID, s1.Root, s1.Store, id, root)
	}
	if got := h.bm.Active(); !slices.Equal(got, []string{root}) {
		t.Fatalf("active access = %v; want one lease on %s", got, root)
	}

	release1()
	release1() // idempotent: must not drop the second reference
	if sessionTestIsClosed(s1) || !sessionTestStoreOpen(s1) {
		t.Fatal("the store closed while a reference was still held")
	}
	if len(h.bm.Active()) != 1 {
		t.Fatal("bookmark access stopped while a reference was still held")
	}

	release2()
	sessionTestWaitClosed(t, s1)
	if sessionTestStoreOpen(s1) {
		t.Fatal("the store still answers after the last release")
	}
	h.assertNoAccess(t)
}

func TestAcquire_CreatesChoixDir(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "fresh")

	_, release := h.acquire(t, id)
	defer release()
	fi, err := os.Lstat(filepath.Join(root, ".choix"))
	if err != nil || !fi.IsDir() {
		t.Fatalf(".choix = %v, %v; want a directory", fi, err)
	}
	fi, err = os.Lstat(filepath.Join(root, ".choix", "state.db"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("state.db = %v, %v; want a regular file", fi, err)
	}
}

func TestAcquire_ReadOnlyFolder(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "locked")
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	s, _, err := h.mgr.Acquire(id)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Acquire = %v, %v; want ErrReadOnly", s, err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf(".choix exists in a read-only folder: %v", err)
	}
	h.assertNoAccess(t)
}

func TestAcquire_SymlinkedChoixDirRefused(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "linked")
	target := filepath.Join(t.TempDir(), "x")
	if err := os.Symlink(target, filepath.Join(root, ".choix")); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.mgr.Acquire(id)
	if !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("Acquire err = %v; want ErrUnsafeState", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("something was created at the link target: %v", err)
	}
	h.assertNoAccess(t)
}

func TestAcquire_SymlinkedDatabaseRefused(t *testing.T) {
	for _, name := range []string{"state.db", "state.db-wal"} {
		t.Run(name, func(t *testing.T) {
			h := newSessionTestHarness(t, sessionTestOptions{})
			id, root := h.folder(t, "folder")
			if err := os.Mkdir(filepath.Join(root, ".choix"), 0o750); err != nil {
				t.Fatal(err)
			}
			elsewhere := filepath.Join(h.parent, "elsewhere.db")
			content := []byte("not a database Choix may touch")
			if err := os.WriteFile(elsewhere, content, 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			if err := os.Chtimes(elsewhere, old, old); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../elsewhere.db", filepath.Join(root, ".choix", name)); err != nil {
				t.Fatal(err)
			}

			_, _, err := h.mgr.Acquire(id)
			if !errors.Is(err, ErrUnsafeState) {
				t.Fatalf("Acquire err = %v; want ErrUnsafeState", err)
			}
			got, err := os.ReadFile(elsewhere) //nolint:gosec // test file
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("link target = %q, %v; want it unchanged", got, err)
			}
			fi, err := os.Stat(elsewhere)
			if err != nil || !fi.ModTime().Equal(old) {
				t.Fatalf("link target modified: %v, %v", fi, err)
			}
			entries, err := os.ReadDir(h.parent)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			if !slices.Equal(names, []string{"elsewhere.db", "folder"}) {
				t.Fatalf("entries beside the folder = %v; want [elsewhere.db folder]", names)
			}
			h.assertNoAccess(t)
		})
	}
}

func TestAcquire_ChoixSwappedBetweenSessions(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "swapped")
	s, release := h.acquire(t, id)
	release()
	sessionTestWaitClosed(t, s)

	choix := filepath.Join(root, ".choix")
	copyDir := filepath.Join(h.parent, "choix-copy")
	sessionTestCopyTree(t, choix, copyDir)
	if err := os.RemoveAll(choix); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(copyDir, choix); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.mgr.Acquire(id)
	if !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("Acquire err = %v; want ErrUnsafeState", err)
	}
	h.assertNoAccess(t)
}

func TestAcquire_FolderNameWithURICharacters(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	const name = "a #1?b%41"
	id, root := h.folder(t, name)

	s, release := h.acquire(t, id)
	fi, err := os.Lstat(filepath.Join(root, ".choix", "state.db"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("state.db inside the folder's .choix = %v, %v; want a regular file", fi, err)
	}
	entries, err := os.ReadDir(h.parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the folder's parent holds %q; want only %q", names, name)
	}
	if err := s.Store.KV().Set("probe", "kept"); err != nil {
		t.Fatal(err)
	}
	release()
	sessionTestWaitClosed(t, s)

	s2, release2 := h.acquire(t, id)
	defer release2()
	if s2 == s {
		t.Fatal("a closed session was handed out again")
	}
	if v, err := s2.Store.KV().Get("probe"); err != nil || v != "kept" {
		t.Fatalf("second open reads %q, %v; want the same database", v, err)
	}
}

func TestAcquire_ReconcileCopiesNothing(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "v1")
	sessionTestCopyV1(t, root)
	deleted := filepath.Join(root, "exports", "out", "DCIM", "100TEST", "TEST0005.JPG")
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	s, release := h.acquire(t, id)
	defer release()
	if _, err := os.Lstat(deleted); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("opening recreated the deleted copy: %v", err)
	}
	if got := s.PicksMissing(); got != 1 {
		t.Fatalf("PicksMissing = %d; want 1", got)
	}
	p, err := s.Store.Picks().Get(5)
	if err != nil || p.State != "picked" || p.ExportedPath.String != "exports/out/DCIM/100TEST/TEST0005.JPG" {
		t.Fatalf("pick of file 5 = %+v, %v; want it left as it was", p, err)
	}
}

// TestAcquire_SweepsInterruptedPickCopies: opening a session removes what
// an interrupted pick copy left in the effective picks directory — here
// v1's exports/out, deep in its mirrored tree — and nothing else: a user's
// own "*.tmp", the copies, and a temporary-looking file outside the picks
// directory stay.
func TestAcquire_SweepsInterruptedPickCopies(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "v1")
	sessionTestCopyV1(t, root)
	write := func(rel, data string) string {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
		return p
	}
	leftover := write("exports/out/DCIM/100TEST/TEST0006.JPG.0123abcd.tmp", "partial")
	suffixed := write("exports/out/DCIM/100TEST/TEST0003_2.JPG.89abcdef.tmp", "partial")
	userTmp := write("exports/out/notes.tmp", "mine")
	// The temporary form alone does not make a file Choix's: its base is no
	// name Choix would export there.
	userLookalike := write("exports/out/notes.0123abcd.tmp", "mine")
	userDeep := write("exports/out/DCIM/100TEST/draft.0123abcd.tmp", "mine")
	elsewhere := write("DCIM/100TEST/TEST0001.JPG.0123abcd.tmp", "not in the picks directory")
	copied := filepath.Join(root, "exports", "out", "DCIM", "100TEST", "TEST0002.JPG")

	_, release := h.acquire(t, id)
	defer release()
	for _, p := range []string{leftover, suffixed} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the interrupted copy's temporary %s is still there: %v", p, err)
		}
	}
	for _, p := range []string{userTmp, userLookalike, userDeep, elsewhere, copied} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("%s was removed: %v", p, err)
		}
	}
}

// TestAcquire_SymlinkedPicksDirNotSwept: a picks directory that is a
// symlink is not followed by the open-time sweep; the session still opens.
func TestAcquire_SymlinkedPicksDirNotSwept(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "trip")
	outside := filepath.Join(h.parent, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "IMG_0001.JPG.0123abcd.tmp")
	if err := os.WriteFile(victim, []byte("elsewhere"), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "picks")); err != nil {
		t.Fatal(err)
	}

	_, release := h.acquire(t, id)
	defer release()
	if _, err := os.Lstat(victim); err != nil {
		t.Fatalf("the sweep followed the symlinked picks directory: %v", err)
	}
}

func TestAcquire_OfflineFolder(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "gone")
	h.bm.Delete(root)

	s, _, err := h.mgr.Acquire(id)
	if !errors.Is(err, library.ErrOffline) {
		t.Fatalf("Acquire = %v, %v; want library.ErrOffline", s, err)
	}
	h.assertNoAccess(t)
}

func TestAcquireOpen_DoesNotOpen(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "closed")

	if s, _, ok := h.mgr.AcquireOpen(id); ok || s != nil {
		t.Fatalf("AcquireOpen of a folder never opened = %v, %v; want nil, false", s, ok)
	}
	h.assertNoAccess(t)
	if _, err := os.Lstat(filepath.Join(root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("AcquireOpen created .choix: %v", err)
	}

	s, release := h.acquire(t, id)
	got, releaseOpen, ok := h.mgr.AcquireOpen(id)
	if !ok || got != s {
		t.Fatalf("AcquireOpen of an open folder = %v, %v; want the open session", got, ok)
	}
	release()
	if sessionTestIsClosed(s) || !sessionTestStoreOpen(s) {
		t.Fatal("the session closed while AcquireOpen's reference was held")
	}
	releaseOpen()
	sessionTestWaitClosed(t, s)
	h.assertNoAccess(t)

	if s, _, ok := h.mgr.AcquireOpen(id); ok || s != nil {
		t.Fatalf("AcquireOpen after the last release = %v, %v; want nil, false", s, ok)
	}
}

func TestRelease_WaitsForDetachedCalls(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "busy")
	s, release := h.acquire(t, id)
	sessionTestHoldDetachedCall(t, s, stills)

	release()
	h.events.waitFor(t, id, managerStageWaiting, 1)
	if got := h.bm.Active(); !slices.Equal(got, []string{root}) {
		t.Fatalf("access = %v after the last release; want it kept while a detached call runs", got)
	}
	if sessionTestIsClosed(s) || !sessionTestStoreOpen(s) {
		t.Fatal("the store closed while a detached call was running")
	}

	close(stills.Gate)
	sessionTestWaitClosed(t, s)
	h.assertNoAccess(t)
	if sessionTestStoreOpen(s) {
		t.Fatal("the store still answers once the detached call returned")
	}
}

func TestShutdown_ClosesIdleSessions(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	idA, _ := h.folder(t, "a")
	idB, _ := h.folder(t, "b")
	sA, releaseA := h.acquire(t, idA)
	sB, releaseB := h.acquire(t, idB)
	releaseA()
	releaseB()

	ctx, cancel := context.WithTimeout(context.Background(), sessionTestTimeout)
	defer cancel()
	if n := h.mgr.Shutdown(ctx); n != 0 {
		t.Fatalf("Shutdown = %d; want 0", n)
	}
	if !sessionTestIsClosed(sA) || !sessionTestIsClosed(sB) {
		t.Fatal("Shutdown returned before closing the idle sessions")
	}
	h.assertNoAccess(t)
}

func TestShutdown_LeavesBusySessionOpen(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	idA, rootA := h.folder(t, "held")
	idB, rootB := h.folder(t, "detached")

	sA, releaseA := h.acquire(t, idA)
	sB, releaseB := h.acquire(t, idB)
	sessionTestHoldDetachedCall(t, sB, stills)
	releaseB()
	t.Cleanup(func() {
		close(stills.Gate)
		releaseA()
		sessionTestWaitClosed(t, sA)
		sessionTestWaitClosed(t, sB)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	n := h.mgr.Shutdown(ctx)
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("Shutdown took %v; want under 1 s", elapsed)
	}
	if n != 2 {
		t.Fatalf("Shutdown = %d; want 2", n)
	}
	if !sessionTestStoreOpen(sA) || !sessionTestStoreOpen(sB) {
		t.Fatal("a store in use was closed by Shutdown")
	}
	want := []string{rootA, rootB}
	slices.Sort(want)
	if got := h.bm.Active(); !slices.Equal(got, want) {
		t.Fatalf("access = %v after Shutdown; want %v", got, want)
	}
}

func TestShutdown_RefusesNewAcquires(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "late")

	ctx, cancel := context.WithTimeout(context.Background(), sessionTestTimeout)
	defer cancel()
	if n := h.mgr.Shutdown(ctx); n != 0 {
		t.Fatalf("Shutdown = %d; want 0", n)
	}
	if s, _, err := h.mgr.Acquire(id); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Acquire after Shutdown = %v, %v; want ErrShuttingDown", s, err)
	}
	h.assertNoAccess(t)
	if _, err := os.Lstat(filepath.Join(root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Acquire after Shutdown created .choix: %v", err)
	}
}

func TestAcquire_RevivesDrainingSession(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "revived")
	s, release := h.acquire(t, id)
	sessionTestHoldDetachedCall(t, s, stills)
	release()
	h.events.waitFor(t, id, managerStageWaiting, 1)

	s2, release2 := h.acquire(t, id)
	if s2 != s {
		t.Fatal("Acquire of a draining folder opened a second session")
	}
	close(stills.Gate)
	h.events.waitFor(t, id, managerStageKept, 1)
	if sessionTestIsClosed(s) || !sessionTestStoreOpen(s) {
		t.Fatal("a revived session was closed")
	}
	if got := h.bm.Active(); !slices.Equal(got, []string{root}) {
		t.Fatalf("access = %v; want the revived session's lease", got)
	}

	release2()
	sessionTestWaitClosed(t, s)
	h.assertNoAccess(t)
}

// sessionTestWaitStale waits until an Acquire has found the folder's open
// session stale.
func sessionTestWaitStale(t *testing.T, m *Manager, folderID string) {
	t.Helper()
	deadline := time.Now().Add(sessionTestTimeout)
	for {
		m.mu.Lock()
		e := m.entries[folderID]
		stale := e != nil && e.stale
		m.mu.Unlock()
		if stale {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session of %s was never found stale", folderID)
		}
		time.Sleep(time.Millisecond)
	}
}

type sessionTestAcquired struct {
	s       *Session
	release func()
	err     error
}

// A folder moved while its session drains behind a detached call: Acquire
// does not revive the old session at the old root. It waits for it to close
// and opens a new one at the new location; only that store is open.
func TestAcquire_MovedWhileDrainingOpensFreshSession(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "trip")
	s, release := h.acquire(t, id)
	sessionTestHoldDetachedCall(t, s, stills)
	release()
	h.events.waitFor(t, id, managerStageWaiting, 1)

	moved := filepath.Join(h.parent, "trip renamed")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(root, moved)

	h.mgr.staleWait = sessionTestTimeout
	got := make(chan sessionTestAcquired, 1)
	go func() {
		s2, release2, err := h.mgr.Acquire(id)
		got <- sessionTestAcquired{s2, release2, err}
	}()
	sessionTestWaitStale(t, h.mgr, id)
	select {
	case r := <-got:
		t.Fatalf("Acquire returned %v, %v while the stale session was still open", r.s, r.err)
	default:
	}

	close(stills.Gate) // the detached call returns; the old session closes
	var r sessionTestAcquired
	select {
	case r = <-got:
	case <-time.After(sessionTestTimeout):
		t.Fatal("Acquire never returned after the stale session closed")
	}
	if r.err != nil {
		t.Fatalf("Acquire: %v", r.err)
	}
	defer r.release()
	if r.s == s || r.s.Root != moved {
		t.Fatalf("Acquire = session at %q (same as the old one: %v); want a new session at %q", r.s.Root, r.s == s, moved)
	}
	if !sessionTestIsClosed(s) || sessionTestStoreOpen(s) {
		t.Fatal("the stale session is still open")
	}
	if !sessionTestStoreOpen(r.s) {
		t.Fatal("the new session's store does not answer")
	}
	if got := h.bm.Active(); !slices.Equal(got, []string{moved}) {
		t.Fatalf("access = %v; want only the new location", got)
	}
}

// A stale session whose detached call never returns: Acquire gives up after
// the bound with ErrBusy (busy for a binding — not unavailable, which means
// quitting), opens nothing and takes no access. Here the session's root
// still exists, but the Library records the folder at its new location.
func TestAcquire_StaleSessionStillClosingIsBusy(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "trip")
	s, release := h.acquire(t, id)
	sessionTestHoldDetachedCall(t, s, stills)
	release()
	h.events.waitFor(t, id, managerStageWaiting, 1)
	t.Cleanup(func() {
		close(stills.Gate)
		sessionTestWaitClosed(t, s)
	})

	moved := filepath.Join(h.parent, "trip moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(root, moved)
	if _, err := h.lib.CheckPresence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f, err := h.lib.Folder(id); err != nil || f.Path != moved {
		t.Fatalf("recorded location = %q, %v; want %q", f.Path, err, moved)
	}
	if err := os.Mkdir(root, 0o755); err != nil { // something new at the old path
		t.Fatal(err)
	}

	h.mgr.staleWait = 50 * time.Millisecond
	start := time.Now()
	s2, release2, err := h.mgr.Acquire(id)
	if !errors.Is(err, ErrBusy) || s2 != nil || release2 != nil {
		t.Fatalf("Acquire = %v, %v; want ErrBusy and nothing", s2, err)
	}
	if took := time.Since(start); took < h.mgr.staleWait || took > sessionTestTimeout {
		t.Fatalf("Acquire gave up after %v; want after the %v bound", took, h.mgr.staleWait)
	}
	if e := apperr.From(err); e.Code != apperr.CodeBusy {
		t.Fatalf("binding error = %+v; want busy", e)
	}
	if sessionTestIsClosed(s) || !sessionTestStoreOpen(s) {
		t.Fatal("the stale session was closed under its detached call")
	}
	if got := h.bm.Active(); !slices.Equal(got, []string{root}) {
		t.Fatalf("access = %v; want only the stale session's lease (taken at %s)", got, root)
	}
	h.mgr.mu.Lock()
	n, e := len(h.mgr.entries), h.mgr.entries[id]
	h.mgr.mu.Unlock()
	if n != 1 || e == nil || e.s != s {
		t.Fatalf("sessions = %d (entry %+v); want only the stale one", n, e)
	}
	if _, err := os.Lstat(filepath.Join(root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a session was opened at the old path: %v", err)
	}
	if _, _, ok := h.mgr.AcquireOpen(id); ok {
		t.Fatal("AcquireOpen handed out the draining stale session")
	}
}

// sessionTestDraining opens the folder's session and leaves it draining
// behind a held detached call; release lets it close.
func sessionTestDraining(t *testing.T, h *sessionTestHarness, stills *platformtest.Stills, id string) (s *Session, release func()) {
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
	return s, release
}

// The Library has recorded a move to a place that is not available now:
// the old session — its root still the same directory — is stale anyway.
// Once it has closed, Acquire answers with the Library's own error for the
// folder (offline), never by reviving the old session.
func TestAcquire_RecordedMoveToUnavailablePlaceIsStale(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "trip")
	s, release := sessionTestDraining(t, h, stills, id)

	elsewhere := filepath.Join(h.parent, "on a card")
	h.bm.Move(root, elsewhere)
	if _, err := h.lib.CheckPresence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f, err := h.lib.Folder(id); err != nil || f.Path != elsewhere {
		t.Fatalf("recorded location = %q, %v; want %q", f.Path, err, elsewhere)
	}
	h.bm.Delete(elsewhere) // the card is out: the new location does not resolve

	h.mgr.staleWait = sessionTestTimeout
	got := make(chan sessionTestAcquired, 1)
	go func() {
		s2, release2, err := h.mgr.Acquire(id)
		got <- sessionTestAcquired{s2, release2, err}
	}()
	sessionTestWaitStale(t, h.mgr, id)
	release()
	var r sessionTestAcquired
	select {
	case r = <-got:
	case <-time.After(sessionTestTimeout):
		t.Fatal("Acquire never returned")
	}
	if r.s != nil || !errors.Is(r.err, library.ErrOffline) {
		t.Fatalf("Acquire = %v, %v; want library.ErrOffline and no session", r.s, r.err)
	}
	if !sessionTestIsClosed(s) {
		t.Fatal("the old session was not closed")
	}
	h.assertNoAccess(t)
}

// The folder moved without the Library knowing, and another directory now
// occupies its old path: the old session's root is not the directory it
// opened, so it is stale and is not handed out.
func TestAcquire_DirectoryReplacingRootIsStale(t *testing.T) {
	stills := &platformtest.Stills{Gate: make(chan struct{})}
	h := newSessionTestHarness(t, sessionTestOptions{stills: stills})
	id, root := h.folder(t, "trip")
	s, _ := sessionTestDraining(t, h, stills, id)

	if err := os.Rename(root, filepath.Join(h.parent, "trip elsewhere")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if f, err := h.lib.Folder(id); err != nil || f.Path != root {
		t.Fatalf("recorded location = %q, %v; want the old path %q (move unrecorded)", f.Path, err, root)
	}

	h.mgr.staleWait = 50 * time.Millisecond
	s2, _, err := h.mgr.Acquire(id)
	if !errors.Is(err, ErrBusy) || s2 != nil {
		t.Fatalf("Acquire = %v, %v; want ErrBusy (the old session is stale and still closing)", s2, err)
	}
	if s2 == s {
		t.Fatal("the stale session was handed out")
	}
	if _, err := os.Lstat(filepath.Join(root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a session was opened in the directory now at the old path: %v", err)
	}
}

// A referenced session that an Acquire found stale is no longer handed out
// by AcquireOpen either (media requests, refreshes), although its holder
// keeps it until it releases it.
func TestAcquireOpen_RefusesStaleSession(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "trip")
	s, release := h.acquire(t, id)
	if got, rel, ok := h.mgr.AcquireOpen(id); !ok || got != s {
		t.Fatal("AcquireOpen refused a current session")
	} else {
		rel()
	}

	moved := filepath.Join(h.parent, "trip moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(root, moved)
	h.mgr.staleWait = 20 * time.Millisecond
	if s2, _, err := h.mgr.Acquire(id); !errors.Is(err, ErrBusy) || s2 != nil {
		t.Fatalf("Acquire while the stale session is referenced = %v, %v; want ErrBusy", s2, err)
	}
	if got, _, ok := h.mgr.AcquireOpen(id); ok || got != nil {
		t.Fatalf("AcquireOpen of a stale session = %v, %v; want nil, false", got, ok)
	}
	if !sessionTestStoreOpen(s) {
		t.Fatal("the holder's store was closed under its reference")
	}

	release()
	sessionTestWaitClosed(t, s)
	h.mgr.staleWait = sessionTestTimeout
	s3, release3, err := h.mgr.Acquire(id)
	if err != nil {
		t.Fatalf("Acquire after the stale session closed: %v", err)
	}
	defer release3()
	if s3 == s || s3.Root != moved {
		t.Fatalf("Acquire = session at %q; want a new one at %q", s3.Root, moved)
	}
}

func TestAcquire_V1FolderOpensUntouched(t *testing.T) {
	h := newSessionTestHarness(t, sessionTestOptions{})
	id, root := h.folder(t, "v1")
	sessionTestCopyV1(t, root)
	ctx := context.Background()

	// The fixture as v1 left it, read before any session opens it.
	st, err := store.Open(filepath.Join(root, ".choix", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	before := sessionTestSnapshotV1(t, &Session{Root: root, Store: st})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	s, release := h.acquire(t, id)
	defer release()
	sessionTestAssertSameV1(t, "after opening", before, sessionTestSnapshotV1(t, s), true)
	reclustered, err := s.EnsureClustered(ctx, h.settings.get)
	if err != nil || reclustered {
		t.Fatalf("EnsureClustered = %v, %v; want false, nil", reclustered, err)
	}
	sessionTestAssertV1Untouched(t, s)
	sessionTestAssertSameV1(t, "after EnsureClustered", before, sessionTestSnapshotV1(t, s), true)
	if _, err := s.Store.KV().Get(sessionKVClusterParams); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cluster_params = %v; want no key stored", err)
	}
	if got := s.PicksMissing(); got != 0 {
		t.Fatalf("PicksMissing = %d; want 0", got)
	}
	dir, err := s.PicksDir(config.Defaults())
	if err != nil || dir != "exports/out" {
		t.Fatalf("PicksDir(defaults) = %q, %v; want exports/out", dir, err)
	}

	p, _ := s.NewPipeline(h.settings.get)
	if n, err := p.Pending(ctx); err != nil || n != 0 {
		t.Fatalf("Pending = %d, %v; want 0", n, err)
	}
	if err := p.Scanner.Walk(ctx); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, rel := range []string{"exports/out/DCIM/100TEST/TEST0002.JPG", "exports/out/DCIM/100TEST/TEST0005.JPG"} {
		if f, err := s.Store.Files().GetByPath(rel); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("exported copy %s is in files: %+v, %v", rel, f, err)
		}
	}
	sessionTestAssertV1Untouched(t, s)
	sessionTestAssertSameV1(t, "after the walk", before, sessionTestSnapshotV1(t, s), true)
	if n, err := p.Pending(ctx); err != nil || n != 0 {
		t.Fatalf("Pending after the walk = %d, %v; want 0", n, err)
	}

	// A whole pipeline pass re-processes nothing either: no native call is
	// made, statuses, raw_exif, thumbs rows and cache files stay; only the
	// cluster stage rebuilds the same clusters (renumbered) and stamps them.
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls := h.stills.Calls(); len(calls) != 0 {
		t.Fatalf("the pipeline made %d native calls on a v1 folder; want 0", len(calls))
	}
	sessionTestAssertSameV1(t, "after a pipeline pass", before, sessionTestSnapshotV1(t, s), false)
	if got, want := sessionTestStoredParams(t, s.Store), ClusterParams(h.settings.get()); got != want {
		t.Fatalf("cluster_params after the pass = %q; want %q", got, want)
	}

	// Unpick still works: the copy under exports/out goes, the original stays.
	svc, err := s.Picks(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Set(ctx, 2, picks.StateNone); err != nil {
		t.Fatalf("unpick: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "exports", "out", "DCIM", "100TEST", "TEST0002.JPG")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unpick left the exported copy: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "DCIM", "100TEST", "TEST0002.JPG")); err != nil {
		t.Fatalf("unpick touched the original: %v", err)
	}
}

// sessionTestAssertV1Untouched checks the v1 fixture's rows: six analyzed
// files on one device, clusters 5 and 6, both picks exported.
func sessionTestAssertV1Untouched(t *testing.T, s *Session) {
	t.Helper()
	if n, err := s.Store.Files().CountByStatus("analyzed"); err != nil || n != 6 {
		t.Fatalf("analyzed files = %d, %v; want 6", n, err)
	}
	var devices int
	if err := s.Store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(DISTINCT device_key) FROM files`).Scan(&devices); err != nil || devices != 1 {
		t.Fatalf("devices = %d, %v; want 1", devices, err)
	}
	cl, err := s.Store.Clusters().ListAll()
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, c := range cl {
		ids = append(ids, c.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{5, 6}) {
		t.Fatalf("cluster ids = %v; want [5 6]", ids)
	}
	picked, err := s.Store.Picks().ListPicked()
	if err != nil || len(picked) != 2 {
		t.Fatalf("picks = %+v, %v; want 2", picked, err)
	}
}
