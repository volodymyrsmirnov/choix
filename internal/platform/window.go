package platform

import "unsafe"

// WindowChrome does, through AppKit, what Wails leaves undone on the main
// window: it keeps the window's minimum size, hands the keyboard to the
// page, and colours what lies under the page. Every method takes the
// NSWindow pointer Wails hands out (WebviewWindow.NativeWindow), must be
// called on the main thread, and returns an error for a nil window.
type WindowChrome interface {
	// KeepMinSize makes the window's minimum frame size (and the matching
	// content minimum) at least width×height points, and grows a window
	// that is smaller than that, keeping its top-left corner, unless it is
	// zoomed or being resized by the user. It does nothing in full screen.
	// It reports whether it changed anything.
	KeepMinSize(window unsafe.Pointer, width, height float64) (bool, error)
	// FocusPage makes the window's web view its first responder when no
	// view has the keyboard: the first responder is the window itself or
	// none, and no sheet is attached. It reports whether it did.
	FocusPage(window unsafe.Pointer) (bool, error)
	// SetPageBackground sets the colour the window's web view draws under
	// the page (where it rubber-bands) to the sRGB colour red, green, blue.
	SetPageBackground(window unsafe.Pointer, red, green, blue uint8) error
}
