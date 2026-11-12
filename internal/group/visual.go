package group

import (
	"container/heap"
	"context"
	"math"
	"slices"
)

// Cluster groups ids by complete-link agglomerative clustering of their
// vectors (spec §5.4). It clusters exactly what it is given; windowing a
// large bucket is the caller's job (clusterIDs).
//
// Similarity is the dot product of two vectors (unit vectors: the cosine);
// vectors of different lengths are never similar. Every id starts alone.
// Repeatedly the two clusters with the highest linkage merge, a pair's
// linkage being the lowest similarity between a member of one and a member
// of the other, while that linkage is strictly greater than threshold. A
// tie in linkage goes to the pair whose smaller minimum id is smaller, then
// whose other minimum id is smaller.
//
// Output: members ascending; clusters ordered by smallest member.
//
// It keeps one similarity matrix of len(vectors)² / 2 float64s, and checks
// ctx once per matrix row and once per merge, returning ctx.Err() when it is
// done.
func Cluster(ctx context.Context, vectors map[int64][]float32, threshold float64) ([][]int64, error) {
	ids := make([]int64, 0, len(vectors))
	for id := range vectors {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	n := len(ids)
	if n == 0 {
		return [][]int64{}, nil
	}

	// The vectors as float64, so the matrix loop converts nothing.
	vecs := make([][]float64, n)
	for i, id := range ids {
		vecs[i] = toFloat64(vectors[id])
	}
	m := newLinkMatrix(n)
	for i := range n {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for j := i + 1; j < n; j++ {
			m.set(i, j, dot64(vecs[i], vecs[j]))
		}
	}

	// Clusters are named by their smallest member's index into ids, which
	// sorts like their smallest id: the tie rule compares names.
	c := &completeLink{m: m, active: make([]bool, n), members: make([][]int, n), best: make([]int, n)}
	c.h = linkHeap{pos: make([]int, n), key: make([]linkKey, n)}
	for i := range n {
		c.active[i] = true
		c.members[i] = []int{i}
		c.h.pos[i] = -1
	}
	for i := range n {
		c.refresh(i)
	}

	for c.h.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		k := c.h.items[0]
		if !c.valid(k) {
			c.refresh(k)
			continue
		}
		if !(c.h.key[k].sim > threshold) {
			break
		}
		c.merge(min(k, c.best[k]), max(k, c.best[k]))
	}

	out := make([][]int64, 0, n)
	for i := range n {
		if !c.active[i] {
			continue
		}
		group := make([]int64, len(c.members[i]))
		for j, idx := range c.members[i] {
			group[j] = ids[idx]
		}
		slices.Sort(group)
		out = append(out, group)
	}
	// Clusters are named by their smallest index, so out is already ordered
	// by smallest member.
	return out, nil
}

// similarity is the dot product of a and b as Cluster computes it (the same
// float64 accumulation as dot64 over the converted vectors, so the same
// value), or -Inf when their lengths differ or the product is not a number.
// It allocates nothing.
func similarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return math.Inf(-1)
	}
	var s0, s1, s2, s3 float64
	i := 0
	for ; i+4 <= len(a); i += 4 {
		x, y := a[i:i+4:i+4], b[i:i+4:i+4]
		s0 += float64(x[0]) * float64(y[0])
		s1 += float64(x[1]) * float64(y[1]) //nolint:gosec // G602: x and y have length 4
		s2 += float64(x[2]) * float64(y[2]) //nolint:gosec // G602: x and y have length 4
		s3 += float64(x[3]) * float64(y[3]) //nolint:gosec // G602: x and y have length 4
	}
	for ; i < len(a); i++ {
		s0 += float64(a[i]) * float64(b[i])
	}
	s := (s0 + s1) + (s2 + s3)
	if math.IsNaN(s) {
		return math.Inf(-1)
	}
	return s
}

func toFloat64(v []float32) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x)
	}
	return out
}

// dot64 is the dot product of a and b, or -Inf when their lengths differ or
// the product is not a number (such a pair never merges).
func dot64(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.Inf(-1)
	}
	var s0, s1, s2, s3 float64
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	s := (s0 + s1) + (s2 + s3)
	if math.IsNaN(s) {
		return math.Inf(-1)
	}
	return s
}

// linkMatrix is the upper triangle of a symmetric n×n matrix without its
// diagonal: the linkage between two clusters, named by index.
type linkMatrix struct {
	n int
	v []float64
}

func newLinkMatrix(n int) linkMatrix {
	return linkMatrix{n: n, v: make([]float64, n*(n-1)/2)}
}

