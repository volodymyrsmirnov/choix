package group

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// makeFile constructs a store.File with the fields EstimateSkew actually
// looks at — every other field is left zero.
func makeFile(id int64, dev string, capturedAt int64) store.File {
	return store.File{
		ID:         id,
		DeviceKey:  sql.NullString{String: dev, Valid: dev != ""},
		CapturedAt: sql.NullInt64{Int64: capturedAt, Valid: true},
	}
}

func TestEstimateSkewSingleDeviceReturnsEmpty(t *testing.T) {
	files := []store.File{
		makeFile(1, "iPhone#A", 100),
		makeFile(2, "iPhone#A", 200),
	}
	embeds := map[int64][]float32{
		1: normalize([]float32{1, 0, 0, 0}),
		2: normalize([]float32{1, 0, 0, 0}),
	}
	out := mustSkew(t, files, embeds)
	if len(out) != 0 {
		t.Errorf("single device, want empty map; got %v", out)
	}
}

func TestEstimateSkewNoEmbeddingsReturnsEmpty(t *testing.T) {
	files := []store.File{
		makeFile(1, "iPhone#A", 100),
		makeFile(2, "X-T5#B", 100),
	}
	out := mustSkew(t, files, map[int64][]float32{})
	if len(out) != 0 {
		t.Errorf("no embeddings, want empty map; got %v", out)
	}
}

func TestEstimateSkewRecoversKnownOffset(t *testing.T) {
	// iPhone (6 shots) is clearly the reference. X-T5 has 5 matching
	// shots at the same scenes but its clock is +90s.
	const offset = int64(90)
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	v3 := normalize([]float32{0, 0, 1, 0})
	v4 := normalize([]float32{0, 0, 0, 1})
	v5 := normalize([]float32{0.5, 0.5, 0.5, 0.5})
	v6 := normalize([]float32{0.5, -0.5, 0.5, -0.5})

	files := []store.File{
		makeFile(10, "iPhone#A", 100),
		makeFile(11, "iPhone#A", 200),
		makeFile(12, "iPhone#A", 300),
		makeFile(13, "iPhone#A", 400),
		makeFile(14, "iPhone#A", 500),
		makeFile(15, "iPhone#A", 600),
		makeFile(20, "X-T5#B", 100+offset),
		makeFile(21, "X-T5#B", 200+offset),
		makeFile(22, "X-T5#B", 300+offset),
		makeFile(23, "X-T5#B", 400+offset),
		makeFile(24, "X-T5#B", 500+offset),
	}
	embeds := map[int64][]float32{
		10: v1, 11: v2, 12: v3, 13: v4, 14: v5, 15: v6,
		20: v1, 21: v2, 22: v3, 23: v4, 24: v5,
	}
	out := mustSkew(t, files, embeds)
	if got := out["iPhone#A"]; got != 0 {
		t.Errorf("skew[iPhone] = %d, want 0 (reference, more shots)", got)
	}
	if got := out["X-T5#B"]; got != offset {
		t.Errorf("skew[X-T5] = %d, want %d", got, offset)
	}
}

// TestEstimateSkewMinAnchors: an estimate needs minAnchors qualifying pairs.
// Each shot is its own scene (distinct vectors), so the second camera's
// shots give exactly one pair each, every one 50 s apart: two pairs are
// rejected (offset 0), three are accepted (offset 50).
func TestEstimateSkewMinAnchors(t *testing.T) {
	const offset = int64(50)
	scenes := []([]float32){
		normalize([]float32{1, 0, 0, 0}),
		normalize([]float32{0, 1, 0, 0}),
		normalize([]float32{0, 0, 1, 0}),
		normalize([]float32{0, 0, 0, 1}),
	}
	for _, tc := range []struct {
		anchors int
		want    int64
	}{{minAnchors - 1, 0}, {minAnchors, offset}} {
		files := []store.File{
			// Reference: four shots of four scenes.
			makeFile(10, "iPhone#A", 100),
			makeFile(11, "iPhone#A", 200),
			makeFile(12, "iPhone#A", 300),
			makeFile(13, "iPhone#A", 400),
		}
		embeds := map[int64][]float32{10: scenes[0], 11: scenes[1], 12: scenes[2], 13: scenes[3]}
		for k := range tc.anchors {
			id := int64(20 + k)
			files = append(files, makeFile(id, "X-T5#B", 100*int64(k+1)+offset))
			embeds[id] = scenes[k]
		}
		deltas, err := pairDeltas(context.Background(), byDeviceForTest(files, "iPhone#A"), byDeviceForTest(files, "X-T5#B"), embeds)
		if err != nil {
			t.Fatal(err)
		}
		if deltas.n != tc.anchors || deltas.median() != offset {
			t.Fatalf("%d anchors: pairs %d with median %d; want %d with median %d", tc.anchors, deltas.n, deltas.median(), tc.anchors, offset)
		}
		if got := mustSkew(t, files, embeds)["X-T5#B"]; got != tc.want {
			t.Errorf("%d anchors (minAnchors %d): offset %d, want %d", tc.anchors, minAnchors, got, tc.want)
		}
	}
}

