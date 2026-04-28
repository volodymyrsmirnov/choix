package group

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// helper: insert a file row with analyzed status + AI signal carrying the supplied embedding.
func insertAnalyzed(t *testing.T, s *store.Store, path, dev string, captured int64, emb []float32) int64 {
	t.Helper()
	id, err := s.Files().Insert(store.File{
		Path:        path,
		Size:        1,
		Mtime:       1,
		ContentHash: path,
		Kind:        "photo",
		Format:      "jpeg",
		ScanStatus:  "analyzed",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}
	// Set device_key and captured_at via UpdateMetadata, then restore scan_status.
	if err := s.Files().UpdateMetadata(store.File{
		ID:         id,
		DeviceKey:  sql.NullString{String: dev, Valid: true},
		CapturedAt: sql.NullInt64{Int64: captured, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	// UpdateMetadata sets scan_status='metadata'; reset to 'analyzed'.
	if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := s.AISignals().Upsert(store.AISignals{FileID: id, Embedding: emb}); err != nil {
		t.Fatalf("Upsert ai_signals: %v", err)
	}
	return id
}

func TestRebuildBucketCreatesClusters(t *testing.T) {
	s := store.NewTestStore(t) // exported helper from store package
	g := NewGrouper(s, 600, 0.92, false)

	// Two near-identical, one orthogonal — same device, same time bucket.
	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	c := normalize([]float32{0.0, 0.0, 1.0, 0.0})
	id1 := insertAnalyzed(t, s, "a.jpg", "Fuji#A", 600, a)
	id2 := insertAnalyzed(t, s, "b.jpg", "Fuji#A", 700, b)
	id3 := insertAnalyzed(t, s, "c.jpg", "Fuji#A", 800, c)

	bucket := sql.NullInt64{Int64: 600, Valid: true}
	if err := g.RebuildBucket(context.Background(), "Fuji#A", bucket); err != nil {
		t.Fatalf("RebuildBucket: %v", err)
	}

	clusters, err := s.Clusters().ListByBucket("Fuji#A", bucket)
	if err != nil {
		t.Fatalf("ListByBucket: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("clusters in bucket: got %d want 2", len(clusters))
	}

	// Verify membership: one cluster has {id1, id2}, the other has {id3}.
	memberSets := make([][]int64, 0, len(clusters))
	for _, c := range clusters {
		ids, err := s.ClusterMembers().ListByCluster(c.ID)
		if err != nil {
			t.Fatalf("ListByCluster: %v", err)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		memberSets = append(memberSets, ids)
	}
	sort.Slice(memberSets, func(i, j int) bool { return len(memberSets[i]) < len(memberSets[j]) })

	want := [][]int64{{id3}, {id1, id2}}
	for i, set := range memberSets {
		if len(set) != len(want[i]) {
			t.Fatalf("cluster %d: got %v want %v", i, set, want[i])
		}
		for j, id := range set {
			if id != want[i][j] {
				t.Errorf("cluster %d member %d: got %d want %d", i, j, id, want[i][j])
			}
		}
	}
}

// insertAnalyzedNoEmbedding inserts an analyzed file with no AI signals row
// (the state that exists when no CLIP model is installed and the analyzer
// produced no embedding for this file).
func insertAnalyzedNoEmbedding(t *testing.T, s *store.Store, path, dev string, captured int64) int64 {
	t.Helper()
	id, err := s.Files().Insert(store.File{
		Path:        path,
		Size:        1,
		Mtime:       1,
		ContentHash: path,
		Kind:        "photo",
		Format:      "heic",
		ScanStatus:  "analyzed",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}
	if err := s.Files().UpdateMetadata(store.File{
		ID:         id,
		DeviceKey:  sql.NullString{String: dev, Valid: true},
		CapturedAt: sql.NullInt64{Int64: captured, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	return id
}

// When no embeddings exist for a bucket — the realistic state when the CLIP
// model isn't installed — the bucket should produce one cluster, not N
// singletons. Time proximity already does the grouping work; without a
// visual signal saying these are different scenes we trust the bucket.
func TestRebuildBucketGroupsEmbeddinglessFilesAsOne(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, false)

	for i := 0; i < 5; i++ {
		insertAnalyzedNoEmbedding(t, s, fmt.Sprintf("img%d.heic", i), "Apple iPhone#X", int64(700+i*10))
	}

	bucket := sql.NullInt64{Int64: 600, Valid: true}
	if err := g.RebuildBucket(context.Background(), "Apple iPhone#X", bucket); err != nil {
		t.Fatalf("RebuildBucket: %v", err)
	}

	clusters, err := s.Clusters().ListByBucket("Apple iPhone#X", bucket)
	if err != nil {
		t.Fatalf("ListByBucket: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 fallback cluster for bucket with no embeddings, got %d", len(clusters))
	}
	members, err := s.ClusterMembers().ListByCluster(clusters[0].ID)
	if err != nil {
		t.Fatalf("ListByCluster: %v", err)
	}
	if len(members) != 5 {
		t.Errorf("fallback cluster should hold all 5 files; got %d", len(members))
	}
}

// Mixed bucket: some files have embeddings, others don't. Embedding-bearing
// files cluster by similarity; embeddingless files merge into a single
// fallback cluster (not one per file).
func TestRebuildBucketMixedEmbeddingFallback(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, false)

	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 1000, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 1010, b)
	insertAnalyzedNoEmbedding(t, s, "c.heic", "Fuji#A", 1020)
	insertAnalyzedNoEmbedding(t, s, "d.heic", "Fuji#A", 1030)
	insertAnalyzedNoEmbedding(t, s, "e.heic", "Fuji#A", 1040)

	bucket := sql.NullInt64{Int64: 600, Valid: true}
	if err := g.RebuildBucket(context.Background(), "Fuji#A", bucket); err != nil {
		t.Fatalf("RebuildBucket: %v", err)
	}

	clusters, err := s.Clusters().ListByBucket("Fuji#A", bucket)
	if err != nil {
		t.Fatalf("ListByBucket: %v", err)
	}
	// Expected: one cluster {a,b} + one fallback cluster {c,d,e} = 2 clusters.
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters (similarity + fallback), got %d", len(clusters))
	}
	sizes := []int{}
	for _, c := range clusters {
		ids, err := s.ClusterMembers().ListByCluster(c.ID)
		if err != nil {
			t.Fatalf("ListByCluster: %v", err)
		}
		sizes = append(sizes, len(ids))
	}
	sort.Ints(sizes)
	if sizes[0] != 2 || sizes[1] != 3 {
		t.Errorf("cluster sizes = %v, want [2 3]", sizes)
	}
}

func TestRebuildBucketIsIdempotent(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, false)

	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 1000, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 1100, b)

	bucket := sql.NullInt64{Int64: 600, Valid: true}
	for i := 0; i < 3; i++ {
		if err := g.RebuildBucket(context.Background(), "Fuji#A", bucket); err != nil {
			t.Fatalf("RebuildBucket pass %d: %v", i, err)
		}
	}
	clusters, _ := s.Clusters().ListByBucket("Fuji#A", bucket)
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster after repeated rebuilds, got %d", len(clusters))
	}
}

// Two shots taken within bucketSec of each other must always end up in
// the same cluster, regardless of where they fall on the absolute clock.
// The previous floor-aligned bucketing would split a pair like 14:34:59
// + 14:35:01 across the 14:30/14:40 boundary even with a 600s window —
// gap-based bucketing must not have that artifact.
func TestRebuildAllUsesGapBasedBuckets(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, false)

	// Use timestamps that straddle a 600s clock boundary at 14:35:00
	// (52500). 14:34:59 = 52499, 14:35:01 = 52501. Gap = 2s, well under
	// the 600s window — must be one cluster.
	insertAnalyzedNoEmbedding(t, s, "a.heic", "Apple iPhone#X", 52499)
	insertAnalyzedNoEmbedding(t, s, "b.heic", "Apple iPhone#X", 52501)
	// And a third shot 800s later (gap > 600) — that one starts a new bucket.
	insertAnalyzedNoEmbedding(t, s, "c.heic", "Apple iPhone#X", 53301)

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	all, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 clusters (one for the pair, one for the lone shot); got %d", len(all))
	}
	sizes := []int{}
	for _, c := range all {
		ids, _ := s.ClusterMembers().ListByCluster(c.ID)
		sizes = append(sizes, len(ids))
	}
	sort.Ints(sizes)
	if sizes[0] != 1 || sizes[1] != 2 {
		t.Errorf("cluster sizes = %v, want [1 2]", sizes)
	}
}

func TestRebuildAllRepopulatesEveryBucket(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, false)

	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	c := normalize([]float32{0.0, 0.0, 1.0, 0.0})

	// Bucket 1: Fuji, time 600 — two similar files.
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 1000, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 1100, b)
	// Bucket 2: Fuji, time 1800 — one file.
	insertAnalyzed(t, s, "c.jpg", "Fuji#A", 2000, c)
	// Bucket 3: Sony, time 600.
	insertAnalyzed(t, s, "d.jpg", "Sony#B", 1000, a)

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	all, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	// 1 (Fuji 600) + 1 (Fuji 1800) + 1 (Sony 600) = 3 clusters.
	if len(all) != 3 {
		t.Errorf("clusters after RebuildAll: got %d want 3", len(all))
	}
}

