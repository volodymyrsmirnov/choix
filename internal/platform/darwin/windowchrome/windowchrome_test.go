//go:build darwin

package windowchrome

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// AppKit allows windows on the main thread only. The main goroutine stays
// on the main thread (init) and TestMain serves onMain there while the
// tests run on other threads. No window is ever shown.
func init() { runtime.LockOSThread() }

var mainQueue = make(chan func())

func TestMain(m *testing.M) {
	done := make(chan int)
	go func() { done <- m.Run() }()
	for {
		select {
		case fn := <-mainQueue:
			fn()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// onMain runs fn on the main thread and waits for it.
func onMain(fn func()) {
	ran := make(chan struct{})
	mainQueue <- func() {
		defer close(ran)
		fn()
	}
	<-ran
}

// The main window's sizes, in points (internal/app/window).
const (
	minWidth  = 960
	minHeight = 600
)

// window is a test window and the minimum Wails gave it.
type window struct {
	*testWindow
	// min is the minimum frame size Wails' windowSetMinSize(960, 600)
	// leaves: AppKit adds the toolbar's height to the frame minimum of a
	// window with a toolbar (Wails' hidden-inset title bar has one), so
	// it is 960×634 on macOS 27.2, not 960×600.
	min testWindowState
}

// newWindow makes, on the main thread, a window like the main one (Wails
// v3.1.0's MacTitleBarHiddenInset window: a full-size content view holding
// a WKWebView and the file-drop overlay above it), at width×height points,
// with Wails' minimum (windowSetMinSize) of minWidth×minHeight, never
// shown. It is closed when the test ends.
func newWindow(t *testing.T, width, height float64) window {
	t.Helper()
	var w *testWindow
	var err error
	onMain(func() { w, err = newTestWindow(width, height) })
	if err != nil {
		t.Fatalf("making a window: %v", err)
	}
	t.Cleanup(func() { onMain(w.close) })
	onMain(func() { w.setWailsMinSize(minWidth, minHeight) })
	win := window{testWindow: w}
	win.min = win.stateOnMain()
	t.Logf("Wails' minimum %v×%v leaves minSize %v×%v, contentMinSize %v×%v",
		minWidth, minHeight, win.min.minWidth, win.min.minHeight, win.min.contentMinWidth, win.min.contentMinHeight)
	if win.min.minWidth < minWidth || win.min.minHeight < minHeight {
		t.Fatalf("Wails' minimum left minSize %v×%v; want at least %v×%v", win.min.minWidth, win.min.minHeight, minWidth, minHeight)
	}
	return win
}

func (w *testWindow) stateOnMain() testWindowState {
	var s testWindowState
	onMain(func() { s = w.state() })
	return s
}

// hasWailsMin reports whether s has the minimum Wails gave the window.
func (w window) hasWailsMin(s testWindowState) bool {
	return s.minWidth == w.min.minWidth && s.minHeight == w.min.minHeight &&
		s.contentMinWidth == w.min.contentMinWidth && s.contentMinHeight == w.min.contentMinHeight
}

// TestProbe_WailsZoomLeavesNoMinimum is the native half of the owner's
// finding (the window resized to about 530 points wide): Wails'
// Maximise() first sets the minimum to 0×0 (DisableSizeConstraints →
// windowSetMinSize(0, 0)), then zooms. Once the window's frame changes by
// any other path than Wails' UnMaximise (an edge dragged, the green
// button, the Window menu's Zoom), the window is no longer zoomed and its
// minimum stays 0 wide (AppKit keeps the toolbar's 34 points of height). It also shows what the fix relies on: AppKit's own
// zoom: keeps the minimum, and setting the minimum on a zoomed window
// leaves it zoomed.
func TestProbe_WailsZoomLeavesNoMinimum(t *testing.T) {
	w := newWindow(t, 1100, 700)

	// AppKit's zoom: (what the zoom button does) leaves the minimum alone.
	onMain(w.zoom)
	s := w.stateOnMain()
	t.Logf("after zoom: frame %v×%v, zoomed %v, minimum %v×%v", s.width, s.height, s.zoomed, s.minWidth, s.minHeight)
	if !w.hasWailsMin(s) {
		t.Errorf("zoom: changed the minimum to %v×%v", s.minWidth, s.minHeight)
	}
	onMain(w.zoom)

	// Wails' Maximise: minimum 0×0, then zoom:.
	onMain(func() {
		w.setWailsMinSize(0, 0)
		w.zoom()
	})
	s = w.stateOnMain()
	t.Logf("after Wails' Maximise: frame %v×%v, zoomed %v, minimum %v×%v", s.width, s.height, s.zoomed, s.minWidth, s.minHeight)
	if s.minWidth >= minWidth {
		t.Fatalf("after Wails' Maximise the minimum = %v×%v; the probe expects it lowered (0 wide)", s.minWidth, s.minHeight)
	}

	// Setting the minimum again on the zoomed window keeps it zoomed.
	if s.zoomed {
		onMain(func() { w.setWailsMinSize(minWidth, minHeight) })
		after := w.stateOnMain()
		if !after.zoomed || after.width != s.width || after.height != s.height {
			t.Errorf("setting the minimum on a zoomed window: zoomed %v, frame %v×%v; want it still zoomed at %v×%v",
				after.zoomed, after.width, after.height, s.width, s.height)
		}
		onMain(func() { w.setWailsMinSize(0, 0) })
	} else {
		t.Log("zoom: did not zoom a window that was never shown; the zoomed-minimum check is skipped")
	}

	// An edge dragged: a new frame, which is no longer the zoomed one.
	onMain(func() { w.setFrameSize(1000, 650) })
	s = w.stateOnMain()
	if s.zoomed {
		t.Fatalf("after a frame change the window is still zoomed (%v×%v)", s.width, s.height)
	}
	if s.minWidth >= minWidth {
		t.Fatalf("after the frame change the minimum = %v×%v; the probe expects it left lowered", s.minWidth, s.minHeight)
	}
	// Nothing stops the window from going below 960×600 now.
	onMain(func() { w.setFrameSize(530, 400) })
	if s := w.stateOnMain(); s.width != 530 {
		t.Fatalf("frame = %v×%v; want 530 wide", s.width, s.height)
	}
}

// TestKeepMinSize_RestoresTheMinimum: the sequence zoom (Wails' way) →
// frame change → un-zoom leaves the minimum 0 wide; KeepMinSize puts the minimum back,
// and grows a window left below it to the minimum, keeping its top-left
// corner.
func TestKeepMinSize_RestoresTheMinimum(t *testing.T) {
	w := newWindow(t, 1100, 700)
	onMain(func() {
		w.setWailsMinSize(0, 0)
		w.zoom()
		w.setFrameSize(1000, 650)
		if w.state().zoomed {
			w.zoom()
		}
		w.setFrameSize(530, 400)
	})
	before := w.stateOnMain()
	if before.minWidth >= minWidth || before.width != 530 {
		t.Fatalf("set-up: minimum %v×%v, frame %v×%v; want it lowered and 530×400", before.minWidth, before.minHeight, before.width, before.height)
	}

	var changed bool
	var err error
	onMain(func() { changed, err = New().KeepMinSize(w.pointer(), minWidth, minHeight) })
	if err != nil || !changed {
		t.Fatalf("KeepMinSize = %v, %v; want true, nil", changed, err)
	}
	s := w.stateOnMain()
	if !w.hasWailsMin(s) {
		t.Errorf("minimum = %v×%v, content %v×%v; want Wails' %v×%v, content %v×%v", s.minWidth, s.minHeight,
			s.contentMinWidth, s.contentMinHeight, w.min.minWidth, w.min.minHeight, w.min.contentMinWidth, w.min.contentMinHeight)
	}
	if s.width != w.min.minWidth || s.height != w.min.minHeight {
		t.Errorf("frame = %v×%v; want grown to the minimum %v×%v", s.width, s.height, w.min.minWidth, w.min.minHeight)
	}
	if s.top != before.top || s.left != before.left {
		t.Errorf("top-left = (%v, %v); want it kept at (%v, %v)", s.left, s.top, before.left, before.top)
	}

	// Already right: nothing to do.
	onMain(func() { changed, err = New().KeepMinSize(w.pointer(), minWidth, minHeight) })
	if err != nil || changed {
		t.Errorf("KeepMinSize again = %v, %v; want false, nil", changed, err)
	}
}

// TestKeepMinSize_ZoomedWindowStaysZoomed: the minimum is put back on a
// window Wails zoomed (minimum lowered) without un-zooming it, so that a later
// un-zoom by any path keeps it.
func TestKeepMinSize_ZoomedWindowStaysZoomed(t *testing.T) {
	w := newWindow(t, 1100, 700)
	onMain(func() {
		w.setWailsMinSize(0, 0)
		w.zoom()
	})
	zoomed := w.stateOnMain()
	if !zoomed.zoomed {
		t.Skip("zoom: did not zoom a window that was never shown")
	}
	var err error
	onMain(func() { _, err = New().KeepMinSize(w.pointer(), minWidth, minHeight) })
	if err != nil {
		t.Fatal(err)
	}
	s := w.stateOnMain()
	if !s.zoomed || s.width != zoomed.width || s.height != zoomed.height {
		t.Errorf("after KeepMinSize: zoomed %v, frame %v×%v; want still zoomed at %v×%v", s.zoomed, s.width, s.height, zoomed.width, zoomed.height)
	}
	if !w.hasWailsMin(s) {
		t.Errorf("minimum = %v×%v; want Wails' %v×%v", s.minWidth, s.minHeight, w.min.minWidth, w.min.minHeight)
	}
	// Un-zoomed by AppKit (the green button): back to the user frame, and the minimum holds.
	onMain(w.zoom)
	s = w.stateOnMain()
	if !w.hasWailsMin(s) || s.width != 1100 || s.height != 700 {
		t.Errorf("after un-zoom: minimum %v×%v, frame %v×%v; want Wails' %v×%v and 1100×700", s.minWidth, s.minHeight, s.width, s.height, w.min.minWidth, w.min.minHeight)
	}
}

func TestKeepMinSize_RefusesBadCalls(t *testing.T) {
	if _, err := New().KeepMinSize(nil, minWidth, minHeight); err == nil {
		t.Error("KeepMinSize(nil) = nil error; want an error")
	}
	var notAWindow byte
	_, err := New().KeepMinSize(unsafe.Pointer(&notAWindow), minWidth, minHeight)
	if err == nil || !strings.Contains(err.Error(), "main thread") {
		t.Errorf("KeepMinSize off the main thread = %v; want a main-thread error", err)
	}
}

// TestFocusPage_MakesTheWebViewFirstResponder: Wails v3.1.0 never makes
// the WKWebView first responder, so a window shown without a click keeps
// the window itself as first responder and the page gets no keys.
// FocusPage hands the keyboard to the web view; it leaves alone a window
// whose first responder is another view, or that has a sheet.
func TestFocusPage_MakesTheWebViewFirstResponder(t *testing.T) {
	w := newWindow(t, 1100, 700)
	onMain(w.makeWindowFirstResponder)
	if s := w.stateOnMain(); s.webViewFirstResponder {
		t.Fatal("set-up: the web view is already first responder")
	}
	var focused bool
	var err error
	onMain(func() { focused, err = New().FocusPage(w.pointer()) })
	if err != nil || !focused {
		t.Fatalf("FocusPage = %v, %v; want true, nil", focused, err)
	}
	if s := w.stateOnMain(); !s.webViewFirstResponder {
		t.Fatal("after FocusPage the web view is not the window's first responder")
	}
	// Already the web view: nothing to do.
	onMain(func() { focused, err = New().FocusPage(w.pointer()) })
	if err != nil || focused {
		t.Errorf("FocusPage again = %v, %v; want false, nil", focused, err)
	}

	// Another view has the focus: it keeps it.
	onMain(w.makeOtherViewFirstResponder)
	onMain(func() { focused, err = New().FocusPage(w.pointer()) })
	if err != nil || focused {
		t.Errorf("FocusPage with another view focused = %v, %v; want false, nil", focused, err)
	}
	if s := w.stateOnMain(); s.webViewFirstResponder {
		t.Error("FocusPage took the focus from another view")
	}
}

func TestFocusPage_RefusesBadCalls(t *testing.T) {
	if _, err := New().FocusPage(nil); err == nil {
		t.Error("FocusPage(nil) = nil error; want an error")
	}
	var notAWindow byte
	_, err := New().FocusPage(unsafe.Pointer(&notAWindow))
	if err == nil || !strings.Contains(err.Error(), "main thread") {
		t.Errorf("FocusPage off the main thread = %v; want a main-thread error", err)
	}
}

// TestSetPageBackground_UnderPage: what Wails leaves at WebKit's default,
// the colour drawn where the page rubber-bands, becomes the window's
// background colour (sRGB 18, 20, 21).
func TestSetPageBackground_UnderPage(t *testing.T) {
	w := newWindow(t, 1100, 700)
	t.Logf("WebKit's default under-page colour (sRGB): %v", w.stateOnMain().underPage)
	var err error
	onMain(func() { err = New().SetPageBackground(w.pointer(), 18, 20, 21) })
	if err != nil {
		t.Fatal(err)
	}
	s := w.stateOnMain()
	got := [3]int{int(s.underPage[0]*255 + 0.5), int(s.underPage[1]*255 + 0.5), int(s.underPage[2]*255 + 0.5)}
	if got != [3]int{18, 20, 21} || s.underPage[3] != 1 {
		t.Fatalf("underPageBackgroundColor = %v (sRGB %v); want 18, 20, 21, opaque", s.underPage, got)
	}
}

func TestSetPageBackground_RefusesBadCalls(t *testing.T) {
	if err := New().SetPageBackground(nil, 18, 20, 21); err == nil {
		t.Error("SetPageBackground(nil) = nil; want an error")
	}
	var notAWindow byte
	err := New().SetPageBackground(unsafe.Pointer(&notAWindow), 18, 20, 21)
	if err == nil || !strings.Contains(err.Error(), "main thread") {
		t.Errorf("SetPageBackground off the main thread = %v; want a main-thread error", err)
	}
}

func TestGuard_CatchesExceptions(t *testing.T) {
	err := raiseForTest()
	if err == nil || !strings.Contains(err.Error(), "ChoixTestException") {
		t.Fatalf("raiseForTest = %v; want the exception as an error", err)
	}
}
