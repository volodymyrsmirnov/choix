package platform

import (
	"context"
	"errors"
)

// ErrNoVideoTrack reports a video file without a video track.
var ErrNoVideoTrack = errors.New("platform: no video track")

// ImageInput is the source of a still: exactly one of Path and Data is set.
// Orientation 1–8 overrides the source's own orientation tag; 0 keeps it.
type ImageInput struct {
	Path        string
	Data        []byte
	Orientation int
}

// Image is an encoded JPEG and its pixel size.
type Image struct {
	JPEG          []byte
	Width, Height int
}

// Fit selects which dimension a thumbnail's limit bounds.
type Fit int

const (
	// FitWidth bounds the width.
	FitWidth Fit = iota
	// FitLongSide bounds the longer side.
	FitLongSide
)

// Stills reads still images through ImageIO.
type Stills interface {
	// Properties returns ImageIO's property dictionary for image 0,
	// JSON-shaped, with non-finite numbers dropped. It decodes no pixels.
	Properties(ctx context.Context, in ImageInput) (map[string]any, error)
	// Thumbnail returns a JPEG (quality 0.85) with orientation applied whose
	// width (FitWidth) or long side (FitLongSide) is at most limit, aspect
	// kept, never upscaled. It writes no file.
	Thumbnail(ctx context.Context, in ImageInput, limit int, fit Fit) (Image, error)
}

// Video reads video files through AVFoundation.
type Video interface {
	// Properties returns the video's properties, JSON-shaped.
	Properties(ctx context.Context, path string) (map[string]any, error)
	// Frames returns one full-size JPEG per fraction of the duration. A file
	// without a video track is ErrNoVideoTrack.
	Frames(ctx context.Context, path string, fractions []float64) ([]Image, error)
}
