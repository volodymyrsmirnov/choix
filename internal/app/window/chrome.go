package window

import (
	"log/slog"
	"sync/atomic"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// minSizeEvents are the window events after which the minimum size is put
// back. Wails v3.1.0 lowers the native minimum to 0×0 in Maximise and in
// Fullscreen (DisableSizeConstraints, windowSetFullScreen) and restores it
// only in its own UnMaximise and UnFullscreen; a window un-zoomed any other
// way (an edge dragged, the green button, the Window menu's Zoom, macOS
// tiling) or brought out of full screen by AppKit keeps 0×0 for the rest of
// the run. Wails' own title-bar double-click maximises for the Zoom
// setting, so this path is reachable whatever Choix does.
var minSizeEvents = []events.WindowEventType{
	events.Common.WindowShow,
	events.Common.WindowRuntimeReady,
	events.Common.WindowDidResize,
	events.Common.WindowMaximise,
	events.Common.WindowUnMaximise,
	events.Common.WindowUnFullscreen,
}

// focusEvents are the window events after which the page is offered the
// keyboard: Wails v3.1.0 never makes its WKWebView first responder, so a
// window shown (or made key) without a click on the page sends no key to
// it.
var focusEvents = []events.WindowEventType{
	events.Common.WindowShow,
	events.Common.WindowRuntimeReady,
	events.Common.WindowFocus,
}

// underPageEvents are the window events after which the colour under the
// page is set, until it is, as a fallback: the colour is set before the
// window is first shown by the hook on beforeShowEvent.
var underPageEvents = []events.WindowEventType{
	events.Common.WindowRuntimeReady,
	events.Common.WindowShow,
	events.Common.WindowFocus,
}

// nativeWindow is what Chrome uses of the Wails window.
type nativeWindow interface {
	NativeWindow() unsafe.Pointer
}

// Chrome does to the main window, through AppKit, what Wails leaves
// undone: it keeps the minimum size (MinWidth × MinHeight, except in full
// screen), hands the keyboard to the page, and colours the area under the
// page (where it rubber-bands) with the window's background. Every call
// runs on the main thread; a window Wails has not made yet is left alone.
type Chrome struct {
	w      nativeWindow
	native platform.WindowChrome
	// onMain runs fn on the main thread (application.InvokeAsync; tests
	// replace it).
	onMain func(fn func())
	// painted is set once the colour under the page is set.
	painted atomic.Bool
}

// beforeShowEvent is the event Wails v3.1.0 shows the main window on: its
// run() registers a listener on mac:WebViewDidFinishNavigation that queues
// Show on the main thread (InvokeAsync; webview_window_darwin.go). A hook
// on that event runs synchronously before Wails starts any listener for it
// (WebviewWindow.HandleWindowEvent: hooks first, then each listener in a
// goroutine), so whatever the hook finishes on the main thread is done
// before Show is queued. A test pins both facts.
var beforeShowEvent = events.Mac.WebViewDidFinishNavigation

// windowEvents is what attachChrome uses of the Wails window.
type windowEvents interface {
	OnWindowEvent(e events.WindowEventType, fn func(*application.WindowEvent)) func()
	RegisterHook(e events.WindowEventType, fn func(*application.WindowEvent)) func()
}

// AttachChrome wires the corrections to w's events. It may be called
// before the application runs: it only registers listeners and a hook.
func AttachChrome(w *application.WebviewWindow, native platform.WindowChrome) {
	attachChrome(w, newChrome(w, native, application.InvokeAsync), application.InvokeSync)
}

func attachChrome(w windowEvents, c *Chrome, onMainSync func(func())) {
	on := func(list []events.WindowEventType, fn func()) {
		for _, e := range list {
			w.OnWindowEvent(e, func(*application.WindowEvent) { fn() })
		}
	}
	on(minSizeEvents, c.keepMinSize)
	on(focusEvents, c.focusPage)
	on(underPageEvents, c.paintUnderPage)
	// The colour under the page from the first frame: set, and waited for
	// on the main thread, before Wails queues the window's Show. Only the
	// first navigation waits; once set, the hook returns at once.
	w.RegisterHook(beforeShowEvent, func(*application.WindowEvent) { c.paint(onMainSync) })
}

func newChrome(w nativeWindow, native platform.WindowChrome, onMain func(func())) *Chrome {
	return &Chrome{w: w, native: native, onMain: onMain}
}

// keepMinSize puts back the window's minimum size where it was lowered,
// and grows a window left below it.
func (c *Chrome) keepMinSize() {
	c.onMain(func() {
		p := c.w.NativeWindow()
		if p == nil {
			return
		}
		changed, err := c.native.KeepMinSize(p, MinWidth, MinHeight)
		switch {
		case err != nil:
			slog.Warn("window: the minimum size was not checked", "err", err)
		case changed:
			slog.Info("window: the minimum size was restored", "width", MinWidth, "height", MinHeight)
		}
	})
}

// focusPage gives the keyboard to the page when no view in the window has
// it.
func (c *Chrome) focusPage() {
	c.onMain(func() {
		p := c.w.NativeWindow()
		if p == nil {
			return
		}
		if _, err := c.native.FocusPage(p); err != nil {
			slog.Warn("window: the page was not given the keyboard", "err", err)
		}
	})
}

// paintUnderPage sets the colour under the page to the window's
// background, once.
func (c *Chrome) paintUnderPage() { c.paint(c.onMain) }

// paint is paintUnderPage run on the main thread through onMain.
func (c *Chrome) paint(onMain func(func())) {
	if c.painted.Load() {
		return
	}
	onMain(func() {
		p := c.w.NativeWindow()
		if p == nil || c.painted.Load() {
			return
		}
		if err := c.native.SetPageBackground(p, background.Red, background.Green, background.Blue); err != nil {
			slog.Warn("window: the colour under the page was not set", "err", err)
			return
		}
		c.painted.Store(true)
	})
}