func (m linkMatrix) index(i, j int) int {
	if i > j {
		i, j = j, i
	}
	return i*m.n - i*(i+1)/2 + j - i - 1
}

func (m linkMatrix) get(i, j int) float64    { return m.v[m.index(i, j)] }
func (m linkMatrix) set(i, j int, v float64) { m.v[m.index(i, j)] = v }

// linkKey orders candidate merges: higher similarity first, then the pair
// (lo, hi) of cluster names, smaller first.
type linkKey struct {
	sim    float64
	lo, hi int
}

func (a linkKey) better(b linkKey) bool {
	if a.sim != b.sim {
		return a.sim > b.sim
	}
	if a.lo != b.lo {
		return a.lo < b.lo
	}
	return a.hi < b.hi
}

func pairKey(sim float64, i, j int) linkKey {
	return linkKey{sim: sim, lo: min(i, j), hi: max(i, j)}
}

// completeLink is the state of one Cluster call. Each active cluster k is in
// the heap under a key that is never worse than its best merge: best[k] is
// its best partner when the key is exact (valid), and the key is an upper
// bound once a merge lowered the linkage behind it. The heap's top is
// refreshed until it is exact; an exact top is the best merge of all.
type completeLink struct {
	m       linkMatrix
	active  []bool
	members [][]int
	best    []int
	h       linkHeap
}

// valid reports whether k's key is exactly its merge with best[k].
func (c *completeLink) valid(k int) bool {
	b := c.best[k]
	return b >= 0 && c.active[b] && c.h.key[k] == pairKey(c.m.get(k, b), k, b)
}

// refresh finds k's best partner by a scan and puts k in the heap under
// that merge, or takes it out when no other cluster is left.
func (c *completeLink) refresh(k int) {
	bestJ := -1
	var bestKey linkKey
	for j, on := range c.active {
		if !on || j == k {
			continue
		}
		key := pairKey(c.m.get(k, j), k, j)
		if bestJ < 0 || key.better(bestKey) {
			bestJ, bestKey = j, key
		}
	}
	c.best[k] = bestJ
	if bestJ < 0 {
		c.h.remove(k)
		return
	}
	c.h.set(k, bestKey)
}

// merge joins cluster b into cluster a (a < b) and keeps every other key an
// upper bound of its cluster's best merge.
func (c *completeLink) merge(a, b int) {
	c.active[b] = false
	c.h.remove(b)
	c.members[a] = append(c.members[a], c.members[b]...)
	c.members[b] = nil
	for k, on := range c.active {
		if !on || k == a {
			continue
		}
		c.m.set(a, k, math.Min(c.m.get(a, k), c.m.get(b, k)))
	}
	// A cluster k whose best partner was b keeps its key, now an upper bound
	// that is refreshed when it reaches the top: its linkage to a∪b is
	// strictly below the key. With an exact key, b beat a when k was last
	// refreshed, and a wins every tie with b (a < b), so the linkage to a
	// was already strictly lower and linkages never rise; a stale key is
	// above the linkage to b, which the merged linkage does not exceed.
	// A cluster whose best partner was a keeps its key: exact when the
	// linkage to a did not drop, an upper bound otherwise. Every other key
	// is unchanged, and so is the merge behind it.
	c.refresh(a)
}

// linkHeap is a max-heap of cluster names by key, indexed so a cluster's
// key can be changed or removed.
type linkHeap struct {
	items []int
	pos   []int // pos[k]: k's index in items, -1 when absent
	key   []linkKey
}

func (h *linkHeap) Len() int           { return len(h.items) }
func (h *linkHeap) Less(i, j int) bool { return h.key[h.items[i]].better(h.key[h.items[j]]) }
func (h *linkHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i]], h.pos[h.items[j]] = i, j
}

func (h *linkHeap) Push(x any) {
	k := x.(int)
	h.pos[k] = len(h.items)
	h.items = append(h.items, k)
}

func (h *linkHeap) Pop() any {
	k := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	h.pos[k] = -1
	return k
}

// set puts k in the heap under key, or moves it there.
func (h *linkHeap) set(k int, key linkKey) {
	h.key[k] = key
	if h.pos[k] < 0 {
		heap.Push(h, k)
		return
	}
	heap.Fix(h, h.pos[k])
}

// remove takes k out of the heap, if it is in.
func (h *linkHeap) remove(k int) {
	if h.pos[k] >= 0 {
		heap.Remove(h, h.pos[k])
	}
}
