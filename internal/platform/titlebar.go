package platform

import "unsafe"

// TitleBar reads, through AppKit, what a window drawing its own title bar
// needs: where the window's standard buttons (the traffic lights) end and
// where their vertical centre lies, and what the user chose a double-click on a title bar to do. It only reads:
// it never moves, hides or restyles a button.
type TitleBar interface {
	// ButtonsRight returns the right edge, in points from the left edge of
	// the window, of the right-most of the window's standard buttons
	// (close, miniaturise, zoom; the largest of the three, whatever their
	// order). window is the NSWindow pointer Wails hands out
	// (WebviewWindow.NativeWindow). It must be called on the main thread.
	// An error when window is nil or has none of the buttons.
	ButtonsRight(window unsafe.Pointer) (float64, error)
	// ButtonsCenterY returns the common vertical centre of the window's
	// standard buttons (the middle of the box around the close, miniaturise
	// and zoom buttons), in points from the top edge of the window's frame:
	// with the full-size content view the main window uses, CSS pixels from
	// the top of the page. Same window and thread rules as ButtonsRight.
	ButtonsCenterY(window unsafe.Pointer) (float64, error)
	// DoubleClickPreference returns the user's AppleActionOnDoubleClick
	// setting ("Maximize", "Minimize", "Fill", "None", or a value a later
	// macOS adds), "" when it is not set.
	DoubleClickPreference() (string, error)
}
