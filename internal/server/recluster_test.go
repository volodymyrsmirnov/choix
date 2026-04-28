package server

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// seedAnalyzed inserts a file row with scan_status='analyzed', metadata
// (device + captured_at), and a CLIP embedding. Returns the file id.
func seedAnalyzed(t *testing.T, st *store.Store, path, dev string, captured int64, emb []float32) int64 {
	t.Helper()
	id, err := st.Files().Insert(store.File{
		Path: path, Size: 1, Mtime: 1,
		ContentHash: "h-" + path, Kind: "photo", Format: "jpeg",
		ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if err := st.Files().UpdateMetadata(store.File{
		ID:         id,
		DeviceKey:  sql.NullString{String: dev, Valid: true},
		CapturedAt: sql.NullInt64{Int64: captured, Valid: true},
	}); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if err := st.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if err := st.AISignals().Upsert(store.AISignals{FileID: id, Embedding: emb}); err != nil {
		t.Fatalf("upsert ai_signals: %v", err)
	}
	return id
}

func l2norm(v []float32) []float32 {
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

// TestSettingsSimilarityChangesClusterContents reproduces the user's
// reported bug: moving the similarity slider should change cluster
// composition. We seed three analyzed files in the same device + bucket —
// two near-identical (cosine ~0.99) and one orthogonal — then drive
// /api/settings with two thresholds and assert the cluster member sets
// differ.
func TestSettingsSimilarityChangesClusterContents(t *testing.T) {
	env := newTestServer(t)

	a := l2norm([]float32{1.0, 0.0, 0.0, 0.0})
	b := l2norm([]float32{0.99, 0.05, 0.0, 0.0})
	c := l2norm([]float32{0.0, 0.0, 1.0, 0.0})
	id1 := seedAnalyzed(t, env.store, "a.jpg", "Cam#A", 1000, a)
	id2 := seedAnalyzed(t, env.store, "b.jpg", "Cam#A", 1100, b)
	id3 := seedAnalyzed(t, env.store, "c.jpg", "Cam#A", 1200, c)

	// Loose threshold (below cosine(a,b) ≈ 0.99): a and b merge, c stays
	// alone. Two clusters total.
	resp := postJSON(t, env, "/api/settings", map[string]any{"similarity": 0.5})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loose settings post: status %d", resp.StatusCode)
	}
	loose := readClusters(t, env)
	if len(loose) != 2 {
		t.Fatalf("loose threshold: got %d clusters, want 2 (members=%v)", len(loose), loose)
	}
	if !sameSet(loose[0], []int64{id1, id2}) || !sameSet(loose[1], []int64{id3}) {
		t.Fatalf("loose threshold: clusters %v, want [[%d %d] [%d]]", loose, id1, id2, id3)
	}

	// Strict threshold (above cosine(a,b) ≈ 0.99): every file lands in
	// its own cluster. Three clusters total.
	resp = postJSON(t, env, "/api/settings", map[string]any{"similarity": 0.999})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("strict settings post: status %d", resp.StatusCode)
	}
	strict := readClusters(t, env)
	if len(strict) != 3 {
		t.Fatalf("strict threshold: got %d clusters, want 3 (members=%v)", len(strict), strict)
	}
}

// readClusters fetches /api/library and returns each cluster's member id
// list, sorted ascending. Outer slice is sorted by smallest member id.
func readClusters(t *testing.T, env *testEnv) [][]int64 {
	t.Helper()
	resp := env.get("/api/library")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("library get: status %d", resp.StatusCode)
	}
	var body struct {
		Clusters []struct {
			Members []struct {
				FileID int64 `json:"file_id"`
			} `json:"members"`
		} `json:"clusters"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	out := make([][]int64, 0, len(body.Clusters))
	for _, c := range body.Clusters {
		ids := make([]int64, 0, len(c.Members))
		for _, m := range c.Members {
			ids = append(ids, m.FileID)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		out = append(out, ids)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) == 0 || len(out[j]) == 0 {
			return len(out[i]) < len(out[j])
		}
		return out[i][0] < out[j][0]
	})
	return out
}

func sameSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
