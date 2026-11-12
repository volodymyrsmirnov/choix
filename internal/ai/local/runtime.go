package local

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// runtimeLibraryName is the file name of the ONNX Runtime library inside the
// app bundle's Contents/Frameworks directory and in every fallback location.
const runtimeLibraryName = "libonnxruntime.dylib"

// errRuntimeMissing reports that none of the candidate locations holds an
// ONNX Runtime library.
var errRuntimeMissing = errors.New("onnxruntime library not found")

// RuntimeLibraryCandidates lists where the ONNX Runtime library is looked
// for, in priority order, for a process whose executable is exe.
//
// When exe is an absolute path to <name>.app/Contents/MacOS/<binary>, the
// first candidate is always the bundled library,
// <name>.app/Contents/Frameworks/libonnxruntime.dylib. Any other exe — a
// relative path, a binary outside an app bundle — contributes no bundle
// candidate, so a stray "../Frameworks" next to an arbitrary binary is never
// loaded. Builds without the `production` tag then add development fallbacks
// (see runtime_dev.go); the shipping build adds nothing.
func RuntimeLibraryCandidates(exe string) []string {
	frameworks := ""
	if filepath.IsAbs(exe) {
		if macOS := filepath.Dir(filepath.Clean(exe)); filepath.Base(macOS) == "MacOS" {
			frameworks = filepath.Join(filepath.Dir(macOS), "Frameworks")
		}
	}
	return runtimeCandidatesFor(frameworks)
}

// runtimeCandidatesFor lists the candidates for an app bundle whose
// Contents/Frameworks directory is frameworks: the bundled library first
// when frameworks really is an absolute <name>.app/Contents/Frameworks
// path, then the development fallbacks.
func runtimeCandidatesFor(frameworks string) []string {
	var c []string
	if isBundleFrameworksDir(frameworks) {
		c = append(c, filepath.Join(frameworks, runtimeLibraryName))
	}
	for _, p := range devRuntimeCandidates() {
		if !slices.Contains(c, p) {
			c = append(c, p)
		}
	}
	return c
}

// isBundleFrameworksDir reports whether p is an absolute, clean path of the
// form <name>.app/Contents/Frameworks.
func isBundleFrameworksDir(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	contents := filepath.Dir(p)
	app := filepath.Base(filepath.Dir(contents))
	return filepath.Base(p) == "Frameworks" && filepath.Base(contents) == "Contents" &&
		len(app) > len(".app") && strings.HasSuffix(app, ".app")
}

// defaultRuntimeCandidates lists the candidates for the running process. The
// bundle's Frameworks directory comes from Foundation's main bundle
// (appdir.BundleFrameworksDir), never from argv[0], which a sandboxed
// process started through a relative path resolves against its container.
func defaultRuntimeCandidates() []string {
	return runtimeCandidatesFor(appdir.BundleFrameworksDir())
}

// Runtime initialisation state. ONNX Runtime is initialised at most once per
// process: the first ensureRuntime call decides, later calls return the same
// outcome. Tests reset it with resetRuntimeForTest.
var (
	runtimeMu sync.Mutex
	// runtimeCandidates supplies the library locations to probe; tests
	// replace it (SetRuntimeCandidatesForTest) to get a deterministic
	// "no library" outcome on any machine.
	runtimeCandidates = defaultRuntimeCandidates
	runtimeDone       bool
	runtimeErr        error
)

// ensureRuntime initialises the ONNX Runtime environment from the first
// candidate library that exists. With none reachable it fails without
// asking the dynamic loader to search on its own.
func ensureRuntime() error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if runtimeDone {
		return runtimeErr
	}
	runtimeDone = true
	candidates := runtimeCandidates()
	lib := firstExisting(candidates)
	if lib == "" {
		runtimeErr = fmt.Errorf("%w (looked in: %s)", errRuntimeMissing, strings.Join(candidates, ", "))
		slog.Warn("onnxruntime unavailable", "err", runtimeErr)
		return runtimeErr
	}
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		runtimeErr = fmt.Errorf("init onnxruntime from %s: %w", lib, err)
		slog.Warn("onnxruntime unavailable", "err", runtimeErr)
		return runtimeErr
	}
	slog.Info("onnxruntime ready", "library", lib, "version", ort.GetVersion())
	return nil
}

// RuntimeVersion initialises ONNX Runtime exactly as the first session would
// — once per process, from the same candidate locations — and returns the
// loaded library's version. With no usable library it returns the error
// every session creation in this process will return.
func RuntimeVersion() (string, error) {
	if err := ensureRuntime(); err != nil {
		return "", err
	}
	return ort.GetVersion(), nil
}

// firstExisting returns the first path in candidates that resolves to an
// existing file (symlinks are followed, so a dangling link counts as
// missing), or "" if none do.
func firstExisting(candidates []string) string {
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// resetRuntimeForTest forgets the initialisation outcome so the next
// ensureRuntime probes the candidates again. An initialised environment is
// destroyed first; no ONNX session may be alive when this is called.
func resetRuntimeForTest() {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if ort.IsInitialized() {
		_ = ort.DestroyEnvironment()
	}
	runtimeDone = false
	runtimeErr = nil
}

// SetRuntimeCandidatesForTest is for tests only. It makes this process look
// for the ONNX Runtime library in exactly paths (nil or empty: nowhere),
// forgets any earlier initialisation (destroying an initialised
// environment — no ONNX session may be alive), and returns a function that
// restores the default lookup the same way. Callers must not run in
// parallel with other users of this package's ONNX state.
func SetRuntimeCandidatesForTest(paths []string) (restore func()) {
	fixed := append([]string(nil), paths...)
	resetRuntimeForTest()
	runtimeMu.Lock()
	prev := runtimeCandidates
	runtimeCandidates = func() []string { return append([]string(nil), fixed...) }
	runtimeMu.Unlock()
	return func() {
		resetRuntimeForTest()
		runtimeMu.Lock()
		runtimeCandidates = prev
		runtimeMu.Unlock()
	}
}
