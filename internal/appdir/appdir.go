// Package appdir centralizes the on-disk location of choix's user-level
// state: config, downloaded models, the bundled onnxruntime dylib, and
// vendored external tools. Everything lives under ~/.choix so users can
// see and manage it without diving into ~/Library.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// homeDirFunc is a seam for tests; production code uses os.UserHomeDir.
var homeDirFunc = os.UserHomeDir

// Root returns ~/.choix. Returns an error only when the user has no home
// directory, which on a macOS desktop effectively never happens.
func Root() (string, error) {
	home, err := homeDirFunc()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".choix"), nil
}

// Config returns ~/.choix/config.toml.
func Config() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "config.toml"), nil
}

// Models returns ~/.choix/models, creating it if missing. Returns "" on
// error so callers can degrade gracefully (the analyzer tolerates a
// missing models dir by skipping ML-backed signals).
func Models() string {
	r, err := Root()
	if err != nil {
		return ""
	}
	dir := filepath.Join(r, "models")
	_ = os.MkdirAll(dir, 0o750)
	return dir
}

// Bin returns ~/.choix/bin or "" if the home dir is unavailable. The
// directory is not created here — callers only read it.
func Bin() string {
	r, err := Root()
	if err != nil {
		return ""
	}
	return filepath.Join(r, "bin")
}

// RuntimeDylib returns ~/.choix/lib/libonnxruntime.dylib, the highest-priority
// location ai/local probes for the ONNX runtime. It is an optional user-level
// override: when present (e.g. a manual symlink to a brew-installed dylib) it
// wins, otherwise ai/local falls back to the standard Homebrew prefixes.
func RuntimeDylib() string {
	r, err := Root()
	if err != nil {
		return ""
	}
	return filepath.Join(r, "lib", "libonnxruntime.dylib")
}
