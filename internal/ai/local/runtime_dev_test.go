//go:build !production

package local

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRuntimeLibraryCandidates_BundleFirst(t *testing.T) {
	t.Setenv("HOMEBREW_PREFIX", "")
	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "")
	const exe = "/x/Choix.app/Contents/MacOS/Choix"
	const bundled = "/x/Choix.app/Contents/Frameworks/libonnxruntime.dylib"

	got := RuntimeLibraryCandidates(exe)
	if len(got) == 0 || got[0] != bundled {
		t.Fatalf("candidates = %v, want %q first", got, bundled)
	}

	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "/d/l.dylib")
	got = RuntimeLibraryCandidates(exe)
	if len(got) < 2 || got[0] != bundled || got[1] != "/d/l.dylib" {
		t.Fatalf("candidates = %v, want %q then /d/l.dylib", got, bundled)
	}
	if !slices.Contains(got, "/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib") {
		t.Errorf("candidates = %v, want the stock Homebrew path", got)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if strings.HasPrefix(c, "/usr/local/") {
			t.Errorf("candidate %q is under /usr/local", c)
		}
		if home != "" && strings.HasPrefix(c, filepath.Clean(home)+"/") {
			t.Errorf("candidate %q is under the home directory %q", c, home)
		}
	}
}

func TestRuntimeLibraryCandidates_DevFallbacks(t *testing.T) {
	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "")
	t.Setenv("HOMEBREW_PREFIX", "/hb")
	got := RuntimeLibraryCandidates("/x/Choix.app/Contents/MacOS/Choix")
	want := []string{
		"/x/Choix.app/Contents/Frameworks/libonnxruntime.dylib",
		"/hb/opt/onnxruntime/lib/libonnxruntime.dylib",
		"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib",
	}
	if !slices.Equal(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}

	// The stock prefix is listed once even when HOMEBREW_PREFIX names it.
	t.Setenv("HOMEBREW_PREFIX", "/opt/homebrew")
	got = RuntimeLibraryCandidates("/x/Choix.app/Contents/MacOS/Choix")
	if want := want[:1]; !slices.Equal(got, append(want, "/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib")) {
		t.Errorf("candidates = %v, want the stock Homebrew path once", got)
	}

	// A relative override is ignored: it would resolve against whatever the
	// working directory happens to be.
	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "lib/libonnxruntime.dylib")
	for _, c := range RuntimeLibraryCandidates("/x/Choix.app/Contents/MacOS/Choix") {
		if !filepath.IsAbs(c) {
			t.Errorf("candidate %q is not absolute", c)
		}
	}
}
