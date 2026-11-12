package window

import (
	"errors"
	"testing"
)

// memKV is an in-memory KV.
type memKV struct {
	m   map[string]string
	err error // returned by every call when set
}

func newMemKV() *memKV { return &memKV{m: map[string]string{}} }

func (k *memKV) KVGet(key string) (string, bool, error) {
	if k.err != nil {
		return "", false, k.err
	}
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *memKV) KVSet(key, v string) error {
	if k.err != nil {
		return k.err
	}
	k.m[key] = v
	return nil
}

func TestState_KVRoundTrip(t *testing.T) {
	kv := newMemKV()

	got, err := Load(kv)
	if err != nil || got != nil {
		t.Fatalf("Load on an empty kv = %+v, %v; want nil, nil", got, err)
	}

	want := State{X: -1200, Y: 40, Width: 1440, Height: 900}
	if err := Save(kv, want); err != nil {
		t.Fatal(err)
	}
	if _, ok := kv.m["window"]; !ok {
		t.Fatalf("Save stored keys %v; want the frame under %q", kv.m, "window")
	}
	got, err = Load(kv)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("Load = %+v; want %+v", got, want)
	}
}

func TestState_BelowMinimumIsClamped(t *testing.T) {
	kv := newMemKV()
	if err := Save(kv, State{X: 10, Y: 20, Width: 400, Height: 300}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(kv)
	if err != nil {
		t.Fatal(err)
	}
	if want := (State{X: 10, Y: 20, Width: 960, Height: 600}); got == nil || *got != want {
		t.Fatalf("Load = %+v; want %+v", got, want)
	}

	// One side below the minimum: only that side grows.
	if err := Save(kv, State{X: 0, Y: 0, Width: 1500, Height: 300}); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(kv); got == nil || got.Width != 1500 || got.Height != 600 {
		t.Fatalf("Load = %+v; want 1500×600", got)
	}
}

func TestState_UnreadableIsAnError(t *testing.T) {
	kv := newMemKV()
	kv.m["window"] = "not json"
	if got, err := Load(kv); err == nil || got != nil {
		t.Fatalf("Load of a corrupt value = %+v, %v; want nil and an error", got, err)
	}

	boom := errors.New("boom")
	kv = &memKV{err: boom}
	if got, err := Load(kv); !errors.Is(err, boom) || got != nil {
		t.Fatalf("Load with a failing kv = %+v, %v; want nil and the kv's error", got, err)
	}
	if err := Save(kv, State{Width: 1280, Height: 800}); !errors.Is(err, boom) {
		t.Fatalf("Save with a failing kv = %v; want the kv's error", err)
	}
}

func TestRestore_OffScreenIsNil(t *testing.T) {
	laptop := Rect{X: 0, Y: 0, Width: 1512, Height: 982}
	for _, tc := range []struct {
		name    string
		saved   State
		screens []Rect
	}{
		{"on a display that is gone", State{X: 1600, Y: 100, Width: 1280, Height: 800}, []Rect{laptop}},
		{"left of every screen", State{X: -2000, Y: 100, Width: 1280, Height: 800}, []Rect{laptop}},
		{"only 99 px wide on screen", State{X: 1413, Y: 100, Width: 1280, Height: 800}, []Rect{laptop}},
		{"only 99 px tall on screen", State{X: 100, Y: 883, Width: 1280, Height: 800}, []Rect{laptop}},
		{"no screen known", State{X: 0, Y: 0, Width: 1280, Height: 800}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Restore(tc.saved, tc.screens); got != nil {
				t.Fatalf("Restore(%+v, %+v) = %+v; want nil", tc.saved, tc.screens, *got)
			}
		})
	}
}

func TestRestore_PartlyVisibleKept(t *testing.T) {
	laptop := Rect{X: 0, Y: 0, Width: 1512, Height: 982}
	left := Rect{X: -1920, Y: -98, Width: 1920, Height: 1080} // an external display left of the laptop
	for _, tc := range []struct {
		name    string
		saved   State
		screens []Rect
	}{
		{"fully on screen", State{X: 100, Y: 50, Width: 1280, Height: 800}, []Rect{laptop}},
		{"hanging off the right edge", State{X: 1000, Y: 500, Width: 1280, Height: 800}, []Rect{laptop}},
		{"exactly 100×100 on screen", State{X: 1412, Y: 882, Width: 1280, Height: 800}, []Rect{laptop}},
		{"on the second display", State{X: -1500, Y: 0, Width: 1280, Height: 800}, []Rect{laptop, left}},
		{"across both displays", State{X: -640, Y: 0, Width: 1280, Height: 800}, []Rect{laptop, left}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Restore(tc.saved, tc.screens)
			if got == nil || *got != tc.saved {
				t.Fatalf("Restore(%+v, %+v) = %v; want the saved frame kept", tc.saved, tc.screens, got)
			}
		})
	}
}

// TestRestore_ClampsBeforeChecking: the frame checked and returned is the
// one the window will have, at least the minimum size.
func TestRestore_ClampsBeforeChecking(t *testing.T) {
	laptop := Rect{X: 0, Y: 0, Width: 1512, Height: 982}
	// 400×300 at x=-450 is off screen, but at 960×600 it shows 510 px.
	got := Restore(State{X: -450, Y: 0, Width: 400, Height: 300}, []Rect{laptop})
	if want := (State{X: -450, Y: 0, Width: 960, Height: 600}); got == nil || *got != want {
		t.Fatalf("Restore = %v; want %+v", got, want)
	}
}
