// Command choix is the Choix application: it builds the platform, creates
// the runtime, binds its services and media handler to the Wails
// application, registers the typed events (events.go) and runs.
//
// What depends on the build — the assets, the native dialogs and menu, the
// window — comes from the shell (shell_desktop.go); this file has no build
// tags.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/avmedia"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/bookmark"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/imageio"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/volumes"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/workspace"
)

func main() {
	os.Exit(run())
}

// run builds and runs the application and returns the exit code. On macOS
// the process usually ends inside app.Run, once the shutdown hooks have run.
func run() int {
	if logs, err := runtime.SetupLogging(); err != nil {
		slog.Warn("choix: logging to stderr only", "err", err)
	} else {
		defer func() { _ = logs.Close() }()
	}

	// The headless self-check (selfcheck.go) runs instead of the app.
	if os.Getenv(selfCheckEnv) == "1" {
		if selfCheck(os.Stdout) {
			return 0
		}
		return 1
	}

	deps := runtime.Deps{
		Bookmarks:  bookmark.New(),
		Volumes:    volumes.New(),
		Workspace:  workspace.New(),
		Stills:     imageio.New(),
		Video:      avmedia.New(),
		Downloader: local.NewInstaller(appdir.Models()),
		MenuState:  shellMenuState(),
		TitleBar:   shellTitleBar(),
		// Home stays empty: the runtime reads the real home directory from
		// the password database ($HOME is the sandbox container).
	}
	deps.Picker, deps.Confirm = shellDialogs()

	app := application.New(application.Options{
		Name:         "Choix",
		Description:  "Photo and video culling",
		MarshalError: apperr.Marshal,
		Assets:       application.AssetOptions{Handler: shellAssets()},
		Mac:          application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.github.volodymyrsmirnov.choix",
			OnSecondInstanceLaunch: shell.Focus,
		},
		// Read only by Wails' `server`-tagged files; the desktop build ignores it and opens no listener.
		Server: application.ServerOptions{Host: "127.0.0.1", Port: 8080},
	})

	deps.Emit = newEmitter(app.Event)
	rt, err := runtime.New(deps)
	if err != nil {
		slog.Error("choix: start", "err", err)
		return 1
	}
	// The runtime exists before its hook is registered, so the hook always
	// has something to shut down.
	app.OnShutdown(rt.Shutdown)

	app.RegisterService(application.NewService(rt.Library))
	app.RegisterService(application.NewService(rt.Catalog))
	app.RegisterService(application.NewService(rt.Settings))
	app.RegisterService(application.NewService(rt.Models))
	app.RegisterService(application.NewService(rt.System))
	app.RegisterService(application.NewServiceWithOptions(rt.Media, application.ServiceOptions{Route: "/media"}))

	shell.Attach(app, rt)

	// The root context is the runtime's own: Shutdown cancels it after
	// closing the bindings' gate. Wails' application context is cancelled
	// before the shutdown hooks run, which would stop the work before the
	// gate closes.
	rt.Start(context.Background())

	err = app.Run()
	// When Run returns (a failed start, or a build whose Run returns after
	// quitting), the runtime still stops in order; Shutdown is idempotent.
	rt.Shutdown()
	if err != nil {
		slog.Error("choix: run", "err", err)
		return 1
	}
	return 0
}
