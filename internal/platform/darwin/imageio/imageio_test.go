//go:build darwin

package imageio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// testdata returns the path of a file under the repository's testdata.
func testdata(t *testing.T, elem ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "..", "..", "testdata"}, elem...)...)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return path
}

func mustProperties(t *testing.T, in platform.ImageInput) map[string]any {
	t.Helper()
	props, err := New().Properties(context.Background(), in)
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	return props
}

func mustThumbnail(t *testing.T, in platform.ImageInput, limit int, fit platform.Fit) platform.Image {
	t.Helper()
	img, err := New().Thumbnail(context.Background(), in, limit, fit)
	if err != nil {
		t.Fatalf("Thumbnail(%d, %v): %v", limit, fit, err)
	}
	return img
}

// decode checks that img.JPEG is a JPEG of the reported size.
func decode(t *testing.T, img platform.Image) image.Image {
	t.Helper()
	got, err := jpeg.Decode(bytes.NewReader(img.JPEG))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if b := got.Bounds(); b.Dx() != img.Width || b.Dy() != img.Height {
		t.Fatalf("decoded %dx%d, reported %dx%d", b.Dx(), b.Dy(), img.Width, img.Height)
	}
	return got
}

func checkSize(t *testing.T, img platform.Image, w, h int) {
	t.Helper()
	if img.Width != w || img.Height != h {
		t.Fatalf("thumbnail is %dx%d, want %dx%d", img.Width, img.Height, w, h)
	}
	decode(t, img)
}

func isRed(c interface{ RGBA() (r, g, b, a uint32) }) bool {
	r, g, b, _ := c.RGBA()
	return r > 0xC000 && g < 0x4000 && b < 0x4000
}

func TestProperties_JPEG(t *testing.T) {
	props := mustProperties(t, platform.ImageInput{Path: testdata(t, "exif", "sample.jpg")})
	for _, key := range []string{"PixelWidth", "PixelHeight"} {
		if _, ok := props[key].(float64); !ok {
			t.Errorf("%s = %#v, want a number", key, props[key])
		}
	}
	_, tiff := props["{TIFF}"].(map[string]any)
	_, exif := props["{Exif}"].(map[string]any)
	if !tiff && !exif {
		t.Errorf("no {TIFF} or {Exif} dictionary in %v", props)
	}
}

func TestProperties_Data(t *testing.T) {
	path := testdata(t, "exif", "sample.jpg")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fromPath := mustProperties(t, platform.ImageInput{Path: path})
	fromData := mustProperties(t, platform.ImageInput{Data: data})
	// FileSize is only known for a file.
	delete(fromPath, "FileSize")
	delete(fromData, "FileSize")
	if !reflect.DeepEqual(fromPath, fromData) {
		t.Fatalf("from data %v, from path %v", fromData, fromPath)
	}
}

func TestProperties_NonFiniteNumbersDropped(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		props := mustProperties(t, platform.ImageInput{Path: testdata(t, "media", "nan-exif.jpg")})
		exif, ok := props["{Exif}"].(map[string]any)
		if !ok {
			t.Fatalf("no {Exif} dictionary in %v", props)
		}
		// ExposureTime is 1/0. Where ImageIO reports it as a non-finite
		// number it is dropped; current ImageIO reports every x/0 rational
		// as 0, which is kept.
		if v, ok := exif["ExposureTime"]; ok && v != float64(0) {
			t.Errorf("ExposureTime = %#v, want it dropped (or ImageIO's 0)", v)
		}
		for _, key := range []string{"FNumber", "ISOSpeedRatings"} {
			if _, ok := exif[key]; !ok {
				t.Errorf("{Exif} has no %s: %v", key, exif)
			}
		}
		for _, key := range []string{"PixelWidth", "PixelHeight", "{TIFF}"} {
			if _, ok := props[key]; !ok {
				t.Errorf("no %s in %v", key, props)
			}
		}
	})

	t.Run("conversion", func(t *testing.T) {
		props, err := jsonForTest()
		if err != nil {
			t.Fatalf("jsonForTest: %v", err)
		}
		for _, key := range []string{"NaN", "Inf", "NegInf", "NotANumber"} {
			if v, ok := props[key]; ok {
				t.Errorf("%s = %#v, want it dropped", key, v)
			}
		}
		want := map[string]any{
			"Finite": 1.5,
			"Bool":   true,
			"Data":   "00ab7f",
			"List":   []any{1.0, 2.0},
			"7":      "number key",
			"{Exif}": map[string]any{"FNumber": 2.8},
		}
		for key, w := range want {
			if !reflect.DeepEqual(props[key], w) {
				t.Errorf("%s = %#v, want %#v", key, props[key], w)
			}
		}
		if s, ok := props["Date"].(string); !ok || s == "" {
			t.Errorf("Date = %#v, want its description", props["Date"])
		}
	})
}

