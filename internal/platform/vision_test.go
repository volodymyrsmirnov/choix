package platform_test

import (
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// TestFeaturePrintLen pins the length of a revision-2 feature print: the
// store keeps exactly FeaturePrintLen float32 values (3,072 bytes) per file.
func TestFeaturePrintLen(t *testing.T) {
	if platform.FeaturePrintLen != 768 {
		t.Fatalf("FeaturePrintLen = %d, want 768", platform.FeaturePrintLen)
	}
	if n := platform.FeaturePrintLen * 4; n != 3072 {
		t.Fatalf("a stored print is %d bytes, want 3072", n)
	}
}