// Cross-device merging: two cameras shoot the same scene seconds apart on
// the canonical clock. With cross-device on, they collapse into one
// cluster tagged with MergedDeviceKey. With cross-device off, they stay
// separate.
func TestRebuildAllCrossDeviceMergesIdenticalScenes(t *testing.T) {
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})

	build := func(s *store.Store) {
		insertAnalyzed(t, s, "ip1.jpg", "iPhone#A", 1000, v1)
		insertAnalyzed(t, s, "ip2.jpg", "iPhone#A", 1100, v2)
		insertAnalyzed(t, s, "ip3.jpg", "iPhone#A", 1200, v1)
		insertAnalyzed(t, s, "ip4.jpg", "iPhone#A", 1300, v2)
		insertAnalyzed(t, s, "fuji1.jpg", "X-T5#B", 1000+60, v1)
		insertAnalyzed(t, s, "fuji2.jpg", "X-T5#B", 1100+60, v2)
		insertAnalyzed(t, s, "fuji3.jpg", "X-T5#B", 1200+60, v1)
	}

	// Cross-device OFF: each device walks its own timeline. Embeds v1 and
	// v2 are orthogonal so the per-device buckets each split into 2
	// visual sub-clusters → 4 total clusters.
	{
		s := store.NewTestStore(t)
		build(s)
		g := NewGrouper(s, 600, 0.92, false)
		if err := g.RebuildAll(context.Background()); err != nil {
			t.Fatalf("RebuildAll (off): %v", err)
		}
		all, err := s.Clusters().ListAll()
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(all) != 4 {
			t.Errorf("cross-device off: got %d clusters, want 4", len(all))
		}
		for _, c := range all {
			if c.DeviceKey == MergedDeviceKey {
				t.Errorf("cross-device off, but cluster %d has merged key", c.ID)
			}
		}
	}

	// Cross-device ON: skew detection finds offset=60 for X-T5; merged
	// timeline produces 2 visual clusters (one per scene), each spanning
	// both devices and tagged MergedDeviceKey.
	{
		s := store.NewTestStore(t)
		build(s)
		g := NewGrouper(s, 600, 0.92, true)
		if err := g.RebuildAll(context.Background()); err != nil {
			t.Fatalf("RebuildAll (on): %v", err)
		}
		all, err := s.Clusters().ListAll()
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("cross-device on: got %d clusters, want 2", len(all))
		}
		for _, c := range all {
			if c.DeviceKey != MergedDeviceKey {
				t.Errorf("cluster %d: device_key=%q, want %q", c.ID, c.DeviceKey, MergedDeviceKey)
			}
			ids, _ := s.ClusterMembers().ListByCluster(c.ID)
			devs := map[string]struct{}{}
			for _, id := range ids {
				f, err := s.Files().GetByID(id)
				if err != nil {
					t.Fatalf("GetByID: %v", err)
				}
				devs[f.DeviceKey.String] = struct{}{}
			}
			if len(devs) != 2 {
				t.Errorf("cluster %d: %d devices represented, want 2", c.ID, len(devs))
			}
		}
	}
}

