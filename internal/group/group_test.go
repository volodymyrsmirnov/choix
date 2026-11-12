package group

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// helper: insert a file row with analyzed status and a print, emb padded
// to the print length (nil: a stamped NULL print).
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
	if err := s.AISignals().UpsertPrint(id, pad(emb), testPrintID); err != nil {
		t.Fatalf("UpsertPrint: %v", err)
	}
	return id
}

// testPrintID stamps the prints the tests store; newGrouper uses it.
const testPrintID = "test-print/v1"

// pad returns v padded with zeros to platform.FeaturePrintLen, the only
// length the store keeps: norms and dot products are unchanged. nil stays
// nil (a stamped NULL print).
func pad(v []float32) []float32 {
	if v == nil {
		return nil
	}
	out := make([]float32, platform.FeaturePrintLen)
	copy(out, v)
	return out
}

// newGrouper is NewGrouper using the prints stamped testPrintID.
func newGrouper(s *store.Store, bucketSec int64, threshold float64, crossDevice bool) *Grouper {
	g := NewGrouper(s, bucketSec, threshold, crossDevice)
	g.UsePrints(testPrintID)
	return g
}

func TestRebuildAllCreatesClusters(t *testing.T) {
	s := store.NewTestStore(t) // exported helper from store package
	g := newGrouper(s, 600, 0.92, false)

	// Two near-identical, one orthogonal — same device, same time bucket.
	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	c := normalize([]float32{0.0, 0.0, 1.0, 0.0})
	id1 := insertAnalyzed(t, s, "a.jpg", "Fuji#A", 600, a)
	id2 := insertAnalyzed(t, s, "b.jpg", "Fuji#A", 700, b)
	id3 := insertAnalyzed(t, s, "c.jpg", "Fuji#A", 800, c)

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	clusters, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("clusters: got %d want 2", len(clusters))
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
// (the state that exists when the file was analyzed while visual grouping
// was off, so no feature print was made for it).
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

// When no prints exist for a bucket — the realistic state when the files
// were analyzed with visual grouping off — the bucket should produce one cluster, not N
// singletons. Time proximity already does the grouping work; without a
// visual signal saying these are different scenes we trust the bucket.
func TestRebuildAllGroupsEmbeddinglessFilesAsOne(t *testing.T) {
	s := store.NewTestStore(t)
	g := newGrouper(s, 600, 0.92, false)

	for i := 0; i < 5; i++ {
		insertAnalyzedNoEmbedding(t, s, fmt.Sprintf("img%d.heic", i), "Apple iPhone#X", int64(700+i*10))
	}

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	clusters, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
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
func TestRebuildAllMixedEmbeddingFallback(t *testing.T) {
	s := store.NewTestStore(t)
	g := newGrouper(s, 600, 0.92, false)

	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 1000, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 1010, b)
	insertAnalyzedNoEmbedding(t, s, "c.heic", "Fuji#A", 1020)
	insertAnalyzedNoEmbedding(t, s, "d.heic", "Fuji#A", 1030)
	insertAnalyzedNoEmbedding(t, s, "e.heic", "Fuji#A", 1040)

	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	clusters, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
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

func TestRebuildAllIsIdempotent(t *testing.T) {
	s := store.NewTestStore(t)
	g := newGrouper(s, 600, 0.92, false)

	a := normalize([]float32{1.0, 0.0, 0.0, 0.0})
	b := normalize([]float32{0.99, 0.05, 0.0, 0.0})
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 1000, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 1100, b)

	for i := 0; i < 3; i++ {
		if err := g.RebuildAll(context.Background()); err != nil {
			t.Fatalf("RebuildAll pass %d: %v", i, err)
		}
	}
	clusters, _ := s.Clusters().ListAll()
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
	g := newGrouper(s, 600, 0.92, false)

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
	g := newGrouper(s, 600, 0.92, false)

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
		g := newGrouper(s, 600, 0.92, false)
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
		g := newGrouper(s, 600, 0.92, true)
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

	gOff := newGrouper(s, 600, 0.92, false)
	gOn := newGrouper(s, 600, 0.92, true)

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
	g := newGrouper(s, 600, 0.92, true)

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
	g := newGrouper(s, 600, 0.92, true)

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
			if err != nil || sig.Print == nil {
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

func TestGrouper_WithoutEmbeddingsIsTimeOnly(t *testing.T) {
	s := store.NewTestStore(t)
	// Cosine similarity of a and b is 0.1: visually different scenes.
	a := []float32{1, 0}
	b := []float32{0.1, float32(math.Sqrt(0.99))}
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 1000, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 1005, b)

	clusterCount := func(t *testing.T, g *Grouper) int {
		t.Helper()
		if err := g.RebuildAll(context.Background()); err != nil {
			t.Fatalf("RebuildAll: %v", err)
		}
		all, err := s.Clusters().ListAll()
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		return len(all)
	}

	g := newGrouper(s, 600, 0.92, false)
	if got := clusterCount(t, g); got != 2 {
		t.Fatalf("embeddings on (default): %d clusters, want 2", got)
	}

	g.UsePrints("")
	if got := clusterCount(t, g); got != 1 {
		t.Fatalf("embeddings off: %d clusters, want 1 (time-only)", got)
	}

	// Cross-device mode needs embeddings; with them off it must fall back
	// to the same time-only result.
	gx := newGrouper(s, 600, 0.92, true)
	gx.UsePrints("")
	if got := clusterCount(t, gx); got != 1 {
		t.Fatalf("cross-device, embeddings off: %d clusters, want 1", got)
	}

	g.UsePrints(testPrintID)
	if got := clusterCount(t, g); got != 2 {
		t.Fatalf("embeddings back on: %d clusters, want 2", got)
	}

}

// insertBulk inserts n analyzed files of device dev in one transaction, the
// i-th captured at captured(i) and carrying the print emb (padded), and returns
// their ids in insertion order.
func insertBulk(t *testing.T, s *store.Store, dev string, n int, captured func(i int) int64, emb []float32) []int64 {
	t.Helper()
	blob := make([]byte, 4*platform.FeaturePrintLen)
	for i, v := range emb {
		binary.LittleEndian.PutUint32(blob[4*i:], math.Float32bits(v))
	}
	ctx := context.Background()
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ids := make([]int64, n)
	for i := range n {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO files (path, size, mtime, content_hash, kind, format, device_key, captured_at, scan_status)
			 VALUES (?, 1, 1, ?, 'photo', 'jpeg', ?, ?, 'analyzed')`,
			fmt.Sprintf("bulk/%s/%05d.jpg", dev, i), fmt.Sprintf("hash-%s-%d", dev, i), dev, captured(i))
		if err != nil {
			t.Fatal(err)
		}
		if ids[i], err = res.LastInsertId(); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ai_signals (file_id, vision_print, vision_print_id) VALUES (?, ?, ?)`, ids[i], blob, testPrintID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// clusterSizes returns the sizes of every cluster, largest first, and the
// members of each cluster keyed by its smallest member.
func clusterSizes(t *testing.T, s *store.Store) []int {
	t.Helper()
	all, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, c := range all {
		ids, err := s.ClusterMembers().ListByCluster(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(ids))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	return sizes
}

// A time bucket of more than MaxClusterWindow printed files is clustered in
// consecutive windows: 4,001 identical frames make a cluster of 4,000 and
// one of 1.
func TestCluster_LargeBucketIsWindowed(t *testing.T) {
	s := store.NewTestStore(t)
	v := normalize([]float32{1, 2, 3, 4})
	insertBulk(t, s, "Cam#1", MaxClusterWindow+1, func(i int) int64 { return 1000 + int64(i)/10 }, v)
	g := newGrouper(s, 600, 0.9, false)
	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
	if got, want := clusterSizes(t, s), []int{MaxClusterWindow, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cluster sizes = %v; want %v", got, want)
	}
}

// countdownCtx is a context that is cancelled by the n-th call of Err:
// cancellation lands at a chosen check, wherever the clock is.
type countdownCtx struct {
	context.Context
	mu   sync.Mutex
	left int
	done chan struct{}
}

func newCountdownCtx(n int) *countdownCtx {
	return &countdownCtx{Context: context.Background(), left: n, done: make(chan struct{})}
}

func (c *countdownCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.left > 0 {
		c.left--
		if c.left > 0 {
			return nil
		}
		close(c.done)
	}
	return context.Canceled
}

func (c *countdownCtx) Done() <-chan struct{} { return c.done }

// clusterSnapshot renders the clusters and their members.
func clusterSnapshot(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	rows, err := s.DB().QueryContext(ctx, `SELECT id, device_key, IFNULL(time_bucket, -1) FROM clusters ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, bucket int64
		var dev string
		if err := rows.Scan(&id, &dev, &bucket); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "c %d %s %d\n", id, dev, bucket)
	}
	_ = rows.Close()
	rows, err = s.DB().QueryContext(ctx, `SELECT cluster_id, file_id FROM cluster_members ORDER BY cluster_id, file_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c, f int64
		if err := rows.Scan(&c, &f); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "m %d %d\n", c, f)
	}
	_ = rows.Close()
	return b.String()
}

// Review Focus 5: a rebuild cancelled while it clusters returns the
// context's error and leaves the clusters as they were, on every path.
func TestRebuild_CancelledKeepsClusters(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	build := func(t *testing.T) *store.Store {
		s := store.NewTestStore(t)
		for i := range 60 {
			dev := "Cam#1"
			if i%2 == 1 {
				dev = "Cam#2"
			}
			insertAnalyzed(t, s, fmt.Sprintf("f%02d.jpg", i), dev, int64(1000+i), randomUnit(r, 8))
		}
		// The clusters of an earlier rebuild, per device and per bucket.
		if err := newGrouper(s, 600, 0.2, false).RebuildAll(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, tc := range []struct {
		name    string
		cross   bool
		checks  int // the check that cancels
		rebuild func(g *Grouper, ctx context.Context) error
	}{
		{"per device", false, 3, func(g *Grouper, ctx context.Context) error { return g.RebuildAll(ctx) }},
		// The skew estimate is the first thing the cross-device rebuild does.
		{"cross device, in EstimateSkew", true, 1, func(g *Grouper, ctx context.Context) error { return g.RebuildAll(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := build(t)
			before := clusterSnapshot(t, s)
			g := newGrouper(s, 600, -0.9, tc.cross)
			if err := tc.rebuild(g, newCountdownCtx(tc.checks)); !errors.Is(err, context.Canceled) {
				t.Fatalf("rebuild = %v; want context.Canceled", err)
			}
			if after := clusterSnapshot(t, s); after != before {
				t.Fatalf("clusters changed by a cancelled rebuild:\nbefore\n%s\nafter\n%s", before, after)
			}
			// Not cancelled, the same rebuild changes them.
			if err := tc.rebuild(g, context.Background()); err != nil {
				t.Fatal(err)
			}
			if after := clusterSnapshot(t, s); after == before {
				t.Fatal("the rebuild changed nothing; the test proves nothing")
			}
		})
	}
}

// Only prints stamped with the grouper's print id count: a print made by
// another method (an earlier build's) is treated as no print, so its file
// goes to the bucket's fallback cluster.
func TestRebuildAll_IgnoresOtherPrintID(t *testing.T) {
	s := store.NewTestStore(t)
	a := normalize([]float32{1, 0, 0, 0})
	c := normalize([]float32{0, 0, 1, 0})
	insertAnalyzed(t, s, "a.jpg", "Fuji#A", 600, a)
	insertAnalyzed(t, s, "b.jpg", "Fuji#A", 650, c)
	old := insertAnalyzedNoEmbedding(t, s, "old.jpg", "Fuji#A", 700)
	if err := s.AISignals().UpsertPrint(old, pad(a), "older-print/v0"); err != nil {
		t.Fatal(err)
	}
	if err := newGrouper(s, 600, 0.9, false).RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
	// a and c are apart; the old print does not join a, it is the
	// bucket's fallback cluster on its own.
	if got, want := clusterSizes(t, s), []int{1, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cluster sizes = %v; want %v", got, want)
	}
	// A grouper with no print id clusters by time alone.
	if err := NewGrouper(s, 600, 0.9, false).RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
	if got, want := clusterSizes(t, s), []int{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("time-only cluster sizes = %v; want %v", got, want)
	}
}
