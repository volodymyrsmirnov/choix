package thumb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// buildEnv is a scan root, a store and a Builder on platformtest fakes.
type buildEnv struct {
	t      *testing.T
	root   string
	st     *store.Store
	stills *platformtest.Stills
	video  *platformtest.Video
	b      *Builder
}

func newBuildEnv(t *testing.T) *buildEnv {
	t.Helper()
	root := t.TempDir()
	st := store.NewTestStore(t)
	stills := &platformtest.Stills{ReadPaths: true}
	video := &platformtest.Video{}
	return &buildEnv{
		t: t, root: root, st: st, stills: stills, video: video,
		b: &Builder{ScanRoot: root, Store: st, Stills: stills, Video: video},
	}
}

// add writes data at rel under the root and inserts its row at 'metadata'.
func (e *buildEnv) add(rel, kind string, data []byte) int64 {
	e.t.Helper()
	abs := e.abs(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		e.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		e.t.Fatalf("write %s: %v", rel, err)
	}
	return e.insert(rel, kind)
}

// insert adds a 'metadata' row for rel without touching the disk.
func (e *buildEnv) insert(rel, kind string) int64 {
	e.t.Helper()
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")
	id, err := e.st.Files().Insert(store.File{
		Path: rel, Size: 1, Mtime: 1, ContentHash: "h-" + rel, Kind: kind, Format: format, ScanStatus: "metadata",
	})
	if err != nil {
		e.t.Fatalf("Insert %s: %v", rel, err)
	}
	return id
}

func (e *buildEnv) abs(rel string) string { return filepath.Join(e.root, filepath.FromSlash(rel)) }

// fixture returns the bytes of a file a platformtest maker wrote.
func fixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func (e *buildEnv) row(id int64) store.File {
	e.t.Helper()
	row, err := e.st.Files().GetByID(id)
	if err != nil {
		e.t.Fatalf("GetByID: %v", err)
	}
	return row
}

func (e *buildEnv) wantStatus(id int64, status string) store.File {
	e.t.Helper()
	row := e.row(id)
	if row.ScanStatus != status {
		e.t.Fatalf("scan_status = %q (error %q); want %q", row.ScanStatus, row.ErrMsg.String, status)
	}
	return row
}

func (e *buildEnv) wantFailed(id int64, reason string) {
	e.t.Helper()
	row := e.wantStatus(id, "failed")
	if !row.ErrMsg.Valid || !strings.Contains(row.ErrMsg.String, reason) {
		e.t.Fatalf("error = %q; want it to contain %q", row.ErrMsg.String, reason)
	}
}

// thumbSize returns the size of a tier's row and checks that its file is a
// JPEG of that size.
func (e *buildEnv) thumbSize(id int64, tier string) (int, int) {
	e.t.Helper()
	th, err := e.st.Thumbs().Get(id, tier)
	if err != nil {
		e.t.Fatalf("Thumbs.Get(%d, %s): %v", id, tier, err)
	}
	data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(th.RelPath)))
	if err != nil {
		e.t.Fatalf("read %s: %v", tier, err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		e.t.Fatalf("decode %s: %v", tier, err)
	}
	if int64(cfg.Width) != th.Width || int64(cfg.Height) != th.Height {
		e.t.Fatalf("%s file is %dx%d, row says %dx%d", tier, cfg.Width, cfg.Height, th.Width, th.Height)
	}
	return cfg.Width, cfg.Height
}

func (e *buildEnv) wantSize(id int64, tier string, w, h int) {
	e.t.Helper()
	if gw, gh := e.thumbSize(id, tier); gw != w || gh != h {
		e.t.Fatalf("%s = %dx%d; want %dx%d", tier, gw, gh, w, h)
	}
}

// thumbnailCalls returns the Thumbnail calls the Stills fake received.
func (e *buildEnv) thumbnailCalls() []platformtest.StillsCall {
	var out []platformtest.StillsCall
	for _, c := range e.stills.Calls() {
		if c.Method == "Thumbnail" {
			out = append(out, c)
		}
	}
	return out
}

