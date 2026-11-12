package group

import (
	"context"
	"database/sql"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// progressCall is one call of RebuildAllProgress's progress func.
type progressCall struct{ done, total int }

// rebuildWithProgress runs RebuildAllProgress and returns its progress calls.
func rebuildWithProgress(t *testing.T, g *Grouper) []progressCall {
	t.Helper()
	var calls []progressCall
	if err := g.RebuildAllProgress(context.Background(), func(done, total int) {
		calls = append(calls, progressCall{done, total})
	}); err != nil {
		t.Fatalf("RebuildAllProgress: %v", err)
	}
	return calls
}

// checkProgress checks the calls count files clustered out of total: the
// first is 0 of total, done never goes back, total never changes, the last
// is total of total, and there is a step for each of at least steps buckets.
func checkProgress(t *testing.T, calls []progressCall, total, steps int) {
	t.Helper()
	if len(calls) < steps+1 {
		t.Fatalf("progress calls %v; want at least %d (0 of %d, then one per bucket)", calls, steps+1, total)
	}
	if calls[0] != (progressCall{0, total}) {
		t.Errorf("first progress %v; want 0 of %d", calls[0], total)
	}
	for i, c := range calls {
		if c.total != total {
			t.Errorf("progress %d = %v; want total %d", i, c, total)
		}
		if i > 0 && c.done < calls[i-1].done {
			t.Errorf("progress went back: %v after %v", c, calls[i-1])
		}
	}
	if last := calls[len(calls)-1]; last != (progressCall{total, total}) {
		t.Errorf("last progress %v; want %d of %d", last, total, total)
	}
}

func TestRebuildAllProgress_PerDeviceCountsFilesBucketByBucket(t *testing.T) {
	s := store.NewTestStore(t)
	// Three buckets on one device (gaps over 600 s), one on another, and a
	// file without a capture time (its own bucket).
	insertAnalyzedNoEmbedding(t, s, "a1.jpg", "Cam#1", 1000)
	insertAnalyzedNoEmbedding(t, s, "a2.jpg", "Cam#1", 1010)
	insertAnalyzedNoEmbedding(t, s, "a3.jpg", "Cam#1", 5000)
	insertAnalyzedNoEmbedding(t, s, "a4.jpg", "Cam#1", 9000)
	insertAnalyzedNoEmbedding(t, s, "b1.jpg", "Cam#2", 1000)
	id, err := s.Files().Insert(store.File{Path: "n.jpg", Size: 1, Mtime: 1, ContentHash: "n", Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Files().UpdateMetadata(store.File{ID: id, DeviceKey: sql.NullString{String: "Cam#2", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatal(err)
	}

	calls := rebuildWithProgress(t, newGrouper(s, 600, 0.92, false))
	checkProgress(t, calls, 6, 5)
}

func TestRebuildAllProgress_CrossDeviceCountsEveryFile(t *testing.T) {
	s := store.NewTestStore(t)
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	insertAnalyzed(t, s, "ip1.jpg", "iPhone#A", 1000, v1)
	insertAnalyzed(t, s, "ip2.jpg", "iPhone#A", 1100, v2)
	insertAnalyzed(t, s, "ip3.jpg", "iPhone#A", 9000, v1)
	insertAnalyzed(t, s, "fuji1.jpg", "X-T5#B", 1060, v1)
	// Without an embedding: the per-device fallback.
	insertAnalyzedNoEmbedding(t, s, "fuji2.jpg", "X-T5#B", 20000)

	calls := rebuildWithProgress(t, newGrouper(s, 600, 0.92, true))
	checkProgress(t, calls, 5, 3)
}

func TestRebuildAllProgress_EmptyFolder(t *testing.T) {
	s := store.NewTestStore(t)
	calls := rebuildWithProgress(t, newGrouper(s, 600, 0.92, false))
	if len(calls) != 1 || calls[0] != (progressCall{0, 0}) {
		t.Errorf("progress calls %v; want one 0 of 0", calls)
	}
}

func TestRebuildAll_NilProgress(t *testing.T) {
	s := store.NewTestStore(t)
	insertAnalyzedNoEmbedding(t, s, "a.jpg", "Cam#1", 1000)
	g := newGrouper(s, 600, 0.92, false)
	if err := g.RebuildAllProgress(context.Background(), nil); err != nil {
		t.Fatalf("RebuildAllProgress(nil): %v", err)
	}
	if err := g.RebuildAll(context.Background()); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
}
