package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/buildinfo"
)

// The self-check tests share the ONNX package's process-wide runtime state,
// so none of them runs in parallel.

// selfCheckDataDir points the data root at a fresh temporary directory and
// returns its absolute path.
func selfCheckDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CHOIX_DATA_DIR", dir)
	return dir
}

// runSelfCheckJSON runs selfCheck and decodes its output, which must be one
// JSON object on one line.
func runSelfCheckJSON(t *testing.T) (map[string]any, bool) {
	t.Helper()
	var buf bytes.Buffer
	ok := selfCheck(&buf)
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("selfCheck output is not one line: %q", out)
	}
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("selfCheck output is not a JSON object: %v\n%s", err, out)
	}
	if dec.More() {
		t.Fatalf("selfCheck printed more than one JSON value: %s", out)
	}
	return got, ok
}

// providers returns the "providers" object of a self-check report.
func providers(t *testing.T, got map[string]any) map[string]any {
	t.Helper()
	p, isObj := got["providers"].(map[string]any)
	if !isObj {
		t.Fatalf("providers = %#v; want an object", got["providers"])
	}
	return p
}

// noRuntime makes the ONNX Runtime lookup find nothing for the test.
func noRuntime(t *testing.T, paths []string) {
	t.Helper()
	t.Cleanup(local.SetRuntimeCandidatesForTest(paths))
}

func TestSelfCheck_ReportsDataDir(t *testing.T) {
	dir := selfCheckDataDir(t)
	noRuntime(t, nil)

	got, _ := runSelfCheckJSON(t)

	for _, key := range []string{"version", "dataDir", "dataDirWritable", "sandboxed", "onnxRuntime",
		"providers", "bookmarkRoundTrip", "trashCheck"} {
		if _, present := got[key]; !present {
			t.Errorf("report has no %q: %v", key, got)
		}
	}
	if got["version"] != buildinfo.Version {
		t.Errorf("version = %#v; want %q", got["version"], buildinfo.Version)
	}
	if got["dataDir"] != dir {
		t.Errorf("dataDir = %#v; want %q", got["dataDir"], dir)
	}
	if got["sandboxed"] != false {
		t.Errorf("sandboxed = %#v; want false in a test binary", got["sandboxed"])
	}
	for _, key := range []string{"dataDirWritable", "bookmarkRoundTrip", "trashCheck"} {
		if got[key] != true {
			t.Errorf("%s = %#v; want true", key, got[key])
		}
	}

	// Nothing is left behind: only the models directory every start creates.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"models"}) {
		t.Errorf("data dir after the self-check holds %v; want only [models]", names)
	}
}

// The machine-readable form the bundle smoke greps for: compact JSON, so a
// true check reads exactly `"key":true`.
func TestSelfCheck_CompactKeys(t *testing.T) {
	selfCheckDataDir(t)
	noRuntime(t, nil)

	var buf bytes.Buffer
	selfCheck(&buf)
	out := buf.String()
	for _, want := range []string{`"sandboxed":false`, `"bookmarkRoundTrip":true`, `"trashCheck":true`,
		`"dataDirWritable":true`, `"onnxRuntime":"error: `} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
}

func TestSelfCheck_MissingRuntimeIsError(t *testing.T) {
	for name, paths := range map[string][]string{
		"missing path": {filepath.Join(t.TempDir(), "libonnxruntime.dylib")},
		"no candidate": nil,
	} {
		t.Run(name, func(t *testing.T) {
			selfCheckDataDir(t)
			noRuntime(t, paths)

			got, ok := runSelfCheckJSON(t)
			rt, _ := got["onnxRuntime"].(string)
			if !strings.HasPrefix(rt, "error:") {
				t.Errorf("onnxRuntime = %#v; want an error", got["onnxRuntime"])
			}
			if ok {
				t.Error("selfCheck = true with no ONNX Runtime library; want false")
			}
		})
	}
}