// wantCalls checks the Thumbnail calls' input kind, limit and fit, in order.
func (e *buildEnv) wantCalls(want ...callSpec) {
	e.t.Helper()
	got := e.thumbnailCalls()
	specs := make([]callSpec, len(got))
	for i, c := range got {
		specs[i] = callSpec{path: c.Path != "", limit: c.Limit, fit: c.Fit}
	}
	if !slices.Equal(specs, want) {
		e.t.Fatalf("Thumbnail calls = %v; want %v", specs, want)
	}
}

type callSpec struct {
	path  bool // a Path input; otherwise Data
	limit int
	fit   platform.Fit
}

func (c callSpec) String() string {
	in, fit := "Data", "FitWidth"
	if c.path {
		in = "Path"
	}
	if c.fit == platform.FitLongSide {
		fit = "FitLongSide"
	}
	return fmt.Sprintf("%s(%d, %s)", in, c.limit, fit)
}

func pathCall(limit int, fit platform.Fit) callSpec {
	return callSpec{path: true, limit: limit, fit: fit}
}
func dataCall(limit int, fit platform.Fit) callSpec { return callSpec{limit: limit, fit: fit} }

// logBuffer collects slog output for one test.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// captureLog sends the default logger to a buffer until the test ends.
func captureLog(t *testing.T) *logBuffer {
	t.Helper()
	prev := slog.Default()
	lb := &logBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(lb, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return lb
}

func (e *buildEnv) build(id int64) error {
	e.t.Helper()
	return e.b.Build(context.Background(), id)
}

func (e *buildEnv) mustBuild(id int64) {
	e.t.Helper()
	if err := e.build(id); err != nil {
		e.t.Fatalf("Build: %v", err)
	}
}

func TestBuild_PhotoWritesBothTiers(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("Day1/IMG_0001.jpg", "photo", platformtest.MakeJPEG(t, 500, 1000))
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	for _, tier := range []string{TierThumb, TierPreview} {
		th, err := e.st.Thumbs().Get(id, tier)
		if err != nil {
			t.Fatalf("Thumbs.Get(%s): %v", tier, err)
		}
		want := fmt.Sprintf(".choix/thumbs/%02x/%d-%s.jpg", id%256, id, tier)
		if th.RelPath != want {
			t.Fatalf("%s rel_path = %q; want %q", tier, th.RelPath, want)
		}
		if _, err := os.Stat(CachePath(e.root, id, tier)); err != nil {
			t.Fatalf("%s file: %v", tier, err)
		}
		e.thumbSize(id, tier)
	}
}

func TestBuild_JPEGScalesByLongSide(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("a.jpg", "photo", platformtest.MakeJPEG(t, 500, 1000))
	e.mustBuild(id)

	e.wantCalls(pathCall(256, platform.FitLongSide), pathCall(1600, platform.FitLongSide))
	for _, c := range e.thumbnailCalls() {
		if c.Path != e.abs("a.jpg") {
			t.Fatalf("Thumbnail path = %q; want the source", c.Path)
		}
	}
	e.wantSize(id, TierThumb, 128, 256)
	e.wantSize(id, TierPreview, 500, 1000)
}

func TestBuild_HEICScalesByLongSide(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("IMG_0002.HEIC", "photo", []byte("not decoded by the fake"))
	e.stills.Props = map[string]map[string]any{e.abs("IMG_0002.HEIC"): {"PixelWidth": 4032, "PixelHeight": 3024}}
	e.mustBuild(id)

	e.wantCalls(pathCall(256, platform.FitLongSide), pathCall(1600, platform.FitLongSide))
	e.wantSize(id, TierThumb, 256, 192)
	e.wantSize(id, TierPreview, 1600, 1200)
}

func TestBuild_RAFScalesByWidth(t *testing.T) {
	e := newBuildEnv(t)
	raf := fixture(t, platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 500, 1000)))
	id := e.add("DSCF0001.RAF", "photo", raf)
	e.mustBuild(id)

	e.wantCalls(dataCall(256, platform.FitWidth), dataCall(1600, platform.FitWidth))
	e.wantSize(id, TierThumb, 256, 512)
	e.wantSize(id, TierPreview, 500, 1000)
}

