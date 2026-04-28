package group

import "sort"

// Cluster groups file ids by single-link agglomerative clustering: any two ids whose
// CLIP-embedding cosine similarity exceeds threshold end up in the same cluster.
//
// Embeddings are assumed L2-normalized, so cosine == dot product. The implementation
// is a straightforward O(N^2) pair scan + union-find. Suitable for buckets of <= a few
// thousand files; revisit if profiling shows the need.
//
// Output ordering: cluster member ids ascending; outer slice ordered by smallest member id.
func Cluster(embeds map[int64][]float32, threshold float64) [][]int64 {
	ids := make([]int64, 0, len(embeds))
	for id := range embeds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	parent := make(map[int64]int64, len(ids))
	for _, id := range ids {
		parent[id] = id
	}
	var find func(int64) int64
	find = func(x int64) int64 {
		if parent[x] == x {
			return x
		}
		root := find(parent[x])
		parent[x] = root
		return root
	}
	union := func(a, b int64) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for i := 0; i < len(ids); i++ {
		ei := embeds[ids[i]]
		for j := i + 1; j < len(ids); j++ {
			if cosine(ei, embeds[ids[j]]) > threshold {
				union(ids[i], ids[j])
			}
		}
	}

	groups := make(map[int64][]int64)
	for _, id := range ids {
		r := find(id)
		groups[r] = append(groups[r], id)
	}

	out := make([][]int64, 0, len(groups))
	for _, members := range groups {
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		out = append(out, members)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// cosine returns the dot product of two equal-length vectors. Caller guarantees L2-normalized
// inputs (CLIP outputs are normalized in Phase 6); we therefore skip the magnitude divide.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