// Toggling cross-device on → off → on on the SAME store must produce the
// expected cluster shape each time. WriteClusters replaces the clusters
// + cluster_members tables atomically, so the transition shouldn't leave
// stale rows behind, but we prove it here.
func TestRebuildAllCrossDeviceToggleCycle(t *testing.T) {
	s := store.NewTestStore(t)
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})

	insertAnalyzed(t, s, "ip1.jpg", "iPhone#A", 1000, v1)
	insertAnalyzed(t, s, "ip2.jpg", "iPhone#A", 1100, v2)
	insertAnalyzed(t, s, "ip3.jpg", "iPhone#A", 1200, v1)
	insertAnalyzed(t, s, "ip4.jpg", "iPhone#A", 1300, v2)
	insertAnalyzed(t, s, "fuji1.jpg", "X-T5#B", 1000+60, v1)
	insertAnalyzed(t, s, "fuji2.jpg", "X-T5#B", 1100+60, v2)
	insertAnalyzed(t, s, "fuji3.jpg", "X-T5#B", 1200+60, v1)

	gOff := NewGrouper(s, 600, 0.92, false)
	gOn := NewGrouper(s, 600, 0.92, true)

	steps := []struct {
		g    *Grouper
		want int
		mode string
	}{
		{gOff, 4, "off"}, {gOn, 2, "on"}, {gOff, 4, "off again"}, {gOn, 2, "on again"},
	}
	for _, step := range steps {
		if err := step.g.RebuildAll(context.Background()); err != nil {
			t.Fatalf("RebuildAll (%s): %v", step.mode, err)
		}
		all, err := s.Clusters().ListAll()
		if err != nil {
			t.Fatalf("ListAll (%s): %v", step.mode, err)
		}
		if len(all) != step.want {
			t.Errorf("toggle %s: got %d clusters, want %d", step.mode, len(all), step.want)
		}
	}
}

