//go:build darwin

package avmedia

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// One native slot for the whole test binary: TestFrames_HoldsOneSlot needs
// it, and NativeSlots is read only once, at the first Acquire.
func TestMain(m *testing.M) {
	platform.NativeSlots = 1
	os.Exit(m.Run())
}

func fixture(name string) string {
	return filepath.Join("..", "..", "..", "..", "testdata", "media", name)
}

// settle waits until every native call of in has had its last callback, so
// the next test finds the only slot free.
func settle(t *testing.T, in *platform.Inflight) {
	t.Helper()
	if !in.Wait(10 * time.Second) {
		t.Fatal("native callbacks still pending after 10 s")
	}
}

func TestProperties_Clip(t *testing.T) {
	props, err := New().Properties(context.Background(), fixture("clip.mov"))
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	d, ok := props["duration"].(float64)
	if !ok || math.Abs(d-2.0) > 0.2 {
		t.Errorf("duration = %#v, want 2.0 ± 0.2", props["duration"])
	}
	if props["width"] != float64(64) || props["height"] != float64(36) {
		t.Errorf("size = %#v×%#v, want 64×36", props["width"], props["height"])
	}
	if props["make"] != "TESTCAM" {
		t.Errorf("make = %#v, want TESTCAM", props["make"])
	}
	if props["model"] != "T1" {
		t.Errorf("model = %#v, want T1", props["model"])
	}
	if loc, _ := props["location"].(string); !strings.HasPrefix(loc, "+10.5000+020.2500") {
		t.Errorf("location = %#v, want prefix +10.5000+020.2500", props["location"])
	}
	for _, k := range []string{"software", "encoder"} {
		if v, ok := props[k]; ok {
			t.Errorf("%s = %#v, want it omitted (absent in the fixture)", k, v)
		}
	}
}

func TestProperties_AudioOnly(t *testing.T) {
	props, err := New().Properties(context.Background(), fixture("audio-only.mov"))
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	if _, ok := props["duration"]; !ok {
		t.Error("duration missing")
	}
	for _, k := range []string{"width", "height"} {
		if v, ok := props[k]; ok {
			t.Errorf("%s = %#v, want it omitted without a video track", k, v)
		}
	}
	// ffmpeg writes its version as QuickTime user data ©swr, which
	// AVFoundation maps to the common key software; it is not an encoder.
	if s, _ := props["software"].(string); !strings.HasPrefix(s, "Lavf") {
		t.Errorf("software = %#v, want the ffmpeg version", props["software"])
	}
	if v, ok := props["encoder"]; ok {
		t.Errorf("encoder = %#v, want it omitted", v)
	}
}

// encoder.mp4 carries ffmpeg's version as an iTunes ©too item (identifier
// itsk/%A9too, common key software), where drones put their encoder.
func TestProperties_EncoderITunesKey(t *testing.T) {
	props, err := New().Properties(context.Background(), fixture("encoder.mp4"))
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	for _, k := range []string{"encoder", "software"} {
		if s, _ := props[k].(string); !strings.HasPrefix(s, "Lavf") {
			t.Errorf("%s = %#v, want the ffmpeg version", k, props[k])
		}
	}
}

// encoders.mov carries user data ©too ("TOOL-TOO") before ©enc ("TOOL-ENC")
// in the file, and AVFoundation reports them in that order; ©enc wins.
func TestProperties_EncoderPrefersEnc(t *testing.T) {
	props, err := New().Properties(context.Background(), fixture("encoders.mov"))
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	if props["encoder"] != "TOOL-ENC" {
		t.Errorf("encoder = %#v, want TOOL-ENC (©enc before ©too)", props["encoder"])
	}
}

func TestProperties_NotAMovie(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.mov")
	if err := os.WriteFile(path, []byte("this is not a movie\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Properties(context.Background(), path); err == nil {
		t.Fatal("Properties on a text file: want an error")
	}
	if _, err := New().Frames(context.Background(), path, []float64{0}); err == nil {
		t.Fatal("Frames on a text file: want an error")
	}
}

func TestProperties_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Properties(ctx, fixture("clip.mov")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A Properties call cancelled while the asset or its track loads returns at
// once with context.Canceled (or its result, if it won the race) and its
// handler still runs, releasing the slot.
func TestProperties_CancelInFlight(t *testing.T) {
	for _, delay := range []time.Duration{0, 100 * time.Microsecond, 500 * time.Microsecond, time.Millisecond, 3 * time.Millisecond} {
		in := &platform.Inflight{}
		ctx, cancel := context.WithCancel(platform.WithInflight(context.Background(), in))
		go func() {
			time.Sleep(delay)
			cancel()
		}()
		start := time.Now()
		_, err := New().Properties(ctx, fixture("clip.mov"))
		elapsed := time.Since(start)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("delay %v: err = %v, want nil or context.Canceled", delay, err)
		}
		if elapsed >= time.Second {
			t.Errorf("delay %v: call took %v, want < 1 s", delay, elapsed)
		}
		settle(t, in)
	}
}

// meanRed returns the average red value of a decoded image.
func meanRed(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			sum += float64(r >> 8)
		}
	}
	return sum / float64(b.Dx()*b.Dy())
}

