package local

import (
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestQualityDisplayMapsRawToZeroToTen(t *testing.T) {
	cases := []struct {
		raw  float64
		want float64
	}{
		{-1, 0},       // below the floor clamps
		{0.10, 0},     // the floor
		{0.475, 5},    // midpoint
		{0.85, 10},    // the ceiling
		{2, 10},       // above the ceiling clamps
		{0.6552, 7.4}, // rounds to one decimal
		{0.16, 0.8},
	}
	for _, c := range cases {
		if got := QualityDisplay(c.raw); got != c.want {
			t.Errorf("QualityDisplay(%v) = %v, want %v", c.raw, got, c.want)
		}
	}
	// Monotonic: a better raw score never displays lower.
	prev := -1.0
	for raw := 0.0; raw <= 1.0; raw += 0.005 {
		if d := QualityDisplay(raw); d < prev {
			t.Fatalf("display fell from %v to %v at raw %v", prev, d, raw)
		} else {
			prev = d
		}
	}
}

func TestQualityCropRectsStayInsideAndCoverCentreAndQuadrants(t *testing.T) {
	for _, size := range [][2]int{{1600, 1067}, {1067, 1600}, {384, 384}, {500, 384}, {1600, 400}} {
		w, h := size[0], size[1]
		rects := qualityCropRects(w, h)
		if len(rects) != 5 {
			t.Fatalf("%dx%d: %d crops, want 5", w, h, len(rects))
		}
		bounds := image.Rect(0, 0, w, h)
		for _, r := range rects {
			if r.Dx() != qualitySide || r.Dy() != qualitySide || !r.In(bounds) {
				t.Errorf("%dx%d: crop %v is not a %dpx square inside the image", w, h, r, qualitySide)
			}
		}
	}
	// On a full-size preview the first crop is centred and the others sit
	// in four different quadrants.
	rects := qualityCropRects(1600, 1200)
	if c := rects[0].Min.Add(image.Pt(192, 192)); c != image.Pt(800, 600) {
		t.Errorf("centre crop is centred on %v, want (800,600)", c)
	}
	seen := map[[2]bool]bool{}
	for _, r := range rects[1:] {
		seen[[2]bool{r.Min.X+192 > 800, r.Min.Y+192 > 600}] = true
	}
	if len(seen) != 4 {
		t.Errorf("quadrant crops cover %d quadrants, want 4", len(seen))
	}
}

func TestQualityTargetSize(t *testing.T) {
	cases := []struct {
		name         string
		pw, ph       int
		ow, oh       int64
		wantW, wantH int
	}{
		{"landscape preview kept", 1600, 1067, 6000, 4000, 1600, 1067},
		{"portrait sips preview kept", 1067, 1600, 4000, 6000, 1067, 1600},
		{"portrait ffmpeg preview (width 1600) reduced to long side 1600", 1600, 2400, 4000, 6000, 1067, 1600},
		{"upscaled preview brought back to the original size", 1600, 1200, 800, 600, 800, 600},
		{"unknown original size", 1600, 1200, 0, 0, 1600, 1200},
		{"small preview never scaled up", 640, 480, 6000, 4000, 640, 480},
	}
	for _, c := range cases {
		w, h := qualityTargetSize(c.pw, c.ph, c.ow, c.oh)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: got %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}

func TestTopMeanAveragesTheBestCrops(t *testing.T) {
	// A sharp subject (three good crops) with a blurred background (two
	// poor ones) scores like its sharp part.
	got := topMean([]float64{0.2, 0.8, 0.7, 0.1, 0.9}, 3)
	if want := 0.8; got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("topMean = %v, want %v", got, want)
	}
}

// qualityEnv is a scorer wired to a real store and a temp preview directory,
// with inference replaced by brightness: the fake "model" returns the mean
// red value of the crop, so tests can see which pixels were sampled.
type qualityEnv struct {
	store    *store.Store
	scorer   *QualityScorer
	previews map[int64]string
	dir      string
	inferred int
	// infer is the fake model the scorer calls; tests may replace it.
	infer func(in []float32) (float64, error)
}

// cropFunc adapts a function to CropInferer.
type cropFunc func(in []float32) (float64, error)

func (f cropFunc) QualityCrop(in []float32) (float64, error) { return f(in) }

func newQualityEnv(t *testing.T) *qualityEnv {
	t.Helper()
	e := &qualityEnv{store: store.NewTestStore(t), previews: map[int64]string{}, dir: t.TempDir()}
	e.infer = func(in []float32) (float64, error) {
		e.inferred++
		var sum float64
		plane := qualitySide * qualitySide
		for _, v := range in[:plane] {
			sum += float64(v)
		}
		return sum / float64(plane), nil
	}
	e.useInferer(cropFunc(func(in []float32) (float64, error) { return e.infer(in) }))
	return e
}

// useInferer rebuilds the scorer around inf.
func (e *qualityEnv) useInferer(inf CropInferer) {
	e.scorer = NewQualityScorer(e.store, inf, func(id int64) (string, error) {
		p, ok := e.previews[id]
		if !ok {
			return "", errors.New("no preview row")
		}
		return p, nil
	})
}

// addPhoto inserts an analyzed photo whose preview is a w×h JPEG painted by
// paint, and returns its pending item.
func (e *qualityEnv) addPhoto(t *testing.T, name string, w, h int, paint func(x, y int) color.Color) store.QualityItem {
	t.Helper()
	id, err := e.store.Files().Insert(store.File{Path: name, Size: 1, Mtime: 1, ContentHash: "hash-" + name, Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	if w > 0 {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Set(x, y, paint(x, y))
			}
		}
		p := filepath.Join(e.dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		e.previews[id] = p
	}
	return store.QualityItem{FileID: id, ContentHash: "hash-" + name}
}

func (e *qualityEnv) stored(t *testing.T, id int64) (sql.NullFloat64, sql.NullString) {
	t.Helper()
	cid, err := e.store.Clusters().InsertCluster(store.Cluster{DeviceKey: "probe"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Clusters().AddMember(cid, id); err != nil {
		t.Fatal(err)
	}
	ms, err := e.store.Clusters().Members(cid)
	if err != nil || len(ms) != 1 {
		t.Fatalf("Members: %v %v", ms, err)
	}
	return ms[0].QualityRaw, ms[0].QualityModel
}

func white(int, int) color.Color { return color.White }

func TestScoreStoresTopThreeMeanOfFiveCrops(t *testing.T) {
	e := newQualityEnv(t)
	// Left half white, right half black: the centre crop straddles the
	// edge (≈0.5), the two left quadrant crops are white (1), the two
	// right ones black (0). Top-3 mean = (1 + 1 + 0.5) / 3.
	item := e.addPhoto(t, "split.jpg", 1600, 1200, func(x, _ int) color.Color {
		if x < 800 {
			return color.White
		}
		return color.Black
	})
	if err := e.scorer.Score(context.Background(), item); err != nil {
		t.Fatalf("Score: %v", err)
	}
	if e.inferred != 5 {
		t.Fatalf("inferences = %d, want 5 crops", e.inferred)
	}
	raw, model := e.stored(t, item.FileID)
	if !raw.Valid || raw.Float64 < 0.82 || raw.Float64 > 0.85 {
		t.Fatalf("raw = %+v, want ≈0.833 (top-3 mean)", raw)
	}
	if model.String != QualityScorerID {
		t.Fatalf("model = %q, want %q", model.String, QualityScorerID)
	}
	if n, _ := e.scorer.CountPending(); n != 0 {
		t.Fatalf("pending = %d after rating", n)
	}
}

func TestScoreMarksUnratableWithoutInference(t *testing.T) {
	e := newQualityEnv(t)
	cases := map[string]store.QualityItem{
		"no preview":     e.addPhoto(t, "nopreview.jpg", 0, 0, nil),
		"too small":      e.addPhoto(t, "tiny.jpg", 300, 200, white),
		"thin panorama":  e.addPhoto(t, "pano.jpg", 1600, 300, white),
		"upscaled small": e.addPhoto(t, "upscaled.jpg", 1600, 1200, white),
	}
	up := cases["upscaled small"]
	up.Width, up.Height = sql.NullInt64{Int64: 320, Valid: true}, sql.NullInt64{Int64: 240, Valid: true}
	cases["upscaled small"] = up
	// A corrupt preview file.
	bad := e.addPhoto(t, "corrupt.jpg", 0, 0, nil)
	p := filepath.Join(e.dir, "corrupt.jpg")
	if err := os.WriteFile(p, []byte("not a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.previews[bad.FileID] = p
	cases["corrupt preview"] = bad

	for name, item := range cases {
		if err := e.scorer.Score(context.Background(), item); err != nil {
			t.Fatalf("%s: Score returned %v; an unratable photo is not an error", name, err)
		}
		raw, model := e.stored(t, item.FileID)
		if raw.Valid || model.String != QualityScorerID {
			t.Errorf("%s: stored raw=%+v model=%q, want NULL score stamped with the scorer id", name, raw, model.String)
		}
	}
	if e.inferred != 0 {
		t.Fatalf("inferences = %d, want 0 for unratable photos", e.inferred)
	}
	if n, _ := e.scorer.CountPending(); n != 0 {
		t.Fatalf("pending = %d; unratable photos must not be retried", n)
	}
}

// A preview that cannot be read or decoded while the folder is unreachable
// (the folder vanished after the caller last checked it) leaves the photo
// pending; with the folder reachable it is stamped unratable as before. An
// image too small to rate is unratable whatever the guard says.
func TestScoreFailureGuard(t *testing.T) {
	gone := func() bool { return false }
	here := func() bool { return true }
	for _, tc := range []struct {
		name        string
		add         func(e *qualityEnv) store.QualityItem
		guard       func() bool
		wantPending bool
	}{
		{"missing preview, folder gone", func(e *qualityEnv) store.QualityItem {
			return e.addPhoto(t, "a.jpg", 0, 0, nil)
		}, gone, true},
		{"vanished preview file, folder gone", func(e *qualityEnv) store.QualityItem {
			item := e.addPhoto(t, "a.jpg", 800, 600, white)
			if err := os.Remove(e.previews[item.FileID]); err != nil {
				t.Fatal(err)
			}
			return item
		}, gone, true},
		{"corrupt preview, folder gone", func(e *qualityEnv) store.QualityItem {
			item := e.addPhoto(t, "a.jpg", 0, 0, nil)
			p := filepath.Join(e.dir, "a.jpg")
			if err := os.WriteFile(p, []byte("not a jpeg"), 0o600); err != nil {
				t.Fatal(err)
			}
			e.previews[item.FileID] = p
			return item
		}, gone, true},
		{"missing preview, folder reachable", func(e *qualityEnv) store.QualityItem {
			return e.addPhoto(t, "a.jpg", 0, 0, nil)
		}, here, false},
		{"too small, folder gone", func(e *qualityEnv) store.QualityItem {
			return e.addPhoto(t, "a.jpg", 300, 200, white)
		}, gone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newQualityEnv(t)
			item := tc.add(e)
			e.scorer.FailureGuard = tc.guard
			err := e.scorer.Score(context.Background(), item)
			if got := errors.Is(err, pipeline.ErrFolderUnavailable); got != tc.wantPending {
				t.Fatalf("Score = %v; ErrFolderUnavailable %v, want %v", err, got, tc.wantPending)
			}
			if !tc.wantPending && err != nil {
				t.Fatalf("Score = %v; an unratable photo is not an error", err)
			}
			raw, model := e.stored(t, item.FileID)
			if tc.wantPending && (raw.Valid || model.Valid) {
				t.Fatalf("stored raw=%+v model=%+v; nothing may be stamped while the folder is gone", raw, model)
			}
			if !tc.wantPending && model.String != QualityScorerID {
				t.Fatalf("stored model=%q; want the photo stamped unratable", model.String)
			}
			if n, _ := e.scorer.CountPending(); (n == 1) != tc.wantPending {
				t.Fatalf("pending = %d; want pending %v", n, tc.wantPending)
			}
		})
	}
}

func TestScoreLeavesPhotoPendingOnCancellationAndInferenceError(t *testing.T) {
	e := newQualityEnv(t)
	item := e.addPhoto(t, "a.jpg", 800, 600, white)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.scorer.Score(ctx, item); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Score = %v, want context.Canceled", err)
	}

	// Cancelled between crops: stops without finishing all five.
	ctx2, cancel2 := context.WithCancel(context.Background())
	inner := e.infer
	e.infer = func(in []float32) (float64, error) {
		cancel2()
		return inner(in)
	}
	if err := e.scorer.Score(ctx2, item); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-photo cancel = %v, want context.Canceled", err)
	}
	if e.inferred != 1 {
		t.Fatalf("inferences after mid-photo cancel = %d, want 1", e.inferred)
	}

	boom := errors.New("coreml exploded")
	e.infer = func([]float32) (float64, error) { return 0, boom }
	if err := e.scorer.Score(context.Background(), item); !errors.Is(err, boom) {
		t.Fatalf("Score = %v, want the inference error", err)
	}

	raw, model := e.stored(t, item.FileID)
	if raw.Valid || model.Valid {
		t.Fatalf("stored raw=%+v model=%+v; nothing may be stamped on cancel or inference failure", raw, model)
	}
	if n, _ := e.scorer.CountPending(); n != 1 {
		t.Fatalf("pending = %d, want the photo to stay pending", n)
	}
}

func TestScoreDropsResultWhenFileChangedMeanwhile(t *testing.T) {
	e := newQualityEnv(t)
	item := e.addPhoto(t, "a.jpg", 800, 600, white)
	item.ContentHash = "hash-of-the-old-bytes"
	if err := e.scorer.Score(context.Background(), item); err != nil {
		t.Fatalf("Score: %v", err)
	}
	if raw, _ := e.stored(t, item.FileID); raw.Valid {
		t.Fatal("a score for superseded bytes must not be stored")
	}
}

func TestScoreReportsUnavailableWhenModelCannotLoad(t *testing.T) {
	e := newQualityEnv(t)
	// The real session path; the model file does not exist.
	sessions := NewSessions(NewModelStore(t.TempDir()), SessionOptions{})
	e.useInferer(sessions)
	item := e.addPhoto(t, "a.jpg", 800, 600, white)
	err := e.scorer.Score(context.Background(), item)
	if !errors.Is(err, ErrQualityUnavailable) {
		t.Fatalf("Score = %v, want ErrQualityUnavailable", err)
	}
	if n, _ := e.scorer.CountPending(); n != 1 {
		t.Fatal("the photo must stay pending when the scorer cannot run")
	}
	if err := sessions.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestQualityModelRanksSharpAboveBlurred runs the real model when it and
// the ONNX runtime are installed (see installedModels) and skips otherwise.
// A detailed pattern must score clearly above its blurred copy.
func TestQualityModelRanksSharpAboveBlurred(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	models := installedModels(t, ModelQuality)
	skipWithoutRuntime(t)

	e := newQualityEnv(t)
	sessions := NewSessions(models, SessionOptions{})
	t.Cleanup(func() { _ = sessions.Close() })
	e.useInferer(sessions)

	// Fine checker + diagonal stripes: plenty of edges.
	detail := func(x, y int) color.Color {
		v := uint8(40)
		if (x/6+y/6)%2 == 0 {
			v = 215
		}
		if (x+y)%37 < 3 {
			v = 128
		}
		return color.RGBA{v, uint8((int(v) + x/8) % 256), uint8((int(v) + y/8) % 256), 255}
	}
	// The same pattern averaged over a 15px box: no edges left.
	blurred := func(x, y int) color.Color {
		var r, g, b int
		const k = 7
		n := 0
		for dy := -k; dy <= k; dy += 2 {
			for dx := -k; dx <= k; dx += 2 {
				c := detail(x+dx, y+dy).(color.RGBA)
				r, g, b, n = r+int(c.R), g+int(c.G), b+int(c.B), n+1
			}
		}
		return color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255}
	}
	sharp := e.addPhoto(t, "sharp.jpg", 900, 700, detail)
	soft := e.addPhoto(t, "soft.jpg", 900, 700, blurred)
	for _, it := range []store.QualityItem{sharp, soft} {
		if err := e.scorer.Score(context.Background(), it); err != nil {
			t.Fatalf("Score: %v", err)
		}
	}
	a, _ := e.stored(t, sharp.FileID)
	b, _ := e.stored(t, soft.FileID)
	t.Logf("sharp raw=%.3f (%.1f) blurred raw=%.3f (%.1f)", a.Float64, QualityDisplay(a.Float64), b.Float64, QualityDisplay(b.Float64))
	if !a.Valid || !b.Valid || a.Float64 <= b.Float64 {
		t.Fatalf("sharp %.3f must score above blurred %.3f", a.Float64, b.Float64)
	}
}
