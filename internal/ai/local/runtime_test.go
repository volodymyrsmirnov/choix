package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bundled library is derived only from an absolute executable path that
// really sits in <name>.app/Contents/MacOS; anything else (a relative
// argv[0] resolved against the sandbox container, a bare binary in a source
// tree) yields no bundle candidate rather than a stray Frameworks path.
func TestRuntimeLibraryCandidates_BundleLayout(t *testing.T) {
	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "")
	t.Setenv("HOMEBREW_PREFIX", "")
	cases := []struct {
		exe  string
		want string // "" = no bundle candidate
	}{
		{"/x/Choix.app/Contents/MacOS/Choix", "/x/Choix.app/Contents/Frameworks/libonnxruntime.dylib"},
		{"/Applications/Choix.app/Contents/MacOS/choix", "/Applications/Choix.app/Contents/Frameworks/libonnxruntime.dylib"},
		{"/x/Choix.app/Contents/MacOS/../MacOS/Choix", "/x/Choix.app/Contents/Frameworks/libonnxruntime.dylib"},
		{"bin/Choix.app/Contents/MacOS/Choix", ""},
		{"", ""},
		{"/Users/me/src/choix/choix", ""},
		{"/x/Choix/Contents/MacOS/Choix", ""},
		{"/x/Choix.app/Contents/Resources/Choix", ""},
		{"/x/Choix.app/MacOS/Choix", ""},
		{"/x/.app/Contents/MacOS/Choix", ""},
	}
	for _, c := range cases {
		got := RuntimeLibraryCandidates(c.exe)
		if c.want != "" {
			if len(got) == 0 || got[0] != c.want {
				t.Errorf("RuntimeLibraryCandidates(%q) = %v, want %q first", c.exe, got, c.want)
			}
			continue
		}
		for _, p := range got {
			if strings.Contains(p, "/Frameworks/") {
				t.Errorf("RuntimeLibraryCandidates(%q) = %v, want no bundle candidate", c.exe, got)
			}
		}
	}
}

// The process's own lookup starts from the main bundle's Frameworks
// directory and accepts only a real <name>.app/Contents/Frameworks path.
func TestRuntimeCandidatesFor_FrameworksDir(t *testing.T) {
	t.Setenv("CHOIX_ONNXRUNTIME_DYLIB", "")
	t.Setenv("HOMEBREW_PREFIX", "")
	const fw = "/x/Choix.app/Contents/Frameworks"
	if got := runtimeCandidatesFor(fw); len(got) == 0 || got[0] != fw+"/libonnxruntime.dylib" {
		t.Errorf("runtimeCandidatesFor(%q) = %v, want the bundled library first", fw, got)
	}
	for _, bad := range []string{"", "Choix.app/Contents/Frameworks", "/x/Choix/Contents/Frameworks",
		"/x/Choix.app/Frameworks", "/x/Choix.app/Contents/Frameworks/../Frameworks", "/tmp/go-build1/b001/Frameworks"} {
		for _, p := range runtimeCandidatesFor(bad) {
			if strings.Contains(p, "/Frameworks/") {
				t.Errorf("runtimeCandidatesFor(%q) = %v, want no bundle candidate", bad, runtimeCandidatesFor(bad))
			}
		}
	}
	// A test binary is not in a bundle, so its own lookup has no bundle entry.
	for _, p := range defaultRuntimeCandidates() {
		if strings.Contains(p, "/Frameworks/") {
			t.Errorf("defaultRuntimeCandidates() = %v outside a bundle", defaultRuntimeCandidates())
		}
	}
}

// With no runtime library reachable, initialisation fails with an error that
// names where it looked, never falling back to the dynamic loader's own
// search; the outcome is remembered until the state is reset.
func TestEnsureRuntime_NoLibraryIsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "libonnxruntime.dylib")
	restore := SetRuntimeCandidatesForTest([]string{missing})
	t.Cleanup(restore)

	err := ensureRuntime()
	if err == nil {
		t.Fatal("ensureRuntime with no library = nil, want an error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the path it tried", err)
	}

	// Remembered: a library appearing later is not picked up by this process.
	if err := os.WriteFile(missing, []byte("not a dylib"), 0o600); err != nil {
		t.Fatal(err)
	}
	if again := ensureRuntime(); again == nil || again.Error() != err.Error() {
		t.Errorf("second ensureRuntime = %v, want the remembered %v", again, err)
	}

	if _, err := NewSession(filepath.Join(t.TempDir(), "m.onnx"), true); err == nil {
		t.Error("NewSession without a runtime = nil error")
	}
}

// RuntimeVersion shares ensureRuntime's outcome: with no library it is the
// same remembered error and no version.
func TestRuntimeVersion_NoLibraryIsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "libonnxruntime.dylib")
	t.Cleanup(SetRuntimeCandidatesForTest([]string{missing}))

	v, err := RuntimeVersion()
	if err == nil || v != "" {
		t.Fatalf("RuntimeVersion with no library = %q, %v; want \"\", an error", v, err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the path it tried", err)
	}
	if again := ensureRuntime(); again == nil || again.Error() != err.Error() {
		t.Errorf("ensureRuntime after RuntimeVersion = %v, want the remembered %v", again, err)
	}
}

// With a library reachable, RuntimeVersion reports the loaded library's
// version.
func TestRuntimeVersion_Loaded(t *testing.T) {
	skipWithoutRuntime(t)
	v, err := RuntimeVersion()
	if err != nil || v == "" {
		t.Fatalf("RuntimeVersion = %q, %v; want a version", v, err)
	}
}

// resetRuntimeForTest makes the next ensureRuntime probe the candidates again.
func TestResetRuntimeForTest(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	restore := SetRuntimeCandidatesForTest(nil)
	t.Cleanup(restore)
	runtimeMu.Lock()
	runtimeCandidates = func() []string { calls++; return []string{filepath.Join(dir, "absent.dylib")} }
	runtimeMu.Unlock()

	_ = ensureRuntime()
	_ = ensureRuntime()
	if calls != 1 {
		t.Fatalf("candidates probed %d times, want once", calls)
	}
	resetRuntimeForTest()
	_ = ensureRuntime()
	if calls != 2 {
		t.Fatalf("candidates probed %d times after reset, want 2", calls)
	}
}
