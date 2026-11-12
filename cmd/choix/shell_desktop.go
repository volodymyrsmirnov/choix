//go:build !server

package main

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/dialogs"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/menu"
	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
	"github.com/volodymyrsmirnov/choix/internal/app/window"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/titlebar"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/windowchrome"
)

// The shell is what main gets from the build it is part of: the assets the
// application serves, the native dialogs, the native menu, and the window
// and its wiring (Attach). This file is the desktop build's. The native UI
// is design-gated ([call] C24) and built only in internal/app/{window,menu,
// dialogs}. main.go does not change.

// shellAssets returns the handler serving the frontend: the embedded build.
func shellAssets() http.Handler {
	return application.AssetFileServerFS(choix.Frontend())
}

// shellDialogs returns the native folder picker and remove confirmation.
// main asks for them before the application exists, so they are the shell's
// lateDialogs, which Attach points at the native dialogs (internal/app/
// dialogs); until then they answer busy.
func shellDialogs() (library.FolderPicker, library.Confirmer) {
	return &shell.dialogs, &shell.dialogs
}

// shellMenuState returns the native menu. main asks for it before the
// application exists, so it is the shell's lateMenu, which Attach points at
// the installed menu (internal/app/menu); until then it does nothing.
func shellMenuState() system.MenuState {
	return &shell.menus
}

// shellTitleBar returns the window's title bar. main asks for it before the
// window exists, so it is the shell's lateTitleBar, which Attach points at
// the window's (internal/app/window); until then it measures 0 and ignores
// a double-click.
func shellTitleBar() system.TitleBar {
	return &shell.titleBar
}

// errNoDialogs is what every dialog answers until the native ones exist:
// busy, since the app is starting, not quitting (unavailable is the closed
// gate's code only, which the frontend drops silently).
func errNoDialogs() error {
	return apperr.New(apperr.CodeBusy, "native dialogs are not available yet")
}

// boundDialogs is the picker and confirmation lateDialogs forwards to.
type boundDialogs struct {
	picker  library.FolderPicker
	confirm library.Confirmer
}

// lateDialogs is the folder picker and remove confirmation the runtime is
// given before the application exists: it forwards to the dialogs set once
// they are made, and until then shows nothing and answers busy.
type lateDialogs struct {
	bound atomic.Pointer[boundDialogs]
}

// set points the dialogs at picker and confirm.
func (l *lateDialogs) set(picker library.FolderPicker, confirm library.Confirmer) {
	l.bound.Store(&boundDialogs{picker: picker, confirm: confirm})
}

// PickFolders shows the folder picker, or answers busy before it
// exists.
func (l *lateDialogs) PickFolders() ([]string, error) {
	b := l.bound.Load()
	if b == nil {
		return nil, errNoDialogs()
	}
	return b.picker.PickFolders()
}

// ConfirmRemove shows the remove confirmation, or answers busy before
// it exists.
func (l *lateDialogs) ConfirmRemove(name string) (bool, error) {
	b := l.bound.Load()
	if b == nil {
		return false, errNoDialogs()
	}
	return b.confirm.ConfirmRemove(name)
}

// singleKeysNative is whether the native menu owns the single-key shortcuts
// (Space, X, C, →, …; menu.NativeAccelerator). It is false, design call
// C31's fallback: spike S2 did not run the key presses showing that a
// single-key menu item neither steals typing in a text field nor lets the
// page see the key as well, so those keys stay with the webview and only
// shortcuts with ⌘, ⌃ or ⌥ are native. Flip it once the owner's
// measurement passes.
const singleKeysNative = false

// boundMenu is the installed menu lateMenu forwards to.
type boundMenu struct{ state system.MenuState }

// lateMenu is the native menu the runtime is given before the application
// exists: it forwards to the menu set once it is installed, and until then
// does nothing and reports that the webview owns the single keys.
type lateMenu struct {
	bound atomic.Pointer[boundMenu]
}

// set points the menu at state.
func (l *lateMenu) set(state system.MenuState) {
	l.bound.Store(&boundMenu{state: state})
}

// SetContext updates the installed menu's items for the frontend's screen.
func (l *lateMenu) SetContext(c system.MenuContext) {
	if b := l.bound.Load(); b != nil {
		b.state.SetContext(c)
	}
}

// PopupFolderMenu opens the installed menu's folder-row context menu.
func (l *lateMenu) PopupFolderMenu(folderID string, flags []string, x, y int) {
	if b := l.bound.Load(); b != nil {
		b.state.PopupFolderMenu(folderID, flags, x, y)
	}
}