func TestBuild_DNGScalesByWidth(t *testing.T) {
	e := newBuildEnv(t)
	dng := fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte("raw sensor data"),
		Previews: []platformtest.DNGPreview{{JPEG: platformtest.MakeJPEG(t, 500, 1000), Width: 500, Height: 1000}},
	}))
	id := e.add("IMG_0001.DNG", "photo", dng)
	e.mustBuild(id)

	e.wantCalls(dataCall(256, platform.FitWidth), dataCall(1600, platform.FitWidth))
	e.wantSize(id, TierThumb, 256, 512)
	e.wantSize(id, TierPreview, 500, 1000)
}

func TestBuild_RAWDecodeFallbackScalesByLongSide(t *testing.T) {
	logs := captureLog(t)
	e := newBuildEnv(t)
	dng := fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: []byte("raw sensor data")}))
	id := e.add("IMG_0001.DNG", "photo", dng)
	e.stills.Props = map[string]map[string]any{e.abs("IMG_0001.DNG"): {"PixelWidth": 500, "PixelHeight": 1000}}
	e.mustBuild(id)

	e.wantCalls(pathCall(256, platform.FitLongSide), pathCall(1600, platform.FitLongSide))
	e.wantSize(id, TierThumb, 128, 256)
	e.wantSize(id, TierPreview, 500, 1000)
	if !strings.Contains(logs.String(), "raw decode fallback") {
		t.Fatalf("log = %q; want a raw decode fallback warning", logs.String())
	}
}

func TestBuild_RAFUsesEmbeddedJPEG(t *testing.T) {
	e := newBuildEnv(t)
	embedded := platformtest.MakeJPEG(t, 320, 240)
	id := e.add("DSCF0001.RAF", "photo", fixture(t, platformtest.MakeRAF(t, embedded)))
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	calls := e.thumbnailCalls()
	if len(calls) != 2 {
		t.Fatalf("Thumbnail calls = %d; want 2", len(calls))
	}
	for _, c := range calls {
		if c.Path != "" || !bytes.Equal(c.Data, embedded) {
			t.Fatalf("Thumbnail call with Path %q and %d bytes of Data; want the embedded JPEG as Data, never Path", c.Path, len(c.Data))
		}
	}
}

func TestBuild_DNGPassesIFD0Orientation(t *testing.T) {
	e := newBuildEnv(t)
	dng := fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{
		Orientation: 6,
		RawStrip:    []byte("raw"),
		Previews:    []platformtest.DNGPreview{{JPEG: platformtest.MakeJPEG(t, 400, 300), Width: 400, Height: 300}},
	}))
	id := e.add("IMG_0001.DNG", "photo", dng)
	e.mustBuild(id)

	calls := e.thumbnailCalls()
	if len(calls) != 2 {
		t.Fatalf("Thumbnail calls = %d; want 2", len(calls))
	}
	for _, c := range calls {
		if c.Orientation != 6 {
			t.Fatalf("Thumbnail orientation = %d; want 6 (IFD0)", c.Orientation)
		}
	}
	// 400x300 rotated by orientation 6 is 300x400; 256 wide.
	e.wantSize(id, TierThumb, 256, 341)
}

func TestBuild_DNGWithoutPreviewFallsBackToDecode(t *testing.T) {
	e := newBuildEnv(t)
	dng := fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: []byte("raw")}))
	id := e.add("IMG_0001.DNG", "photo", dng)
	e.stills.Props = map[string]map[string]any{e.abs("IMG_0001.DNG"): {"PixelWidth": 600, "PixelHeight": 400}}
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	for _, c := range e.thumbnailCalls() {
		if c.Path != e.abs("IMG_0001.DNG") {
			t.Fatalf("Thumbnail call with Path %q; want the DNG decoded by path", c.Path)
		}
	}
}

func TestBuild_PreviewThumbnailFailureFallsBackToPath(t *testing.T) {
	logs := captureLog(t)
	e := newBuildEnv(t)
	id := e.add("DSCF0001.RAF", "photo", fixture(t, platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 500, 1000))))
	e.stills.Fail = map[string]error{"": errors.New("cannot decode the preview")} // every Data input
	e.stills.Props = map[string]map[string]any{e.abs("DSCF0001.RAF"): {"PixelWidth": 500, "PixelHeight": 1000}}
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	e.wantCalls(
		dataCall(256, platform.FitWidth), pathCall(256, platform.FitLongSide),
		dataCall(1600, platform.FitWidth), pathCall(1600, platform.FitLongSide),
	)
	e.wantSize(id, TierThumb, 128, 256)
	if !strings.Contains(logs.String(), "raw decode fallback") {
		t.Fatalf("log = %q; want a raw decode fallback warning", logs.String())
	}
}

