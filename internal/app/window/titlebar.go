package window

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync/atomic"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// doubleClickAction is what a double-click on a title bar does.
type doubleClickAction int

const (
	doubleClickNothing  doubleClickAction = iota
	doubleClickZoom                       // the zoom button's maximise/restore toggle
	doubleClickMinimise                   // into the Dock
)

func (a doubleClickAction) String() string {
	switch a {
	case doubleClickNothing:
		return "nothing"
	case doubleClickZoom:
		return "zoom"
	case doubleClickMinimise:
		return "minimise"
	default:
		return fmt.Sprintf("doubleClickAction(%d)", int(a))
	}
}

// nativeDoubleClick is what a native title bar does on a double-click for
// the user's AppleActionOnDoubleClick setting: minimise for Minimize,
// nothing for None, and otherwise zoom — Maximize, Fill, an unset value and
// any value a later macOS adds.
func nativeDoubleClick(pref string) doubleClickAction {
	switch pref {
	case "Minimize":
		return doubleClickMinimise
	case "None":
		return doubleClickNothing
	default:
		return doubleClickZoom
	}
}

// wailsActsOn reports whether Wails acts on a title-bar double-click
// itself for pref. Wails v3.1.0's runtime sends `wails:drag:doubleclick`
// for a dblclick on a draggable element (runtime src/drag.ts), and its Go
// side zooms for "" and "Maximize", minimises for "Minimize" and does
// nothing for anything else (parseMacTitlebarDoubleClickAction in
// pkg/application/titlebar_doubleclick_action.go, applied by
// webview_window_titlebar_doubleclick.go). A test pins this set to the
// Wails version in go.mod.
func wailsActsOn(pref string) bool {
	switch pref {
	case "", "Maximize", "Minimize":
		return true
	default:
		return false
	}
}

// choixDoubleClick is what Choix itself does on a title-bar double-click
// for pref: what macOS does, for the preferences Wails leaves alone, and
// nothing where Wails acts. Both read the same setting the same way, so
// for any one value exactly one of them acts (or neither, for None).
func choixDoubleClick(pref string) doubleClickAction {
	if wailsActsOn(pref) {
		return doubleClickNothing
	}
	return nativeDoubleClick(pref)
}

// chromeWindow is what TitleBar uses of the Wails window.
type chromeWindow interface {
	NativeWindow() unsafe.Pointer
	IsFullscreen() bool
	Zoom()
	Minimise() application.Window
}

var errNoNativeWindow = errors.New("the window has no native window yet")

// TitleBar is the main window's title bar as the frontend's own title bar
// needs it (the System binding's system.TitleBar): where the traffic lights
// end and where their vertical centre lies, whether the window is in full
// screen, and a double-click on the bar acted on like a native title
// bar's. It is safe for concurrent use.
//
// Nothing goes to the main thread until the window is ready: before
// App.Run, Wails' InvokeSync only queues the work and waits for a run loop
// that does not run yet, so a binding asking then would hang. Ready is the
// first of the window's own events that prove both the native window and
// the run loop: it was shown (common:WindowShow) or its page's runtime
// loaded (common:WindowRuntimeReady). Until then ButtonsRight and
// ButtonsCenterY answer 0, FullScreen false and DoubleClick does nothing,
// at once.
type TitleBar struct {
	w      chromeWindow
	native platform.TitleBar
	// onMain runs fn on the main thread and waits for it
	// (application.InvokeSync; tests replace it).
	onMain func(fn func())
	// ready is set once the window is shown or its page's runtime loaded.
	ready atomic.Bool
	// right is the measured edge (math.Float64bits); 0 until measured.
	right atomic.Uint64
	// centerY is the measured vertical centre (math.Float64bits); 0 until
	// measured.
	centerY atomic.Uint64
}

// NewTitleBar returns the title bar of w, read through native. It may be
// called before the application runs; it becomes ready with the window.
func NewTitleBar(w *application.WebviewWindow, native platform.TitleBar) *TitleBar {
	t := newTitleBar(w, native, application.InvokeSync)
	w.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) { t.markReady() })
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { t.markReady() })
	return t
}

func newTitleBar(w chromeWindow, native platform.TitleBar, onMain func(func())) *TitleBar {
	return &TitleBar{w: w, native: native, onMain: onMain}
}

