package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/buildinfo"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/bookmark"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/volumes"
)

// selfCheckEnv runs the headless self-check instead of the application when
// it is "1" ([call] C24). It is honoured by every build, the shipped one
// included: the signed, sandboxed bundle is verified with it.
const selfCheckEnv = "CHOIX_SELFCHECK"

// skippedModelMissing is a provider's value when its model is not installed.
// It is not a failure.
const skippedModelMissing = "skipped: model missing"

// selfCheckReport is the self-check's output: one compact JSON object, so a
// passing check reads exactly `"key":true` (the bundle smoke greps for it).
// Every value that failed is a string starting with "error: ".
type selfCheckReport struct {
	Version string `json:"version"`
	// DataDir is the data root, or "error: …".
	DataDir string `json:"dataDir"`
	// DataDirWritable is true, or "error: …".
	DataDirWritable any  `json:"dataDirWritable"`
	Sandboxed       bool `json:"sandboxed"`
	// OnnxRuntime is the loaded library's version, or "error: …".
	OnnxRuntime string `json:"onnxRuntime"`
	// Providers holds, per model, the execution provider its one inference
	// ran on ("coreml" or "cpu"), skippedModelMissing, or "error: …".
	Providers selfCheckProviders `json:"providers"`
	// BookmarkRoundTrip is true, or "error: …".
	BookmarkRoundTrip any `json:"bookmarkRoundTrip"`
	// TrashCheck is true, or "error: …".
	TrashCheck any `json:"trashCheck"`
}

// selfCheckProviders is the report's "providers" object.
type selfCheckProviders struct {
	CLIP    string `json:"clip"`
	Quality string `json:"quality"`
}

// selfCheck runs every check, prints the report to w as one JSON object on
// one line and reports whether nothing is an error. It reads and writes only
// inside the data directory (creating it, and its models directory, as any
// start does), makes no network request, downloads nothing and changes no
// state.
func selfCheck(w io.Writer) (ok bool) {
	r, failed := runSelfCheck()
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
// checks that failed (report keys; providers as "providers.<model>").
func runSelfCheck() (selfCheckReport, []string) {
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

	if v, err := local.RuntimeVersion(); err != nil {
		r.OnnxRuntime = fail("onnxRuntime", err)
	} else {
		r.OnnxRuntime = v
	}

	clip, quality := checkProviders()
	provider := func(kind local.ModelKind, p string, err error) string {
		if err != nil {
			return fail("providers."+kind.String(), err)
		}
		return p
	}
	r.Providers.CLIP = provider(local.ModelCLIP, clip.provider, clip.err)
	r.Providers.Quality = provider(local.ModelQuality, quality.provider, quality.err)

	return r, failed
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

// providerResult is one model's provider check.
type providerResult struct {
	provider string
	err      error
}

// checkProviders runs one inference on each installed model through the
// app's own session set (same models directory, same execution-provider
// defaults) and reports the provider each ran on. Only a model file that
// does not exist is skipped; one that exists but is empty or unreadable is a
// failure. Nothing is ever downloaded.
func checkProviders() (clip, quality providerResult) {
	dir := appdir.Models()
	if dir == "" {
		err := errors.New("models directory unavailable")
		return providerResult{err: err}, providerResult{err: err}
	}
	models := local.NewModelStore(dir)
	s := local.NewSessions(models, local.SessionOptions{})
	defer func() {
		if err := s.Close(); err != nil {
			slog.Warn("choix: self-check: close onnx sessions", "err", err)
		}
	}()

	clip = checkProvider(s, models.Path(local.ModelCLIP), local.ModelCLIP, func() error {
		_, err := s.Embed(selfCheckImage())
		return err
	})
	quality = checkProvider(s, models.Path(local.ModelQuality), local.ModelQuality, func() error {
		in := make([]float32, tensorLen(local.KnownModels[local.ModelQuality].InputShape))
		for i := range in {
			in[i] = 0.5
		}
		_, err := s.QualityCrop(in)
		return err
	})
	return clip, quality
}

// checkProvider checks kind's model file at path and, when it is present,
// runs infer and reports the execution provider kind's session ran on.
func checkProvider(s *local.Sessions, path string, kind local.ModelKind, infer func() error) providerResult {
	present, err := modelFilePresent(path)
	switch {
	case err != nil:
		return providerResult{err: fmt.Errorf("%s: %w", kind, err)}
	case !present:
		return providerResult{provider: skippedModelMissing}
	}
	// Any inference error fails, ErrModelMissing included: the file was
	// there a moment ago.
	if err := infer(); err != nil {
		return providerResult{err: err}
	}
	p := s.Provider(kind)
	if p == "" {
		return providerResult{err: fmt.Errorf("%s: no execution provider reported", kind)}
	}
	return providerResult{provider: p}
}

// modelFilePresent reports whether a model file exists at path. Only a path
// with nothing at it is absent (false, nil). Anything else that is not a
// readable, non-empty regular file — an empty file, a directory, a dangling
// symlink, a stat or read error — is an error. ModelStore.Has deliberately
// counts those as "not installed" for the app; the self-check must not.
func modelFilePresent(path string) (bool, error) {
	name := filepath.Base(path)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("model file %s: %w", name, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("model file %s: %w", name, err)
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("model file %s is not a regular file", name)
	}
	if fi.Size() == 0 {
		return false, fmt.Errorf("model file %s is empty", name)
	}
	f, err := os.Open(path) //nolint:gosec // G304: a model path in the app's own models directory
	if err != nil {
		return false, fmt.Errorf("model file %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Read(make([]byte, 1)); err != nil {
		return false, fmt.Errorf("model file %s: read: %w", name, err)
	}
	return true, nil
}

// selfCheckImage is the image the CLIP inference embeds: a plain grey square
// at the model's input size.
func selfCheckImage() image.Image {
	side := 224
	if s := local.KnownModels[local.ModelCLIP].InputShape; len(s) == 4 && s[2] > 0 {
		side = s[2]
	}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Gray{Y: 128}), image.Point{}, draw.Src)
	return img
}

// tensorLen is the number of elements of a tensor of the given shape.
func tensorLen(shape []int) int {
	n := 1
	for _, d := range shape {
		n *= d
	}
	return n
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