func TestBuild_DNGDamagedWidestPreviewUsesNextPreview(t *testing.T) {
	logs := captureLog(t)
	e := newBuildEnv(t)
	dng := fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte("raw"),
		Previews: []platformtest.DNGPreview{
			{JPEG: platformtest.MakeJPEG(t, 1600, 1066), Width: 1600, Height: 1066, DamageBody: true},
			{JPEG: platformtest.MakeJPEG(t, 256, 170), Width: 256, Height: 170},
		},
	}))
	id := e.add("IMG_0001.DNG", "photo", dng)
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	// Each tier tries the damaged 1600-wide preview, then the 256-wide one.
	e.wantCalls(
		dataCall(256, platform.FitWidth), dataCall(256, platform.FitWidth),
		dataCall(1600, platform.FitWidth), dataCall(1600, platform.FitWidth),
	)
	e.wantSize(id, TierThumb, 256, 170)
	e.wantSize(id, TierPreview, 256, 170)
	if strings.Contains(logs.String(), "raw decode fallback") {
		t.Fatalf("log = %q; want no raw decode fallback", logs.String())
	}
}

func TestBuild_DNGAllPreviewsDamagedFallsBackToDecode(t *testing.T) {
	logs := captureLog(t)
	e := newBuildEnv(t)
	dng := fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte("raw"),
		Previews: []platformtest.DNGPreview{
			{JPEG: platformtest.MakeJPEG(t, 1600, 1066), Width: 1600, Height: 1066, DamageBody: true},
			{JPEG: platformtest.MakeJPEG(t, 256, 170), Width: 256, Height: 170, DamageBody: true},
		},
	}))
	id := e.add("IMG_0001.DNG", "photo", dng)
	e.stills.Props = map[string]map[string]any{e.abs("IMG_0001.DNG"): {"PixelWidth": 500, "PixelHeight": 1000}}
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	// Path only after every preview was tried, for each tier.
	e.wantCalls(
		dataCall(256, platform.FitWidth), dataCall(256, platform.FitWidth), pathCall(256, platform.FitLongSide),
		dataCall(1600, platform.FitWidth), dataCall(1600, platform.FitWidth), pathCall(1600, platform.FitLongSide),
	)
	e.wantSize(id, TierThumb, 128, 256)
	if !strings.Contains(logs.String(), "raw decode fallback") {
		t.Fatalf("log = %q; want a raw decode fallback warning", logs.String())
	}
}

func TestBuild_ZeroByteJPEGIsFailed(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("empty.jpg", "photo", nil)
	if err := e.build(id); err == nil {
		t.Fatal("Build of a zero-byte JPEG: want an error")
	}
	e.wantFailed(id, "")
	if _, err := e.st.Thumbs().Get(id, TierThumb); err == nil {
		t.Fatal("a failed file has a thumb row")
	}
}

func TestBuild_TruncatedRAFIsFailed(t *testing.T) {
	e := newBuildEnv(t)
	raf := fixture(t, platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 64, 64)))
	id := e.add("DSCF0001.RAF", "photo", raf[:160]) // the header points past the end
	if err := e.build(id); err == nil {
		t.Fatal("Build of a truncated RAF: want an error")
	}
	e.wantFailed(id, "")
	// No candidate lies inside the file, so the decode path was tried.
	e.wantCalls(pathCall(256, platform.FitLongSide))
}

func TestBuild_MissingSourceIsFailed(t *testing.T) {
	e := newBuildEnv(t)
	id := e.insert("gone.jpg", "photo")
	if err := e.build(id); err == nil {
		t.Fatal("Build of a missing source: want an error")
	}
	e.wantFailed(id, "stat source")
}

