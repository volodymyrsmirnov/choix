//go:build !server

package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
)

// TestShellDialogs_Busy: until Attach has made the native dialogs, both
// answer busy — not unavailable, which the frontend reads as quitting — and
// show nothing.
func TestShellDialogs_Busy(t *testing.T) {
	picker, confirm := shellDialogs()
	if picker != &shell.dialogs || confirm != &shell.dialogs {
		t.Fatalf("shellDialogs() = %T, %T; want the shell's lateDialogs", picker, confirm)
	}

	paths, err := picker.PickFolders()
	if code := codeOf(err); code != apperr.CodeBusy || paths != nil {
		t.Errorf("PickFolders = %v, %v (code %q); want nil, busy", paths, err, code)
	}
	ok, err := confirm.ConfirmRemove("folder-1")
	if code := codeOf(err); code != apperr.CodeBusy || ok {
		t.Errorf("ConfirmRemove = %v, %v (code %q); want false, busy", ok, err, code)
	}
}

// fakeDialogs answers with fixed values and records what it was asked.
type fakeDialogs struct {
	paths []string
	ok    bool
	names []string
}

func (f *fakeDialogs) PickFolders() ([]string, error) { return f.paths, nil }

func (f *fakeDialogs) ConfirmRemove(name string) (bool, error) {
	f.names = append(f.names, name)
	return f.ok, nil
}

// TestLateDialogs_ForwardsOnceSet: once the native dialogs are set, the
// dialogs the runtime was given before they existed answer through them.
func TestLateDialogs_ForwardsOnceSet(t *testing.T) {
	var l lateDialogs
	if _, err := l.PickFolders(); codeOf(err) != apperr.CodeBusy {
		t.Fatalf("PickFolders before set: err %v; want busy", err)
	}

	picker := &fakeDialogs{paths: []string{"/a"}}
	confirm := &fakeDialogs{ok: true}
	l.set(picker, confirm)

	paths, err := l.PickFolders()
	if err != nil || len(paths) != 1 || paths[0] != "/a" {
		t.Errorf("PickFolders = %v, %v; want [/a], nil", paths, err)
	}
	ok, err := l.ConfirmRemove("folder-1")
	if !ok || err != nil || len(confirm.names) != 1 || confirm.names[0] != "folder-1" {
		t.Errorf("ConfirmRemove = %v, %v (asked %q); want true, nil, asked for folder-1", ok, err, confirm.names)
	}
	if len(picker.names) != 0 {
		t.Errorf("the picker was asked to confirm %q", picker.names)
	}
}

func codeOf(err error) string {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}

// TestShellMenuState_Late: the runtime is given the shell's lateMenu, which
// does nothing and leaves the single keys to the webview until Attach has
// installed the native menu.
func TestShellMenuState_Late(t *testing.T) {
	if m := shellMenuState(); m != &shell.menus {
		t.Fatalf("shellMenuState() = %T; want the shell's lateMenu", m)
	}
	var l lateMenu
	l.SetContext(system.MenuContext{Screen: "focus"})
	l.PopupFolderMenu("folder-1", nil, 1, 2)
	if l.SingleKeysNative() {
		t.Error("SingleKeysNative() = true before the menu exists; want false")
	}
}

// TestSingleKeysNative_Fallback pins design call C31's fallback: until the
// owner's spike S2 key presses pass, single keys stay with the webview.
func TestSingleKeysNative_Fallback(t *testing.T) {
	if singleKeysNative {
		t.Error("singleKeysNative = true; spike S2's single-key measurement has not passed")
	}
}

// fakeMenu records what the menu was asked.
type fakeMenu struct {
	contexts   []system.MenuContext
	popups     []string
	singleKeys bool
}

func (f *fakeMenu) SetContext(c system.MenuContext) { f.contexts = append(f.contexts, c) }

func (f *fakeMenu) PopupFolderMenu(folderID string, flags []string, x, y int) {
	f.popups = append(f.popups, fmt.Sprintf("%s %v %d,%d", folderID, flags, x, y))
}

