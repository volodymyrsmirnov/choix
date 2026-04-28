package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrtSessionRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping ONNX session test")
	}

	model := filepath.Join("testdata", "identity.onnx")
	if _, err := os.Stat(model); err != nil {
		t.Skipf("identity.onnx fixture not available: %v", err)
	}

	sess, err := NewSession(model)
	if err != nil {
		t.Skipf("ONNX runtime unavailable: %v", err)
	}
	defer sess.Close()

	in := []float32{1, 2, 3, 4}
	out, err := sess.Run(in, []int64{1, 4})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out)=%d want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("out[%d]=%v want %v", i, out[i], in[i])
		}
	}
}
