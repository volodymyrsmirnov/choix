//go:build darwin

// Package windowchrome implements platform.WindowChrome on AppKit and
// WebKit, called in-process through cgo: the main window's minimum size
// (NSWindow minSize and contentMinSize, which Wails v3.1.0 sets to 0×0
// whenever it maximises the window and restores only through its own
// UnMaximise), the keyboard handed to the page (makeFirstResponder: with
// the window's WKWebView, which Wails never does), and the colour under
// the page (WKWebView underPageBackgroundColor, which Wails leaves at
// WebKit's default). Public API only; nothing is swizzled.
//
// Every method changes views, so it must be called on the main thread (its
// caller, internal/app/window, runs it there through Wails); it refuses any
// other thread before touching the window.
package windowchrome

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework AppKit -framework WebKit
#include <stdlib.h>
#include "windowchrome.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

type chrome struct{}

// New returns the AppKit implementation of platform.WindowChrome.
func New() platform.WindowChrome { return chrome{} }

// errFromNative converts a native failure message, freeing it; nil means
// success.
func errFromNative(msg *C.char) error {
	if msg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

// KeepMinSize implements platform.WindowChrome.
func (chrome) KeepMinSize(window unsafe.Pointer, width, height float64) (bool, error) {
	if window == nil {
		return false, errors.New("windowchrome: keep the minimum size: no window")
	}
	var changed C.int
	if err := errFromNative(C.choix_window_keep_min_size(window, C.double(width), C.double(height), &changed)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &changed
		return false, fmt.Errorf("windowchrome: keep the minimum size: %w", err)
	}
	return changed != 0, nil
}

// FocusPage implements platform.WindowChrome.
func (chrome) FocusPage(window unsafe.Pointer) (bool, error) {
	if window == nil {
		return false, errors.New("windowchrome: focus the page: no window")
	}
	var focused C.int
	if err := errFromNative(C.choix_window_focus_page(window, &focused)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &focused
		return false, fmt.Errorf("windowchrome: focus the page: %w", err)
	}
	return focused != 0, nil
}

// SetPageBackground implements platform.WindowChrome.
func (chrome) SetPageBackground(window unsafe.Pointer, red, green, blue uint8) error {
	if window == nil {
		return errors.New("windowchrome: page background: no window")
	}
	err := errFromNative(C.choix_window_set_page_background(window,
		C.double(float64(red)/255), C.double(float64(green)/255), C.double(float64(blue)/255)))
	if err != nil {
		return fmt.Errorf("windowchrome: page background: %w", err)
	}
	return nil
}

// testWindow is a window made by newTestWindow: like the main one, never
// shown. Its methods run on the main thread only.
type testWindow struct{ p unsafe.Pointer }

// testWindowState is what the tests read of a testWindow.
type testWindowState struct {
	minWidth, minHeight               float64
	contentMinWidth, contentMinHeight float64
	width, height                     float64
	left, top                         float64
	zoomed                            bool
	webViewFirstResponder             bool
	underPage                         [4]float64 // sRGB red, green, blue, alpha in 0…1
}

func newTestWindow(width, height float64) (*testWindow, error) {
	var p unsafe.Pointer
	if err := errFromNative(C.choix_window_new_for_test(C.double(width), C.double(height), &p)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &p
		return nil, err
	}
	return &testWindow{p: p}, nil
}

func (w *testWindow) pointer() unsafe.Pointer { return w.p }

func (w *testWindow) close() { C.choix_window_close_for_test(w.p) }

func (w *testWindow) state() testWindowState {
	var s C.choix_window_test_state
	C.choix_window_state_for_test(w.p, &s)
	return testWindowState{
		minWidth:              float64(s.minWidth),
		minHeight:             float64(s.minHeight),
		contentMinWidth:       float64(s.contentMinWidth),
		contentMinHeight:      float64(s.contentMinHeight),
		width:                 float64(s.width),
		height:                float64(s.height),
		left:                  float64(s.left),
		top:                   float64(s.top),
		zoomed:                s.zoomed != 0,
		webViewFirstResponder: s.webViewFirstResponder != 0,
		underPage: [4]float64{
			float64(s.underPage[0]), float64(s.underPage[1]), float64(s.underPage[2]), float64(s.underPage[3]),
		},
	}
}

// setWailsMinSize is Wails v3.1.0's windowSetMinSize.
func (w *testWindow) setWailsMinSize(width, height float64) {
	C.choix_window_set_wails_min_size_for_test(w.p, C.double(width), C.double(height))
}

// zoom is AppKit's zoom: (the zoom button, Wails' Zoom and its maximise).
func (w *testWindow) zoom() { C.choix_window_zoom_for_test(w.p) }

// setFrameSize resizes the window keeping its top-left corner, as an edge
// dragged does.
func (w *testWindow) setFrameSize(width, height float64) {
	C.choix_window_set_frame_size_for_test(w.p, C.double(width), C.double(height))
}

// makeWindowFirstResponder leaves no view with the keyboard, as a window
// shown without a click is.
func (w *testWindow) makeWindowFirstResponder() {
	C.choix_window_make_window_first_responder_for_test(w.p)
}

// makeOtherViewFirstResponder gives the keyboard to a text field beside
// the web view.
func (w *testWindow) makeOtherViewFirstResponder() {
	C.choix_window_make_other_view_first_responder_for_test(w.p)
}

// raiseForTest raises an NSException inside the entry points' guard and
// returns the resulting error.
func raiseForTest() error {
	return errFromNative(C.choix_window_raise_for_test())
}