func TestThumbnail_FitWidth(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{1000, 500, 256, 128},
		{500, 1000, 256, 512},
	} {
		in := platform.ImageInput{Data: platformtest.MakeJPEG(t, tc.w, tc.h)}
		checkSize(t, mustThumbnail(t, in, 256, platform.FitWidth), tc.wantW, tc.wantH)
	}
}

// ImageIO's own rounding makes these 257 and 300 wide; the result is
// brought back to the limit.
func TestThumbnail_FitWidthNeverOverLimit(t *testing.T) {
	for _, tc := range []struct{ w, h, limit int }{
		{600, 653, 256},
		{674, 865, 299},
	} {
		in := platform.ImageInput{Data: platformtest.MakeJPEG(t, tc.w, tc.h)}
		img := mustThumbnail(t, in, tc.limit, platform.FitWidth)
		if img.Width != tc.limit {
			t.Errorf("%dx%d at width %d: %dx%d", tc.w, tc.h, tc.limit, img.Width, img.Height)
		}
		decode(t, img)
	}
}

func TestThumbnail_FitLongSide(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{1000, 500, 256, 128},
		{500, 1000, 128, 256},
	} {
		in := platform.ImageInput{Data: platformtest.MakeJPEG(t, tc.w, tc.h)}
		checkSize(t, mustThumbnail(t, in, 256, platform.FitLongSide), tc.wantW, tc.wantH)
	}
}

func TestThumbnail_NeverUpscales(t *testing.T) {
	in := platform.ImageInput{Data: platformtest.MakeJPEG(t, 100, 50)}
	for _, fit := range []platform.Fit{platform.FitWidth, platform.FitLongSide} {
		checkSize(t, mustThumbnail(t, in, 256, fit), 100, 50)
	}
}

func TestThumbnail_AppliesOrientation(t *testing.T) {
	in := platform.ImageInput{Path: testdata(t, "media", "orient6.jpg")}
	checkSize(t, mustThumbnail(t, in, 16, platform.FitWidth), 16, 32)

	img := mustThumbnail(t, in, 32, platform.FitLongSide)
	checkSize(t, img, 16, 32)
	// The stored left half is red; turned 90° clockwise it is the top half.
	px := decode(t, img)
	if !isRed(px.At(8, 4)) || isRed(px.At(8, 28)) {
		t.Errorf("top %v, bottom %v: want only the top half red", px.At(8, 4), px.At(8, 28))
	}
}

func TestThumbnail_OrientationOverride(t *testing.T) {
	in := platform.ImageInput{Data: platformtest.MakeJPEG(t, 64, 32), Orientation: 6}
	checkSize(t, mustThumbnail(t, in, 32, platform.FitWidth), 32, 64)
}