func TestBuild_CancelDuringNativeCall(t *testing.T) {
	e := newBuildEnv(t)
	e.stills.Gate = make(chan struct{})
	released := false
	release := func() {
		if !released {
			released = true
			close(e.stills.Gate)
		}
	}
	t.Cleanup(release)
	id := e.add("a.jpg", "photo", platformtest.MakeJPEG(t, 64, 64))

	var inflight platform.Inflight
	ctx, cancel := context.WithCancel(platform.WithInflight(context.Background(), &inflight))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.b.Build(ctx, id) }()

	deadline := time.Now().Add(5 * time.Second)
	for len(e.thumbnailCalls()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Build never reached the native call")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Build = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Build did not return within 1 s of the cancellation")
	}

	noOutput := func(when string) {
		t.Helper()
		row := e.row(id)
		if row.ScanStatus != "metadata" || row.ErrMsg.Valid {
			t.Fatalf("%s: scan_status=%q error=%+v; want 'metadata' and no error", when, row.ScanStatus, row.ErrMsg)
		}
		if _, err := os.Lstat(filepath.Join(e.root, ".choix")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: .choix exists (%v); want nothing written", when, err)
		}
		if _, err := e.st.Thumbs().Get(id, TierThumb); err == nil {
			t.Fatalf("%s: a thumb row was written", when)
		}
	}
	noOutput("after cancel")
	release()
	if !inflight.Wait(5 * time.Second) {
		t.Fatal("the abandoned native call never returned")
	}
	noOutput("after the gate was released")
}

// setPersistHook installs persistHook until the test ends.
func setPersistHook(t *testing.T, fn func(step string)) {
	t.Helper()
	persistHook = fn
	t.Cleanup(func() { persistHook = nil })
}

// Cancelling while the results are being persisted stops before the next
// write or database call: Build returns context.Canceled, the status is not
// advanced (nor set to failed), and no later step runs.
func TestBuild_CancelDuringPersistence(t *testing.T) {
	steps := []string{"thumb cache", "thumb row", "preview cache", "preview row"}
	for i, step := range steps {
		t.Run(step, func(t *testing.T) {
			e := newBuildEnv(t)
			id := e.add("a.jpg", "photo", platformtest.MakeJPEG(t, 64, 48))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var seen []string
			setPersistHook(t, func(s string) {
				seen = append(seen, s)
				if s == step {
					cancel()
				}
			})

			if err := e.b.Build(ctx, id); !errors.Is(err, context.Canceled) {
				t.Fatalf("Build = %v; want context.Canceled", err)
			}
			if want := steps[:i+1]; !slices.Equal(seen, want) {
				t.Fatalf("persistence steps = %v; want %v (nothing after the cancellation)", seen, want)
			}
			row := e.row(id)
			if row.ScanStatus != "metadata" || row.ErrMsg.Valid {
				t.Fatalf("scan_status=%q error=%+v; want 'metadata' and no error", row.ScanStatus, row.ErrMsg)
			}
			if _, err := e.st.Thumbs().Get(id, TierPreview); (err == nil) != (step == "preview row") {
				t.Fatalf("preview row present = %v after cancelling at %q", err == nil, step)
			}
			if _, err := os.Stat(CachePath(e.root, id, TierPreview)); err == nil && i < 2 {
				t.Fatalf("the preview cache file was written after cancelling at %q", step)
			}
		})
	}

	t.Run("keyframe 2", func(t *testing.T) {
		e := newBuildEnv(t)
		id := e.add("CLIP.MOV", "video", []byte("x"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		setPersistHook(t, func(s string) {
			if s == "keyframe 2" {
				cancel()
			}
		})
		if err := e.b.Build(ctx, id); !errors.Is(err, context.Canceled) {
			t.Fatalf("Build = %v; want context.Canceled", err)
		}
		e.wantStatus(id, "metadata")
		if _, err := os.Stat(filepath.Join(KeyframesDir(e.root, id), "3.jpg")); err == nil {
			t.Fatal("keyframe 3 was written after the cancellation")
		}
		if _, err := os.Stat(CachePath(e.root, id, TierThumb)); err == nil {
			t.Fatal("the thumb cache file was written after the cancellation")
		}
	})
}

// A build interrupted by cancellation is not a failure of the file: the row
// keeps its status so the next run picks it up again.
func TestBuilderBuildCancelledLeavesStatus(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("a.jpg", "photo", platformtest.MakeJPEG(t, 64, 64))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.b.Build(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("Build = %v; want context.Canceled", err)
	}
	row := e.row(id)
	if row.ScanStatus != "metadata" || row.ErrMsg.Valid {
		t.Fatalf("scan_status=%q error=%+v; want 'metadata' and no error", row.ScanStatus, row.ErrMsg)
	}
}

// A build that fails while the folder is unreachable is not the file's
// failure: with a FailureGuard reporting the folder gone, the row keeps its
// status and the error wraps pipeline.ErrFolderUnavailable.
func TestBuild_FailureGuard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		guard      func() bool
		wantOutage bool
	}{
		{"no guard", nil, false},
		{"folder reachable", func() bool { return true }, false},
		{"folder gone", func() bool { return false }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newBuildEnv(t)
			id := e.insert("gone.jpg", "photo") // the source cannot be read
			e.b.FailureGuard = tc.guard
			err := e.build(id)
			if err == nil {
				t.Fatal("Build = nil; want an error")
			}
			if got := errors.Is(err, pipeline.ErrFolderUnavailable); got != tc.wantOutage {
				t.Fatalf("Build = %v; ErrFolderUnavailable %v, want %v", err, got, tc.wantOutage)
			}
			if tc.wantOutage {
				if row := e.wantStatus(id, "metadata"); row.ErrMsg.Valid {
					t.Fatalf("error = %q; want none recorded", row.ErrMsg.String)
				}
				return
			}
			e.wantFailed(id, "stat source")
		})
	}
}

