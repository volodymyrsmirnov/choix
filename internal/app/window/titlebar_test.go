package window

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// wailsV310DoubleClick is Wails v3.1.0's own mapping
// (parseMacTitlebarDoubleClickAction in
// pkg/application/titlebar_doubleclick_action.go), which it applies to
// every `wails:drag:doubleclick` message (webview_window_titlebar_doubleclick.go):
// a preference missing here is one Wails does nothing for.
var wailsV310DoubleClick = map[string]doubleClickAction{
	"":         doubleClickZoom,
	"Maximize": doubleClickZoom,
	"Minimize": doubleClickMinimise,
}

// preferences are the AppleActionOnDoubleClick values the tests try: the
// ones macOS writes today, an unset one, and values a later macOS might add.
var preferences = []string{"", "Maximize", "Minimize", "Fill", "None", "Zoom", "maximize", "SomethingNew"}

// TestWailsActsOn_IsWailsV310: the set Choix leaves to Wails is exactly
// the set Wails v3.1.0 acts on, and go.mod still pins v3.1.0 — an upgrade
// fails here until the set is checked against the new Wails source.
func TestWailsActsOn_IsWailsV310(t *testing.T) {
	gomod, err := os.ReadFile(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*github\.com/wailsapp/wails/v3 v3\.1\.0$`).Match(gomod) {
		t.Fatal("go.mod no longer requires github.com/wailsapp/wails/v3 v3.1.0: check its title-bar double-click mapping " +
			"(pkg/application/titlebar_doubleclick_action.go) and update wailsActsOn and wailsV310DoubleClick")
	}
	for _, pref := range preferences {
		_, wails := wailsV310DoubleClick[pref]
		if got := wailsActsOn(pref); got != wails {
			t.Errorf("wailsActsOn(%q) = %v; want %v", pref, got, wails)
		}
	}
}

// TestNativeDoubleClick: zoom for Maximize, Fill, an unset value and any
// value that is not Minimize or None; minimise for Minimize; nothing for
// None.
func TestNativeDoubleClick(t *testing.T) {
	want := map[string]doubleClickAction{
		"":             doubleClickZoom,
		"Maximize":     doubleClickZoom,
		"Fill":         doubleClickZoom,
		"Zoom":         doubleClickZoom,
		"maximize":     doubleClickZoom,
		"SomethingNew": doubleClickZoom,
		"Minimize":     doubleClickMinimise,
		"None":         doubleClickNothing,
	}
	for _, pref := range preferences {
		if got := nativeDoubleClick(pref); got != want[pref] {
			t.Errorf("nativeDoubleClick(%q) = %v; want %v", pref, got, want[pref])
		}
	}
}

// TestChoixDoubleClick_ExactlyOneActs: for every preference, at most one of
// Wails and Choix acts on a double-click, and together they do what macOS
// does — Choix never acts where Wails does.
func TestChoixDoubleClick_ExactlyOneActs(t *testing.T) {
	for _, pref := range preferences {
		wails := wailsV310DoubleClick[pref]
		choix := choixDoubleClick(pref)
		if wails != doubleClickNothing && choix != doubleClickNothing {
			t.Errorf("%q: Wails does %v and Choix does %v: the window would act twice", pref, wails, choix)
		}
		got := wails
		if got == doubleClickNothing {
			got = choix
		}
		if want := nativeDoubleClick(pref); got != want {
			t.Errorf("%q: Wails %v + Choix %v = %v; want macOS's %v", pref, wails, choix, got, want)
		}
	}
	if got := choixDoubleClick("Fill"); got != doubleClickZoom {
		t.Errorf(`choixDoubleClick("Fill") = %v; want zoom (the owner's setting, which Wails ignores)`, got)
	}
}

// fakeWindow is a chromeWindow that records what it is asked to do.
type fakeWindow struct {
	mu              sync.Mutex
	native          unsafe.Pointer
	fullscreen      bool
	fullscreenReads int
	zooms           int // AppKit's zoom: (Wails' Zoom), which keeps the minimum size
	maximises       int // Wails' ToggleMaximise, which drops the minimum size
	minimises       int
}

func (w *fakeWindow) NativeWindow() unsafe.Pointer { return w.native }

func (w *fakeWindow) IsFullscreen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fullscreenReads++
	return w.fullscreen
}

func (w *fakeWindow) Zoom() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.zooms++
}

func (w *fakeWindow) ToggleMaximise() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.maximises++
}

func (w *fakeWindow) Minimise() application.Window {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.minimises++
	return nil
}

// mainThread stands for application.InvokeSync: it runs fn at once and
// counts the calls.
type mainThread struct{ calls int }

func (m *mainThread) run(fn func()) {
	m.calls++
	fn()
}

var nsWindowStandIn byte

// readyTitleBar is newTitleBar with the window already ready (shown, or
// its page's runtime loaded).
func readyTitleBar(w chromeWindow, native *platformtest.TitleBar, onMain func(func())) *TitleBar {
	tb := newTitleBar(w, native, onMain)
	tb.markReady()
	return tb
}

