//go:build darwin

package vision

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// encode returns img as a JPEG.
func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// gradient is a w×h colour gradient.
func gradient(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: uint8(255 - x*255/w), A: 255}) //nolint:gosec // every value is in 0..255
		}
	}
	return encode(t, img)
}

// checkerboard is a w×h black and white checkerboard of 50 px squares.
func checkerboard(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{A: 255}
			if (x/50+y/50)%2 == 0 {
				c.R, c.G, c.B = 255, 255, 255
			}
			img.Set(x, y, c)
		}
	}
	return encode(t, img)
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustPrint(t *testing.T, in platform.ImageInput) []float32 {
	t.Helper()
	v, err := New().FeaturePrint(context.Background(), in)
	if err != nil {
		t.Fatalf("FeaturePrint: %v", err)
	}
	return v
}

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func TestFeaturePrint_LengthAndFinite(t *testing.T) {
	data := gradient(t, 800, 600)
	path := writeFile(t, "gradient.jpg", data)
	for name, in := range map[string]platform.ImageInput{
		"path": {Path: path},
		"data": {Data: data},
	} {
		t.Run(name, func(t *testing.T) {
			v := mustPrint(t, in)
			if len(v) != platform.FeaturePrintLen {
				t.Fatalf("len = %d, want %d", len(v), platform.FeaturePrintLen)
			}
			for i, x := range v {
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
					t.Fatalf("v[%d] = %v", i, x)
				}
			}
			if n := norm(v); n < 0.9 || n > 1.1 {
				t.Fatalf("norm = %v, want about 1", n)
			}
			t.Logf("length %d, norm %.6f", len(v), norm(v))
		})
	}
}

// The real call reports the revision it ran at; it is the pinned one.
func TestFeaturePrint_Revision(t *testing.T) {
	src, err := newSource(platform.ImageInput{Data: gradient(t, 320, 240)})
	if err != nil {
		t.Fatal(err)
	}
	v, rev, err := src.print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	if rev != Revision || len(v) != platform.FeaturePrintLen {
		t.Fatalf("revision %d, length %d; want %d, %d", rev, len(v), Revision, platform.FeaturePrintLen)
	}
	t.Logf("revision %d", rev)
}

func TestFeaturePrint_Deterministic(t *testing.T) {
	in := platform.ImageInput{Data: gradient(t, 800, 600)}
	a, b := mustPrint(t, in), mustPrint(t, in)
	if !slices.Equal(a, b) {
		t.Fatalf("two prints of one image differ (dot %v)", dot(a, b))
	}
}

func TestFeaturePrint_DifferentImagesDiffer(t *testing.T) {
	a := mustPrint(t, platform.ImageInput{Data: gradient(t, 800, 600)})
	b := mustPrint(t, platform.ImageInput{Data: checkerboard(t, 800, 600)})
	if d := dot(a, b) / (norm(a) * norm(b)); d >= 0.999 {
		t.Fatalf("gradient·checkerboard = %v, want < 0.999", d)
	}
}

// withOrientation returns the JPEG data with an Exif APP1 segment, right
// after SOI, whose IFD0 holds one Orientation tag (a little-endian TIFF
// block, as platformtest's DNG fixtures write their IFDs).
func withOrientation(t *testing.T, data []byte, orientation uint16) []byte {
	t.Helper()
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Fatal("not a JPEG")
	}
	tiff := []byte{
		'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, // header, IFD0 at 8
		0x01, 0x00, // one entry
		0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, // Orientation, SHORT, count 1
		byte(orientation), byte(orientation >> 8), 0x00, 0x00, // value
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	n := len(payload) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, payload...) //nolint:gosec // n is a few dozen bytes
	out := append([]byte{0xFF, 0xD8}, app1...)
	return append(out, data[2:]...)
}

// asymmetric is a w×h image with no symmetry: a bright block in the top-left
// corner over a horizontal colour ramp and a vertical luminance ramp.
func asymmetric(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{R: uint8(x * 255 / w), G: uint8(y * 200 / h), B: 60, A: 255} //nolint:gosec // every value is in 0..255
			if x < w/3 && y < h/4 {
				c = color.NRGBA{R: 250, G: 250, B: 250, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// rotateCW returns img turned 90° clockwise: what EXIF orientation 6 asks a
// viewer to show for stored pixels img.
func rotateCW(img *image.NRGBA) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := range w {
		for x := range h {
			out.Set(x, y, img.At(y, h-1-x))
		}
	}
	return out
}

// The source's EXIF orientation is applied to the request (orientationOf in
// vision.m): pixels stored sideways with orientation 6 print like the same
// pixels stored upright, and unlike the sideways pixels without the tag.
func TestFeaturePrint_AppliesOrientation(t *testing.T) {
	stored := asymmetric(320, 240)
	sideways := encode(t, stored)
	tagged := mustPrint(t, platform.ImageInput{Data: withOrientation(t, sideways, 6)})
	upright := mustPrint(t, platform.ImageInput{Data: encode(t, rotateCW(stored))})
	untagged := mustPrint(t, platform.ImageInput{Data: sideways})

	cos := func(a, b []float32) float64 { return dot(a, b) / (norm(a) * norm(b)) }
	same, other := cos(tagged, upright), cos(tagged, untagged)
	t.Logf("cos(oriented, upright) = %.6f, cos(oriented, sideways) = %.6f", same, other)
	if same < 0.99 {
		t.Fatalf("cos(oriented, upright) = %.6f; want ≥ 0.99 (the orientation tag was not applied)", same)
	}
	if other >= same {
		t.Fatalf("cos(oriented, sideways) = %.6f ≥ cos(oriented, upright) = %.6f", other, same)
	}
}

func TestFeaturePrint_Unreadable(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty.jpg": nil,
		"text.jpg":  []byte("this is not an image, just some text\n"),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, name, data)
			_, err := New().FeaturePrint(context.Background(), platform.ImageInput{Path: path})
			if err == nil || !strings.HasPrefix(err.Error(), "vision:") || !errors.Is(err, platform.ErrUnreadableImage) {
				t.Fatalf("err = %v, want a vision: error wrapping platform.ErrUnreadableImage", err)
			}
			if len(data) > 0 {
				_, err = New().FeaturePrint(context.Background(), platform.ImageInput{Data: data})
				if err == nil || !strings.HasPrefix(err.Error(), "vision:") || !errors.Is(err, platform.ErrUnreadableImage) {
					t.Fatalf("data err = %v, want a vision: error wrapping platform.ErrUnreadableImage", err)
				}
			}
		})
	}
}

