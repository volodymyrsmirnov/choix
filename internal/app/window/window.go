// Package window builds Choix's one native window and wires its events to
// the runtime: folders dropped on it are added to the Library, its frame is
// saved in library.db's kv table and restored at the next launch ([call]
// C29), and its visibility pauses the periodic presence check.
//
// The package is design-gated: the window's chrome (the hidden-inset title
// bar with the traffic lights, the sizes, the background) is the approved
// design's.
package window

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
)

// The window's default and minimum sizes, in points.
const (
	DefaultWidth  = 1280
	DefaultHeight = 800
	MinWidth      = 960
	MinHeight     = 600
)

const (
	// saveDelay debounces saving the frame while it is being moved or
	// resized.
	saveDelay = 500 * time.Millisecond
	// screensWait bounds how long, after the application has started,
	// settle waits for Wails' screen list (filled on its own goroutine once
	// launching has finished).
	screensWait  = 2 * time.Second
	screensEvery = 20 * time.Millisecond
)

// background is the page background, --color-bg-1 (the app root's, in
// frontend/src/design/base.css) of frontend/src/design/tokens.css,
// oklch(0.19 0.004 240), in sRGB. The window shows it until the page draws.
var background = application.NewRGB(18, 20, 21)

// Options returns the main window's options: the default frame, centred,
// or the saved one when saved is not nil. fileDrop turns on dropping files
// on the page's drop targets.
//
// Mac.InvisibleTitleBarHeight stays 0: a non-zero value makes Wails start a
// native window drag on every mouse-down in that band, which would swallow
// clicks on toolbar buttons; the design's title bar marks its drag region
// with --wails-draggable instead. There is no backdrop or translucency.
func Options(saved *State, fileDrop bool) application.WebviewWindowOptions {
	o := application.WebviewWindowOptions{
		Title:                      "Choix",
		Width:                      DefaultWidth,
		Height:                     DefaultHeight,
		MinWidth:                   MinWidth,
		MinHeight:                  MinHeight,
		URL:                        "/",
		BackgroundType:             application.BackgroundTypeSolid,
		BackgroundColour:           background,
		EnableFileDrop:             fileDrop,
		ZoomControlEnabled:         false,
		DefaultContextMenuDisabled: true,
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
	}
	if saved != nil {
		o.InitialPosition = application.WindowXY
		o.X, o.Y = saved.X, saved.Y
		o.Width, o.Height = saved.Width, saved.Height
	}
	return o
}

// Open creates the main window at its saved frame (or the default one) and
// wires its events:
//
//   - files dropped → rt.AddDropped with the directories among them;
//   - moved or resized → the frame saved in kv, debounced by 500 ms, and
//     saved at once when the window is closing;
//   - focused or un-minimised → rt.SetVisible(true); minimised → false.
//
// Once the application has started, the frame is settled (settle): a
// restored frame that no longer overlaps any screen by 100×100 points
// (Restore) is replaced by the default size, centred, and a frame Wails
// created a point too small is corrected. Open is called before the
// application runs.
func Open(app *application.App, rt *runtime.Runtime, kv KV, fileDrop bool) *application.WebviewWindow {
	saved, err := Load(kv)
	if err != nil {
		slog.Warn("window: opening at the default frame", "err", err)
		saved = nil
	}
	w := app.Window.NewWithOptions(Options(saved, fileDrop))
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		settle(app, w, saved)
	})

	if fileDrop {
		w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
			ctx := e.Context()
			if ctx == nil {
				return
			}
			if dirs := directories(ctx.DroppedFiles()); len(dirs) > 0 {
				rt.AddDropped(dirs)
			}
		})
	}

	save := newDebouncer(saveDelay, func() { saveFrame(w, kv) })
	w.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) { save.Trigger() })
	w.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) { save.Trigger() })
	// A hook runs before the listener that closes the window, so the frame
	// can still be read; closing the last window quits the app.
	w.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { save.Flush() })

	w.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { rt.SetVisible(true) })
	w.OnWindowEvent(events.Common.WindowMinimise, func(*application.WindowEvent) { rt.SetVisible(false) })
	w.OnWindowEvent(events.Common.WindowUnMinimise, func(*application.WindowEvent) { rt.SetVisible(true) })
	return w
}