// markReady records that the native window exists and the run loop runs.
func (t *TitleBar) markReady() { t.ready.Store(true) }

// ButtonsRight returns the right edge of the window's right-most traffic
// light, in points from the window's left edge, measured on the main thread
// the first time it is known and kept; 0 while it is not (the window is not
// ready, or the measurement failed). An unknown value is never kept.
func (t *TitleBar) ButtonsRight() float64 {
	if v := math.Float64frombits(t.right.Load()); v > 0 {
		return v
	}
	right, _ := t.measure()
	return right
}

// ButtonsCenterY returns the traffic lights' common vertical centre, in
// points from the top of the window (CSS pixels from the top of the page),
// under the same rules as ButtonsRight.
func (t *TitleBar) ButtonsCenterY() float64 {
	if v := math.Float64frombits(t.centerY.Load()); v > 0 {
		return v
	}
	_, centerY := t.measure()
	return centerY
}

// measure measures, in one trip to the main thread, what is not known yet
// of the right edge and the vertical centre, keeps what it learns, and
// returns both (0 for what is still unknown). Before the window is ready
// it returns what is kept, at once.
func (t *TitleBar) measure() (right, centerY float64) {
	right = math.Float64frombits(t.right.Load())
	centerY = math.Float64frombits(t.centerY.Load())
	needRight, needCenter := right <= 0, centerY <= 0
	if !t.ready.Load() || (!needRight && !needCenter) {
		return right, centerY
	}
	var errRight, errCenter error
	t.onMain(func() {
		p := t.w.NativeWindow()
		if p == nil {
			errRight, errCenter = errNoNativeWindow, errNoNativeWindow
			return
		}
		if needRight {
			right, errRight = t.native.ButtonsRight(p)
		}
		if needCenter {
			centerY, errCenter = t.native.ButtonsCenterY(p)
		}
	})
	if needRight {
		right = keep(&t.right, right, errRight, "window: the traffic lights were not measured; the title bar keeps its default inset")
	}
	if needCenter {
		centerY = keep(&t.centerY, centerY, errCenter, "window: the traffic lights' centre was not measured; the title bar keeps the design's vertical alignment")
	}
	return right, centerY
}

// keep stores v in dst and returns it when it is a measurement (err nil, a
// positive finite number); otherwise it logs warning and returns 0,
// keeping nothing.
func keep(dst *atomic.Uint64, v float64, err error, warning string) float64 {
	if err == nil && (math.IsNaN(v) || math.IsInf(v, 0) || v <= 0) {
		err = fmt.Errorf("measured %v points", v)
	}
	if err != nil {
		slog.Warn(warning, "err", err)
		return 0
	}
	dst.Store(math.Float64bits(v))
	return v
}

// FullScreen reports whether the window is in native full screen now, read
// on the main thread (WebviewWindow.IsFullscreen); false while the window is
// not ready.
func (t *TitleBar) FullScreen() bool {
	if !t.ready.Load() {
		return false
	}
	return t.w.IsFullscreen()
}

// DoubleClick acts on a double-click on the frontend's title bar, for the
// preferences Wails does not act on itself (choixDoubleClick): Wails
// handles the same double-click on its own, so acting on the others would
// zoom or minimise the window twice. Nothing happens in full screen, as in
// Wails, and nothing before the window is ready.
func (t *TitleBar) DoubleClick() {
	if !t.ready.Load() {
		return
	}
	pref, err := t.native.DoubleClickPreference()
	if err != nil {
		slog.Warn("window: the title-bar double-click setting was not read", "err", err)
		return
	}
	act := choixDoubleClick(pref)
	if act == doubleClickNothing || t.w.IsFullscreen() {
		return
	}
	switch act {
	case doubleClickZoom:
		// AppKit's zoom: (Wails' Zoom), as the zoom button: it keeps the
		// window's minimum size. Wails' ToggleMaximise would not: its
		// Maximise sets the minimum to 0×0 and only its own UnMaximise
		// puts it back, so a window then un-zoomed by dragging an edge
		// could be made as small as the user liked.
		t.w.Zoom()
	case doubleClickMinimise:
		t.w.Minimise()
	}
}
