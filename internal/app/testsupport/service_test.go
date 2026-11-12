//go:build server

package testsupport

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// testTimeout bounds every wait for a condition; the tests wait for
// conditions, never for time to pass.
const testTimeout = 60 * time.Second

// TestMain points CHOIX_DATA_DIR at a temporary directory before any test
// runs, so nothing reaches the real Application Support directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "choix-testsupport-test-")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	if err := os.Setenv("CHOIX_DATA_DIR", dir); err != nil {
		panic("test setup: setenv CHOIX_DATA_DIR: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// recEmitter records the events it is given.
type recEmitter struct {
	mu     sync.Mutex
	events []recEvent
}

type recEvent struct {
	name string
	data any
}

func (e *recEmitter) Emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, recEvent{name, data})
}

func (e *recEmitter) all() []recEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]recEvent(nil), e.events...)
}

// fixture is one test's runtime, its test-support service and the root the
// service accepts paths under.
type fixture struct {
	rt   *runtime.Runtime
	svc  *Service
	d    *Dialogs
	em   *recEmitter
	root string // resolved E2E root
	out  string // a resolved directory outside root
}

// newFixture starts a runtime over the platform fakes in a fresh data
// directory, with visual grouping and the quality rating off, and a Service whose E2E root is
// root (rootFn's result; "" when rootFn is nil).
func newFixture(t *testing.T, rootFn func(root string) string) *fixture {
	t.Helper()
	t.Setenv("CHOIX_DATA_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.VisualGrouping = false
	cfg.QualityRating = false
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	parent := resolved(t, t.TempDir())
	root := filepath.Join(parent, "e2e")
	out := filepath.Join(parent, "outside")
	for _, dir := range []string{root, out} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vols := &platformtest.Volumes{}
	vols.Add(parent, platform.VolumeInfo{UUID: "UUID-TEST", Name: "Test HD"})

	d := &Dialogs{}
	rt, err := runtime.New(runtime.Deps{
		Bookmarks: &platformtest.Bookmarks{},
		Volumes:   vols,
		Workspace: &platformtest.Workspace{},
		Stills:    &platformtest.Stills{},
		Video:     &platformtest.Video{},
		Vision:    &platformtest.Vision{},
		Picker:    d,
		Confirm:   d,
		Home:      parent,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	t.Cleanup(rt.Shutdown)
	rt.Start(context.Background())

	given := ""
	if rootFn != nil {
		given = rootFn(root)
	}
	em := &recEmitter{}
	return &fixture{rt: rt, svc: New(rt, d, em, given), d: d, em: em, root: root, out: out}
}

// resolved returns path with its symlinks resolved (t.TempDir is under
// /var, a symlink to /private/var).
func resolved(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// mkdir makes the directory rel under base and returns its path.
func mkdir(t *testing.T, base, rel string) string {
	t.Helper()
	p := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// waitIdle calls WaitIdle, a capped wait, until the runtime is idle or
// testTimeout has passed.
func waitIdle(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !f.svc.WaitIdle(int(maxWait / time.Millisecond)) {
		if time.Now().After(deadline) {
			t.Fatal("WaitIdle: the runtime did not become idle")
		}
	}
}

// sameRoot is a rootFn giving the service the fixture's root as is.
func sameRoot(root string) string { return root }

// list returns the Library's folders.
func list(t *testing.T, f *fixture) []library.FolderSummary {
	t.Helper()
	got, err := f.rt.Library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return got
}

// wantInvalid fails the test unless err is an invalid-input error.
func wantInvalid(t *testing.T, what string, err error) {
	t.Helper()
	e := apperr.From(err)
	if e == nil || e.Code != apperr.CodeInvalid {
		t.Fatalf("%s: err = %v; want an %q error", what, err, apperr.CodeInvalid)
	}
}

func TestAddFolderPath_InsideRootAddsAndQueues(t *testing.T) {
	f := newFixture(t, sameRoot)
	dir := mkdir(t, f.root, "trip")

	res, err := f.svc.AddFolderPath(dir)
	if err != nil {
		t.Fatalf("AddFolderPath: %v", err)
	}
	if res.ID == "" || res.Existing {
		t.Fatalf("AddFolderPath = %+v; want a new folder", res)
	}
	waitIdle(t, f)
	got := list(t, f)
	if len(got) != 1 || got[0].ID != res.ID {
		t.Fatalf("Library = %+v; want only %s", got, res.ID)
	}
	if got[0].State != "ready" {
		t.Errorf("folder state = %q; want ready once its full job ran", got[0].State)
	}
}

func TestAddFolderPath_OutsideRootIsInvalid(t *testing.T) {
	f := newFixture(t, sameRoot)
	for _, p := range []string{
		mkdir(t, f.out, "trip"),
		filepath.Join(f.root, "..", "outside"), // escapes through ".."
		filepath.Dir(f.root),                   // the root's parent
		"trip",                                 // not absolute
	} {
		if _, err := f.svc.AddFolderPath(p); err == nil {
			t.Errorf("AddFolderPath(%s) succeeded; want invalid", p)
		} else {
			wantInvalid(t, "AddFolderPath("+p+")", err)
		}
	}
	if got := list(t, f); len(got) != 0 {
		t.Errorf("Library = %+v; want empty", got)
	}
}

func TestAddFolderPath_SymlinkOutOfRootIsInvalid(t *testing.T) {
	f := newFixture(t, sameRoot)
	target := mkdir(t, f.out, "trip")
	link := filepath.Join(f.root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// A symlinked parent too: root/via → outside, then via/trip.
	via := filepath.Join(f.root, "via")
	if err := os.Symlink(f.out, via); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{link, filepath.Join(via, "trip")} {
		_, err := f.svc.AddFolderPath(p)
		wantInvalid(t, "AddFolderPath("+p+")", err)
	}
	if got := list(t, f); len(got) != 0 {
		t.Errorf("Library = %+v; want empty", got)
	}
}

func TestAddFolderPath_SymlinkedRootIsResolved(t *testing.T) {
	// The root given through a symlink still accepts the folders under it.
	var linkRoot string
	f := newFixture(t, func(root string) string {
		linkRoot = filepath.Join(filepath.Dir(root), "root-link")
		if err := os.Symlink(root, linkRoot); err != nil {
			t.Fatal(err)
		}
		return linkRoot
	})
	dir := mkdir(t, f.root, "trip")
	if _, err := f.svc.AddFolderPath(filepath.Join(linkRoot, "trip")); err != nil {
		t.Fatalf("AddFolderPath through the root's symlink: %v", err)
	}
	if _, err := f.svc.AddFolderPath(dir); err != nil {
		t.Fatalf("AddFolderPath(%s): %v", dir, err)
	}
}

func TestAddFolderPath_EmptyRootRefusesEverything(t *testing.T) {
	f := newFixture(t, nil)
	for _, p := range []string{mkdir(t, f.root, "trip"), f.root, "/"} {
		_, err := f.svc.AddFolderPath(p)
		wantInvalid(t, "AddFolderPath("+p+")", err)
	}
	wantInvalid(t, "SetPickerPaths", f.svc.SetPickerPaths([]string{mkdir(t, f.root, "other")}))
	if got := list(t, f); len(got) != 0 {
		t.Errorf("Library = %+v; want empty", got)
	}
}

func TestAddFolderPath_MissingRootRefusesEverything(t *testing.T) {
	f := newFixture(t, func(root string) string { return filepath.Join(root, "does-not-exist") })
	_, err := f.svc.AddFolderPath(mkdir(t, f.root, "trip"))
	wantInvalid(t, "AddFolderPath", err)
}

func TestAddFolderPath_FilesystemRootRefusesEverything(t *testing.T) {
	f := newFixture(t, func(string) string { return "/" })
	_, err := f.svc.AddFolderPath(mkdir(t, f.root, "trip"))
	wantInvalid(t, "AddFolderPath", err)
}

func TestAddFolderPath_FileIsInvalid(t *testing.T) {
	f := newFixture(t, sameRoot)
	file := filepath.Join(f.root, "photo.jpg")
	if err := os.WriteFile(file, platformtest.MakeJPEG(t, 8, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.AddFolderPath(file)
	wantInvalid(t, "AddFolderPath(a file)", err)
	_, err = f.svc.AddFolderPath(filepath.Join(f.root, "missing"))
	wantInvalid(t, "AddFolderPath(a missing path)", err)
}

func TestSetPickerPaths_FeedsAddFolders(t *testing.T) {
	f := newFixture(t, sameRoot)
	a := mkdir(t, f.root, "a")
	b := mkdir(t, f.root, "b")

	if err := f.svc.SetPickerPaths([]string{a, b}); err != nil {
		t.Fatalf("SetPickerPaths: %v", err)
	}
	res, err := f.rt.Library.AddFolders()
	if err != nil {
		t.Fatalf("AddFolders: %v", err)
	}
	if len(res) != 2 || res[0].Name != "a" || res[1].Name != "b" {
		t.Fatalf("AddFolders = %+v; want a then b", res)
	}

	// The paths answer one AddFolders only: the next picker is cancelled.
	res, err = f.rt.Library.AddFolders()
	if err != nil || len(res) != 0 {
		t.Fatalf("second AddFolders = %+v, %v; want nothing picked", res, err)
	}
	waitIdle(t, f)
	if got := list(t, f); len(got) != 2 {
		t.Errorf("Library has %d folders; want 2", len(got))
	}
}

func TestSetPickerPaths_RefusesAnyPathOutsideRoot(t *testing.T) {
	f := newFixture(t, sameRoot)
	in := mkdir(t, f.root, "a")
	if err := f.svc.SetPickerPaths([]string{in}); err != nil {
		t.Fatalf("SetPickerPaths: %v", err)
	}
	// A list with one path outside the root is refused whole and leaves
	// what was set before.
	wantInvalid(t, "SetPickerPaths", f.svc.SetPickerPaths([]string{in, mkdir(t, f.out, "b")}))

	res, err := f.rt.Library.AddFolders()
	if err != nil {
		t.Fatalf("AddFolders: %v", err)
	}
	if len(res) != 1 || res[0].Name != "a" {
		t.Fatalf("AddFolders = %+v; want only a", res)
	}
}

func TestSetConfirmAnswer_FeedsRemove(t *testing.T) {
	f := newFixture(t, sameRoot)
	res, err := f.svc.AddFolderPath(mkdir(t, f.root, "trip"))
	if err != nil {
		t.Fatalf("AddFolderPath: %v", err)
	}
	waitIdle(t, f)

	// The default answer declines.
	ok, err := f.rt.Library.Remove(res.ID)
	if err != nil || ok {
		t.Fatalf("Remove with no answer set = %v, %v; want false (declined)", ok, err)
	}
	f.svc.SetConfirmAnswer(false)
	if ok, err := f.rt.Library.Remove(res.ID); err != nil || ok {
		t.Fatalf("Remove after SetConfirmAnswer(false) = %v, %v; want false", ok, err)
	}
	if got := list(t, f); len(got) != 1 {
		t.Fatalf("Library has %d folders after a declined removal; want 1", len(got))
	}

	f.svc.SetConfirmAnswer(true)
	if ok, err := f.rt.Library.Remove(res.ID); err != nil || !ok {
		t.Fatalf("Remove after SetConfirmAnswer(true) = %v, %v; want true", ok, err)
	}
	if got := list(t, f); len(got) != 0 {
		t.Errorf("Library = %+v; want empty after the removal", got)
	}
}

func TestWaitIdle_TimesOutWhileBusy(t *testing.T) {
	f := newFixture(t, sameRoot)
	f.svc.idle = func() bool { return false }
	for _, ms := range []int{-5, 0, 1} {
		if f.svc.WaitIdle(ms) {
			t.Errorf("WaitIdle(%d) = true while busy; want false", ms)
		}
	}
	f.svc.idle = func() bool { return true }
	if !f.svc.WaitIdle(0) {
		t.Error("WaitIdle(0) = false while idle; want true")
	}
}

func TestEmitMenuAction_EmitsTheEvent(t *testing.T) {
	f := newFixture(t, sameRoot)
	f.svc.EmitMenuAction("library.add", "folder-1")
	got := f.em.all()
	want := recEvent{events.MenuAction, events.MenuActionEvent{ID: "library.add", Data: "folder-1"}}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("events = %+v; want [%+v]", got, want)
	}
}

func TestEmitMenuAction_NilEmitterIsNoOp(t *testing.T) {
	f := newFixture(t, sameRoot)
	New(f.rt, f.d, nil, f.root).EmitMenuAction("library.add", "")
}