func TestFrames_FiveFractions(t *testing.T) {
	fractions := []float64{0, 0.25, 0.5, 0.75, 0.95}
	frames, err := New().Frames(context.Background(), fixture("clip.mov"), fractions)
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(frames) != len(fractions) {
		t.Fatalf("got %d frames, want %d", len(frames), len(fractions))
	}
	// The fixture's red channel grows with the frame index (see
	// testdata/media/_make_clip.swift), so frames in order have increasing
	// mean red.
	prev := -1.0
	for i, f := range frames {
		img, err := jpeg.Decode(bytes.NewReader(f.JPEG))
		if err != nil {
			t.Fatalf("frame %d: decode: %v", i, err)
		}
		if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 36 {
			t.Errorf("frame %d: decoded %d×%d, want 64×36", i, b.Dx(), b.Dy())
		}
		if f.Width != 64 || f.Height != 36 {
			t.Errorf("frame %d: reported %d×%d, want 64×36", i, f.Width, f.Height)
		}
		red := meanRed(img)
		if red <= prev {
			t.Errorf("frame %d: mean red %.1f not above previous %.1f: frames out of order", i, red, prev)
		}
		prev = red
	}
}

func TestFrames_NoVideoTrack(t *testing.T) {
	_, err := New().Frames(context.Background(), fixture("audio-only.mov"), []float64{0, 0.5})
	if !errors.Is(err, platform.ErrNoVideoTrack) {
		t.Fatalf("err = %v, want platform.ErrNoVideoTrack", err)
	}
}

func TestFrames_BadFraction(t *testing.T) {
	for _, f := range []float64{-0.1, 1.1, math.NaN()} {
		if _, err := New().Frames(context.Background(), fixture("clip.mov"), []float64{f}); err == nil {
			t.Errorf("fraction %v: want an error", f)
		}
	}
}

func manyFractions(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i) / float64(n)
	}
	return out
}

// cancelPoint says when cancelledFrames cancels.
type cancelPoint int

const (
	// afterCall cancels 1 ms after the call, usually while the asset loads.
	afterCall cancelPoint = iota
	// afterGenerationStarts cancels 1 ms after the times have been handed
	// to the image generator.
	afterGenerationStarts
)

// cancelledFrames requests 200 frames, cancels at the given point and
// returns the call's duration, the Inflight that counts it and its error.
func cancelledFrames(t *testing.T, at cancelPoint) (time.Duration, *platform.Inflight, error) {
	t.Helper()
	in := &platform.Inflight{}
	ctx, cancel := context.WithCancel(platform.WithInflight(context.Background(), in))
	defer cancel()
	requested := stats.framesRequested.Load()
	go func() {
		if at == afterGenerationStarts {
			deadline := time.Now().Add(5 * time.Second)
			for stats.framesRequested.Load() == requested && time.Now().Before(deadline) {
				time.Sleep(50 * time.Microsecond)
			}
		}
		time.Sleep(time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := New().Frames(ctx, fixture("clip.mov"), manyFractions(200))
	return time.Since(start), in, err
}

func TestFrames_CancelDuringGeneration(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   cancelPoint
	}{
		{"1ms after the call", afterCall},
		{"1ms after generation starts", afterGenerationStarts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elapsed, in, err := cancelledFrames(t, tc.at)
			defer settle(t, in)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if elapsed >= time.Second {
				t.Fatalf("cancelled call took %v, want < 1 s", elapsed)
			}
		})
	}
}

func TestFrames_InflightOutlivesCancel(t *testing.T) {
	requested, callbacks := stats.framesRequested.Load(), stats.frameCallbacks.Load()
	_, in, err := cancelledFrames(t, afterGenerationStarts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	t.Logf("Inflight.Wait(0) right after the cancelled call: %v", in.Wait(0))
	if !in.Wait(10 * time.Second) {
		t.Fatal("Inflight.Wait(10s) = false: callbacks never finished")
	}
	gotReq := stats.framesRequested.Load() - requested
	gotCB := stats.frameCallbacks.Load() - callbacks
	if gotReq != 200 {
		t.Fatalf("times handed to the generator = %d, want 200", gotReq)
	}
	if gotCB != gotReq {
		t.Fatalf("callbacks run = %d, want %d (one per requested time)", gotCB, gotReq)
	}
}

func TestFrames_HoldsOneSlot(t *testing.T) {
	stats.maxActive.Store(stats.active.Load())
	_, in, err := cancelledFrames(t, afterGenerationStarts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first call: err = %v, want context.Canceled", err)
	}
	// The first call has returned but may still own the only slot; the
	// second must not start native work until its last callback has run.
	frames, err := New().Frames(platform.WithInflight(context.Background(), in), fixture("clip.mov"), []float64{0, 0.5})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("second call: %d frames, want 2", len(frames))
	}
	settle(t, in)
	if got := stats.maxActive.Load(); got != 1 {
		t.Fatalf("native calls active at once = %d, want 1", got)
	}
	if got := stats.active.Load(); got != 0 {
		t.Fatalf("native calls still active = %d, want 0", got)
	}
}

// Every native request object is deallocated once its last callback has
// run, also after a cancellation: no block, generator or asset keeps it in a
// retain cycle.
func TestRequests_Released(t *testing.T) {
	in := &platform.Inflight{}
	ctx := platform.WithInflight(context.Background(), in)
	if _, err := New().Frames(ctx, fixture("clip.mov"), []float64{0, 0.5}); err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if _, err := New().Properties(ctx, fixture("clip.mov")); err != nil {
		t.Fatalf("Properties: %v", err)
	}
	_, cin, err := cancelledFrames(t, afterGenerationStarts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call: err = %v, want context.Canceled", err)
	}
	settle(t, in)
	settle(t, cin)
	// Dispatch queues drain their autorelease pools a little later.
	deadline := time.Now().Add(5 * time.Second)
	for liveRequests() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d native request objects still alive", liveRequests())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
