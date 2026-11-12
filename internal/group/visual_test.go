package group

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"
)

// normalize returns v / |v|.
func normalize(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(s))
	if n == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

// mustCluster runs Cluster with a background context.
func mustCluster(t *testing.T, vectors map[int64][]float32, threshold float64) [][]int64 {
	t.Helper()
	got, err := Cluster(context.Background(), vectors, threshold)
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	return got
}

// randomUnit returns a random unit vector of dim dimensions.
func randomUnit(r *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return normalize(v)
}

func randomVectors(r *rand.Rand, n, dim int) map[int64][]float32 {
	out := make(map[int64][]float32, n)
	for i := range n {
		out[int64(i+1)] = randomUnit(r, dim)
	}
	return out
}

func TestClusterSinglesAreOwnCluster(t *testing.T) {
	embs := map[int64][]float32{
		1: normalize([]float32{1, 0, 0, 0}),
		2: normalize([]float32{0, 1, 0, 0}),
		3: normalize([]float32{0, 0, 1, 0}),
	}
	got := mustCluster(t, embs, 0.92)
	if len(got) != 3 {
		t.Fatalf("expected 3 singleton clusters, got %d (%v)", len(got), got)
	}
}

func TestClusterMergesSimilarPairs(t *testing.T) {
	// 1 and 2 are near-duplicate (cos ~ 0.9994). 3 is orthogonal.
	embs := map[int64][]float32{
		1: normalize([]float32{1.0, 0.0, 0.0, 0.0}),
		2: normalize([]float32{0.99, 0.05, 0.0, 0.0}),
		3: normalize([]float32{0.0, 0.0, 1.0, 0.0}),
	}
	got := mustCluster(t, embs, 0.92)
	want := [][]int64{{1, 2}, {3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Cluster: got %v want %v", got, want)
	}
}

func TestClusterChainIsNotMerged(t *testing.T) {
	// 1~2 and 2~3 above threshold; 1~3 below. Complete-link keeps the far
	// ends of the chain apart: two clusters.
	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.95, 0.31, 0.0, 0.0}) // cos(a,b) ~ 0.95
	c := normalize([]float32{0.80, 0.60, 0.0, 0.0}) // cos(b,c) ~ 0.95, cos(a,c) ~ 0.80
	got := mustCluster(t, map[int64][]float32{1: a, 2: b, 3: c}, 0.92)
	if len(got) != 2 {
		t.Fatalf("complete-link must not merge the chain: got %v", got)
	}
}

func TestClusterBelowThresholdStaysSplit(t *testing.T) {
	// Cosine ~ 0.85 — below 0.92 threshold.
	embs := map[int64][]float32{
		1: normalize([]float32{1.0, 0.0, 0.0, 0.0}),
		2: normalize([]float32{0.85, 0.527, 0.0, 0.0}),
	}
	got := mustCluster(t, embs, 0.92)
	if len(got) != 2 {
		t.Errorf("expected 2 clusters below threshold, got %d (%v)", len(got), got)
	}
}