func TestBuild_SymlinkedCacheDirRefused(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("a.jpg", "photo", platformtest.MakeJPEG(t, 64, 64))
	if err := os.Mkdir(filepath.Join(e.root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(e.root, ".choix", "thumbs")); err != nil {
		t.Fatal(err)
	}
	before := listTree(t, e.root)

	if err := e.build(id); err == nil {
		t.Fatal("Build through a symlinked cache directory: want an error")
	}
	e.wantFailed(id, "symbolic link")
	if after := listTree(t, e.root); !slices.Equal(after, before) {
		t.Fatalf("tree changed:\nbefore %v\nafter  %v", before, after)
	}
}

// listTree returns every path under root, without following symlinks.
func listTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func TestBuild_NeverOpensSourceForWrite(t *testing.T) {
	e := newBuildEnv(t)
	jpg := platformtest.MakeJPEG(t, 300, 200)
	raf := fixture(t, platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 300, 200)))
	ids := []int64{e.add("Day1/a.jpg", "photo", jpg), e.add("Day1/b.RAF", "photo", raf)}
	srcDir := e.abs("Day1")
	for _, name := range []string{"a.jpg", "b.RAF"} {
		if err := os.Chmod(filepath.Join(srcDir, name), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(srcDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srcDir, 0o700) })
	before := listTree(t, srcDir)

	for _, id := range ids {
		e.mustBuild(id)
		e.wantStatus(id, "thumbed")
	}
	if got := fixture(t, filepath.Join(srcDir, "a.jpg")); !bytes.Equal(got, jpg) {
		t.Fatal("a.jpg changed")
	}
	if got := fixture(t, filepath.Join(srcDir, "b.RAF")); !bytes.Equal(got, raf) {
		t.Fatal("b.RAF changed")
	}
	if after := listTree(t, srcDir); !slices.Equal(after, before) {
		t.Fatalf("source directory changed: %v -> %v", before, after)
	}
}

func TestBuilderBuildByStatus(t *testing.T) {
	e := newBuildEnv(t)
	var ids []int64
	for i := 1; i <= 3; i++ {
		ids = append(ids, e.add(fmt.Sprintf("Day1/IMG_%04d.jpg", i), "photo", platformtest.MakeJPEG(t, 64, 48)))
	}
	broken := e.add("Day1/broken.jpg", "photo", []byte("not a jpeg"))
	if _, err := e.st.Files().Insert(store.File{
		Path: "skip.jpg", Size: 1, Mtime: 1, ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	}); err != nil {
		t.Fatalf("Insert skip: %v", err)
	}

	n, err := e.b.BuildByStatus(context.Background(), "metadata", 10)
	if err != nil {
		t.Fatalf("BuildByStatus: %v", err)
	}
	if n != 4 {
		t.Fatalf("processed = %d; want 4", n)
	}
	for _, id := range ids {
		e.wantStatus(id, "thumbed")
	}
	e.wantFailed(broken, "")
}
