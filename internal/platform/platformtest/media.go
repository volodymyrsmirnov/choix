package platformtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// Default frame size of a Video fake when Props gives none.
const (
	defaultFrameWidth  = 64
	defaultFrameHeight = 48
)

// Stills is a platform.Stills that answers from configuration and makes
// real JPEGs with image/jpeg. Every call goes through platform.Detach, so it
// takes a native slot and honours cancellation like the real one.
//
// Props and Fail are keyed by ImageInput.Path; an input given as Data uses
// the key "". Set the fields before the fake is used. Calls reports every
// call made with a valid input.
type Stills struct {
	// Props is what Properties returns for a key (a copy); an unconfigured
	// key yields an empty map. "PixelWidth", "PixelHeight" and "Orientation"
	// also give a Path input its source size and orientation in Thumbnail;
	// without a size, a Path input is treated as a limit×limit square.
	Props map[string]map[string]any
	// Fail makes every call for a key return that error.
	Fail map[string]error
	// Gate, when non-nil, holds every call open, inside its detached
	// function, until it receives a value or is closed.
	Gate chan struct{}
	// ReadPaths makes Thumbnail size a Path input that has no size in Props
	// from the file itself: the file is opened read-only and its JPEG header
	// decoded, and the call fails, as a native decode does, when it cannot be
	// read or is not a JPEG. Without it such an input is a limit×limit
	// square.
	ReadPaths bool

	mu    sync.Mutex
	calls []StillsCall
}

var _ platform.Stills = (*Stills)(nil)

// StillsCall is one call a Stills fake received with a valid input, as
// Calls reports it.
type StillsCall struct {
	// Method is "Properties" or "Thumbnail".
	Method string
	// Path, Data and Orientation are the call's ImageInput; Data is a copy.
	Path        string
	Data        []byte
	Orientation int
	// Limit and Fit are Thumbnail's arguments; zero for Properties.
	Limit int
	Fit   platform.Fit
}