// The spec's example: a·b and b·c tie at 0.95, a·c is about 0.805. The tie
// goes to (a, b); c then cannot join, since its link to a is too low.
// Single-link would have returned one group.
func TestCluster_CompleteLinkDoesNotChain(t *testing.T) {
	theta := math.Acos(0.95)
	a := []float32{float32(math.Cos(theta)), float32(math.Sin(theta))}
	b := []float32{1, 0}
	c := []float32{float32(math.Cos(theta)), float32(-math.Sin(theta))}
	if ab, bc := similarity(a, b), similarity(b, c); ab != bc {
		t.Fatalf("a·b = %v, b·c = %v; the construction must tie exactly", ab, bc)
	}
	if ac := similarity(a, c); math.Abs(ac-0.805) > 1e-3 {
		t.Fatalf("a·c = %v; want about 0.805", ac)
	}
	got := mustCluster(t, map[int64][]float32{1: a, 2: b, 3: c}, 0.90)
	if want := [][]int64{{1, 2}, {3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Cluster = %v; want %v", got, want)
	}
}

func TestCluster_StrictThreshold(t *testing.T) {
	v := []float32{1, 0, 0}
	got := mustCluster(t, map[int64][]float32{1: v, 2: slices.Clone(v)}, 1.0)
	if want := [][]int64{{1}, {2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Cluster at a similarity equal to the threshold = %v; want %v", got, want)
	}
}

// naiveCluster is the rule of spec §5.4 written as plainly as possible.
func naiveCluster(vectors map[int64][]float32, threshold float64) [][]int64 {
	var clusters [][]int64
	for id := range vectors {
		clusters = append(clusters, []int64{id})
	}
	slices.SortFunc(clusters, func(x, y []int64) int { return int(x[0] - y[0]) })
	for {
		bi, bj := -1, -1
		var best float64
		var bLo, bHi int64
		for i := range clusters {
			for j := i + 1; j < len(clusters); j++ {
				link := math.Inf(1)
				for _, p := range clusters[i] {
					for _, q := range clusters[j] {
						link = math.Min(link, similarity(vectors[p], vectors[q]))
					}
				}
				lo, hi := slices.Min(clusters[i]), slices.Min(clusters[j])
				if lo > hi {
					lo, hi = hi, lo
				}
				if bi < 0 || link > best || (link == best && (lo < bLo || (lo == bLo && hi < bHi))) {
					bi, bj, best, bLo, bHi = i, j, link, lo, hi
				}
			}
		}
		if bi < 0 || !(best > threshold) {
			break
		}
		clusters[bi] = append(clusters[bi], clusters[bj]...)
		clusters = slices.Delete(clusters, bj, bj+1)
	}
	for _, c := range clusters {
		slices.Sort(c)
	}
	slices.SortFunc(clusters, func(x, y []int64) int { return int(x[0] - y[0]) })
	return clusters
}

func TestCluster_MatchesNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for i := range 200 {
		n := 2 + r.IntN(39)
		vectors := map[int64][]float32{}
		var made [][]float32
		for _, id := range r.Perm(1000)[:n] {
			v := randomUnit(r, 8)
			// Some cases repeat earlier vectors, so linkages tie and the
			// tie rule decides.
			if len(made) > 0 && i%3 == 0 && r.IntN(3) == 0 {
				v = slices.Clone(made[r.IntN(len(made))])
			}
			made = append(made, v)
			vectors[int64(id+1)] = v
		}
		threshold := -0.3 + 1.2*r.Float64()
		got := mustCluster(t, vectors, threshold)
		want := naiveCluster(vectors, threshold)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d (n=%d, threshold %.4f): Cluster = %v; naive = %v", i, n, threshold, got, want)
		}
	}
}

// Coarse vectors (components in {-1, 0, 1}) and repeated ones make many
// linkages tie exactly, so the tie rule and the merge's key upkeep for ties
// decide most cases.
func TestCluster_MatchesNaiveWithTies(t *testing.T) {
	r := rand.New(rand.NewPCG(99, 100))
	for i := range 2000 {
		n := 2 + r.IntN(25)
		dim := 2 + r.IntN(4)
		vectors := map[int64][]float32{}
		var made [][]float32
		for _, id := range r.Perm(1000)[:n] {
			v := make([]float32, dim)
			for k := range v {
				v[k] = float32(r.IntN(3) - 1)
			}
			if len(made) > 0 && r.IntN(2) == 0 {
				v = slices.Clone(made[r.IntN(len(made))])
			}
			made = append(made, v)
			vectors[int64(id+1)] = v
		}
		threshold := -2 + 4*r.Float64()
		got := mustCluster(t, vectors, threshold)
		want := naiveCluster(vectors, threshold)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d (n=%d, threshold %.4f): Cluster = %v; naive = %v", i, n, threshold, got, want)
		}
	}
}

func TestCluster_Ordering(t *testing.T) {
	x := normalize([]float32{1, 0, 0})
	y := normalize([]float32{0, 1, 0})
	got := mustCluster(t, map[int64][]float32{9: y, 5: x, 3: y, 1: x, 7: normalize([]float32{0, 0, 1})}, 0.9)
	if want := [][]int64{{1, 5}, {3, 9}, {7}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Cluster = %v; want %v", got, want)
	}
}

