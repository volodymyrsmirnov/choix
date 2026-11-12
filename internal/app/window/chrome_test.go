package window

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// nativeOnly is a window that only hands out its NSWindow pointer.
type nativeOnly struct{ p unsafe.Pointer }

func (w nativeOnly) NativeWindow() unsafe.Pointer { return w.p }

// TestChrome_KeepMinSize: the window's minimum (MinWidth × MinHeight, the
// one Options gives Wails) is put back on the main thread.
func TestChrome_KeepMinSize(t *testing.T) {
	native := &platformtest.WindowChrome{Changed: true}
	main := &mainThread{}
	c := newChrome(nativeOnly{unsafe.Pointer(&nsWindowStandIn)}, native, main.run)
	c.keepMinSize()
	c.keepMinSize()
	want := platformtest.MinSizeCall{Window: unsafe.Pointer(&nsWindowStandIn), Width: MinWidth, Height: MinHeight}
	if got := native.MinSizes(); len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("KeepMinSize calls = %v; want %v twice (it is put back every time it can have been lowered)", got, want)
	}
	if main.calls != 2 {
		t.Errorf("main-thread calls = %d; want 2", main.calls)
	}
	native.KeepErr = errors.New("no window buttons")
	c.keepMinSize() // logged, nothing else
}

// TestChrome_FocusPage: the page is offered the keyboard on the main thread
// each time (the native side leaves a view that has it alone).
func TestChrome_FocusPage(t *testing.T) {
	native := &platformtest.WindowChrome{Focused: true}
	main := &mainThread{}
	c := newChrome(nativeOnly{unsafe.Pointer(&nsWindowStandIn)}, native, main.run)
	c.focusPage()
	c.focusPage()
	if got := native.Focuses(); len(got) != 2 || got[0] != unsafe.Pointer(&nsWindowStandIn) {
		t.Fatalf("FocusPage calls = %v; want the window twice", got)
	}
	native.FocusErr = errors.New("no web view")
	c.focusPage() // logged, nothing else
	if main.calls != 3 {
		t.Errorf("main-thread calls = %d; want 3", main.calls)
	}
}

// TestChrome_UnderPage_SetOnce: the colour under the page is the window's
// background (--color-bg-1, sRGB 18, 20, 21), set once; a failure is tried
// again on the next event.
func TestChrome_UnderPage_SetOnce(t *testing.T) {
	native := &platformtest.WindowChrome{BackgroundErr: errors.New("no web view yet")}
	main := &mainThread{}
	c := newChrome(nativeOnly{unsafe.Pointer(&nsWindowStandIn)}, native, main.run)
	c.paintUnderPage()
	native.BackgroundErr = nil
	c.paintUnderPage()
	c.paintUnderPage()
	want := platformtest.BackgroundCall{Window: unsafe.Pointer(&nsWindowStandIn), Red: 18, Green: 20, Blue: 21}
	if got := native.Backgrounds(); len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("SetPageBackground calls = %v; want %v twice (failed, then set and kept)", got, want)
	}
	if main.calls != 2 {
		t.Errorf("main-thread calls = %d; want 2 (none once set)", main.calls)
	}
	if want.Red != background.Red || want.Green != background.Green || want.Blue != background.Blue {
		t.Errorf("the under-page colour %v is not the window's background %v", want, background)
	}
}

// TestChrome_NoNativeWindow: before Wails made the NSWindow nothing is
// called, and nothing is kept.
func TestChrome_NoNativeWindow(t *testing.T) {
	native := &platformtest.WindowChrome{}
	c := newChrome(nativeOnly{}, native, (&mainThread{}).run)
	c.keepMinSize()
	c.focusPage()
	c.paintUnderPage()
	if len(native.MinSizes())+len(native.Focuses())+len(native.Backgrounds()) != 0 {
		t.Fatal("the native side was called without a window")
	}
	c.w = nativeOnly{unsafe.Pointer(&nsWindowStandIn)}
	c.paintUnderPage()
	if len(native.Backgrounds()) != 1 {
		t.Fatal("the under-page colour was not set once the window exists")
	}
}

// TestChrome_Events: what each correction follows.
//
//   - The minimum: Wails lowers it to 0×0 in Maximise (its own title-bar
//     double-click for the Zoom setting) and in Fullscreen, and puts it back
//     only in its own UnMaximise / UnFullscreen; so it is put back after
//     every resize, zoom, un-zoom and exit from full screen, and when the
//     window is shown or its page loaded.
//   - The keyboard: Wails never makes the web view first responder, so the
//     page is offered it when the window is shown, becomes key, or its page
//     loaded.
//   - The colour under the page: set as soon as the page's runtime loaded
//     (before Wails shows the window), or at the latest when it is shown.
func TestChrome_Events(t *testing.T) {
	for _, e := range []events.WindowEventType{
		events.Common.WindowDidResize, events.Common.WindowMaximise, events.Common.WindowUnMaximise,
		events.Common.WindowUnFullscreen, events.Common.WindowShow, events.Common.WindowRuntimeReady,
	} {
		if !slices.Contains(minSizeEvents, e) {
			t.Errorf("minSizeEvents lacks %v", e)
		}
	}
	for _, e := range []events.WindowEventType{events.Common.WindowShow, events.Common.WindowFocus, events.Common.WindowRuntimeReady} {
		if !slices.Contains(focusEvents, e) {
			t.Errorf("focusEvents lacks %v", e)
		}
	}
	for _, e := range []events.WindowEventType{events.Common.WindowRuntimeReady, events.Common.WindowShow} {
		if !slices.Contains(underPageEvents, e) {
			t.Errorf("underPageEvents lacks %v", e)
		}
	}
}