func TestSelfCheck_ModelsMissingAreSkipped(t *testing.T) {
	selfCheckDataDir(t)
	noRuntime(t, nil)

	got, _ := runSelfCheckJSON(t)
	p := providers(t, got)
	for _, kind := range []string{"clip", "quality"} {
		if p[kind] != "skipped: model missing" {
			t.Errorf("providers.%s = %#v; want skipped: model missing", kind, p[kind])
		}
	}

	// A missing model is not a failure: with no runtime, the runtime is the
	// only one.
	if _, failed := runSelfCheck(); !slices.Equal(failed, []string{"onnxRuntime"}) {
		t.Errorf("failed checks = %v; want only [onnxRuntime]", failed)
	}
}

// A model that is present is used, so a model that cannot run is an error.
func TestSelfCheck_PresentModelThatCannotRunIsError(t *testing.T) {
	dir := selfCheckDataDir(t)
	noRuntime(t, nil)
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o700); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(models, local.KnownModels[local.ModelCLIP].FileName)
	if err := os.WriteFile(clip, []byte("not a model"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok := runSelfCheckJSON(t)
	p := providers(t, got)
	if s, _ := p["clip"].(string); !strings.HasPrefix(s, "error:") {
		t.Errorf("providers.clip = %#v; want an error", p["clip"])
	}
	if p["quality"] != "skipped: model missing" {
		t.Errorf("providers.quality = %#v; want skipped: model missing", p["quality"])
	}
	if ok {
		t.Error("selfCheck = true; want false")
	}
	if _, failed := runSelfCheck(); !slices.Contains(failed, "providers.clip") {
		t.Errorf("failed checks = %v; want providers.clip among them", failed)
	}
}

// Only a model file that does not exist is skipped. A model file that is
// there but empty, not a regular file, a dangling link or unreadable is a
// failed check for either model — never "skipped: model missing", which
// would let a truncated install pass without any inference.
func TestSelfCheck_BrokenModelFileIsError(t *testing.T) {
	type breaker func(t *testing.T, path string)
	cases := []struct {
		name   string
		breaks breaker
		reason string // substring of the error
	}{
		{"empty file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "is empty"},
		{"directory", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"dangling symlink", func(t *testing.T, path string) {
			if err := os.Symlink(filepath.Join(filepath.Dir(path), "gone.onnx"), path); err != nil {
				t.Fatal(err)
			}
		}, "no such file"},
		{"unreadable file", func(t *testing.T, path string) {
			if os.Geteuid() == 0 {
				t.Skip("root reads any file")
			}
			if err := os.WriteFile(path, []byte("model bytes"), 0o000); err != nil {
				t.Fatal(err)
			}
		}, "permission denied"},
	}
	kinds := map[string]local.ModelKind{"clip": local.ModelCLIP, "quality": local.ModelQuality}
	for kindName, kind := range kinds {
		for _, c := range cases {
			t.Run(kindName+"/"+c.name, func(t *testing.T) {
				dir := selfCheckDataDir(t)
				noRuntime(t, nil)
				models := filepath.Join(dir, "models")
				if err := os.MkdirAll(models, 0o700); err != nil {
					t.Fatal(err)
				}
				c.breaks(t, filepath.Join(models, local.KnownModels[kind].FileName))

				got, ok := runSelfCheckJSON(t)
				p := providers(t, got)
				s, _ := p[kindName].(string)
				if !strings.HasPrefix(s, "error:") || !strings.Contains(s, c.reason) {
					t.Errorf("providers.%s = %#v; want an error mentioning %q", kindName, p[kindName], c.reason)
				}
				for other := range kinds {
					if other != kindName && p[other] != skippedModelMissing {
						t.Errorf("providers.%s = %#v; want %q", other, p[other], skippedModelMissing)
					}
				}
				if ok {
					t.Error("selfCheck = true; want false")
				}
				if _, failed := runSelfCheck(); !slices.Contains(failed, "providers."+kindName) {
					t.Errorf("failed checks = %v; want providers.%s among them", failed, kindName)
				}
			})
		}
	}
}

// A models directory whose entries cannot be looked up is a failure for both
// models, not "missing".
func TestSelfCheck_ModelStatErrorIsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := selfCheckDataDir(t)
	noRuntime(t, nil)
	models := filepath.Join(dir, "models")
	if err := os.Mkdir(models, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(models, 0o700) }) //nolint:gosec // G302: restore so TempDir can be removed

	got, ok := runSelfCheckJSON(t)
	p := providers(t, got)
	for _, kind := range []string{"clip", "quality"} {
		if s, _ := p[kind].(string); !strings.HasPrefix(s, "error:") || !strings.Contains(s, "permission denied") {
			t.Errorf("providers.%s = %#v; want a permission error", kind, p[kind])
		}
	}
	if ok {
		t.Error("selfCheck = true; want false")
	}
}