func TestCluster_LengthMismatch(t *testing.T) {
	got := mustCluster(t, map[int64][]float32{1: {1, 0}, 2: {1, 0, 0}}, 0.5)
	if want := [][]int64{{1}, {2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Cluster = %v; want vectors of different lengths apart", got)
	}
}

func TestCluster_Empty(t *testing.T) {
	if got := mustCluster(t, nil, 0.9); len(got) != 0 {
		t.Fatalf("Cluster(nil) = %v; want none", got)
	}
	if got := mustCluster(t, map[int64][]float32{4: {1, 0}}, 0.9); !reflect.DeepEqual(got, [][]int64{{4}}) {
		t.Fatalf("Cluster of one = %v; want [[4]]", got)
	}
}

func TestCluster_Cancelled(t *testing.T) {
	vectors := randomVectors(rand.New(rand.NewPCG(1, 2)), 3000, 768)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := Cluster(ctx, vectors, 0.9)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Cluster = %v; want context.Canceled", err)
		}
	case <-time.After(50*time.Millisecond + time.Second):
		t.Fatal("Cluster did not return within 1 s of its cancellation")
	}
	if took := time.Since(start); took > 50*time.Millisecond+time.Second {
		t.Fatalf("Cluster returned after %v", took)
	}
}

func TestCluster_TwoThousandUnderFiveSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	if raceEnabled {
		t.Skip("timing test: the race detector slows the similarity loop many times over")
	}
	vectors := randomVectors(rand.New(rand.NewPCG(3, 4)), 2000, 768)
	start := time.Now()
	if _, err := Cluster(context.Background(), vectors, 0.9); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("2,000 prints took %v; want under 5 s", took)
	}
}

// timelapse returns n distinct but near-identical unit vectors of dim
// dimensions: one base vector plus small seeded noise, so every pair is
// above 0.9 and complete-link merges them all, one merge at a time.
func timelapse(r *rand.Rand, n, dim int) map[int64][]float32 {
	base := randomUnit(r, dim)
	out := make(map[int64][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for k := range v {
			v[k] = base[k] + float32(0.004*r.NormFloat64())
		}
		out[int64(i+1)] = normalize(v)
	}
	return out
}

// The merge phase dominates a timelapse: 1,999 merges, each touching every
// cluster. It too must fit in the 5 s budget.
func TestCluster_TimelapseTwoThousandUnderFiveSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	if raceEnabled {
		t.Skip("timing test: the race detector slows the merge loop many times over")
	}
	vectors := timelapse(rand.New(rand.NewPCG(5, 6)), 2000, 768)
	start := time.Now()
	got, err := Cluster(context.Background(), vectors, 0.9)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if len(got) != 1 || len(got[0]) != 2000 {
		t.Fatalf("Cluster = %d clusters; want the whole timelapse in one", len(got))
	}
	if took > 5*time.Second {
		t.Fatalf("a 2,000-frame timelapse took %v; want under 5 s", took)
	}
	t.Logf("2,000-frame timelapse clustered in %v", took)
}

// A cancellation that lands during the merge phase (after the n row checks
// of the matrix build) stops Cluster at its next merge.
func TestCluster_CancelledDuringMerge(t *testing.T) {
	const n = 600
	vectors := timelapse(rand.New(rand.NewPCG(8, 9)), n, 768)
	if got := mustCluster(t, vectors, 0.9); len(got) != 1 {
		t.Fatalf("Cluster = %d clusters; want the timelapse merged into one", len(got))
	}
	ctx := newCountdownCtx(n + 100) // the matrix is built; 99 merges are made
	flipped := make(chan time.Time, 1)
	go func() {
		<-ctx.Done()
		flipped <- time.Now()
	}()
	_, err := Cluster(ctx, vectors, 0.9)
	returned := time.Now()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Cluster = %v; want context.Canceled", err)
	}
	select {
	case at := <-flipped:
		if d := returned.Sub(at); d > time.Second {
			t.Fatalf("Cluster returned %v after the cancellation", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Cluster returned before its context was cancelled")
	}
}

// similarity, which allocates nothing, gives exactly the value Cluster's
// matrix holds (dot64 over the converted vectors).
func TestSimilarity_MatchesClusterDot(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	for range 200 {
		n := 1 + rng.IntN(800)
		a, b := make([]float32, n), make([]float32, n)
		for i := range n {
			a[i], b[i] = rng.Float32()*2-1, rng.Float32()*2-1
		}
		if got, want := similarity(a, b), dot64(toFloat64(a), toFloat64(b)); got != want {
			t.Fatalf("len %d: similarity = %v, dot64 = %v", n, got, want)
		}
	}
	if got := similarity([]float32{1, 2}, []float32{1}); !math.IsInf(got, -1) {
		t.Fatalf("lengths differ: similarity = %v; want -Inf", got)
	}
	nan := float32(math.NaN())
	if got := similarity([]float32{nan, 1}, []float32{1, 1}); !math.IsInf(got, -1) {
		t.Fatalf("NaN: similarity = %v; want -Inf", got)
	}
}
