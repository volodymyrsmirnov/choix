package window

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestOptions_Defaults(t *testing.T) {
	for _, fileDrop := range []bool{true, false} {
		o := Options(nil, fileDrop)
		if o.Title != "Choix" || o.URL != "/" {
			t.Errorf("Title, URL = %q, %q; want Choix, /", o.Title, o.URL)
		}
		if o.Width != 1280 || o.Height != 800 {
			t.Errorf("size = %d×%d; want 1280×800", o.Width, o.Height)
		}
		if o.MinWidth != 960 || o.MinHeight != 600 {
			t.Errorf("minimum = %d×%d; want 960×600", o.MinWidth, o.MinHeight)
		}
		if o.Mac.TitleBar != application.MacTitleBarHiddenInset {
			t.Errorf("Mac.TitleBar = %+v; want MacTitleBarHiddenInset", o.Mac.TitleBar)
		}
		if o.Mac.InvisibleTitleBarHeight != 0 {
			t.Errorf("Mac.InvisibleTitleBarHeight = %d; want 0 (a drag band would swallow toolbar clicks)", o.Mac.InvisibleTitleBarHeight)
		}
		if o.EnableFileDrop != fileDrop {
			t.Errorf("EnableFileDrop = %v; want %v", o.EnableFileDrop, fileDrop)
		}
		if o.ZoomControlEnabled || !o.DefaultContextMenuDisabled {
			t.Errorf("ZoomControlEnabled, DefaultContextMenuDisabled = %v, %v; want false, true",
				o.ZoomControlEnabled, o.DefaultContextMenuDisabled)
		}
		if o.BackgroundType != application.BackgroundTypeSolid || o.Mac.Backdrop != application.MacBackdropNormal {
			t.Errorf("background = %v, backdrop %v; want solid and normal (no translucency)", o.BackgroundType, o.Mac.Backdrop)
		}
		if o.BackgroundColour != application.NewRGB(18, 20, 21) {
			t.Errorf("BackgroundColour = %+v; want rgb(18, 20, 21)", o.BackgroundColour)
		}
		if o.InitialPosition != application.WindowCentered || o.X != 0 || o.Y != 0 {
			t.Errorf("position = %v (%d, %d); want centred", o.InitialPosition, o.X, o.Y)
		}
	}
}

func TestOptions_SavedFrame(t *testing.T) {
	saved := &State{X: -1200, Y: 40, Width: 1440, Height: 900}
	o := Options(saved, true)
	if o.InitialPosition != application.WindowXY || o.X != -1200 || o.Y != 40 {
		t.Errorf("position = %v (%d, %d); want WindowXY (-1200, 40)", o.InitialPosition, o.X, o.Y)
	}
	if o.Width != 1440 || o.Height != 900 {
		t.Errorf("size = %d×%d; want 1440×900", o.Width, o.Height)
	}
	if o.MinWidth != 960 || o.MinHeight != 600 || o.Mac.TitleBar != application.MacTitleBarHiddenInset {
		t.Errorf("a saved frame changed the rest of the options: %+v", o)
	}
}

// TestBackgroundColour_IsThePageToken: the window's background is the
// colour the app root paints (base.css's [data-app-root] background token in
// tokens.css), converted from OKLCH to sRGB, so no other colour flashes
// before the page draws.
func TestBackgroundColour_IsThePageToken(t *testing.T) {
	design := filepath.Join("..", "..", "..", "frontend", "src", "design")
	base := readFile(t, filepath.Join(design, "base.css"))
	tokens := readFile(t, filepath.Join(design, "tokens.css"))

	root := regexp.MustCompile(`(?s)\[data-app-root\]\s*\{[^}]*?background:\s*var\((--[a-z0-9-]+)\)`).FindStringSubmatch(base)
	if root == nil {
		t.Fatal("base.css: no background var() on [data-app-root]")
	}
	decl := regexp.MustCompile(regexp.QuoteMeta(root[1]) + `:\s*oklch\(([0-9.]+)\s+([0-9.]+)\s+([0-9.]+)\)`).FindStringSubmatch(tokens)
	if decl == nil {
		t.Fatalf("tokens.css: %s is not an oklch() colour", root[1])
	}
	var lch [3]float64
	for i := range lch {
		v, err := strconv.ParseFloat(decl[i+1], 64)
		if err != nil {
			t.Fatal(err)
		}
		lch[i] = v
	}
	want := oklchToSRGB(lch[0], lch[1], lch[2])
	got := Options(nil, false).BackgroundColour
	for i, c := range []uint8{got.Red, got.Green, got.Blue} {
		if math.Abs(float64(c)-want[i]) > 0.5 {
			t.Fatalf("BackgroundColour = %+v; want %s = rgb(%.1f, %.1f, %.1f)", got, root[1], want[0], want[1], want[2])
		}
	}
	if got.Alpha != 255 {
		t.Fatalf("BackgroundColour alpha = %d; want opaque", got.Alpha)
	}
}

// oklchToSRGB converts an OKLCH colour to sRGB channels in 0..255 (CSS
// Color 4's OKLab matrices, then the sRGB transfer function).
func oklchToSRGB(l, c, h float64) [3]float64 {
	a, b := c*math.Cos(h*math.Pi/180), c*math.Sin(h*math.Pi/180)
	lp := l + 0.3963377774*a + 0.2158037573*b
	mp := l - 0.1055613458*a - 0.0638541728*b
	sp := l - 0.0894841775*a - 1.2914855480*b
	lc, mc, sc := lp*lp*lp, mp*mp*mp, sp*sp*sp
	lin := [3]float64{
		4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc,
		-1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc,
		-0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc,
	}
	var out [3]float64
	for i, v := range lin {
		v = math.Max(0, math.Min(1, v))
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		out[i] = v * 255
	}
	return out
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // a repository file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDirectories_KeepsOnlyFolders(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Trip")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "IMG_0001.jpg")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "Trip link")
	if err := os.Symlink(folder, link); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone")

	got := directories([]string{file, folder, missing, link})
	if want := []string{folder, link}; !slices.Equal(got, want) {
		t.Fatalf("directories = %q; want %q", got, want)
	}
	if got := directories(nil); got != nil {
		t.Fatalf("directories(nil) = %q; want nil", got)
	}
}

