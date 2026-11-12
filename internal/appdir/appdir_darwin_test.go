//go:build darwin

package appdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sandboxed reads libc's environment. This package links runtime/cgo, so
// os.Setenv / os.Unsetenv (and therefore t.Setenv and its cleanup) also
// call libc setenv / unsetenv (runtime.setenv_c → _cgo_setenv): the libc
// read sees what the test sets, and the original value is restored.
func TestSandboxed(t *testing.T) {
	const key = "APP_SANDBOX_CONTAINER_ID"
	orig := sandboxContainerID()

	t.Run("cases", func(t *testing.T) {
		t.Setenv(key, "") // registers the restore of the original value
		if Sandboxed() {
			t.Error("Sandboxed() = true with the variable empty")
		}

		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		if Sandboxed() {
			t.Error("Sandboxed() = true with the variable unset")
		}

		t.Setenv(key, "com.github.volodymyrsmirnov.choix")
		if got := sandboxContainerID(); got != "com.github.volodymyrsmirnov.choix" {
			t.Fatalf("libc getenv = %q: os.Setenv did not reach libc", got)
		}
		if !Sandboxed() {
			t.Error("Sandboxed() = false with the variable set")
		}
	})

	if got := sandboxContainerID(); got != orig {
		t.Errorf("libc value after the test = %q, want the original %q", got, orig)
	}
}

// A `go test` binary is not inside an .app bundle, so there is no bundle
// Frameworks directory. (Inside a sandboxed bundle, launched by absolute and
// by relative path, it was verified by hand: see the Task 22 report.)
func TestBundleFrameworksDir_NotABundle(t *testing.T) {
	if got := BundleFrameworksDir(); got != "" {
		t.Errorf("BundleFrameworksDir = %q outside an app bundle, want empty", got)
	}
}

// The search-path lookups only read; they create nothing.
func TestSearchPaths(t *testing.T) {
	support, err := applicationSupportDir()
	if err != nil {
		t.Fatalf("applicationSupportDir: %v", err)
	}
	if !filepath.IsAbs(support) || !strings.HasSuffix(support, "/Library/Application Support") {
		t.Errorf("applicationSupportDir = %q, want an absolute path ending with /Library/Application Support", support)
	}

	lib, err := libraryDir()
	if err != nil {
		t.Fatalf("libraryDir: %v", err)
	}
	if !filepath.IsAbs(lib) || !strings.HasSuffix(lib, "/Library") {
		t.Errorf("libraryDir = %q, want an absolute path ending with /Library", lib)
	}
}