// Calls returns the calls received so far, in order.
func (s *Stills) Calls() []StillsCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// record appends a call to the log.
func (s *Stills) record(c StillsCall) {
	c.Data = bytes.Clone(c.Data)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

// Video is a platform.Video that answers from configuration and makes real
// JPEG frames with image/jpeg. Every call goes through platform.Detach.
// Props and Fail are keyed by path; set the fields before the fake is used.
type Video struct {
	// Props is what Properties returns for a path (a copy); an unconfigured
	// path yields an empty map. "PixelWidth" and "PixelHeight" also set the
	// frame size, 64×48 by default.
	Props map[string]map[string]any
	// Fail makes every call for a path return that error, for example
	// platform.ErrNoVideoTrack.
	Fail map[string]error
	// Gate, when non-nil, holds every call open, inside its detached
	// function, until it receives a value or is closed.
	Gate chan struct{}
}

var _ platform.Video = (*Video)(nil)

// inputKey returns the Props/Fail key of in after checking that exactly one
// of Path and Data is set.
func inputKey(in platform.ImageInput) (string, error) {
	if (in.Path == "") == (in.Data == nil) {
		return "", errors.New("platformtest: image input: exactly one of Path and Data must be set")
	}
	return in.Path, nil
}

// wait blocks on gate when it is non-nil.
func wait(gate chan struct{}) {
	if gate != nil {
		<-gate
	}
}

// Properties returns a copy of Props for the input's key.
func (s *Stills) Properties(ctx context.Context, in platform.ImageInput) (map[string]any, error) {
	key, err := inputKey(in)
	if err != nil {
		return nil, err
	}
	s.record(StillsCall{Method: "Properties", Path: in.Path, Data: in.Data, Orientation: in.Orientation})
	return platform.Detach(ctx, func() (map[string]any, error) {
		wait(s.Gate)
		if err := s.Fail[key]; err != nil {
			return nil, err
		}
		return propsCopy(s.Props[key]), nil
	})
}

// Thumbnail returns a real JPEG whose size follows the platform contract.
// Data is first decoded completely with image/jpeg, and the call fails when
// that fails, as a damaged preview does natively.
func (s *Stills) Thumbnail(ctx context.Context, in platform.ImageInput, limit int, fit platform.Fit) (platform.Image, error) {
	key, err := inputKey(in)
	if err != nil {
		return platform.Image{}, err
	}
	if limit <= 0 {
		return platform.Image{}, fmt.Errorf("platformtest: thumbnail: limit %d must be positive", limit)
	}
	if fit != platform.FitWidth && fit != platform.FitLongSide {
		return platform.Image{}, fmt.Errorf("platformtest: thumbnail: unknown fit %d", fit)
	}
	s.record(StillsCall{Method: "Thumbnail", Path: in.Path, Data: in.Data, Orientation: in.Orientation, Limit: limit, Fit: fit})
	return platform.Detach(ctx, func() (platform.Image, error) {
		wait(s.Gate)
		if err := s.Fail[key]; err != nil {
			return platform.Image{}, err
		}
		props := s.Props[key]
		orientation := in.Orientation
		if orientation == 0 {
			orientation, _ = propInt(props, "Orientation")
		}
		var w, h int
		if in.Data != nil {
			img, err := jpeg.Decode(bytes.NewReader(in.Data))
			if err != nil {
				return platform.Image{}, fmt.Errorf("platformtest: thumbnail: decode data: %w", err)
			}
			w, h = img.Bounds().Dx(), img.Bounds().Dy()
		} else {
			pw, okW := propInt(props, "PixelWidth")
			ph, okH := propInt(props, "PixelHeight")
			switch {
			case okW && okH && pw > 0 && ph > 0:
				w, h = pw, ph
			case s.ReadPaths:
				var rerr error
				if w, h, rerr = jpegFileSize(in.Path); rerr != nil {
					return platform.Image{}, fmt.Errorf("platformtest: thumbnail: %w", rerr)
				}
			default:
				w, h = limit, limit
			}
		}
		if orientation >= 5 && orientation <= 8 {
			w, h = h, w
		}
		w, h = fitSize(w, h, limit, fit)
		data, err := encodeJPEG(w, h)
		if err != nil {
			return platform.Image{}, fmt.Errorf("platformtest: thumbnail: %w", err)
		}
		return platform.Image{JPEG: data, Width: w, Height: h}, nil
	})
}

// Properties returns a copy of Props for path.
func (v *Video) Properties(ctx context.Context, path string) (map[string]any, error) {
	return platform.Detach(ctx, func() (map[string]any, error) {
		wait(v.Gate)
		if err := v.Fail[path]; err != nil {
			return nil, err
		}
		return propsCopy(v.Props[path]), nil
	})
}

// Frames returns one real JPEG per fraction, each fraction in [0, 1].
func (v *Video) Frames(ctx context.Context, path string, fractions []float64) ([]platform.Image, error) {
	for _, f := range fractions {
		if !(f >= 0 && f <= 1) { // also rejects NaN
			return nil, fmt.Errorf("platformtest: frames %s: fraction %v outside [0, 1]", path, f)
		}
	}
	return platform.Detach(ctx, func() ([]platform.Image, error) {
		wait(v.Gate)
		if err := v.Fail[path]; err != nil {
			return nil, err
		}
		w, h := defaultFrameWidth, defaultFrameHeight
		props := v.Props[path]
		if pw, ok := propInt(props, "PixelWidth"); ok && pw > 0 {
			w = pw
		}
		if ph, ok := propInt(props, "PixelHeight"); ok && ph > 0 {
			h = ph
		}
		frames := make([]platform.Image, 0, len(fractions))
		for range fractions {
			data, err := encodeJPEG(w, h)
			if err != nil {
				return nil, fmt.Errorf("platformtest: frames %s: %w", path, err)
			}
			frames = append(frames, platform.Image{JPEG: data, Width: w, Height: h})
		}
		return frames, nil
	})
}

// jpegFileSize opens path read-only and returns the size in its JPEG header.
func jpegFileSize(path string) (int, int, error) {
	f, err := os.Open(path) //nolint:gosec // a test fixture chosen by the test; only read
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		return 0, 0, fmt.Errorf("decode %s: %w", path, err)
	}
	return cfg.Width, cfg.Height, nil
}

// propsCopy returns a shallow copy of props, never nil.
func propsCopy(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	maps.Copy(out, props)
	return out
}

// propInt reads an integral number from props.
func propInt(props map[string]any, key string) (int, bool) {
	switch n := props[key].(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// fitSize scales w×h so the bounded dimension is at most limit, keeping the
// aspect ratio and never upscaling; neither side drops below 1.
func fitSize(w, h, limit int, fit platform.Fit) (int, int) {
	bound := w
	if fit == platform.FitLongSide && h > w {
		bound = h
	}
	if bound <= limit {
		return w, h
	}
	scale := func(n int) int { return max(1, (n*limit+bound/2)/bound) }
	return scale(w), scale(h)
}
