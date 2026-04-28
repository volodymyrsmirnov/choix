package local

import (
	"errors"
	"fmt"
	"image"
	"math"

	"golang.org/x/image/draw"
)

// preprocessNCHW resizes img to side×side, converts to float32 NCHW with
// (pixel/255 − mean)/std per channel. Buffer is freshly allocated so the
// caller can hand it straight to OrtSession.Run.
func preprocessNCHW(img image.Image, side int, mean, std [3]float32) []float32 {
	resized := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.CatmullRom.Scale(resized, resized.Rect, img, img.Bounds(), draw.Over, nil)
	out := make([]float32, 3*side*side)
	plane := side * side
	off := 0
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			r := float32(resized.Pix[y*resized.Stride+x*4])
			g := float32(resized.Pix[y*resized.Stride+x*4+1])
			b := float32(resized.Pix[y*resized.Stride+x*4+2])
			out[off] = (r/255 - mean[0]) / std[0]
			out[off+plane] = (g/255 - mean[1]) / std[1]
			out[off+2*plane] = (b/255 - mean[2]) / std[2]
			off++
		}
	}
	return out
}

// CLIP-specific normalization (OpenAI CLIP).
var (
	clipMean = [3]float32{0.48145466, 0.4578275, 0.40821073}
	clipStd  = [3]float32{0.26862954, 0.26130258, 0.27577711}
)

// CLIPEmbed runs a ViT-B/32 ONNX session on img and returns a 512-dim
// L2-normalized embedding.
func CLIPEmbed(session *OrtSession, img image.Image) ([]float32, error) {
	if session == nil {
		return nil, errors.New("nil session")
	}
	const side = 224
	in := preprocessNCHW(img, side, clipMean, clipStd)

	out, err := session.Run(in, []int64{1, 3, side, side})
	if err != nil {
		return nil, fmt.Errorf("clip inference: %w", err)
	}
	if len(out) != 512 {
		return nil, fmt.Errorf("clip output len=%d, want 512", len(out))
	}

	// L2 normalize in-place on the freshly allocated slice.
	var sumSq float64
	for _, v := range out {
		sumSq += float64(v) * float64(v)
	}
	norm := math.Sqrt(sumSq)
	if norm == 0 {
		return out, nil
	}
	inv := float32(1.0 / norm)
	for i := range out {
		out[i] *= inv
	}
	return out, nil
}