// byDeviceForTest returns dev's files as skew entries, by capture time.
func byDeviceForTest(files []store.File, dev string) []skewEntry {
	var out []skewEntry
	for _, f := range files {
		if f.DeviceKey.String == dev {
			out = append(out, skewEntry{ID: f.ID, CapturedAt: f.CapturedAt.Int64})
		}
	}
	slices.SortFunc(out, func(a, b skewEntry) int { return cmp.Compare(a.CapturedAt, b.CapturedAt) })
	return out
}

// TestDeltaHistogram_MedianMatchesSort: the bounded histogram gives the
// median a sort of the deltas gives, for odd and even counts, negative
// deltas and the window's ends.
func TestDeltaHistogram_MedianMatchesSort(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for trial := range 500 {
		n := 1 + rng.IntN(40)
		var h deltaHistogram
		xs := make([]int64, n)
		for i := range xs {
			switch rng.IntN(10) {
			case 0:
				xs[i] = -maxSkewSec
			case 1:
				xs[i] = maxSkewSec
			default:
				xs[i] = int64(rng.IntN(2*maxSkewSec+1)) - maxSkewSec
			}
			if trial%3 == 0 {
				xs[i] = int64(rng.IntN(7)) - 3 // many ties
			}
			h.add(xs[i])
		}
		slices.Sort(xs)
		want := xs[n/2]
		if n%2 == 0 {
			want = (xs[n/2-1] + xs[n/2]) / 2
		}
		if got := h.median(); got != want || h.n != n {
			t.Fatalf("trial %d: median of %v = %d (n %d); want %d", trial, xs, got, h.n, want)
		}
	}
}

// TestPairDeltas_CancelledDuringComparisons: cancellation that lands while
// one reference shot is compared with many others is seen inside that inner
// loop, not only before the next reference shot.
func TestPairDeltas_CancelledDuringComparisons(t *testing.T) {
	v := normalize([]float32{1, 0, 0, 0})
	ref := []skewEntry{{ID: 1, CapturedAt: 1000}}
	embeds := map[int64][]float32{1: v}
	var other []skewEntry
	for i := range 20000 {
		id := int64(100 + i)
		other = append(other, skewEntry{ID: id, CapturedAt: 1000 + int64(i%600)})
		embeds[id] = v
	}
	slices.SortFunc(other, func(a, b skewEntry) int { return cmp.Compare(a.CapturedAt, b.CapturedAt) })
	ctx := newCountdownCtx(2) // the first check passes; comparisons begin
	if _, err := pairDeltas(ctx, ref, other, embeds); !errors.Is(err, context.Canceled) {
		t.Fatalf("pairDeltas = %v; want context.Canceled", err)
	}
}

// TestEstimateSkew_CancelledAfterComparisonsBegin: a cancellation after the
// pair search has started stops the estimate.
func TestEstimateSkew_CancelledAfterComparisonsBegin(t *testing.T) {
	v := normalize([]float32{1, 0, 0, 0})
	var files []store.File
	embeds := map[int64][]float32{}
	for i := range 3000 {
		for j, dev := range []string{"iPhone#A", "X-T5#B"} {
			id := int64(2*i + j + 1)
			files = append(files, makeFile(id, dev, 1000+int64(i%600)))
			embeds[id] = v
		}
	}
	ctx := newCountdownCtx(2)
	if _, err := EstimateSkew(ctx, files, embeds); !errors.Is(err, context.Canceled) {
		t.Fatalf("EstimateSkew = %v; want context.Canceled", err)
	}
}

func TestEstimateSkewIgnoresPairsBelowSimilarity(t *testing.T) {
	// 5 ref shots, 5 candidate shots — all with orthogonal embeddings, so
	// no pair clears 0.95. Result: offset stays 0.
	files := []store.File{
		makeFile(10, "iPhone#A", 100),
		makeFile(11, "iPhone#A", 200),
		makeFile(12, "iPhone#A", 300),
		makeFile(13, "iPhone#A", 400),
		makeFile(14, "iPhone#A", 500),
		makeFile(20, "X-T5#B", 150),
		makeFile(21, "X-T5#B", 250),
		makeFile(22, "X-T5#B", 350),
		makeFile(23, "X-T5#B", 450),
		makeFile(24, "X-T5#B", 550),
	}
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	embeds := map[int64][]float32{
		10: v1, 11: v1, 12: v1, 13: v1, 14: v1,
		20: v2, 21: v2, 22: v2, 23: v2, 24: v2,
	}
	out := mustSkew(t, files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("no high-similarity pairs, expected offset 0; got %d", out["X-T5#B"])
	}
}

