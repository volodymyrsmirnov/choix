//go:build production

package local

import "testing"

func TestRuntimeLibraryCandidates_ShippingHasBundleOnly(t *testing.T) {
	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "/d/l.dylib")
	t.Setenv("HOMEBREW_PREFIX", "/hb")
	got := RuntimeLibraryCandidates("/x/Choix.app/Contents/MacOS/Choix")
	if len(got) != 1 || got[0] != "/x/Choix.app/Contents/Frameworks/libonnxruntime.dylib" {
		t.Fatalf("candidates = %v, want exactly the bundled library", got)
	}

	// Outside an app bundle the shipping build has nowhere to look.
	if got := RuntimeLibraryCandidates("/usr/local/bin/choix"); len(got) != 0 {
		t.Errorf("candidates outside a bundle = %v, want none", got)
	}
}
