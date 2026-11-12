package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/buildinfo"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/bookmark"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/vision"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/volumes"
	"github.com/volodymyrsmirnov/choix/internal/quality"
)

// selfCheckEnv runs the headless self-check instead of the application when
// it is "1" ([call] C24). It is honoured by every build, the shipped one
// included: the signed, sandboxed bundle is verified with it.
const selfCheckEnv = "CHOIX_SELFCHECK"

// checkOK is the value of a passing check that is not a plain true.
const checkOK = "ok"

// visionTimeout bounds the self-check's one feature print.
const visionTimeout = 30 * time.Second

// Reference image of the self-check: the spec's image A (§6.3), 640×480,
// whose quality score is selfCheckRaw within selfCheckRawTolerance.
const (
	selfCheckWidth        = 640
	selfCheckHeight       = 480
	selfCheckRaw          = -40.7055461
	selfCheckRawTolerance = 1e-6
)

// selfCheckReport is the self-check's output: one compact JSON object, so a
// passing check reads exactly `"key":true` or `"key":"ok"` (the bundle smoke
// greps for them). Every value that failed is a string starting with
// "error: ".
type selfCheckReport struct {
	Version string `json:"version"`
	// DataDir is the data root, or "error: …".
	DataDir string `json:"dataDir"`
	// DataDirWritable is true, or "error: …".
	DataDirWritable any  `json:"dataDirWritable"`
	Sandboxed       bool `json:"sandboxed"`
	// Vision is the feature print of a generated image.
	Vision selfCheckVision `json:"vision"`
	// Quality is "ok" when the quality formula scores the generated image
	// as the reference says, or "error: …".
	Quality string `json:"quality"`
	// BookmarkRoundTrip is true, or "error: …".
	BookmarkRoundTrip any `json:"bookmarkRoundTrip"`
	// TrashCheck is true, or "error: …".
	TrashCheck any `json:"trashCheck"`
}

// selfCheckVision is the report's "vision" object.
type selfCheckVision struct {
	// FeaturePrint is "ok", or "error: …".
	FeaturePrint string `json:"featurePrint"`
	// Length is the number of values of the print Vision made (0: none).
	Length int `json:"length"`
	// Revision is the feature-print revision the app requests.
	Revision int `json:"revision"`
}

// selfCheck runs every check, prints the report to w as one JSON object on
// one line and reports whether nothing is an error. v makes the feature
// print (vision.New() in the app). It reads and writes only inside the data
// directory (creating it, as any start does), makes no network request and
// changes no state.
func selfCheck(w io.Writer, v platform.Vision) (ok bool) {
	r, failed := runSelfCheck(v)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		slog.Error("choix: self-check: write report", "err", err)
		return false
	}
	if len(failed) > 0 {
		slog.Warn("choix: self-check failed", "checks", strings.Join(failed, ","))
	}
	return len(failed) == 0
}

// runSelfCheck runs every check and returns the report and the names of the
// checks that failed (report keys).
func runSelfCheck(v platform.Vision) (selfCheckReport, []string) {
	var failed []string
	homes := homeDirs()
	fail := func(name string, err error) string {
		failed = append(failed, name)
		return "error: " + redactHome(err.Error(), homes)
	}
	// result is true when err is nil, otherwise the failure's "error: …".
	result := func(name string, err error) any {
		if err == nil {
			return true
		}
		return fail(name, err)
	}
	// okOr is "ok" when err is nil, otherwise the failure's "error: …".
	okOr := func(name string, err error) string {
		if err == nil {
			return checkOK
		}
		return fail(name, err)
	}

	r := selfCheckReport{Version: buildinfo.Version, Sandboxed: appdir.Sandboxed()}

	root, err := appdir.Root()
	if err != nil {
		r.DataDir = fail("dataDir", err)
		noRoot := fmt.Errorf("no data directory: %w", err)
		r.DataDirWritable = result("dataDirWritable", noRoot)
		r.BookmarkRoundTrip = result("bookmarkRoundTrip", noRoot)
		r.TrashCheck = result("trashCheck", noRoot)
	} else {
		r.DataDir = root
		r.DataDirWritable = result("dataDirWritable", checkWritable(root))
		r.BookmarkRoundTrip = result("bookmarkRoundTrip", checkBookmark(bookmark.New(), root))
		r.TrashCheck = result("trashCheck", checkNotInTrash(volumes.New(), root))
	}

	n, err := checkVision(v)
	r.Vision = selfCheckVision{FeaturePrint: okOr("vision", err), Length: n, Revision: vision.Revision}
	r.Quality = okOr("quality", checkQuality())

	return r, failed
}

// selfCheckImage is the spec's reference image A (§6.3): a smooth grey
// pattern of 640×480 with fine detail, defined by formula.
func selfCheckImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, selfCheckWidth, selfCheckHeight))
	for y := range selfCheckHeight {
		for x := range selfCheckWidth {
			fx, fy := float64(x), float64(y)
			a := 0.5 + 0.25*math.Sin(2*math.Pi*fx/16)*math.Cos(2*math.Pi*fy/24) +
				0.1*math.Sin(2*math.Pi*(fx+2*fy)/7)
			v := uint8(math.Floor(255*math.Min(math.Max(a, 0), 1) + 0.5))
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
		}
	}
	return img
}