func TestEstimateSkewIgnoresPairsOutsideMaxSkew(t *testing.T) {
	v := normalize([]float32{1, 0, 0, 0})
	// X-T5 shots are >maxSkewSec away from the iPhone shots.
	files := []store.File{
		makeFile(10, "iPhone#A", 1000),
		makeFile(11, "iPhone#A", 1100),
		makeFile(12, "iPhone#A", 1200),
		makeFile(13, "iPhone#A", 1300),
		// Other device: shifted by 2h (> 1h max).
		makeFile(20, "X-T5#B", 1000+7200),
		makeFile(21, "X-T5#B", 1100+7200),
		makeFile(22, "X-T5#B", 1200+7200),
		makeFile(23, "X-T5#B", 1300+7200),
	}
	embeds := map[int64][]float32{
		10: v, 11: v, 12: v, 13: v,
		20: v, 21: v, 22: v, 23: v,
	}
	out := mustSkew(t, files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("pairs outside maxSkewSec, expected offset 0; got %d", out["X-T5#B"])
	}
}

func TestEstimateSkewReferenceIsLargestDevice(t *testing.T) {
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	v3 := normalize([]float32{0, 0, 1, 0})
	v4 := normalize([]float32{0, 0, 0, 1})
	v5 := normalize([]float32{0.5, 0.5, 0.5, 0.5})
	// X-T5 has 5 shots, iPhone has 3 — reference is X-T5. Distinct scenes
	// per shot, so anchor pairs are 1:1 and median is exact.
	files := []store.File{
		makeFile(20, "X-T5#B", 1000),
		makeFile(21, "X-T5#B", 1100),
		makeFile(22, "X-T5#B", 1200),
		makeFile(23, "X-T5#B", 1300),
		makeFile(24, "X-T5#B", 1400),
		makeFile(10, "iPhone#A", 1000+30),
		makeFile(11, "iPhone#A", 1100+30),
		makeFile(12, "iPhone#A", 1200+30),
	}
	embeds := map[int64][]float32{
		20: v1, 21: v2, 22: v3, 23: v4, 24: v5,
		10: v1, 11: v2, 12: v3,
	}
	out := mustSkew(t, files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("X-T5 should be reference (offset 0); got %d", out["X-T5#B"])
	}
	if got := out["iPhone#A"]; got != 30 {
		t.Errorf("iPhone offset = %d, want 30", got)
	}
}

// mustSkew runs EstimateSkew with a background context.
func mustSkew(t *testing.T, files []store.File, embeds map[int64][]float32) map[string]int64 {
	t.Helper()
	out, err := EstimateSkew(context.Background(), files, embeds)
	if err != nil {
		t.Fatalf("EstimateSkew: %v", err)
	}
	return out
}

func TestEstimateSkew_Cancelled(t *testing.T) {
	v := normalize([]float32{1, 0, 0, 0})
	files := []store.File{makeFile(1, "iPhone#A", 100), makeFile(2, "X-T5#B", 110)}
	embeds := map[int64][]float32{1: v, 2: v}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EstimateSkew(ctx, files, embeds); !errors.Is(err, context.Canceled) {
		t.Fatalf("EstimateSkew = %v; want context.Canceled", err)
	}
}

// TestSkew_AnchorThreshold: on the feature print's scale a pair anchors the
// skew estimate at a similarity of 0.93 or more: pairs at 0.94 recover the
// offset, pairs at 0.92 leave it at 0.
func TestSkew_AnchorThreshold(t *testing.T) {
	const offset = int64(90)
	for _, tc := range []struct {
		sim  float64
		want int64
	}{{0.94, offset}, {0.92, 0}} {
		files := []store.File{
			makeFile(10, "iPhone#A", 100),
			makeFile(11, "iPhone#A", 200),
			makeFile(12, "iPhone#A", 300),
			makeFile(13, "iPhone#A", 400),
			makeFile(20, "X-T5#B", 100+offset),
			makeFile(21, "X-T5#B", 200+offset),
			makeFile(22, "X-T5#B", 300+offset),
		}
		// Scene k lies in dimensions 2k and 2k+1, so only the two shots of
		// one scene are alike; the second camera's shot is tc.sim away.
		scene := func(k int, sim float64) []float32 {
			v := make([]float32, 8)
			v[2*k] = float32(sim)
			v[2*k+1] = float32(math.Sqrt(1 - sim*sim))
			return v
		}
		embeds := map[int64][]float32{
			10: scene(0, 1), 11: scene(1, 1), 12: scene(2, 1), 13: scene(3, 1),
			20: scene(0, tc.sim), 21: scene(1, tc.sim), 22: scene(2, tc.sim),
		}
		out := mustSkew(t, files, embeds)
		if got := out["X-T5#B"]; got != tc.want {
			t.Errorf("similarity %.2f: skew = %d, want %d", tc.sim, got, tc.want)
		}
	}
}