func TestTitleBar_ButtonsRight_MeasuredOnceOnTheMainThread(t *testing.T) {
	w := &fakeWindow{native: unsafe.Pointer(&nsWindowStandIn)}
	native := &platformtest.TitleBar{Right: 79, CenterY: 26}
	main := &mainThread{}
	tb := readyTitleBar(w, native, main.run)

	for range 3 {
		if got := tb.ButtonsRight(); got != 79 {
			t.Fatalf("ButtonsRight = %v; want 79", got)
		}
		if got := tb.ButtonsCenterY(); got != 26 {
			t.Fatalf("ButtonsCenterY = %v; want 26", got)
		}
	}
	if main.calls != 1 {
		t.Errorf("main-thread calls = %d; want 1 (both are measured in one trip and kept)", main.calls)
	}
	if m := native.Measured(); len(m) != 1 || m[0] != unsafe.Pointer(&nsWindowStandIn) {
		t.Errorf("measured windows = %v; want the window's NSWindow once", m)
	}
}

// TestTitleBar_ButtonsRight_UnknownIsZero: no native window yet, a failed
// measurement or a value that is not a positive number all read 0 (the
// frontend keeps the design's inset), and are measured again next time.
func TestTitleBar_ButtonsRight_UnknownIsZero(t *testing.T) {
	cases := []struct {
		name   string
		window unsafe.Pointer
		native *platformtest.TitleBar
	}{
		{"no window", nil, &platformtest.TitleBar{Right: 79}},
		{"failed", unsafe.Pointer(&nsWindowStandIn), &platformtest.TitleBar{RightErr: errors.New("no buttons")}},
		{"zero", unsafe.Pointer(&nsWindowStandIn), &platformtest.TitleBar{Right: 0}},
		{"negative", unsafe.Pointer(&nsWindowStandIn), &platformtest.TitleBar{Right: -3}},
		{"NaN", unsafe.Pointer(&nsWindowStandIn), &platformtest.TitleBar{Right: math.NaN()}},
		{"infinite", unsafe.Pointer(&nsWindowStandIn), &platformtest.TitleBar{Right: math.Inf(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			main := &mainThread{}
			tb := readyTitleBar(&fakeWindow{native: tc.window}, tc.native, main.run)
			for range 2 {
				if got := tb.ButtonsRight(); got != 0 {
					t.Fatalf("ButtonsRight = %v; want 0", got)
				}
			}
			if main.calls != 2 {
				t.Errorf("main-thread calls = %d; want 2 (an unknown value is not kept)", main.calls)
			}
		})
	}
}

// TestTitleBar_ButtonsCenterY_UnknownIsZero: a centre that is not a
// positive number, or a failed measurement, reads 0 and is measured again
// next time, alone (the right edge, known, is kept).
func TestTitleBar_ButtonsCenterY_UnknownIsZero(t *testing.T) {
	for _, native := range []*platformtest.TitleBar{
		{Right: 79, CenterY: 0},
		{Right: 79, CenterY: -1},
		{Right: 79, CenterY: math.NaN()},
		{Right: 79, CenterYErr: errors.New("no buttons")},
	} {
		main := &mainThread{}
		tb := readyTitleBar(&fakeWindow{native: unsafe.Pointer(&nsWindowStandIn)}, native, main.run)
		if got := tb.ButtonsRight(); got != 79 {
			t.Fatalf("ButtonsRight = %v; want 79", got)
		}
		for range 2 {
			if got := tb.ButtonsCenterY(); got != 0 {
				t.Fatalf("ButtonsCenterY (%v, %v) = %v; want 0", native.CenterY, native.CenterYErr, got)
			}
		}
		if main.calls != 3 {
			t.Errorf("main-thread calls = %d; want 3 (an unknown centre is not kept)", main.calls)
		}
		if n := len(native.Measured()); n != 1 {
			t.Errorf("right edge measured %d times; want 1 (kept)", n)
		}
	}
	// No window yet: 0, and nothing measured.
	tb := readyTitleBar(&fakeWindow{}, &platformtest.TitleBar{Right: 79, CenterY: 26}, (&mainThread{}).run)
	if got := tb.ButtonsCenterY(); got != 0 {
		t.Errorf("ButtonsCenterY without a native window = %v; want 0", got)
	}
}

// TestTitleBar_DoubleClick: Choix zooms the window only for the preferences
// Wails v3.1.0 leaves alone, and never in full screen. It zooms the way
// the zoom button does (AppKit's zoom:, Wails' Zoom), never through Wails'
// ToggleMaximise, whose Maximise sets the window's minimum size to 0×0
// until Wails' own UnMaximise (the owner resized the window to about 530
// points wide after a double-click).
func TestTitleBar_DoubleClick(t *testing.T) {
	cases := []struct {
		pref       string
		prefErr    error
		fullscreen bool
		zooms      int
	}{
		{pref: "Fill", zooms: 1},
		{pref: "SomethingNew", zooms: 1},
		{pref: "Fill", fullscreen: true},
		{pref: ""},
		{pref: "Maximize"},
		{pref: "Minimize"},
		{pref: "None"},
		{pref: "Fill", prefErr: errors.New("defaults unreadable")},
	}
	for _, tc := range cases {
		w := &fakeWindow{native: unsafe.Pointer(&nsWindowStandIn), fullscreen: tc.fullscreen}
		main := &mainThread{}
		tb := readyTitleBar(w, &platformtest.TitleBar{Preference: tc.pref, PreferenceErr: tc.prefErr}, main.run)
		tb.DoubleClick()
		if w.zooms != tc.zooms || w.maximises != 0 || w.minimises != 0 {
			t.Errorf("%q (full screen %v, error %v): zooms %d, maximises %d, minimises %d; want %d, 0, 0",
				tc.pref, tc.fullscreen, tc.prefErr, w.zooms, w.maximises, w.minimises, tc.zooms)
		}
	}
}

// TestTitleBar_NotReady_AnswersAtOnce: until the window is ready (shown,
// or its page's runtime loaded) nothing goes to the main thread — before
// App.Run, Wails' InvokeSync only queues the work and waits — so the
// measurement is 0, full screen is false, a double-click does nothing, and
// none of it is kept. Once ready, the window is measured.
func TestTitleBar_NotReady_AnswersAtOnce(t *testing.T) {
	w := &fakeWindow{native: unsafe.Pointer(&nsWindowStandIn), fullscreen: true}
	native := &platformtest.TitleBar{Right: 79, CenterY: 26, Preference: "Fill"}
	blocked := func(func()) { t.Fatal("the main thread was asked before the window was ready") }
	tb := newTitleBar(w, native, blocked)
	if got := tb.ButtonsRight(); got != 0 {
		t.Errorf("ButtonsRight before ready = %v; want 0", got)
	}
	if got := tb.ButtonsCenterY(); got != 0 {
		t.Errorf("ButtonsCenterY before ready = %v; want 0", got)
	}
	if tb.FullScreen() {
		t.Error("FullScreen before ready = true; want false")
	}
	tb.DoubleClick()
	if w.fullscreenReads != 0 || w.zooms != 0 || len(native.Measured()) != 0 {
		t.Errorf("before ready: %d full-screen reads, %d zooms, %d measurements; want none",
			w.fullscreenReads, w.zooms, len(native.Measured()))
	}

	main := &mainThread{}
	tb.onMain = main.run
	tb.markReady()
	if got := tb.ButtonsRight(); got != 79 {
		t.Errorf("ButtonsRight once ready = %v; want 79 (the 0 before was not kept)", got)
	}
	if got := tb.ButtonsCenterY(); got != 26 {
		t.Errorf("ButtonsCenterY once ready = %v; want 26 (the 0 before was not kept)", got)
	}
	if !tb.FullScreen() {
		t.Error("FullScreen once ready = false; want the window's true")
	}
}

// TestTitleBar_FullScreen_FollowsTheWindow: once ready, FullScreen is the
// window's current state, read each time.
func TestTitleBar_FullScreen_FollowsTheWindow(t *testing.T) {
	w := &fakeWindow{native: unsafe.Pointer(&nsWindowStandIn)}
	tb := readyTitleBar(w, &platformtest.TitleBar{}, (&mainThread{}).run)
	if tb.FullScreen() {
		t.Fatal("FullScreen = true; want false")
	}
	w.fullscreen = true
	if !tb.FullScreen() {
		t.Fatal("FullScreen = false after the window entered full screen; want true")
	}
}

// TestNewTitleBar_AttachedBeforeRun: the real adapter on a real Wails
// window, attached as Attach does it but with the application not running
// (no run loop): System.Info's calls answer at once with 0 and false
// instead of waiting for a main thread that does not run yet.
func TestNewTitleBar_AttachedBeforeRun(t *testing.T) {
	app := application.New(application.Options{Name: "Choix"})
	w := app.Window.NewWithOptions(Options(nil, false))
	tb := NewTitleBar(w, &platformtest.TitleBar{Right: 79, Preference: "Fill"})

	type answer struct {
		right, centerY float64
		fullScreen     bool
	}
	done := make(chan answer, 1)
	go func() {
		tb.DoubleClick()
		done <- answer{tb.ButtonsRight(), tb.ButtonsCenterY(), tb.FullScreen()}
	}()
	select {
	case got := <-done:
		if got.right != 0 || got.centerY != 0 || got.fullScreen {
			t.Fatalf("before Run: ButtonsRight, ButtonsCenterY, FullScreen = %v, %v, %v; want 0, 0, false",
				got.right, got.centerY, got.fullScreen)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the title bar waited for the main thread before the application runs")
	}
}
