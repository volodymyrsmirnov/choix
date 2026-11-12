package platform

import (
	"context"
	"errors"
)

// ErrUnreadableImage is wrapped by a FeaturePrint error when the image
// itself cannot be decoded: the input's fault, so asking again gives the same
// answer. Any other FeaturePrint error is the framework's, possibly
// transient.
var ErrUnreadableImage = errors.New("unreadable image")

// FeaturePrintLen is the length of a Vision image feature print at revision 2.
const FeaturePrintLen = 768

// Vision is Apple's Vision framework, as far as the app uses it.
type Vision interface {
	// FeaturePrint returns the image feature print of a still: revision 2,
	// crop-and-scale "scale fill", FeaturePrintLen float32 values, as Vision
	// returns them. Exactly one of in.Path and in.Data is set; in.Orientation
	// is ignored (the source's own orientation tag is applied). An image
	// Vision cannot decode gives an error wrapping ErrUnreadableImage. It
	// writes no file.
	FeaturePrint(ctx context.Context, in ImageInput) ([]float32, error)
}
