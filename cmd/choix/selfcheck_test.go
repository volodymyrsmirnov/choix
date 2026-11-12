package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/buildinfo"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/vision"
)

// selfCheckDataDir points the data root at a fresh temporary directory and
// returns its absolute path.
func selfCheckDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CHOIX_DATA_DIR", dir)
	return dir
}

// runSelfCheckJSON runs selfCheck with v and decodes its output, which must
// be one JSON object on one line.
func runSelfCheckJSON(t *testing.T, v platform.Vision) (map[string]any, bool) {
	t.Helper()
	var buf bytes.Buffer
	ok := selfCheck(&buf, v)
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

// visionReport returns the "vision" object of a self-check report.
func visionReport(t *testing.T, got map[string]any) map[string]any {
	t.Helper()
	v, isObj := got["vision"].(map[string]any)
	if !isObj {
		t.Fatalf("vision = %#v; want an object", got["vision"])
	}
	return v
}

// fakeVision answers every feature print with vec, or with err.
type fakeVision struct {
	vec []float32
	err error
}

func (f fakeVision) FeaturePrint(context.Context, platform.ImageInput) ([]float32, error) {
	return f.vec, f.err
}

// The report of a healthy machine, through the real Vision framework: the
// data directory, the feature print and the quality formula pass, and no
// ONNX key is left.
func TestSelfCheckReport(t *testing.T) {
	dir := selfCheckDataDir(t)

	got, ok := runSelfCheckJSON(t, vision.New())
	if !ok {
		t.Errorf("selfCheck = false; want true: %v", got)
	}
	for _, key := range []string{"version", "dataDir", "dataDirWritable", "sandboxed", "vision", "quality",
		"bookmarkRoundTrip", "trashCheck"} {
		if _, present := got[key]; !present {
			t.Errorf("report has no %q: %v", key, got)
		}
	}
	for _, key := range []string{"onnxRuntime", "providers"} {
		if _, present := got[key]; present {
			t.Errorf("report has %q: %v; want it gone", key, got)
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
	v := visionReport(t, got)
	if v["featurePrint"] != "ok" || v["length"] != float64(768) || v["revision"] != float64(2) {
		t.Errorf("vision = %v; want featurePrint ok, length 768, revision 2", v)
	}
	if got["quality"] != "ok" {
		t.Errorf("quality = %#v; want ok", got["quality"])
	}

	// Nothing is left behind, and no models directory is made.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("data dir after the self-check holds %v; want nothing", names)
	}
}

// The machine-readable form the bundle smoke greps for: compact JSON, so a
// passing check reads exactly `"key":true` or `"key":"ok"`.
func TestSelfCheck_CompactKeys(t *testing.T) {
	selfCheckDataDir(t)

	var buf bytes.Buffer
	selfCheck(&buf, vision.New())
	out := buf.String()
	for _, want := range []string{`"sandboxed":false`, `"bookmarkRoundTrip":true`, `"trashCheck":true`,
		`"dataDirWritable":true`, `"featurePrint":"ok"`, `"length":768`, `"revision":2`, `"quality":"ok"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
}

// A feature print that fails, or that is not what revision 2 makes, puts the
// message in place of "ok" and fails the self-check.
func TestSelfCheck_VisionFailure(t *testing.T) {
	unit := make([]float32, platform.FeaturePrintLen)
	unit[0] = 1
	cases := map[string]struct {
		v    fakeVision
		want string // substring of the message
	}{
		"error":       {fakeVision{err: errors.New("vision unavailable")}, "vision unavailable"},
		"short print": {fakeVision{vec: unit[:10]}, "10 values"},
		"zero print":  {fakeVision{vec: make([]float32, platform.FeaturePrintLen)}, "zero"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			selfCheckDataDir(t)
			got, ok := runSelfCheckJSON(t, c.v)
			v := visionReport(t, got)
			msg, _ := v["featurePrint"].(string)
			if !strings.HasPrefix(msg, "error: ") || !strings.Contains(msg, c.want) {
				t.Errorf("vision.featurePrint = %#v; want an error mentioning %q", v["featurePrint"], c.want)
			}
			if v["revision"] != float64(2) {
				t.Errorf("vision.revision = %#v; want 2", v["revision"])
			}
			if ok {
				t.Error("selfCheck = true; want false")
			}
			if _, failed := runSelfCheck(c.v); !slices.Equal(failed, []string{"vision"}) {
				t.Errorf("failed checks = %v; want only [vision]", failed)
			}
			if got["quality"] != "ok" {
				t.Errorf("quality = %#v; want ok", got["quality"])
			}
		})
	}
}

// The quality check compares the formula's score of the generated image with
// the reference value.
func TestSelfCheck_QualityReference(t *testing.T) {
	if err := checkQuality(); err != nil {
		t.Fatalf("checkQuality: %v", err)
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
