package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// setHideRejected toggles the machine-wide "hide rejected photos" setting
// via the same path the UI uses, then waits for the live config to
// reload. Tests must use this rather than poking KV directly because the
// library handler now reads from liveCfg.
func setHideRejected(t *testing.T, env *testEnv, on bool) {
	t.Helper()
	resp := postJSON(t, env, "/api/settings", map[string]any{
		"hide_rejected_photos": on,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings POST status %d", resp.StatusCode)
	}
}

// setRejected marks the given file as rejected via the store directly (the
// test seeds a pick row without going through the picks package, which tries
// to copy the file on disk).
func setRejected(t *testing.T, st *store.Store, fileID int64) {
	t.Helper()
	if err := st.Picks().Upsert(store.Pick{
		FileID:   fileID,
		State:    "rejected",
		PickedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("upsert reject: %v", err)
	}
}

func setPicked(t *testing.T, st *store.Store, fileID int64) {
	t.Helper()
	if err := st.Picks().Upsert(store.Pick{
		FileID:   fileID,
		State:    "picked",
		PickedAt: time.Now().Unix(),
		Rating:   sql.NullInt64{Int64: 3, Valid: true},
	}); err != nil {
		t.Fatalf("upsert pick: %v", err)
	}
}

func decodeLibrary(t *testing.T, env *testEnv) libraryJSON {
	t.Helper()
	resp := env.get("/api/library")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("library status %d", resp.StatusCode)
	}
	var lib libraryJSON
	if err := json.NewDecoder(resp.Body).Decode(&lib); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	return lib
}

func TestLibraryHidesRejectedWhenSettingOn(t *testing.T) {
	env := newTestServer(t)
	a := seedFile(t, env.store, "Day1/A.JPG")
	b := seedFile(t, env.store, "Day1/B.JPG")
	c := seedFile(t, env.store, "Day1/C.JPG")
	seedCluster(t, env.store, "X-T5#A1", 1714185600, []int64{a, b, c}, a)
	setPicked(t, env.store, a)
	setRejected(t, env.store, b)

	// Off (default): all three members visible.
	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	if got := len(lib.Clusters[0].Members); got != 3 {
		t.Errorf("members default = %d, want 3", got)
	}

	// On: rejected hidden.
	setHideRejected(t, env, true)
	lib = decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	got := lib.Clusters[0].Members
	if len(got) != 2 {
		t.Fatalf("members = %d, want 2", len(got))
	}
	for _, m := range got {
		if m.Rejected {
			t.Errorf("rejected member %d still present", m.FileID)
		}
		if m.FileID == b {
			t.Errorf("rejected file %d still present", b)
		}
	}
}

func TestLibraryDropsClusterWithOnlyRejected(t *testing.T) {
	env := newTestServer(t)
	keep1 := seedFile(t, env.store, "Day1/A.JPG")
	keep2 := seedFile(t, env.store, "Day1/B.JPG")
	rej1 := seedFile(t, env.store, "Day2/X.JPG")
	rej2 := seedFile(t, env.store, "Day2/Y.JPG")
	seedCluster(t, env.store, "X-T5#A1", 1714185600, []int64{keep1, keep2}, keep1)
	rejectedCluster := seedCluster(t, env.store, "X-T5#A1", 1714272000, []int64{rej1, rej2}, rej1)
	setRejected(t, env.store, rej1)
	setRejected(t, env.store, rej2)

	setHideRejected(t, env, true)

	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1 (all-rejected cluster should be hidden)", len(lib.Clusters))
	}
	if lib.Clusters[0].ID == rejectedCluster {
		t.Errorf("the all-rejected cluster %d is still present", rejectedCluster)
	}
}

func TestLibraryMergedClusterListsInvolvedCameras(t *testing.T) {
	env := newTestServer(t)
	a := seedFile(t, env.store, "Day1/A.JPG")
	b := seedFile(t, env.store, "Day1/B.JPG")
	if err := env.store.Files().UpdateMetadata(store.File{
		ID:        a,
		DeviceKey: sql.NullString{String: "Apple iPhone 15#A1", Valid: true},
	}); err != nil {
		t.Fatalf("update meta a: %v", err)
	}
	if err := env.store.Files().UpdateStatus(a, "analyzed", ""); err != nil {
		t.Fatalf("status a: %v", err)
	}
	if err := env.store.Files().UpdateMetadata(store.File{
		ID:        b,
		DeviceKey: sql.NullString{String: "FUJIFILM X-T5#B2", Valid: true},
	}); err != nil {
		t.Fatalf("update meta b: %v", err)
	}
	if err := env.store.Files().UpdateStatus(b, "analyzed", ""); err != nil {
		t.Fatalf("status b: %v", err)
	}

	seedCluster(t, env.store, group.MergedDeviceKey, 1714185600, []int64{a, b}, a)

	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	// Sorted, deduped, joined with " + ". "Apple iPhone 15" < "FUJIFILM X-T5" alphabetically.
	if got := lib.Clusters[0].Device; got != "Apple iPhone 15 + FUJIFILM X-T5" {
		t.Errorf("merged Device = %q, want %q", got, "Apple iPhone 15 + FUJIFILM X-T5")
	}
}

