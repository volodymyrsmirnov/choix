package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/buildinfo"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// TestMain points CHOIX_DATA_DIR at a temporary directory before any test
// runs, so nothing here can reach the real data directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "choix-system-test-")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	if err := os.Setenv("CHOIX_DATA_DIR", filepath.Clean(dir)); err != nil {
		panic("test setup: setenv CHOIX_DATA_DIR: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// systemTestTimeout bounds every wait for something that must happen.
const systemTestTimeout = 10 * time.Second

// systemTestWorkspace is the platformtest.Workspace with a hook run inside
// each call, before the call is recorded and returns.
type systemTestWorkspace struct {
	platformtest.Workspace
	during func(method, arg string)
}

func (w *systemTestWorkspace) hook(method, arg string) {
	if w.during != nil {
		w.during(method, arg)
	}
}

func (w *systemTestWorkspace) Reveal(path string) error {
	w.hook("Reveal", path)
	return w.Workspace.Reveal(path)
}

func (w *systemTestWorkspace) OpenFolder(path string) error {
	w.hook("OpenFolder", path)
	return w.Workspace.OpenFolder(path)
}

func (w *systemTestWorkspace) OpenURL(rawURL string) error {
	w.hook("OpenURL", rawURL)
	return w.Workspace.OpenURL(rawURL)
}

// systemTestPopup is one PopupFolderMenu call.
type systemTestPopup struct {
	folderID string
	flags    []string
	x, y     int
}

// systemTestMenu is a MenuState that records its calls.
type systemTestMenu struct {
	mu       sync.Mutex
	contexts []MenuContext
	popups   []systemTestPopup
	single   bool
}

func (m *systemTestMenu) SetContext(c MenuContext) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contexts = append(m.contexts, c)
}

func (m *systemTestMenu) PopupFolderMenu(folderID string, flags []string, x, y int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.popups = append(m.popups, systemTestPopup{folderID: folderID, flags: flags, x: x, y: y})
}

func (m *systemTestMenu) SingleKeysNative() bool { return m.single }

func (m *systemTestMenu) recorded() ([]MenuContext, []systemTestPopup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.contexts), slices.Clone(m.popups)
}

// systemTestTitleBar is a TitleBar with a fixed edge that counts the
// double-clicks it is asked to act on. entered and free, when set, stand for
// the main thread as in systemTestMainThreadMenu: each call signals entered
// and blocks until free is closed.
type systemTestTitleBar struct {
	right      float64
	centerY    float64
	fullScreen bool
	clicks     atomic.Int32
	entered    chan string
	free       chan struct{}
}

func (b *systemTestTitleBar) wait(call string) {
	if b.entered == nil {
		return
	}
	b.entered <- call
	<-b.free
}

func (b *systemTestTitleBar) ButtonsRight() float64 {
	b.wait("ButtonsRight")
	return b.right
}

func (b *systemTestTitleBar) ButtonsCenterY() float64 {
	b.wait("ButtonsCenterY")
	return b.centerY
}

func (b *systemTestTitleBar) FullScreen() bool {
	b.wait("FullScreen")
	return b.fullScreen
}

func (b *systemTestTitleBar) DoubleClick() {
	b.wait("DoubleClick")
	b.clicks.Add(1)
}

// systemTestHarness is a Service over a real Library and session Manager
// with the platform fakes.
type systemTestHarness struct {
	lib      *library.Library
	bm       *platformtest.Bookmarks
	mgr      *session.Manager
	ws       *systemTestWorkspace
	menu     *systemTestMenu
	titleBar *systemTestTitleBar
	gate     *opgate.Gate
	cfg      config.Config
	states   map[string]string
	parent   string // a resolved temporary directory holding the folders
	svc      *Service
	stateErr error
}

func newSystemTestHarness(t *testing.T) *systemTestHarness {
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

	h := &systemTestHarness{
		lib:      lib,
		bm:       bm,
		ws:       &systemTestWorkspace{},
		menu:     &systemTestMenu{},
		titleBar: &systemTestTitleBar{right: 79, centerY: 26},
		gate:     &opgate.Gate{},
		cfg:      config.Defaults(),
		states:   map[string]string{},
		parent:   parent,
	}
	settings := func() config.Config { return h.cfg }
	h.mgr = session.NewManager(session.Deps{
		Library:  lib,
		Settings: settings,
		Stills:   &platformtest.Stills{ReadPaths: true},
		Video:    &platformtest.Video{},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), systemTestTimeout)
		defer cancel()
		if n := h.mgr.Shutdown(ctx); n != 0 {
			t.Errorf("cleanup: Shutdown left %d sessions open", n)
		}
	})
	h.svc = NewService(Deps{
		Library:  lib,
		Sessions: h.mgr,
		Settings: settings,
		FolderState: func(id string) (string, error) {
			if h.stateErr != nil {
				return "", h.stateErr
			}
			return h.states[id], nil
		},
		Workspace: h.ws,
		MenuState: h.menu,
		TitleBar:  h.titleBar,
		Gate:      h.gate,
	})
	return h
}

