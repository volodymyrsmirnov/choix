package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "libonnxruntime.dylib")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "absent.dylib")

	// A dangling symlink must be treated as missing — this is the exact
	// failure mode that broke CLIP loading when brew upgraded onnxruntime
	// out from under a versioned Cellar symlink.
	dangling := filepath.Join(dir, "dangling.dylib")
	if err := os.Symlink(filepath.Join(dir, "gone"), dangling); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"skips blanks", []string{"", missing}, ""},
		{"first match wins", []string{real, missing}, real},
		{"falls through to second", []string{missing, real}, real},
		{"dangling symlink skipped", []string{dangling, real}, real},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstExisting(tc.in); got != tc.want {
				t.Errorf("firstExisting(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRuntimeDylibCandidatesIncludesHomebrew(t *testing.T) {
	t.Setenv("HOMEBREW_PREFIX", "/fake/brew")
	got := runtimeDylibCandidates()

	wantSubstrings := []string{
		"/fake/brew/opt/onnxruntime/lib/libonnxruntime.dylib",
		"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib",
		"/usr/local/opt/onnxruntime/lib/libonnxruntime.dylib",
	}
	for _, want := range wantSubstrings {
		found := false
		for _, c := range got {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("candidates %v missing %q", got, want)
		}
	}
}

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