// settle gives the window, once the application has started, the frame it
// should have (target), when it has another:
//
//   - Wails v3.1.0 creates a macOS window with a content rect of
//     Width-1 × Height-1 points (createNativeWindow in
//     webview_window_darwin.go), so without this the default window would
//     be 1279×799 and a restored frame would shrink by a point at every
//     launch;
//   - a restored frame that is off every screen gives way to the default.
//
// Wails fills its screen list on another goroutine once launching has
// finished, after the window was made; settle waits for it, at most
// screensWait.
func settle(app *application.App, w *application.WebviewWindow, saved *State) {
	deadline := time.Now().Add(screensWait)
	screens := app.Screen.GetAll()
	for len(screens) == 0 && time.Now().Before(deadline) {
		time.Sleep(screensEvery)
		screens = app.Screen.GetAll()
	}
	rects := make([]Rect, 0, len(screens))
	for _, s := range screens {
		rects = append(rects, Rect(s.Bounds))
	}
	if saved != nil && len(rects) == 0 {
		slog.Warn("window: no screen list; the saved frame is not checked")
	}

	want, centred := target(saved, rects)
	if saved != nil && centred {
		slog.Info("window: the saved frame is off every screen; opening at the default size",
			"x", saved.X, "y", saved.Y, "width", saved.Width, "height", saved.Height)
	}
	b := w.Bounds()
	if !misplaced(State(b), want, centred) {
		return
	}
	// The size first: it keeps the bottom-left corner, so the position is
	// set after it.
	w.SetSize(want.Width, want.Height)
	if centred {
		w.Center()
	} else {
		w.SetPosition(want.X, want.Y)
	}
}

// target returns the frame the window should open at: the saved one as
// Restore keeps it (unchecked when no screen is known), otherwise the
// default size, centred.
func target(saved *State, screens []Rect) (want State, centred bool) {
	def := State{Width: DefaultWidth, Height: DefaultHeight}
	switch {
	case saved == nil:
		return def, true
	case len(screens) == 0:
		return saved.clamped(), false
	}
	if s := Restore(*saved, screens); s != nil {
		return *s, false
	}
	return def, true
}

// misplaced reports whether the window's frame got differs from want: in
// size, and in position unless the window is to be centred.
func misplaced(got, want State, centred bool) bool {
	if got.Width <= 0 || got.Height <= 0 {
		return false // the window is gone, or not made yet
	}
	if got.Width != want.Width || got.Height != want.Height {
		return true
	}
	return !centred && (got.X != want.X || got.Y != want.Y)
}

// saveFrame saves the window's frame in kv. A minimised or full-screen
// window keeps the frame saved before.
func saveFrame(w *application.WebviewWindow, kv KV) {
	if w.IsMinimised() || w.IsFullscreen() {
		return
	}
	b := w.Bounds()
	if b.Width <= 0 || b.Height <= 0 {
		return // the window is gone
	}
	if err := Save(kv, State{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}); err != nil {
		slog.Warn("window: the frame was not saved", "err", err)
	}
}

// directories returns the paths among paths that are directories (a
// symbolic link to one counts: the Library resolves it), in order; nil when
// there is none.
func directories(paths []string) []string {
	var dirs []string
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// debouncer runs a function once, delay after the last Trigger. It is safe
// for concurrent use; runs never overlap.
type debouncer struct {
	delay time.Duration
	run   func()
	// after arms a timer that calls f after d and returns its stop; tests
	// replace it.
	after func(d time.Duration, f func()) (stop func() bool)

	mu      sync.Mutex
	gen     uint64      // the generation of the armed timer
	pending bool        // a run is due
	stop    func() bool // stops the armed timer; nil when none
	runMu   sync.Mutex  // serialises runs
}

func newDebouncer(delay time.Duration, run func()) *debouncer {
	return &debouncer{
		delay: delay,
		run:   run,
		after: func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop },
	}
}

// Trigger (re)arms the timer: the run happens delay after the last call.
func (d *debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stop != nil {
		d.stop()
	}
	d.gen++
	gen := d.gen
	d.pending = true
	d.stop = d.after(d.delay, func() { d.fire(gen) })
}

// Flush runs a pending run now, on the caller's goroutine, and disarms the
// timer; it does nothing when no run is pending.
func (d *debouncer) Flush() {
	d.mu.Lock()
	if !d.pending {
		d.mu.Unlock()
		return
	}
	d.take()
	d.mu.Unlock()
	d.runNow()
}

// fire is the timer's callback for generation gen; a superseded or flushed
// timer runs nothing.
func (d *debouncer) fire(gen uint64) {
	d.mu.Lock()
	if !d.pending || gen != d.gen {
		d.mu.Unlock()
		return
	}
	d.take()
	d.mu.Unlock()
	d.runNow()
}

// take clears the pending run and stops the timer; d.mu is held.
func (d *debouncer) take() {
	d.pending = false
	if d.stop != nil {
		d.stop()
		d.stop = nil
	}
}

func (d *debouncer) runNow() {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	d.run()
}