// folder creates the folder name under the harness's parent, adds it to the
// Library and returns its id and root.
func (h *systemTestHarness) folder(t *testing.T, name string) (id, root string) {
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

// assertReleased waits until every session is closed — which happens only
// once its last reference is released — and checks that no security-scoped
// access is left.
func (h *systemTestHarness) assertReleased(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), systemTestTimeout)
	defer cancel()
	if n := h.mgr.Shutdown(ctx); n != 0 {
		t.Fatalf("%d sessions still referenced", n)
	}
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("bookmark access still active for %v", got)
	}
}

// assertNoCalls fails when the Workspace was called.
func (h *systemTestHarness) assertNoCalls(t *testing.T) {
	t.Helper()
	if calls := h.ws.Calls(); len(calls) != 0 {
		t.Fatalf("Workspace calls = %v, want none", calls)
	}
}

// assertCode fails unless err is an *apperr.Error with code and, when
// reason is not empty, that reason.
func assertCode(t *testing.T, err error, code, reason string) {
	t.Helper()
	var e *apperr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v (%T), want an *apperr.Error with code %q", err, err, code)
	}
	if e.Code != code || (reason != "" && e.Reason != reason) {
		t.Fatalf("error = %+v, want code %q reason %q", *e, code, reason)
	}
}

// systemTestCopyTree copies the regular files and directories under src
// into dst.
func systemTestCopyTree(t *testing.T, src, dst string) {
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

func TestOpenPicksFolder_CreatesAndOpens(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "trip")

	if err := h.svc.OpenPicksFolder(id); err != nil {
		t.Fatalf("OpenPicksFolder: %v", err)
	}
	picksDir := filepath.Join(root, "picks")
	fi, err := os.Lstat(picksDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("Lstat(%s) = %v, %v; want a directory", picksDir, fi, err)
	}
	want := []platformtest.WorkspaceCall{{Method: "OpenFolder", Arg: picksDir}}
	if got := h.ws.Calls(); !slices.Equal(got, want) {
		t.Fatalf("Workspace calls = %v, want %v", got, want)
	}
	h.assertReleased(t)
}

func TestOpenPicksFolder_LegacyDir(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "v1")
	// A folder last scanned by v1, its picks exported under exports/out
	// (internal/app/session/testdata/README.md).
	fixtures := filepath.Join("..", "session", "testdata")
	systemTestCopyTree(t, filepath.Join(fixtures, "v1-folder"), root)
	if err := os.Mkdir(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fixtures, "v1-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".choix", "state.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.svc.OpenPicksFolder(id); err != nil {
		t.Fatalf("OpenPicksFolder: %v", err)
	}
	want := []platformtest.WorkspaceCall{{Method: "OpenFolder", Arg: filepath.Join(root, "exports", "out")}}
	if got := h.ws.Calls(); !slices.Equal(got, want) {
		t.Fatalf("Workspace calls = %v, want %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(root, "picks")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the configured picks directory was touched: Lstat = %v", err)
	}
	h.assertReleased(t)
}

func TestOpenPicksFolder_SymlinkedPicksRefused(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "trip")
	if err := os.Symlink("..", filepath.Join(root, "picks")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(h.parent)
	if err != nil {
		t.Fatal(err)
	}

	err = h.svc.OpenPicksFolder(id)
	assertCode(t, err, apperr.CodeInvalid, "symlink")
	h.assertNoCalls(t)
	after, err := os.ReadDir(h.parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("entries next to the folder: %d before, %d after", len(before), len(after))
	}
	h.assertReleased(t)
}