// quadrants is a w×h JPEG with red, green, blue and yellow quadrants
// (clockwise from the top left) and, when o > 0, EXIF Orientation o.
func quadrants(t *testing.T, w, h, o int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{A: 255}
			switch {
			case x < w/2 && y < h/2:
				c.R = 255
			case y < h/2:
				c.G = 255
			case x >= w/2:
				c.R, c.G = 255, 255
			default:
				c.B = 255
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	j := buf.Bytes()
	if o == 0 {
		return j
	}
	le := binary.LittleEndian
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0}
	tiff = le.AppendUint16(tiff, 1)      // one entry
	tiff = le.AppendUint16(tiff, 0x0112) // Orientation
	tiff = le.AppendUint16(tiff, 3)      // SHORT
	tiff = le.AppendUint32(tiff, 1)
	tiff = le.AppendUint16(tiff, uint16(o))
	tiff = append(tiff, 0, 0, 0, 0, 0, 0) // value padding, no next IFD
	payload := append([]byte("Exif\x00\x00"), tiff...)
	out := append([]byte{}, j[:2]...)
	out = append(out, 0xFF, 0xE1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	out = append(out, payload...)
	return append(out, j[2:]...)
}

// The override is drawn by hand; for every orientation it must match what
// ImageIO does with the same value as the file's tag, also over another tag.
func TestThumbnail_OrientationOverrideMatchesTag(t *testing.T) {
	for o := 1; o <= 8; o++ {
		tagged := mustThumbnail(t, platform.ImageInput{Data: quadrants(t, 64, 32, o)}, 64, platform.FitLongSide)
		want := decode(t, tagged)
		for _, tag := range []int{0, 3} {
			in := platform.ImageInput{Data: quadrants(t, 64, 32, tag), Orientation: o}
			img := mustThumbnail(t, in, 64, platform.FitLongSide)
			if img.Width != tagged.Width || img.Height != tagged.Height {
				t.Fatalf("orientation %d over tag %d: %dx%d, want %dx%d", o, tag, img.Width, img.Height, tagged.Width, tagged.Height)
			}
			got := decode(t, img)
			w, h := img.Width, img.Height
			for _, p := range []image.Point{{w / 4, h / 4}, {3 * w / 4, h / 4}, {w / 4, 3 * h / 4}, {3 * w / 4, 3 * h / 4}} {
				r1, g1, b1, _ := want.At(p.X, p.Y).RGBA()
				r2, g2, b2, _ := got.At(p.X, p.Y).RGBA()
				if r1>>15 != r2>>15 || g1>>15 != g2>>15 || b1>>15 != b2>>15 {
					t.Errorf("orientation %d over tag %d at %v: %v, want %v", o, tag, p, got.At(p.X, p.Y), want.At(p.X, p.Y))
				}
			}
		}
	}
}

func TestThumbnail_HEIC(t *testing.T) {
	in := platform.ImageInput{Path: testdata(t, "media", "sample.heic")}
	checkSize(t, mustThumbnail(t, in, 256, platform.FitLongSide), 256, 192)
}

func TestThumbnail_NotAnImage(t *testing.T) {
	in := platform.ImageInput{Data: []byte("this is not an image, just some text\n")}
	_, err := New().Thumbnail(context.Background(), in, 256, platform.FitLongSide)
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("err = %v, want cannot read", err)
	}
	if _, err := New().Properties(context.Background(), in); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("Properties err = %v, want cannot read", err)
	}
}

func TestThumbnail_ZeroBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jpg")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, in := range []platform.ImageInput{{Path: path}, {Data: []byte{}}} {
		if _, err := New().Thumbnail(context.Background(), in, 256, platform.FitWidth); err == nil {
			t.Errorf("Thumbnail(%q, %d bytes): no error", in.Path, len(in.Data))
		}
	}
}

func TestThumbnail_ReturnsOnCancelDuringCall(t *testing.T) {
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
	// Cancel only once the detached function is running, so the call is
	// really abandoned rather than refused before it starts.
	go func() {
		select {
		case <-entered:
			time.Sleep(100 * time.Millisecond)
		case <-time.After(5 * time.Second):
		}
		cancel()
	}()
	in := platform.ImageInput{Data: platformtest.MakeJPEG(t, 64, 32)}
	_, err := New().Thumbnail(ctx, in, 32, platform.FitWidth)
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
}

func TestEntryPoints_CatchObjCException(t *testing.T) {
	err := raiseForTest("ChoixTestException")
	if err == nil || !strings.Contains(err.Error(), "ChoixTestException") {
		t.Fatalf("err = %v, want one naming ChoixTestException", err)
	}
}
