//go:build darwin

package titlebar

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// The main goroutine stays on the main thread, so TestMain can make and
// measure a real window there (AppKit allows windows on the main thread
// only); the tests themselves run on other threads.
func init() { runtime.LockOSThread() }

// probe is the measurement of a real window made like Wails'
// MacTitleBarHiddenInset window, taken on the main thread by TestMain.
var probe struct {
	right   float64
	centerY float64
	err     error
}

func TestMain(m *testing.M) {
	probe.right, probe.centerY, probe.err = probeHiddenInsetForTest()
	os.Exit(m.Run())
}

// TestButtonsRight_HiddenInsetWindow: on a window with the hidden-inset
// title bar the main window uses, the traffic lights end where AppKit draws
// them — the controller measured x = 79 on macOS 27.2, about 72 on earlier
// releases — so the edge lies well inside the first 120 points and right
// of where the first button starts.
func TestButtonsRight_HiddenInsetWindow(t *testing.T) {
	if probe.err != nil {
		t.Fatalf("measuring a hidden-inset window: %v", probe.err)
	}
	t.Logf("traffic lights end at x = %v points", probe.right)
	if probe.right < 50 || probe.right > 120 {
		t.Fatalf("ButtonsRight = %v points; want the traffic lights' right edge, between 50 and 120", probe.right)
	}
}

// TestButtonsCenterY_HiddenInsetWindow: on the same window the traffic
// lights' common vertical centre lies where AppKit draws them — the owner
// measured the 14-point buttons at y = 19…33 from the window's top on
// macOS 27.2, a centre of 26 — so well inside the first 50 points, and
// below the window's top edge.
func TestButtonsCenterY_HiddenInsetWindow(t *testing.T) {
	if probe.err != nil {
		t.Fatalf("measuring a hidden-inset window: %v", probe.err)
	}
	t.Logf("traffic lights centred at y = %v points from the window's top", probe.centerY)
	if probe.centerY < 8 || probe.centerY > 50 {
		t.Fatalf("ButtonsCenterY = %v points; want the traffic lights' centre, between 8 and 50 from the top", probe.centerY)
	}
}

func TestButtonsCenterY_NilWindow(t *testing.T) {
	y, err := New().ButtonsCenterY(nil)
	if err == nil || y != 0 {
		t.Fatalf("ButtonsCenterY(nil) = %v, %v; want 0 and an error", y, err)
	}
}

// TestButtonsCenterY_OffTheMainThread: refused before the pointer is
// touched, as ButtonsRight.
func TestButtonsCenterY_OffTheMainThread(t *testing.T) {
	var notAWindow byte
	y, err := New().ButtonsCenterY(unsafe.Pointer(&notAWindow))
	if err == nil || !strings.Contains(err.Error(), "main thread") || y != 0 {
		t.Fatalf("ButtonsCenterY off the main thread = %v, %v; want 0 and a main-thread error", y, err)
	}
}

func TestButtonsRight_NilWindow(t *testing.T) {
	right, err := New().ButtonsRight(nil)
	if err == nil || right != 0 {
		t.Fatalf("ButtonsRight(nil) = %v, %v; want 0 and an error", right, err)
	}
}

// TestButtonsRight_OffTheMainThread: AppKit views may be read on the main
// thread only, so a call from elsewhere is refused before the pointer is
// touched (the pointer here is never dereferenced).
func TestButtonsRight_OffTheMainThread(t *testing.T) {
	var notAWindow byte
	right, err := New().ButtonsRight(unsafe.Pointer(&notAWindow))
	if err == nil || !strings.Contains(err.Error(), "main thread") || right != 0 {
		t.Fatalf("ButtonsRight off the main thread = %v, %v; want 0 and a main-thread error", right, err)
	}
}

// TestDoubleClickPreference_Reads: the user's setting is read without
// error. Its value is the machine's; only its shape is checked.
func TestDoubleClickPreference_Reads(t *testing.T) {
	pref, err := New().DoubleClickPreference()
	if err != nil {
		t.Fatalf("DoubleClickPreference: %v", err)
	}
	if strings.ContainsRune(pref, 0) {
		t.Fatalf("DoubleClickPreference = %q; want a plain string", pref)
	}
}

func TestGuard_CatchesExceptions(t *testing.T) {
	err := raiseForTest()
	if err == nil || !strings.Contains(err.Error(), "ChoixTestException") {
		t.Fatalf("raiseForTest = %v; want the exception as an error", err)
	}
}