// fakeTimers records the timers a debouncer arms; the test fires them.
type fakeTimers struct {
	mu     sync.Mutex
	armed  []*fakeTimer
	delays []time.Duration
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (ft *fakeTimers) after(d time.Duration, f func()) func() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{f: f}
	ft.armed = append(ft.armed, t)
	ft.delays = append(ft.delays, d)
	return func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

// fire runs timer i the way time.AfterFunc would if it had not been
// stopped in time: a stopped timer whose callback already started still
// runs, so the debouncer must ignore stale fires itself.
func (ft *fakeTimers) fire(i int) {
	ft.mu.Lock()
	f := ft.armed[i].f
	ft.mu.Unlock()
	f()
}

func TestDebouncer_CoalescesToOneRun(t *testing.T) {
	ft := &fakeTimers{}
	var runs int
	d := newDebouncer(500*time.Millisecond, func() { runs++ })
	d.after = ft.after

	d.Trigger()
	d.Trigger()
	d.Trigger()
	if len(ft.armed) != 3 || ft.delays[2] != 500*time.Millisecond {
		t.Fatalf("armed %d timers, delays %v; want 3 of 500ms", len(ft.armed), ft.delays)
	}
	if !ft.armed[0].stopped || !ft.armed[1].stopped || ft.armed[2].stopped {
		t.Fatal("each Trigger must stop the timer before it")
	}
	ft.fire(0) // stale: superseded
	ft.fire(1) // stale
	if runs != 0 {
		t.Fatalf("stale timers ran the save %d times; want 0", runs)
	}
	ft.fire(2)
	ft.fire(2) // a second fire of the same timer runs nothing
	if runs != 1 {
		t.Fatalf("runs = %d; want 1", runs)
	}
}

func TestDebouncer_FlushRunsPendingAtOnce(t *testing.T) {
	ft := &fakeTimers{}
	var runs int
	d := newDebouncer(500*time.Millisecond, func() { runs++ })
	d.after = ft.after

	d.Flush()
	if runs != 0 {
		t.Fatalf("Flush with nothing pending ran %d times; want 0", runs)
	}

	d.Trigger()
	d.Flush()
	if runs != 1 || !ft.armed[0].stopped {
		t.Fatalf("Flush: runs = %d, timer stopped = %v; want 1, true", runs, ft.armed[0].stopped)
	}
	ft.fire(0) // the timer raced the flush: nothing more runs
	d.Flush()
	if runs != 1 {
		t.Fatalf("runs = %d after a raced timer and a second Flush; want 1", runs)
	}
}

// TestDebouncer_RealTimer: by default the debouncer uses the real clock and
// runs on the timer's goroutine.
func TestDebouncer_RealTimer(t *testing.T) {
	done := make(chan struct{}, 1)
	d := newDebouncer(time.Millisecond, func() { done <- struct{}{} })
	d.Trigger()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the debounced run never happened")
	}
}

func TestTarget(t *testing.T) {
	laptop := []Rect{{X: 0, Y: 0, Width: 1512, Height: 982}}
	def := State{Width: 1280, Height: 800}
	for _, tc := range []struct {
		name        string
		saved       *State
		screens     []Rect
		want        State
		wantCentred bool
	}{
		{"nothing saved", nil, laptop, def, true},
		{"nothing saved, no screen known", nil, nil, def, true},
		{"saved and on screen", &State{X: 100, Y: 50, Width: 1300, Height: 820}, laptop, State{X: 100, Y: 50, Width: 1300, Height: 820}, false},
		{"saved off every screen", &State{X: 3000, Y: 50, Width: 1300, Height: 820}, laptop, def, true},
		{"saved, no screen known", &State{X: 3000, Y: 50, Width: 400, Height: 820}, nil, State{X: 3000, Y: 50, Width: 960, Height: 820}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, centred := target(tc.saved, tc.screens)
			if got != tc.want || centred != tc.wantCentred {
				t.Fatalf("target = %+v, centred %v; want %+v, %v", got, centred, tc.want, tc.wantCentred)
			}
		})
	}
}

// TestMisplaced: the window Wails made a point too small (1279×799) is
// corrected; the right size is left alone, at any position when centred.
func TestMisplaced(t *testing.T) {
	want := State{X: 100, Y: 50, Width: 1280, Height: 800}
	for _, tc := range []struct {
		name    string
		got     State
		centred bool
		moved   bool
	}{
		{"a point too small", State{X: 100, Y: 50, Width: 1279, Height: 799}, false, true},
		{"a point too small, centred", State{X: 215, Y: 134, Width: 1279, Height: 799}, true, true},
		{"right frame", want, false, false},
		{"right size, centred anywhere", State{X: 215, Y: 134, Width: 1280, Height: 800}, true, false},
		{"right size, wrong place", State{X: 101, Y: 50, Width: 1280, Height: 800}, false, true},
		{"no window", State{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := misplaced(tc.got, want, tc.centred); got != tc.moved {
				t.Fatalf("misplaced(%+v, %+v, %v) = %v; want %v", tc.got, want, tc.centred, got, tc.moved)
			}
		})
	}
}
