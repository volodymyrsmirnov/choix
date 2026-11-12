package platformtest

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// TitleBar is a platform.TitleBar that answers from its fields instead of
// asking AppKit. Like the real one, ButtonsRight refuses a nil window. The
// zero value measures 0 and reads an unset preference; set the fields
// before use. It is safe for concurrent use.
type TitleBar struct {
	// Right is what ButtonsRight returns for a non-nil window.
	Right float64
	// RightErr, when set, is ButtonsRight's error.
	RightErr error
	// CenterY is what ButtonsCenterY returns for a non-nil window.
	CenterY float64
	// CenterYErr, when set, is ButtonsCenterY's error.
	CenterYErr error
	// Preference is what DoubleClickPreference returns.
	Preference string
	// PreferenceErr, when set, is DoubleClickPreference's error.
	PreferenceErr error

	mu       sync.Mutex
	measured []unsafe.Pointer
}

var _ platform.TitleBar = (*TitleBar)(nil)

// ButtonsCenterY returns CenterY, or CenterYErr; it refuses a nil window.
func (t *TitleBar) ButtonsCenterY(window unsafe.Pointer) (float64, error) {
	if window == nil {
		return 0, errors.New("platformtest: title bar: no window")
	}
	if t.CenterYErr != nil {
		return 0, t.CenterYErr
	}
	return t.CenterY, nil
}

// ButtonsRight records window and returns Right, or RightErr.
func (t *TitleBar) ButtonsRight(window unsafe.Pointer) (float64, error) {
	if window == nil {
		return 0, errors.New("platformtest: title bar: no window")
	}
	t.mu.Lock()
	t.measured = append(t.measured, window)
	t.mu.Unlock()
	if t.RightErr != nil {
		return 0, t.RightErr
	}
	return t.Right, nil
}

// DoubleClickPreference returns Preference, or PreferenceErr.
func (t *TitleBar) DoubleClickPreference() (string, error) {
	if t.PreferenceErr != nil {
		return "", t.PreferenceErr
	}
	return t.Preference, nil
}

// Measured returns the windows ButtonsRight measured, in order.
func (t *TitleBar) Measured() []unsafe.Pointer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]unsafe.Pointer(nil), t.measured...)
}
