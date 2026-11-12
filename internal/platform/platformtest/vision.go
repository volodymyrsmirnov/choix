package platformtest

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // decode JPEG inputs
	_ "image/png"  // decode PNG inputs
	"math"
	"os"
	"slices"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// visionGrid is the side of the grid of cells whose mean colours make the
// fake's vector: 16 × 16 cells × 3 channels = platform.FeaturePrintLen.
const visionGrid = 16

// Vision is a platform.Vision whose feature print is a deterministic
// function of the decoded pixels (FeaturePrintOf): equal images give equal
// vectors and visibly different images different ones. Inputs are decoded
// with the standard library (JPEG and PNG); a Path input is opened
// read-only. Every call goes through platform.Detach, so it takes a native
// slot and honours cancellation like the real one.
//
// An input the standard library cannot decode gives an error wrapping
// platform.ErrUnreadableImage, as the real one does for an image Vision
// cannot read; Fail can return either class of failure.
//
// Fail is keyed by ImageInput.Path; an input given as Data uses the key "".
// Set the fields before the fake is used; SetFail changes Fail while it is.
type Vision struct {
	// Fail makes every call for a key return that error.
	Fail map[string]error
	// Gate, when non-nil, holds every call open, inside its detached
	// function, until it receives a value or is closed.
	Gate chan struct{}

	mu    sync.Mutex
	calls []string
}

var _ platform.Vision = (*Vision)(nil)

// Calls returns the key (Path, or "" for Data) of every call made with a
// valid input, in order.
func (v *Vision) Calls() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.calls)
}

// SetFail makes every later call for key return err (nil: answer
// normally). It is safe while calls run.
func (v *Vision) SetFail(key string, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err == nil {
		delete(v.Fail, key)
		return
	}
	if v.Fail == nil {
		v.Fail = map[string]error{}
	}
	v.Fail[key] = err
}

func (v *Vision) failure(key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.Fail[key]
}

// FeaturePrint implements platform.Vision.
func (v *Vision) FeaturePrint(ctx context.Context, in platform.ImageInput) ([]float32, error) {
	key, err := inputKey(in)
	if err != nil {
		return nil, err
	}
	data := bytes.Clone(in.Data)
	v.mu.Lock()
	v.calls = append(v.calls, key)
	v.mu.Unlock()
	return platform.Detach(ctx, func() ([]float32, error) {
		wait(v.Gate)
		if err := v.failure(key); err != nil {
			return nil, err
		}
		if in.Path != "" {
			b, err := os.ReadFile(in.Path) //nolint:gosec // a test fixture chosen by the test; only read
			if err != nil {
				return nil, fmt.Errorf("vision: %w", err)
			}
			data = b
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("vision: cannot read %s: %w (%w)", nameOf(in), platform.ErrUnreadableImage, err)
		}
		return FeaturePrintOf(img), nil
	})
}

func nameOf(in platform.ImageInput) string {
	if in.Path != "" {
		return in.Path
	}
	return "image data"
}

// FeaturePrintOf is the fake's feature print of img: the mean colour of each
// cell of a 16 × 16 grid over the image, centred on mid grey, as a unit
// vector of platform.FeaturePrintLen values. An image of uniform mid grey,
// whose centred vector is zero, gets the first unit vector.
func FeaturePrintOf(img image.Image) []float32 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	sums := make([]float64, platform.FeaturePrintLen)
	counts := make([]float64, visionGrid*visionGrid)
	for y := range h {
		cy := y * visionGrid / max(h, 1)
		for x := range w {
			cx := x * visionGrid / max(w, 1)
			cell := cy*visionGrid + cx
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			sums[3*cell] += float64(r) / 0xFFFF
			sums[3*cell+1] += float64(g) / 0xFFFF
			sums[3*cell+2] += float64(bl) / 0xFFFF
			counts[cell]++
		}
	}
	vec := make([]float64, platform.FeaturePrintLen)
	var norm float64
	for i := range vec {
		if n := counts[i/3]; n > 0 {
			vec[i] = sums[i]/n - 0.5
		}
		norm += vec[i] * vec[i]
	}
	out := make([]float32, platform.FeaturePrintLen)
	norm = math.Sqrt(norm)
	if norm < 1e-6 {
		out[0] = 1
		return out
	}
	for i, x := range vec {
		out[i] = float32(x / norm)
	}
	return out
}
