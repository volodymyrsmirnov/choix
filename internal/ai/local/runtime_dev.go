//go:build !production

package local

import (
	"os"
	"path/filepath"
)

// devRuntimeCandidates are the development fallbacks after the bundled
// library, in priority order:
//
//  1. $CHOIX_ONNXRUNTIME_DYLIB — an explicit library, e.g. the one
//     build/onnxruntime/fetch.sh puts in build/.tools/onnxruntime/lib/.
//     Only an absolute path is honoured.
//  2. $HOMEBREW_PREFIX/opt/onnxruntime/lib/libonnxruntime.dylib — a
//     non-standard Homebrew root.
//  3. /opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib — the stock
//     Apple Silicon Homebrew prefix.
//
// The Homebrew paths go through the *opt* symlink, never a versioned Cellar
// path, so `brew upgrade onnxruntime` cannot strand the loader. None of
// this exists in the shipping build (runtime_ship.go).
func devRuntimeCandidates() []string {
	var c []string
	if p := os.Getenv("CHOIX_ONNXRUNTIME_DYLIB"); p != "" && filepath.IsAbs(p) {
		c = append(c, filepath.Clean(p))
	}
	const rel = "opt/onnxruntime/lib/" + runtimeLibraryName
	if pfx := os.Getenv("HOMEBREW_PREFIX"); pfx != "" && filepath.IsAbs(pfx) {
		c = append(c, filepath.Join(pfx, rel))
	}
	return append(c, filepath.Join("/opt/homebrew", rel))
}