func TestLibrarySingleDeviceClusterUnaffected(t *testing.T) {
	env := newTestServer(t)
	a := seedFile(t, env.store, "Day1/A.JPG")
	b := seedFile(t, env.store, "Day1/B.JPG")
	seedCluster(t, env.store, "FUJIFILM X-T5#B2", 1714185600, []int64{a, b}, a)

	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	if got := lib.Clusters[0].Device; got != "FUJIFILM X-T5" {
		t.Errorf("single-device label = %q, want %q", got, "FUJIFILM X-T5")
	}
}

func TestLibraryUnchangedWhenSettingOff(t *testing.T) {
	env := newTestServer(t)
	a := seedFile(t, env.store, "Day1/A.JPG")
	b := seedFile(t, env.store, "Day1/B.JPG")
	seedCluster(t, env.store, "X-T5#A1", 1714185600, []int64{a, b}, a)
	setRejected(t, env.store, b)

	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	if got := len(lib.Clusters[0].Members); got != 2 {
		t.Errorf("members = %d, want 2 (setting off, rejected still visible)", got)
	}
}

// seedVideoFile inserts a minimal video file row and returns its id.
func seedVideoFile(t *testing.T, st *store.Store, path string) int64 {
	t.Helper()
	id, err := st.Files().Insert(store.File{
		Path: path, Size: 1, Mtime: time.Now().Unix(),
		ContentHash: "h-" + path, Kind: "video", Format: "mov",
		ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert video file: %v", err)
	}
	return id
}

func decodeCluster(t *testing.T, env *testEnv, clusterID int64) focusJSON {
	t.Helper()
	resp := env.get("/api/clusters/" + strconv.FormatInt(clusterID, 10))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cluster status %d", resp.StatusCode)
	}
	var focus focusJSON
	if err := json.NewDecoder(resp.Body).Decode(&focus); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	return focus
}

func TestLibraryMemberIncludesKindAndFormatForPhoto(t *testing.T) {
	env := newTestServer(t)
	// seedFile inserts a photo/jpeg file
	a := seedFile(t, env.store, "Day1/A.JPG")
	cid := seedCluster(t, env.store, "X-T5#A1", 1714185600, []int64{a}, a)

	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	if len(lib.Clusters[0].Members) != 1 {
		t.Fatalf("members=%d, want 1", len(lib.Clusters[0].Members))
	}
	m := lib.Clusters[0].Members[0]
	if m.Kind != "photo" {
		t.Errorf("library member Kind = %q, want %q", m.Kind, "photo")
	}
	if m.Format != "jpeg" {
		t.Errorf("library member Format = %q, want %q", m.Format, "jpeg")
	}

	// Also check /api/clusters/{id}.
	focus := decodeCluster(t, env, cid)
	if len(focus.Cluster.Members) != 1 {
		t.Fatalf("cluster members=%d, want 1", len(focus.Cluster.Members))
	}
	cm := focus.Cluster.Members[0]
	if cm.Kind != "photo" {
		t.Errorf("cluster member Kind = %q, want %q", cm.Kind, "photo")
	}
	if cm.Format != "jpeg" {
		t.Errorf("cluster member Format = %q, want %q", cm.Format, "jpeg")
	}
}

func TestLibraryMemberIncludesKindAndFormatForVideo(t *testing.T) {
	env := newTestServer(t)
	a := seedVideoFile(t, env.store, "Day1/clip.mov")
	cid := seedCluster(t, env.store, "X-T5#A1", 1714185600, []int64{a}, a)

	lib := decodeLibrary(t, env)
	if len(lib.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(lib.Clusters))
	}
	if len(lib.Clusters[0].Members) != 1 {
		t.Fatalf("members=%d, want 1", len(lib.Clusters[0].Members))
	}
	m := lib.Clusters[0].Members[0]
	if m.Kind != "video" {
		t.Errorf("library member Kind = %q, want %q", m.Kind, "video")
	}
	if m.Format != "mov" {
		t.Errorf("library member Format = %q, want %q", m.Format, "mov")
	}

	// Also check /api/clusters/{id}.
	focus := decodeCluster(t, env, cid)
	if len(focus.Cluster.Members) != 1 {
		t.Fatalf("cluster members=%d, want 1", len(focus.Cluster.Members))
	}
	cm := focus.Cluster.Members[0]
	if cm.Kind != "video" {
		t.Errorf("cluster member Kind = %q, want %q", cm.Kind, "video")
	}
	if cm.Format != "mov" {
		t.Errorf("cluster member Format = %q, want %q", cm.Format, "mov")
	}
}
