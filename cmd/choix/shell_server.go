//go:build server

package main

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
	"github.com/volodymyrsmirnov/choix/internal/app/testsupport"
)

// This file is the shell of the server build (`-tags server`): the same app,
// its services and its assets over HTTP on 127.0.0.1, for the Playwright
// end-to-end tests. It is test-only and never shipped. Native dialogs
// cannot be automated, so the folder picker and the remove confirmation are
// testsupport.Dialogs, whose answers a test sets through the bound
// testsupport.Service. It defines the same names as shell_desktop.go, so
// main.go builds unchanged with either.
//
// Environment, read by this build only:
//
//   - CHOIX_ASSETS_DIR: serve the frontend from this directory instead of
//     the embedded build;
//   - CHOIX_E2E_ROOT: the only directory the test-support service accepts
//     folder paths from; unset, it refuses every path.
//
// Wails' own WAILS_SERVER_PORT picks the port; WAILS_SERVER_HOST is dropped
// (pinLoopback).
const (
	envAssetsDir  = "CHOIX_ASSETS_DIR"
	envE2ERoot    = "CHOIX_E2E_ROOT"
	envServerHost = "WAILS_SERVER_HOST"
)

// serverDialogs are the picker and confirmation the runtime is given and the
// test-support service feeds.
var serverDialogs = &testsupport.Dialogs{}

// shellAssets returns the handler serving the frontend: the directory in
// CHOIX_ASSETS_DIR when it is set, otherwise the embedded build.
func shellAssets() http.Handler {
	if dir := os.Getenv(envAssetsDir); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		// CHOIX_ASSETS_DIR comes from the harness that starts this test-only
		// build; it is only checked here, for a clearer log.
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() { //nolint:gosec // G703: trusted test-harness setting

			slog.Warn("choix: CHOIX_ASSETS_DIR is not a directory; requests for assets will fail", "dir", abs)
		}
		return application.AssetFileServerFS(os.DirFS(abs))
	}
	return application.AssetFileServerFS(choix.Frontend())
}

// shellDialogs returns the test-support dialogs as both the folder picker
// and the remove confirmation.
func shellDialogs() (library.FolderPicker, library.Confirmer) {
	return serverDialogs, serverDialogs
}

// shellMenuState returns nil: a server build has no native menu, which the
// System binding treats as a no-op menu. Tests fire menu actions through
// testsupport.Service.EmitMenuAction.
func shellMenuState() system.MenuState {
	return nil
}

// shellTitleBar returns nil: a server build has no window, which the System
// binding treats as a title bar that measures 0 and ignores a double-click.
func shellTitleBar() system.TitleBar {
	return nil
}

// serverShell is the server build's wiring: no window, the test-support
// service.
type serverShell struct{}

// shell is the build's shell; main calls Attach once the runtime exists,
// and Focus is the single-instance callback.
var shell serverShell

// Attach pins the server to the loopback address and registers the
// test-support service over the runtime.
func (serverShell) Attach(app *application.App, rt *runtime.Runtime) {
	pinLoopback()
	// The service's own emitter for the menu actions it fires; like the
	// runtime's, it goes silent at shutdown.
	em := newEmitter(app.Event)
	app.OnShutdown(em.Close)
	app.RegisterService(application.NewService(testsupport.New(rt, serverDialogs, em, os.Getenv(envE2ERoot))))
}

// Focus does nothing: a server build has no window to bring forward.
func (serverShell) Focus(application.SecondInstanceData) {}

// pinLoopback removes WAILS_SERVER_HOST, with which Wails would bind to
// another address than main's 127.0.0.1: the test-support service adds
// folders by path and must not be reachable from another machine.
func pinLoopback() {
	host, ok := os.LookupEnv(envServerHost)
	if !ok {
		return
	}
	if host != "" {
		slog.Warn("choix: ignoring WAILS_SERVER_HOST; the server build listens on 127.0.0.1 only", "host", host)
	}
	if err := os.Unsetenv(envServerHost); err != nil {
		slog.Warn("choix: unset WAILS_SERVER_HOST", "err", err)
	}
}
