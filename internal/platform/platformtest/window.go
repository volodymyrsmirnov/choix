package platformtest

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// WindowChrome is a platform.WindowChrome that records its calls instead of
// changing a window. Like the real one, every method refuses a nil window.
// The zero value succeeds and reports no change; set the fields before use.
// It is safe for concurrent use.
type WindowChrome struct {
	// Changed is what KeepMinSize reports.
	Changed bool
	// KeepErr, when set, is KeepMinSize's error.
	KeepErr error
	// Focused is what FocusPage reports.
	Focused bool
	// FocusErr, when set, is FocusPage's error.
	FocusErr error
	// BackgroundErr, when set, is SetPageBackground's error.
	BackgroundErr error

	mu          sync.Mutex
	minSizes    []MinSizeCall
	focuses     []unsafe.Pointer
	backgrounds []BackgroundCall
}

// MinSizeCall is one KeepMinSize call.
type MinSizeCall struct {
	Window        unsafe.Pointer
	Width, Height float64
}

// BackgroundCall is one SetPageBackground call.
type BackgroundCall struct {
	Window           unsafe.Pointer
	Red, Green, Blue uint8
}

var _ platform.WindowChrome = (*WindowChrome)(nil)

var errNoWindow = errors.New("platformtest: window chrome: no window")

// KeepMinSize records the call and returns Changed, or KeepErr.
func (c *WindowChrome) KeepMinSize(window unsafe.Pointer, width, height float64) (bool, error) {
	if window == nil {
		return false, errNoWindow
	}
	c.mu.Lock()
	c.minSizes = append(c.minSizes, MinSizeCall{Window: window, Width: width, Height: height})
	c.mu.Unlock()
	if c.KeepErr != nil {
		return false, c.KeepErr
	}
	return c.Changed, nil
}

// FocusPage records the call and returns Focused, or FocusErr.
func (c *WindowChrome) FocusPage(window unsafe.Pointer) (bool, error) {
	if window == nil {
		return false, errNoWindow
	}
	c.mu.Lock()
	c.focuses = append(c.focuses, window)
	c.mu.Unlock()
	if c.FocusErr != nil {
		return false, c.FocusErr
	}
	return c.Focused, nil
}

// SetPageBackground records the call and returns BackgroundErr.
func (c *WindowChrome) SetPageBackground(window unsafe.Pointer, red, green, blue uint8) error {
	if window == nil {
		return errNoWindow
	}
	c.mu.Lock()
	c.backgrounds = append(c.backgrounds, BackgroundCall{Window: window, Red: red, Green: green, Blue: blue})
	c.mu.Unlock()
	return c.BackgroundErr
}

// MinSizes returns the KeepMinSize calls, in order.
func (c *WindowChrome) MinSizes() []MinSizeCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]MinSizeCall(nil), c.minSizes...)
}

// Focuses returns the windows FocusPage was called with, in order.
func (c *WindowChrome) Focuses() []unsafe.Pointer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]unsafe.Pointer(nil), c.focuses...)
}

// Backgrounds returns the SetPageBackground calls, in order.
func (c *WindowChrome) Backgrounds() []BackgroundCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]BackgroundCall(nil), c.backgrounds...)
}
