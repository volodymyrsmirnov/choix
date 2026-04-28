package local

import (
	"image"
	"image/color"
	"math"
	"os"
	"testing"
)

func TestCLIPRequiresSession(t *testing.T) {
	if _, err := CLIPEmbed(nil, image.NewRGBA(image.Rect(0, 0, 1, 1))); err == nil {
		t.Fatal("expected error with nil session")
	}
}

func TestCLIPEmbedShapeAndNorm(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := os.Getenv("CHOIX_MODELS_DIR")
	if dir == "" {
		t.Skip("CHOIX_MODELS_DIR not set")
	}
	store := NewModelStore(dir)
	if !store.Has(ModelCLIP) {
		t.Skipf("clip model not installed at %s", store.Path(ModelCLIP))
	}
	sess, err := NewSession(store.Path(ModelCLIP))
	if err != nil {
		t.Skipf("ONNX runtime unavailable: %v", err)
	}
	defer sess.Close()

	img := image.NewRGBA(image.Rect(0, 0, 224, 224))
	for y := 0; y < 224; y++ {
		for x := 0; x < 224; x++ {
			img.Set(x, y, color.RGBA{200, 100, 50, 255})
		}
	}
	emb, err := CLIPEmbed(sess, img)
	if err != nil {
		t.Fatalf("CLIPEmbed: %v", err)
	}
	if len(emb) != 512 {
		t.Errorf("len=%d, want 512", len(emb))
	}
	var sumSq float64
	for _, v := range emb {
		sumSq += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(sumSq)-1.0) > 1e-3 {
		t.Errorf("not L2-normalized; ||v|| = %v", math.Sqrt(sumSq))
	}
}
