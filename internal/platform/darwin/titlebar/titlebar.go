//go:build darwin

// Package titlebar implements platform.TitleBar on AppKit, called
// in-process through cgo: the right edge and the vertical centre of a
// window's traffic lights (standardWindowButton: and frame conversion to
// window coordinates) and the
// user's title-bar double-click setting (NSUserDefaults). Public API only;
// nothing is moved, hidden or swizzled.
//
// ButtonsRight and ButtonsCenterY read views, so they must be called on the main thread (its
// caller, internal/app/window, runs it there through Wails); it refuses any
// other thread. DoubleClickPreference may be called from any goroutine.
package titlebar

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework AppKit
#include <stdlib.h>
#include "titlebar.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

type titleBar struct{}

// New returns the AppKit implementation of platform.TitleBar.
func New() platform.TitleBar { return titleBar{} }

// errFromNative converts a native failure message, freeing it; nil means
// success.
func errFromNative(msg *C.char) error {
	if msg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

// ButtonsRight implements platform.TitleBar.
func (titleBar) ButtonsRight(window unsafe.Pointer) (float64, error) {
	if window == nil {
		return 0, errors.New("titlebar: buttons right: no window")
	}
	var right C.double
	if err := errFromNative(C.choix_titlebar_buttons_right(window, &right)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &right
		return 0, fmt.Errorf("titlebar: buttons right: %w", err)
	}
	return float64(right), nil
}

// ButtonsCenterY implements platform.TitleBar.
func (titleBar) ButtonsCenterY(window unsafe.Pointer) (float64, error) {
	if window == nil {
		return 0, errors.New("titlebar: buttons centre: no window")
	}
	var y C.double
	if err := errFromNative(C.choix_titlebar_buttons_center_y(window, &y)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &y
		return 0, fmt.Errorf("titlebar: buttons centre: %w", err)
	}
	return float64(y), nil
}

// DoubleClickPreference implements platform.TitleBar.
func (titleBar) DoubleClickPreference() (string, error) {
	var value *C.char
	if err := errFromNative(C.choix_titlebar_double_click_preference(&value)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &value
		return "", fmt.Errorf("titlebar: double-click preference: %w", err)
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value), nil
}

// probeHiddenInsetForTest measures a window made like Wails'
// MacTitleBarHiddenInset one: the buttons' right edge and vertical centre.
// Main thread only.
func probeHiddenInsetForTest() (right, centerY float64, err error) {
	var r, y C.double
	if err := errFromNative(C.choix_titlebar_probe_for_test(&r, &y)); err != nil { //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer checks
		return 0, 0, err
	}
	return float64(r), float64(y), nil
}

// raiseForTest raises an NSException inside the entry points' guard and
// returns the resulting error.
func raiseForTest() error {
	return errFromNative(C.choix_titlebar_raise_for_test())
}