func TestOpenPicksFolder_RefusedDirectories(t *testing.T) {
	cases := []struct {
		name     string
		picksDir string
		setup    func(t *testing.T, root string)
		reason   string
	}{
		{name: "traversal", picksDir: "../elsewhere", reason: "traversal"},
		{name: "the folder itself", picksDir: ".", reason: "reserved"},
		{name: "absolute", picksDir: "/tmp/picks", reason: "absolute"},
		{
			name: "a file", picksDir: "picks", reason: "not_dir",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "picks"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSystemTestHarness(t)
			h.cfg.PicksDir = tc.picksDir
			id, root := h.folder(t, "trip")
			if tc.setup != nil {
				tc.setup(t, root)
			}
			err := h.svc.OpenPicksFolder(id)
			assertCode(t, err, apperr.CodeInvalid, tc.reason)
			h.assertNoCalls(t)
			h.assertReleased(t)
		})
	}
}

func TestOpenPicksFolder_Offline(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "trip")
	h.bm.Delete(root)

	err := h.svc.OpenPicksFolder(id)
	assertCode(t, err, apperr.CodeOffline, "")
	h.assertNoCalls(t)
	h.assertReleased(t)
}

func TestOpenPicksFolder_UnknownID(t *testing.T) {
	h := newSystemTestHarness(t)
	assertCode(t, h.svc.OpenPicksFolder("nope"), apperr.CodeNotFound, "")
	h.assertNoCalls(t)
}

// systemTestHeldCheck is what a Workspace hook saw of the folder's access:
// at the start of the call and just before it returned, after a delay.
type systemTestHeldCheck struct {
	mu             sync.Mutex
	atStart, atEnd []string
}

// hold returns a Workspace hook recording the active leases at the start of
// the call and again after a 100 ms delay, just before the call returns.
func (c *systemTestHeldCheck) hold(bm *platformtest.Bookmarks) func(method, arg string) {
	return func(string, string) {
		start := bm.Active()
		time.Sleep(100 * time.Millisecond)
		end := bm.Active()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.atStart, c.atEnd = start, end
	}
}

// assertHeld fails unless root was active at both points.
func (c *systemTestHeldCheck) assertHeld(t *testing.T, root string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Contains(c.atStart, root) {
		t.Errorf("access when the Workspace call started = %v, want %s held", c.atStart, root)
	}
	if !slices.Contains(c.atEnd, root) {
		t.Errorf("access when the Workspace call returned = %v, want %s held", c.atEnd, root)
	}
}

func TestRevealFolder_UnderAccess(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "trip")
	var held systemTestHeldCheck
	h.ws.during = held.hold(h.bm)

	if err := h.svc.RevealFolder(id); err != nil {
		t.Fatalf("RevealFolder: %v", err)
	}
	want := []platformtest.WorkspaceCall{{Method: "Reveal", Arg: root}}
	if got := h.ws.Calls(); !slices.Equal(got, want) {
		t.Fatalf("Workspace calls = %v, want %v", got, want)
	}
	held.assertHeld(t, root)
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("access still active after RevealFolder: %v", got)
	}
}

func TestOpenPicksFolder_AccessHeldUntilWorkspaceReturns(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "trip")
	var held systemTestHeldCheck
	h.ws.during = held.hold(h.bm)

	if err := h.svc.OpenPicksFolder(id); err != nil {
		t.Fatalf("OpenPicksFolder: %v", err)
	}
	held.assertHeld(t, root)
	h.assertReleased(t)
}

func TestRevealFolder_UnknownID(t *testing.T) {
	h := newSystemTestHarness(t)
	assertCode(t, h.svc.RevealFolder("nope"), apperr.CodeNotFound, "")
	h.assertNoCalls(t)
}

func TestRevealFolder_Offline(t *testing.T) {
	h := newSystemTestHarness(t)
	id, root := h.folder(t, "trip")
	h.bm.Delete(root)
	assertCode(t, h.svc.RevealFolder(id), apperr.CodeOffline, "")
	h.assertNoCalls(t)
}

func TestOpenHelp_FixedURL(t *testing.T) {
	h := newSystemTestHarness(t)
	if err := h.svc.OpenHelp(); err != nil {
		t.Fatalf("OpenHelp: %v", err)
	}
	want := []platformtest.WorkspaceCall{{Method: "OpenURL", Arg: "https://github.com/volodymyrsmirnov/choix"}}
	if got := h.ws.Calls(); !slices.Equal(got, want) {
		t.Fatalf("Workspace calls = %v, want %v", got, want)
	}
}

