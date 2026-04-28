package group

import (
	"math"
	"reflect"
	"sort"
	"testing"
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

func TestClusterSinglesAreOwnCluster(t *testing.T) {
	embs := map[int64][]float32{
		1: normalize([]float32{1, 0, 0, 0}),
		2: normalize([]float32{0, 1, 0, 0}),
		3: normalize([]float32{0, 0, 1, 0}),
	}
	got := Cluster(embs, 0.92)
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
	got := Cluster(embs, 0.92)
	// Sort each cluster and the outer list for stable comparison.
	for i := range got {
		sort.Slice(got[i], func(a, b int) bool { return got[i][a] < got[i][b] })
	}
	sort.Slice(got, func(a, b int) bool { return got[a][0] < got[b][0] })

	want := [][]int64{{1, 2}, {3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Cluster: got %v want %v", got, want)
	}
}

func TestClusterSingleLinkChain(t *testing.T) {
	// 1~2 and 2~3 above threshold; 1~3 below. Single-link should still merge all three.
	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.95, 0.31, 0.0, 0.0}) // cos(a,b) ~ 0.95
	c := normalize([]float32{0.80, 0.60, 0.0, 0.0}) // cos(b,c) ~ 0.95, cos(a,c) ~ 0.80
	embs := map[int64][]float32{1: a, 2: b, 3: c}

	got := Cluster(embs, 0.92)
	if len(got) != 1 {
		t.Fatalf("single-link should merge chain into 1 cluster, got %d (%v)", len(got), got)
	}
	if len(got[0]) != 3 {
		t.Fatalf("cluster size = %d, want 3", len(got[0]))
	}
}

func TestClusterBelowThresholdStaysSplit(t *testing.T) {
	// Cosine ~ 0.85 — below 0.92 threshold.
	embs := map[int64][]float32{
		1: normalize([]float32{1.0, 0.0, 0.0, 0.0}),
		2: normalize([]float32{0.85, 0.527, 0.0, 0.0}),
	}
	got := Cluster(embs, 0.92)
	if len(got) != 2 {
		t.Errorf("expected 2 clusters below threshold, got %d (%v)", len(got), got)
	}
}