func TestFeaturePrint_InputValidation(t *testing.T) {
	data := gradient(t, 64, 48)
	for name, in := range map[string]platform.ImageInput{
		"both":    {Path: "/x/a.jpg", Data: data},
		"neither": {},
		"NUL":     {Path: "/x/a\x00b.jpg"},
	} {
		t.Run(name, func(t *testing.T) {
			before := platform.NativeRunning()
			hooked := false
			slowHook = func() { hooked = true }
			t.Cleanup(func() { slowHook = nil })
			_, err := New().FeaturePrint(context.Background(), in)
			if err == nil || !strings.HasPrefix(err.Error(), "vision:") {
				t.Fatalf("err = %v, want a vision: error", err)
			}
			if hooked || platform.NativeRunning() != before {
				t.Fatal("an invalid input reached the native call")
			}
		})
	}
}

func TestFeaturePrint_Cancelled(t *testing.T) {
	before := platform.NativeRunning()
	entered := make(chan struct{})
	var enteredAt time.Time // written before entered is closed
	slowHook = func() {
		enteredAt = time.Now()
		close(entered)
		time.Sleep(3 * time.Second)
	}
	var inflight platform.Inflight
	t.Cleanup(func() {
		if !inflight.Wait(5 * time.Second) {
			t.Error("detached call still running after 5 s")
		}
		slowHook = nil
	})

	ctx, cancel := context.WithCancel(platform.WithInflight(context.Background(), &inflight))
	defer cancel()
	go func() {
		select {
		case <-entered:
			time.Sleep(100 * time.Millisecond)
		case <-time.After(5 * time.Second):
		}
		cancel()
	}()
	_, err := New().FeaturePrint(ctx, platform.ImageInput{Data: gradient(t, 64, 48)})
	returned := time.Now()
	select {
	case <-entered:
	default:
		t.Fatalf("returned (%v) before the detached function started", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := returned.Sub(enteredAt); d >= time.Second {
		t.Fatalf("returned %v after the native call started, want under 1 s", d)
	}
	if !inflight.Wait(5 * time.Second) {
		t.Fatal("Inflight.Wait(5s) = false")
	}
	if got := platform.NativeRunning(); got != before {
		t.Fatalf("NativeRunning = %d after the call ended, want %d", got, before)
	}
}

func TestEntryPoints_CatchObjCException(t *testing.T) {
	err := raiseForTest("ChoixTestException")
	if err == nil || !strings.Contains(err.Error(), "ChoixTestException") || !strings.HasPrefix(err.Error(), "vision:") {
		t.Fatalf("err = %v, want a vision: error naming ChoixTestException", err)
	}
	// A framework failure is not the image's fault: it is retried.
	if errors.Is(err, platform.ErrUnreadableImage) {
		t.Fatalf("err = %v wraps platform.ErrUnreadableImage; want a framework failure", err)
	}
}

// An exception raised while the ARC-owned request handler and request exist
// is caught, and they are still released: none is left behind by the unwind.
func TestEntryPoints_ExceptionReleasesRequest(t *testing.T) {
	raiseAfterRequestForTest(true)
	t.Cleanup(func() { raiseAfterRequestForTest(false) })
	src, err := newSource(platform.ImageInput{Data: gradient(t, 64, 48)})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = src.print()
	if err == nil || !strings.Contains(err.Error(), "ChoixTestException") {
		t.Fatalf("err = %v, want a vision: error naming ChoixTestException", err)
	}
	if !requestReleasedForTest() {
		t.Fatal("the request handler or the request outlived the call: the exception leaked them")
	}
}

func BenchmarkFeaturePrint1600(b *testing.B) {
	img := image.NewNRGBA(image.Rect(0, 0, 1600, 1067))
	for y := range 1067 {
		for x := range 1600 {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255}) //nolint:gosec // wraps on purpose
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	in := platform.ImageInput{Data: buf.Bytes()}
	v := New()
	if _, err := v.FeaturePrint(context.Background(), in); err != nil { // warm-up: loads the model
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := v.FeaturePrint(context.Background(), in); err != nil {
			b.Fatal(err)
		}
	}
}