// runtimeLibrary returns an ONNX Runtime library on this machine, or skips.
func runtimeLibrary(t *testing.T) string {
	t.Helper()
	var c []string
	if p := os.Getenv("CHOIX_ONNXRUNTIME_DYLIB"); filepath.IsAbs(p) {
		c = append(c, p)
	}
	c = append(c, "/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib")
	for _, p := range c {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skipf("no ONNX Runtime library (looked in %v)", c)
	return ""
}

// With a runtime and no models, every check passes.
func TestSelfCheck_RuntimeWithoutModelsIsOK(t *testing.T) {
	lib := runtimeLibrary(t)
	selfCheckDataDir(t)
	noRuntime(t, []string{lib})

	got, ok := runSelfCheckJSON(t)
	rt, _ := got["onnxRuntime"].(string)
	if rt == "" || strings.HasPrefix(rt, "error:") {
		t.Errorf("onnxRuntime = %#v; want a version", got["onnxRuntime"])
	}
	if !ok {
		t.Errorf("selfCheck = false; want true: %v", got)
	}
}

// installedModelsDir returns a directory holding both models, or skips.
func installedModelsDir(t *testing.T) string {
	t.Helper()
	var dirs []string
	if d := os.Getenv("CHOIX_MODELS_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Library", "Application Support", "Choix", "models"))
	}
	for _, d := range dirs {
		m := local.NewModelStore(d)
		if m.Has(local.ModelCLIP) && m.Has(local.ModelQuality) {
			return d
		}
	}
	t.Skipf("models not installed (looked in %v)", dirs)
	return ""
}

// Each present model runs one inference and reports its provider.
func TestSelfCheck_PresentModelsRunInference(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	lib := runtimeLibrary(t)
	src := installedModelsDir(t)
	dir := selfCheckDataDir(t)
	noRuntime(t, []string{lib})
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []local.ModelKind{local.ModelCLIP, local.ModelQuality} {
		name := local.KnownModels[kind].FileName
		if err := os.Symlink(filepath.Join(src, name), filepath.Join(models, name)); err != nil {
			t.Fatal(err)
		}
	}

	got, ok := runSelfCheckJSON(t)
	p := providers(t, got)
	for _, kind := range []string{"clip", "quality"} {
		if p[kind] != "coreml" && p[kind] != "cpu" {
			t.Errorf("providers.%s = %#v; want coreml or cpu", kind, p[kind])
		}
	}
	if !ok {
		t.Errorf("selfCheck = false; want true: %v", got)
	}
}

func TestRedactHome(t *testing.T) {
	homes := []string{"/Users/me", "/Users/me/Library/Containers/x/Data"}
	cases := map[string]string{
		"open /Users/me/Library/Containers/x/Data/Library/a: denied": "open ~/Library/a: denied",
		"looked in: /Users/me/bin/Choix.app/Contents/Frameworks/lib": "looked in: ~/bin/Choix.app/Contents/Frameworks/lib",
		"looked in: /Users/meadow/lib":                               "looked in: /Users/meadow/lib",
		"looked in: /x/Users/me/lib, /Users/me/lib":                  "looked in: /x/Users/me/lib, ~/lib",
		"no home here": "no home here",
		"/Users/me":    "~",
	}
	for in, want := range cases {
		if got := redactHome(in, homes); got != want {
			t.Errorf("redactHome(%q) = %q; want %q", in, got, want)
		}
	}
}