// TestAttachChrome_BeforeRun: attached as Attach does it, before the
// application runs, it only registers listeners: nothing waits for the
// main thread and nothing is called.
func TestAttachChrome_BeforeRun(t *testing.T) {
	app := application.New(application.Options{Name: "Choix"})
	w := app.Window.NewWithOptions(Options(nil, false))
	native := &platformtest.WindowChrome{}
	done := make(chan struct{})
	go func() {
		AttachChrome(w, native)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AttachChrome waited before the application runs")
	}
	if len(native.MinSizes())+len(native.Focuses())+len(native.Backgrounds()) != 0 {
		t.Fatal("AttachChrome called the native side before the window exists")
	}
}

// serialMain stands for the main thread: one goroutine running queued
// functions in order. sync is application.InvokeSync (queue and wait),
// async is application.InvokeAsync (queue).
type serialMain struct{ queue chan func() }

func newSerialMain(t *testing.T) *serialMain {
	m := &serialMain{queue: make(chan func(), 64)}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case fn := <-m.queue:
				fn()
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop) })
	return m
}

func (m *serialMain) async(fn func()) { m.queue <- fn }

func (m *serialMain) sync(fn func()) {
	done := make(chan struct{})
	m.queue <- func() {
		defer close(done)
		fn()
	}
	<-done
}

// recorder records, in order, what happened on the main thread.
type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

// paintRecorder is a WindowChrome that records SetPageBackground on r.
type paintRecorder struct {
	platformtest.WindowChrome
	r *recorder
}

func (p *paintRecorder) SetPageBackground(window unsafe.Pointer, red, green, blue uint8) error {
	p.r.add("paint")
	return p.WindowChrome.SetPageBackground(window, red, green, blue)
}

// TestAttachChrome_PaintsBeforeTheWindowIsShown: Wails v3.1.0 shows the
// main window from a listener on mac:WebViewDidFinishNavigation, which
// queues Show on the main thread (InvokeAsync). Listeners run in
// goroutines, so a listener painting the colour under the page could lose
// that race; a hook on the same event runs, synchronously, before Wails
// starts any listener (WebviewWindow.HandleWindowEvent). The colour is set
// from such a hook, waiting for the main thread, so it is on the web view
// before Show is even queued. This drives the real Wails window's event
// handling, with Wails' show listener reproduced and a serial main thread.
func TestAttachChrome_PaintsBeforeTheWindowIsShown(t *testing.T) {
	app := application.New(application.Options{Name: "Choix"})
	w := app.Window.NewWithOptions(Options(nil, false))
	main := newSerialMain(t)
	r := &recorder{}
	native := &paintRecorder{r: r}
	c := newChrome(nativeOnly{unsafe.Pointer(&nsWindowStandIn)}, native, main.async)
	attachChrome(w, c, main.sync)
	// Wails v3.1.0's own listener (webview_window_darwin.go run()).
	shown := make(chan struct{})
	w.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(*application.WindowEvent) {
		main.async(func() {
			r.add("show")
			close(shown)
		})
	})

	w.HandleWindowEvent(uint(events.Mac.WebViewDidFinishNavigation))
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("the window was never shown")
	}
	main.sync(func() {}) // let anything queued before it run
	got := r.snapshot()
	paint, show := slices.Index(got, "paint"), slices.Index(got, "show")
	if paint < 0 || paint > show {
		t.Fatalf("main thread: %v; want the colour under the page set before the window is shown", got)
	}
}

// TestWailsShowsFromNavigationListener pins the Wails fact the hook above
// relies on: v3.1.0 shows the window from its WebViewDidFinishNavigation
// listener. It reads the module's source; it skips when the module cache
// is not where Go keeps it by default.
func TestWailsShowsFromNavigationListener(t *testing.T) {
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		cache = filepath.Join(build.Default.GOPATH, "pkg", "mod")
	}
	src, err := os.ReadFile(filepath.Join(cache, "github.com", "wailsapp", "wails", "v3@v3.1.0", "pkg", "application", "webview_window_darwin.go"))
	if err != nil {
		t.Skipf("Wails v3.1.0 source not found: %v", err)
	}
	s := string(src)
	listener := strings.Index(s, "w.parent.OnWindowEvent(events.Mac.WebViewDidFinishNavigation")
	show := strings.Index(s, "w.parent.Show()")
	if listener < 0 || show < listener || !strings.Contains(s[listener:show], "InvokeAsync") {
		t.Fatal("Wails no longer shows the window from an InvokeAsync in its WebViewDidFinishNavigation listener: " +
			"check that the hook in attachChrome still runs before the window is shown")
	}
	if beforeShowEvent != events.Mac.WebViewDidFinishNavigation {
		t.Fatalf("beforeShowEvent = %v; want mac:WebViewDidFinishNavigation", beforeShowEvent)
	}
}