// Cross-device with no embeddings on any file must short-circuit to the
// per-device path: merging cameras on time alone with no visual evidence
// would mix unrelated scenes.
func TestRebuildAllCrossDeviceNoEmbeddingsShortCircuits(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, true)

	insertAnalyzedNoEmbedding(t, s, "ip.heic", "iPhone#A", 1000)
	insertAnalyzedNoEmbedding(t, s, "fuji.heic", "X-T5#B", 1010)

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
	all, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 per-device clusters, got %d", len(all))
	}
	for _, c := range all {
		if c.DeviceKey == MergedDeviceKey {
			t.Errorf("no-embeddings short-circuit failed: cluster %d is merged", c.ID)
		}
	}
}

// In cross-device mode, files inside a merged time bucket that have no
// embedding must NOT merge across devices — they fall back per device.
func TestRebuildAllCrossDeviceNoEmbeddingsPerBucketStaySeparate(t *testing.T) {
	s := store.NewTestStore(t)
	g := NewGrouper(s, 600, 0.92, true)

	v := normalize([]float32{1, 0, 0, 0})
	for i := 0; i < 3; i++ {
		insertAnalyzed(t, s, fmt.Sprintf("ipa%d.jpg", i), "iPhone#A", int64(2000+i*10), v)
		insertAnalyzed(t, s, fmt.Sprintf("fja%d.jpg", i), "X-T5#B", int64(2000+i*10), v)
	}
	insertAnalyzedNoEmbedding(t, s, "ipnoemb.heic", "iPhone#A", 2100)
	insertAnalyzedNoEmbedding(t, s, "fjnoemb.heic", "X-T5#B", 2100)

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
	all, _ := s.Clusters().ListAll()
	merged := 0
	noembPerDev := map[string]int{}
	for _, c := range all {
		ids, _ := s.ClusterMembers().ListByCluster(c.ID)
		hasNoEmb := false
		for _, id := range ids {
			sig, err := s.AISignals().GetByFileID(id)
			if err != nil || sig.Embedding == nil {
				hasNoEmb = true
				break
			}
		}
		if hasNoEmb {
			noembPerDev[c.DeviceKey]++
			if c.DeviceKey == MergedDeviceKey {
				t.Errorf("noEmb cluster tagged merged; want per-device key")
			}
			continue
		}
		if c.DeviceKey == MergedDeviceKey {
			merged++
		}
	}
	if merged < 1 {
		t.Errorf("embedded scene didn't form a merged cluster; clusters=%v", all)
	}
	if len(noembPerDev) != 2 {
		t.Errorf("noEmb fallback should yield one cluster per device; got %v", noembPerDev)
	}
}

