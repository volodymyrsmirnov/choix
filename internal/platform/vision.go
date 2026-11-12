package platform

import "context"

// FeaturePrintLen is the length of a Vision image feature print at revision 2.
const FeaturePrintLen = 768

// Vision is Apple's Vision framework, as far as the app uses it.
type Vision interface {
	// FeaturePrint returns the image feature print of a still: revision 2,
	// crop-and-scale "scale fill", FeaturePrintLen float32 values, as Vision
	// returns them. Exactly one of in.Path and in.Data is set; in.Orientation
	// is ignored (the source's own orientation tag is applied). It writes no
	// file.
	FeaturePrint(ctx context.Context, in ImageInput) ([]float32, error)
}