func (f *fakeMenu) SingleKeysNative() bool { return f.singleKeys }

// TestLateMenu_ForwardsOnceSet: once the native menu is set, the menu the
// runtime was given before it existed forwards to it.
func TestLateMenu_ForwardsOnceSet(t *testing.T) {
	var l lateMenu
	f := &fakeMenu{singleKeys: true}
	l.set(f)

	l.SetContext(system.MenuContext{Screen: "folder", HasSelection: true})
	l.PopupFolderMenu("folder-1", []string{"offline"}, 3, 4)
	if len(f.contexts) != 1 || f.contexts[0].Screen != "folder" || !f.contexts[0].HasSelection {
		t.Errorf("contexts = %+v; want the folder screen with a selection", f.contexts)
	}
	if want := []string{"folder-1 [offline] 3,4"}; !slices.Equal(f.popups, want) {
		t.Errorf("popups = %q; want %q", f.popups, want)
	}
	if !l.SingleKeysNative() {
		t.Error("SingleKeysNative() = false; want the installed menu's true")
	}
}

// TestShellTitleBar_Late: the runtime is given the shell's lateTitleBar,
// which measures 0 (the frontend keeps the design's inset) and ignores a
// double-click until Attach has opened the window.
func TestShellTitleBar_Late(t *testing.T) {
	if b := shellTitleBar(); b != &shell.titleBar {
		t.Fatalf("shellTitleBar() = %T; want the shell's lateTitleBar", b)
	}
	var l lateTitleBar
	if got := l.ButtonsRight(); got != 0 {
		t.Errorf("ButtonsRight() = %v before the window exists; want 0", got)
	}
	if got := l.ButtonsCenterY(); got != 0 {
		t.Errorf("ButtonsCenterY() = %v before the window exists; want 0", got)
	}
	if l.FullScreen() {
		t.Error("FullScreen() = true before the window exists; want false")
	}
	l.DoubleClick()
}

// fakeTitleBar answers a fixed edge and centre and counts double-clicks.
type fakeTitleBar struct {
	right      float64
	centerY    float64
	fullScreen bool
	clicks     int
}

func (f *fakeTitleBar) ButtonsRight() float64   { return f.right }
func (f *fakeTitleBar) ButtonsCenterY() float64 { return f.centerY }
func (f *fakeTitleBar) FullScreen() bool        { return f.fullScreen }
func (f *fakeTitleBar) DoubleClick()            { f.clicks++ }

// TestLateTitleBar_ForwardsOnceSet: once the window's title bar is set, the
// title bar the runtime was given before it existed forwards to it.
func TestLateTitleBar_ForwardsOnceSet(t *testing.T) {
	var l lateTitleBar
	f := &fakeTitleBar{right: 79, centerY: 26, fullScreen: true}
	l.set(f)
	if got := l.ButtonsRight(); got != 79 {
		t.Errorf("ButtonsRight() = %v; want the window's 79", got)
	}
	if got := l.ButtonsCenterY(); got != 26 {
		t.Errorf("ButtonsCenterY() = %v; want the window's 26", got)
	}
	if !l.FullScreen() {
		t.Error("FullScreen() = false; want the window's true")
	}
	l.DoubleClick()
	if f.clicks != 1 {
		t.Errorf("DoubleClick calls = %d; want 1", f.clicks)
	}
}

// TestShellFocus_BeforeAttach: a second launch before the window exists
// does nothing.
func TestShellFocus_BeforeAttach(t *testing.T) {
	var s desktopShell
	s.Focus(application.SecondInstanceData{})
	if s.win.Load() != nil {
		t.Fatal("Focus created a window")
	}
}

// TestShellAssets_ServesIndex: the asset handler serves the embedded
// frontend's index.html at the root.
func TestShellAssets_ServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	shellAssets().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d; want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("GET / returned an empty body")
	}
}