func TestInfo_Reports(t *testing.T) {
	orig := sandboxed
	t.Cleanup(func() { sandboxed = orig })
	sandboxed = func() bool { return true }

	h := newSystemTestHarness(t)
	h.menu.single = true
	got, err := h.svc.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	want := Info{Name: "Choix", Version: buildinfo.Version, Commit: buildinfo.Commit, Sandboxed: true, SingleKeysNative: true,
		WindowButtonsRight: 79, WindowButtonsCenterY: 26}
	if got != want {
		t.Fatalf("Info = %+v, want %+v", got, want)
	}

	// Full screen is the window's current state.
	h.titleBar.fullScreen = true
	if got, err := h.svc.Info(); err != nil || !got.WindowFullScreen {
		t.Fatalf("Info = %+v, %v; want WindowFullScreen true", got, err)
	}
}

// TestInfo_WindowButtonsRightJSON: the fields' names in the bindings.
func TestInfo_WindowButtonsRightJSON(t *testing.T) {
	data, err := json.Marshal(Info{WindowButtonsRight: 79.5, WindowButtonsCenterY: 26, WindowFullScreen: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"windowButtonsRight":79.5,"windowButtonsCenterY":26,"windowFullScreen":true`) {
		t.Fatalf("Info JSON = %s; want windowButtonsRight 79.5, windowButtonsCenterY 26, windowFullScreen true", data)
	}
}

func TestTitleBarDoubleClick_Forwards(t *testing.T) {
	h := newSystemTestHarness(t)
	for range 2 {
		if err := h.svc.TitleBarDoubleClick(); err != nil {
			t.Fatalf("TitleBarDoubleClick: %v", err)
		}
	}
	if n := h.titleBar.clicks.Load(); n != 2 {
		t.Fatalf("DoubleClick calls = %d; want 2", n)
	}
}

// TestService_NilTitleBar: without a window (server builds) Info reports
// 0 — the frontend keeps the design's inset — and a double-click does
// nothing.
func TestService_NilTitleBar(t *testing.T) {
	h := newSystemTestHarness(t)
	svc := NewService(Deps{Library: h.lib, Sessions: h.mgr, Settings: config.Defaults, Workspace: h.ws, Gate: h.gate})
	info, err := svc.Info()
	if err != nil || info.WindowButtonsRight != 0 || info.WindowButtonsCenterY != 0 || info.WindowFullScreen {
		t.Fatalf("Info = %+v, %v; want WindowButtonsRight 0, WindowButtonsCenterY 0, WindowFullScreen false", info, err)
	}
	if err := svc.TitleBarDoubleClick(); err != nil {
		t.Fatalf("TitleBarDoubleClick: %v", err)
	}
}

// TestTitleBarCalls_OutsideTheGate: measuring the traffic lights and acting
// on a double-click both wait for the main thread, so Info and
// TitleBarDoubleClick leave the gate before they call the title bar (see
// TestMenuCalls_OutsideTheGate).
func TestTitleBarCalls_OutsideTheGate(t *testing.T) {
	calls := []struct {
		name string
		call func(svc *Service) error
	}{
		{"ButtonsRight", func(svc *Service) error {
			info, err := svc.Info()
			if err == nil && info.WindowButtonsRight != 79 {
				return fmt.Errorf("WindowButtonsRight = %v; want 79", info.WindowButtonsRight)
			}
			return err
		}},
		{"DoubleClick", (*Service).TitleBarDoubleClick},
	}
	// ButtonsCenterY and FullScreen are asked by Info after ButtonsRight;
	// they are let through.
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			h := newSystemTestHarness(t)
			bar := &systemTestTitleBar{right: 79, entered: make(chan string, 3), free: make(chan struct{})}
			freed := sync.OnceFunc(func() { close(bar.free) })
			t.Cleanup(freed)
			svc := NewService(Deps{Library: h.lib, Sessions: h.mgr, Settings: config.Defaults,
				Workspace: h.ws, TitleBar: bar, Gate: h.gate})

			done := make(chan error, 1)
			go func() { done <- tc.call(svc) }()
			select {
			case got := <-bar.entered:
				if got != tc.name {
					t.Fatalf("title bar call = %s; want %s", got, tc.name)
				}
			case <-time.After(systemTestTimeout):
				t.Fatalf("%s was never called", tc.name)
			}
			if !h.gate.Close(0) {
				t.Fatalf("the gate is still held while %s waits for the main thread", tc.name)
			}
			freed()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("binding = %v; want nil (it was admitted before the gate closed)", err)
				}
			case <-time.After(systemTestTimeout):
				t.Fatal("the binding never returned")
			}
		})
	}
}

func TestInfo_AfterGateClosed(t *testing.T) {
	h := newSystemTestHarness(t)
	h.gate.Close(0)
	_, err := h.svc.Info()
	assertCode(t, err, apperr.CodeUnavailable, "")
}

func TestService_EveryBindingRefusedAfterGateClosed(t *testing.T) {
	h := newSystemTestHarness(t)
	id, _ := h.folder(t, "trip")
	h.gate.Close(0)
	calls := map[string]func() error{
		"OpenPicksFolder":     func() error { return h.svc.OpenPicksFolder(id) },
		"RevealFolder":        func() error { return h.svc.RevealFolder(id) },
		"OpenHelp":            h.svc.OpenHelp,
		"SetMenuContext":      func() error { return h.svc.SetMenuContext(MenuContext{Screen: "library"}) },
		"ShowFolderMenu":      func() error { return h.svc.ShowFolderMenu(id, 1, 2) },
		"TitleBarDoubleClick": h.svc.TitleBarDoubleClick,
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			assertCode(t, call(), apperr.CodeUnavailable, "")
		})
	}
	h.assertNoCalls(t)
	if ctxs, popups := h.menu.recorded(); len(ctxs) != 0 || len(popups) != 0 {
		t.Fatalf("menu calls after the gate closed: %v, %v", ctxs, popups)
	}
	if n := h.titleBar.clicks.Load(); n != 0 {
		t.Fatalf("title-bar double-clicks after the gate closed: %d", n)
	}
}

func TestSetMenuContext_RejectsUnknownScreen(t *testing.T) {
	h := newSystemTestHarness(t)
	for _, screen := range []string{"library", "folder", "focus", "compare", "settings"} {
		c := MenuContext{Screen: screen, HasSelection: true, Flags: []string{"x"}}
		if err := h.svc.SetMenuContext(c); err != nil {
			t.Fatalf("SetMenuContext(%q): %v", screen, err)
		}
	}
	for _, screen := range []string{"", "Library", "about", "welcome"} {
		assertCode(t, h.svc.SetMenuContext(MenuContext{Screen: screen}), apperr.CodeInvalid, "")
	}
	ctxs, _ := h.menu.recorded()
	if len(ctxs) != 5 {
		t.Fatalf("MenuState got %d contexts, want the 5 valid ones: %v", len(ctxs), ctxs)
	}
	if c := ctxs[1]; c.Screen != "folder" || !c.HasSelection || !slices.Equal(c.Flags, []string{"x"}) {
		t.Fatalf("context passed through = %+v", c)
	}
}

func TestShowFolderMenu_FlagsFromState(t *testing.T) {
	h := newSystemTestHarness(t)
	cases := []struct {
		name, state string
		want        []string
	}{
		{"offline", "offline", []string{"offline"}},
		{"scanning", "scanning", []string{"scanning"}},
		{"ready", "ready", nil},
	}
	for i, tc := range cases {
		id, _ := h.folder(t, tc.name)
		h.states[id] = tc.state
		if err := h.svc.ShowFolderMenu(id, 10+i, 20+i); err != nil {
			t.Fatalf("ShowFolderMenu(%s): %v", tc.name, err)
		}
		_, popups := h.menu.recorded()
		got := popups[len(popups)-1]
		if got.folderID != id || got.x != 10+i || got.y != 20+i || !slices.Equal(got.flags, tc.want) {
			t.Errorf("%s: PopupFolderMenu(%q, %v, %d, %d); want (%q, %v, %d, %d)",
				tc.name, got.folderID, got.flags, got.x, got.y, id, tc.want, 10+i, 20+i)
		}
	}
	h.assertNoCalls(t)
}

func TestShowFolderMenu_UnknownFolder(t *testing.T) {
	h := newSystemTestHarness(t)
	assertCode(t, h.svc.ShowFolderMenu("nope", 1, 2), apperr.CodeNotFound, "")
	if _, popups := h.menu.recorded(); len(popups) != 0 {
		t.Fatalf("popups for an unknown folder: %v", popups)
	}
}

func TestShowFolderMenu_StateError(t *testing.T) {
	h := newSystemTestHarness(t)
	id, _ := h.folder(t, "trip")
	h.stateErr = errors.New("summary unavailable")
	assertCode(t, h.svc.ShowFolderMenu(id, 1, 2), apperr.CodeInternal, "")
	if _, popups := h.menu.recorded(); len(popups) != 0 {
		t.Fatalf("popups after a state error: %v", popups)
	}
}

// systemTestMainThreadMenu is a MenuState whose calls wait, as Wails' do,
// for the main thread: each signals entered and then blocks until free is
// closed. On quit the main thread is the one running Shutdown, so a call
// made under the gate would hold the gate until the quit deadline.
type systemTestMainThreadMenu struct {
	systemTestMenu
	entered chan string
	free    chan struct{}
}

func (m *systemTestMainThreadMenu) SetContext(c MenuContext) {
	m.entered <- "SetContext"
	<-m.free
	m.systemTestMenu.SetContext(c)
}

func (m *systemTestMainThreadMenu) PopupFolderMenu(folderID string, flags []string, x, y int) {
	m.entered <- "PopupFolderMenu"
	<-m.free
	m.systemTestMenu.PopupFolderMenu(folderID, flags, x, y)
}

// TestMenuCalls_OutsideTheGate: SetMenuContext and ShowFolderMenu leave the
// gate before they call into the native menu, so a Shutdown on the main
// thread — which closes the gate and waits for it — does not wait for a menu
// update that itself waits for the main thread. The gate closes at once,
// with the menu call still in flight.
func TestMenuCalls_OutsideTheGate(t *testing.T) {
	calls := []struct {
		name string
		call func(svc *Service, id string) error
	}{
		{"SetContext", func(svc *Service, _ string) error {
			return svc.SetMenuContext(MenuContext{Screen: "folder", Flags: []string{"x"}})
		}},
		{"PopupFolderMenu", func(svc *Service, id string) error {
			return svc.ShowFolderMenu(id, 1, 2)
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			h := newSystemTestHarness(t)
			id, _ := h.folder(t, "trip")
			menu := &systemTestMainThreadMenu{entered: make(chan string, 1), free: make(chan struct{})}
			freed := sync.OnceFunc(func() { close(menu.free) })
			t.Cleanup(freed)
			svc := NewService(Deps{Library: h.lib, Sessions: h.mgr, Settings: config.Defaults,
				Workspace: h.ws, MenuState: menu, Gate: h.gate})

			done := make(chan error, 1)
			go func() { done <- tc.call(svc, id) }()
			select {
			case got := <-menu.entered:
				if got != tc.name {
					t.Fatalf("menu call = %s; want %s", got, tc.name)
				}
			case <-time.After(systemTestTimeout):
				t.Fatalf("%s was never called", tc.name)
			}

			// What Shutdown's waitUnused does, without waiting: true only when
			// no binding is inside the gate.
			if !h.gate.Close(0) {
				t.Fatalf("the gate is still held while %s waits for the main thread", tc.name)
			}
			freed()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("binding = %v; want nil (it was admitted before the gate closed)", err)
				}
			case <-time.After(systemTestTimeout):
				t.Fatal("the binding never returned")
			}
			ctxs, popups := menu.recorded()
			if len(ctxs)+len(popups) != 1 {
				t.Fatalf("menu calls = %v, %v; want the one in flight", ctxs, popups)
			}
		})
	}
}

func TestService_NilMenuState(t *testing.T) {
	h := newSystemTestHarness(t)
	svc := NewService(Deps{Library: h.lib, Sessions: h.mgr, Settings: config.Defaults, Workspace: h.ws, Gate: h.gate})
	id, _ := h.folder(t, "trip")
	info, err := svc.Info()
	if err != nil || info.SingleKeysNative {
		t.Fatalf("Info = %+v, %v; want SingleKeysNative false", info, err)
	}
	if err := svc.SetMenuContext(MenuContext{Screen: "library"}); err != nil {
		t.Fatalf("SetMenuContext: %v", err)
	}
	if err := svc.ShowFolderMenu(id, 1, 2); err != nil {
		t.Fatalf("ShowFolderMenu: %v", err)
	}
}

func TestService_ExportedMethods(t *testing.T) {
	typ := reflect.TypeFor[*Service]()
	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	want := []string{"Info", "OpenHelp", "OpenPicksFolder", "ReportClientError", "RevealFolder", "SetMenuContext", "ShowFolderMenu", "TitleBarDoubleClick"}
	if !slices.Equal(got, want) {
		t.Errorf("exported methods of *Service = %v, want exactly %v (each is a frontend binding)", got, want)
	}
}