// SingleKeysNative reports whether the installed menu owns the single-key
// shortcuts; false while there is none, so the webview keeps them.
func (l *lateMenu) SingleKeysNative() bool {
	b := l.bound.Load()
	return b != nil && b.state.SingleKeysNative()
}

// boundTitleBar is the window's title bar lateTitleBar forwards to.
type boundTitleBar struct{ bar system.TitleBar }

// lateTitleBar is the title bar the runtime is given before the window
// exists: it forwards to the window's once set, and until then measures 0
// (the frontend keeps the design's inset) and ignores a double-click.
type lateTitleBar struct {
	bound atomic.Pointer[boundTitleBar]
}

// set points the title bar at bar.
func (l *lateTitleBar) set(bar system.TitleBar) {
	l.bound.Store(&boundTitleBar{bar: bar})
}

// ButtonsRight returns where the window's traffic lights end, 0 while there
// is no window.
func (l *lateTitleBar) ButtonsRight() float64 {
	if b := l.bound.Load(); b != nil {
		return b.bar.ButtonsRight()
	}
	return 0
}

// ButtonsCenterY returns the traffic lights' vertical centre, 0 while
// there is no window.
func (l *lateTitleBar) ButtonsCenterY() float64 {
	if b := l.bound.Load(); b != nil {
		return b.bar.ButtonsCenterY()
	}
	return 0
}

// FullScreen reports whether the window is in full screen, false while
// there is no window.
func (l *lateTitleBar) FullScreen() bool {
	if b := l.bound.Load(); b != nil {
		return b.bar.FullScreen()
	}
	return false
}

// DoubleClick acts on a double-click on the frontend's title bar.
func (l *lateTitleBar) DoubleClick() {
	if b := l.bound.Load(); b != nil {
		b.bar.DoubleClick()
	}
}

// fileDrop turns dropping folders on the window on or off, in this one
// place. A dropped folder that the sandbox does not let Choix read fails in
// Library.Add with its access error, which the frontend shows.
const fileDrop = true

// desktopShell is the desktop build's window, its dialogs, its menu and
// their wiring.
type desktopShell struct {
	win      atomic.Pointer[application.WebviewWindow] // set by Attach
	dialogs  lateDialogs                               // pointed at the native dialogs by Attach
	menus    lateMenu                                  // pointed at the native menu by Attach
	titleBar lateTitleBar                              // pointed at the window's title bar by Attach
}

// shell is the build's shell; main calls Attach once the runtime exists,
// and Focus is the single-instance callback.
var shell desktopShell

// Attach opens the main window, at its saved frame, and wires its events to
// the runtime, its title bar to the System binding, and the corrections
// Wails leaves undone (the minimum size kept, the keyboard handed to the
// page, the colour under the page) to its events (internal/app/window);
// installs the native menu bar and the folder row's context menu
// from the approved manifest (internal/app/menu); then it makes the native
// dialogs, with the approved alert copy, attached to that window
// (internal/app/dialogs).
func (s *desktopShell) Attach(app *application.App, rt *runtime.Runtime) {
	w := window.Open(app, rt, rt.KV(), fileDrop)
	s.win.Store(w)
	s.titleBar.set(window.NewTitleBar(w, titlebar.New()))
	window.AttachChrome(w, windowchrome.New())
	s.installMenu(app)

	alerts, err := dialogs.ParseAlerts(choix.DialogManifest)
	if err != nil {
		// Not reached by a tested build: internal/app/dialogs' tests parse
		// the embedded manifest.
		slog.Error("choix: native dialogs unavailable", "err", err)
		return
	}
	d := dialogs.New(app, dialogs.Parent(s.win.Load), alerts)
	s.dialogs.set(d, d)
}

// installMenu installs the native menu from the approved manifest and points
// the runtime's menu at it. Its clicks go to the frontend through an emitter
// of its own, which goes silent at shutdown like the runtime's.
func (s *desktopShell) installMenu(app *application.App) {
	m, err := menu.Parse(choix.MenuManifest)
	if err != nil {
		// Not reached by a tested build: internal/app/menu's tests parse
		// the embedded manifest.
		slog.Error("choix: native menu unavailable", "err", err)
		return
	}
	em := newEmitter(app.Event)
	app.OnShutdown(em.Close)
	s.menus.set(menu.Install(app, s.win.Load, m, em, singleKeysNative))
	slog.Info("choix: native menu installed", "single_keys_native", singleKeysNative)
}

// Focus runs when Choix is launched again while running: it brings the
// window forward, un-minimising it first.
func (s *desktopShell) Focus(application.SecondInstanceData) {
	w := s.win.Load()
	if w == nil {
		return
	}
	w.UnMinimise()
	w.Focus()
}