// checkVision makes the feature print of the generated image through v and
// checks it is what revision 2 makes: FeaturePrintLen finite values, not all
// zero. It returns the number of values Vision returned.
func checkVision(v platform.Vision) (int, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, selfCheckImage()); err != nil {
		return 0, fmt.Errorf("encode the test image: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), visionTimeout)
	defer cancel()
	vec, err := v.FeaturePrint(ctx, platform.ImageInput{Data: buf.Bytes()})
	if err != nil {
		return 0, fmt.Errorf("feature print: %w", err)
	}
	if len(vec) != platform.FeaturePrintLen {
		return len(vec), fmt.Errorf("feature print has %d values, want %d", len(vec), platform.FeaturePrintLen)
	}
	var norm float64
	for _, x := range vec {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return len(vec), errors.New("feature print has a value that is not finite")
		}
		norm += f * f
	}
	if norm == 0 {
		return len(vec), errors.New("feature print is zero")
	}
	return len(vec), nil
}

// checkQuality scores the generated image with the quality formula and
// compares the score with the reference value.
func checkQuality() error {
	f, ok := quality.Measure(selfCheckImage())
	if !ok {
		return errors.New("the test image is not measurable")
	}
	raw := quality.Raw(f)
	if math.Abs(raw-selfCheckRaw) > selfCheckRawTolerance || math.IsNaN(raw) {
		return fmt.Errorf("score of the test image is %.7f, want %.7f", raw, selfCheckRaw)
	}
	return nil
}

// checkWritable creates, writes, syncs and removes a temporary file directly
// inside dir.
func checkWritable(dir string) (err error) {
	f, err := os.CreateTemp(dir, ".selfcheck-*.tmp")
	if err != nil {
		return fmt.Errorf("create file in data dir: %w", err)
	}
	name := f.Name()
	defer func() {
		if rmErr := os.Remove(name); rmErr != nil && err == nil {
			err = fmt.Errorf("remove file in data dir: %w", rmErr)
		}
	}()
	if _, err := f.WriteString("choix self-check\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("write file in data dir: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync file in data dir: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file in data dir: %w", err)
	}
	return nil
}

// checkBookmark makes a security-scoped bookmark for dir and checks that it
// resolves, grants access and refreshes — each time to dir itself.
func checkBookmark(b platform.Bookmarks, dir string) error {
	data, err := b.Create(dir)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	path, _, err := b.Resolve(data)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	if err := sameDir(dir, path); err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	path, stop, err := b.StartAccess(data)
	if err != nil {
		return fmt.Errorf("start access: %w", err)
	}
	stop()
	if err := sameDir(dir, path); err != nil {
		return fmt.Errorf("start access: %w", err)
	}
	fresh, err := b.Refresh(data)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	if len(fresh) == 0 {
		return errors.New("refresh: empty bookmark")
	}
	return nil
}

// sameDir reports an error unless got names the same directory as want.
func sameDir(want, got string) error {
	wi, err := os.Stat(want)
	if err != nil {
		return err
	}
	gi, err := os.Stat(got)
	if err != nil {
		return fmt.Errorf("bookmark resolved to an unreadable path: %w", err)
	}
	if !os.SameFile(wi, gi) {
		return errors.New("bookmark resolved to another directory")
	}
	return nil
}

// checkNotInTrash checks that the Trash test answers "not in the Trash" for
// dir without an error.
func checkNotInTrash(v platform.Volumes, dir string) error {
	in, err := v.InTrash(dir)
	if err != nil {
		return err
	}
	if in {
		return errors.New("the data directory is reported to be in the Trash")
	}
	return nil
}

// homeDirs returns the home directories to hide in error texts: $HOME (the
// sandbox container's, inside the sandbox) and the user's real one.
func homeDirs() []string {
	var homes []string
	if h, err := os.UserHomeDir(); err == nil {
		homes = append(homes, h)
	}
	if u, err := user.Current(); err == nil {
		homes = append(homes, u.HomeDir)
	}
	return homes
}

// redactHome replaces each home directory in s with "~", longest first, and
// only where it is a whole path: "/Users/me" is replaced in "/Users/me/x"
// but not inside "/Users/meadow" or "/x/Users/me".
func redactHome(s string, homes []string) string {
	hs := make([]string, 0, len(homes))
	for _, h := range homes {
		if h = filepath.Clean(h); filepath.IsAbs(h) && h != "/" {
			hs = append(hs, h)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return len(hs[i]) > len(hs[j]) })
	for _, h := range hs {
		var b strings.Builder
		last := 0
		for from := 0; from < len(s); {
			i := strings.Index(s[from:], h)
			if i < 0 {
				break
			}
			i += from
			end := i + len(h)
			starts := i == 0 || !isPathByte(s[i-1]) && s[i-1] != '/'
			ends := end == len(s) || !isPathByte(s[end])
			if !starts || !ends {
				from = i + 1
				continue
			}
			b.WriteString(s[last:i])
			b.WriteString("~")
			last, from = end, end
		}
		b.WriteString(s[last:])
		s = b.String()
	}
	return s
}

// isPathByte reports whether c continues a path component's name.
func isPathByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '-'
}