// TestWriteBucketIsTransactional verifies that WriteBucket leaves the bucket
// in a consistent state (fully written) and that the operation is idempotent
// (calling it twice with the same defs yields the same result, not doubled rows).
func TestWriteBucketIsTransactional(t *testing.T) {
	s := store.NewTestStore(t)

	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.0, 1.0, 0.0, 0.0})
	id1 := insertAnalyzed(t, s, "x1.jpg", "Fuji#A", 1000, a)
	id2 := insertAnalyzed(t, s, "x2.jpg", "Fuji#A", 1100, b)

	bucket := sql.NullInt64{Int64: 600, Valid: true}
	defs := []store.ClusterDef{
		{DeviceKey: "Fuji#A", TimeBucket: bucket, Members: []int64{id1}},
		{DeviceKey: "Fuji#A", TimeBucket: bucket, Members: []int64{id2}},
	}

	// First call: creates 2 clusters.
	if err := s.WriteBucket(context.Background(), "Fuji#A", bucket, defs); err != nil {
		t.Fatalf("WriteBucket (first): %v", err)
	}
	clusters, err := s.Clusters().ListByBucket("Fuji#A", bucket)
	if err != nil {
		t.Fatalf("ListByBucket: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("after first WriteBucket: got %d clusters, want 2", len(clusters))
	}

	// Second call with identical defs: old clusters are deleted atomically,
	// new ones inserted. The count must still be 2 (not 4 from doubling).
	if err := s.WriteBucket(context.Background(), "Fuji#A", bucket, defs); err != nil {
		t.Fatalf("WriteBucket (second): %v", err)
	}
	clusters, err = s.Clusters().ListByBucket("Fuji#A", bucket)
	if err != nil {
		t.Fatalf("ListByBucket (second): %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("after second WriteBucket: got %d clusters, want 2 (no doubling)", len(clusters))
	}

	// Passing nil defs clears the bucket entirely.
	if err := s.WriteBucket(context.Background(), "Fuji#A", bucket, nil); err != nil {
		t.Fatalf("WriteBucket (clear): %v", err)
	}
	clusters, err = s.Clusters().ListByBucket("Fuji#A", bucket)
	if err != nil {
		t.Fatalf("ListByBucket (clear): %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("after clear WriteBucket: got %d clusters, want 0", len(clusters))
	}
}
