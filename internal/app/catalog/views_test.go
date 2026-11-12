package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// viewsTestMark stores a pick row of state for fileID without exporting
// anything (the views only read the state).
func viewsTestMark(t *testing.T, st *store.Store, fileID int64, state string) {
	t.Helper()
	if err := st.Picks().Upsert(store.Pick{FileID: fileID, State: state, PickedAt: time.Now().Unix()}); err != nil {
		t.Fatalf("mark %d %s: %v", fileID, state, err)
	}
}

// viewsTestDevice gives fileID a device key and leaves it analyzed.
func viewsTestDevice(t *testing.T, st *store.Store, fileID int64, key string) {
	t.Helper()
	if err := st.Files().UpdateMetadata(store.File{ID: fileID, DeviceKey: sql.NullString{String: key, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Files().UpdateStatus(fileID, "analyzed", ""); err != nil {
		t.Fatal(err)
	}
}

// viewsTestOpen seeds a folder with fn and opens it.
func viewsTestOpen(t *testing.T, h *catalogTestHarness, fn func(st *store.Store, root string)) string {
	t.Helper()
	id, root := h.folder(t, "f")
	catalogTestSeed(t, root, func(st *store.Store) { fn(st, root) })
	h.open(t, id)
	return id
}

func viewsTestClusters(t *testing.T, h *catalogTestHarness, id string) FolderView {
	t.Helper()
	v, err := h.cat.Clusters(id)
	if err != nil {
		t.Fatalf("Clusters: %v", err)
	}
	return v
}

// --- ported from internal/server/library_test.go --------------------------------

func TestClusters_HidesRejectedWhenSettingOn(t *testing.T) {
	h := newCatalogTestHarness(t)
	var b int64
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		a := catalogTestFile(t, st, root, "Day1/A.JPG", nil)
		b = catalogTestFile(t, st, root, "Day1/B.JPG", nil)
		c := catalogTestFile(t, st, root, "Day1/C.JPG", nil)
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a, b, c)
		viewsTestMark(t, st, a, "picked")
		viewsTestMark(t, st, b, "rejected")
	})

	v := viewsTestClusters(t, h, id)
	if len(v.Clusters) != 1 || len(v.Clusters[0].Members) != 3 || v.Total != 3 {
		t.Fatalf("default: clusters %d, total %d; want 1 cluster of 3", len(v.Clusters), v.Total)
	}

	h.settings.set(func(c *config.Config) { c.HideRejectedPhotos = true })
	v = viewsTestClusters(t, h, id)
	if len(v.Clusters) != 1 {
		t.Fatalf("clusters = %d; want 1", len(v.Clusters))
	}
	got := v.Clusters[0].Members
	if len(got) != 2 || v.Total != 2 {
		t.Fatalf("members = %d, total = %d; want 2", len(got), v.Total)
	}
	for _, m := range got {
		if m.Rejected || m.FileID == b {
			t.Errorf("rejected member %d still present", m.FileID)
		}
	}
	if v.Picked != 1 {
		t.Errorf("picked = %d; want 1", v.Picked)
	}
}

func TestClusters_DropsClusterWithOnlyRejected(t *testing.T) {
	h := newCatalogTestHarness(t)
	h.settings.set(func(c *config.Config) { c.HideRejectedPhotos = true })
	var rejected int64
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		k1 := catalogTestFile(t, st, root, "Day1/A.JPG", nil)
		k2 := catalogTestFile(t, st, root, "Day1/B.JPG", nil)
		r1 := catalogTestFile(t, st, root, "Day2/X.JPG", nil)
		r2 := catalogTestFile(t, st, root, "Day2/Y.JPG", nil)
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, k1, k2)
		rejected = catalogTestCluster(t, st, "X-T5#A1", 1714272000, r1, r2)
		viewsTestMark(t, st, r1, "rejected")
		viewsTestMark(t, st, r2, "rejected")
	})
	v := viewsTestClusters(t, h, id)
	if len(v.Clusters) != 1 {
		t.Fatalf("clusters = %d; want 1 (the all-rejected cluster hidden)", len(v.Clusters))
	}
	if v.Clusters[0].ID == rejected {
		t.Errorf("the all-rejected cluster %d is still present", rejected)
	}
}

func TestClusters_MergedClusterListsInvolvedCameras(t *testing.T) {
	h := newCatalogTestHarness(t)
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		a := catalogTestFile(t, st, root, "Day1/A.JPG", nil)
		b := catalogTestFile(t, st, root, "Day1/B.JPG", nil)
		viewsTestDevice(t, st, a, "Apple iPhone 15#A1")
		viewsTestDevice(t, st, b, "FUJIFILM X-T5#B2")
		catalogTestCluster(t, st, group.MergedDeviceKey, 1714185600, a, b)
	})
	v := viewsTestClusters(t, h, id)
	if len(v.Clusters) != 1 {
		t.Fatalf("clusters = %d; want 1", len(v.Clusters))
	}
	if got, want := v.Clusters[0].Device, "Apple iPhone 15 + FUJIFILM X-T5"; got != want {
		t.Errorf("merged device = %q; want %q", got, want)
	}
	if want := []string{"Apple iPhone 15", "FUJIFILM X-T5"}; !slices.Equal(v.Devices, want) {
		t.Errorf("devices = %v; want %v (single-camera labels)", v.Devices, want)
	}
}

func TestClusters_SingleDeviceClusterUnaffected(t *testing.T) {
	h := newCatalogTestHarness(t)
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		a := catalogTestFile(t, st, root, "Day1/A.JPG", nil)
		b := catalogTestFile(t, st, root, "Day1/B.JPG", nil)
		catalogTestCluster(t, st, "FUJIFILM X-T5#B2", 1714185600, a, b)
	})
	v := viewsTestClusters(t, h, id)
	if len(v.Clusters) != 1 || v.Clusters[0].Device != "FUJIFILM X-T5" {
		t.Fatalf("clusters = %+v; want one labelled FUJIFILM X-T5", v.Clusters)
	}
}

func TestClusters_UnchangedWhenSettingOff(t *testing.T) {
	h := newCatalogTestHarness(t)
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		a := catalogTestFile(t, st, root, "Day1/A.JPG", nil)
		b := catalogTestFile(t, st, root, "Day1/B.JPG", nil)
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a, b)
		viewsTestMark(t, st, b, "rejected")
	})
	v := viewsTestClusters(t, h, id)
	if len(v.Clusters) != 1 || len(v.Clusters[0].Members) != 2 {
		t.Fatalf("clusters = %+v; want one of 2 (setting off, rejected still visible)", v.Clusters)
	}
}

func TestClusters_MemberKindAndFormat(t *testing.T) {
	for _, tt := range []struct{ kind, format string }{{"photo", "jpeg"}, {"video", "mov"}} {
		t.Run(tt.kind, func(t *testing.T) {
			h := newCatalogTestHarness(t)
			var cid int64
			id := viewsTestOpen(t, h, func(st *store.Store, _ string) {
				f, err := st.Files().Insert(store.File{
					Path: "Day1/x." + tt.format, Size: 1, Mtime: 1, ContentHash: "h",
					Kind: tt.kind, Format: tt.format, ScanStatus: "analyzed",
				})
				if err != nil {
					t.Fatal(err)
				}
				cid = catalogTestCluster(t, st, "X-T5#A1", 1714185600, f)
			})
			v := viewsTestClusters(t, h, id)
			if len(v.Clusters) != 1 || len(v.Clusters[0].Members) != 1 {
				t.Fatalf("clusters = %+v; want one of 1", v.Clusters)
			}
			if m := v.Clusters[0].Members[0]; m.Kind != tt.kind || m.Format != tt.format {
				t.Errorf("folder member kind/format = %q/%q; want %q/%q", m.Kind, m.Format, tt.kind, tt.format)
			}
			focus, err := h.cat.Cluster(id, cid)
			if err != nil {
				t.Fatal(err)
			}
			if len(focus.Cluster.Members) != 1 {
				t.Fatalf("focus members = %d; want 1", len(focus.Cluster.Members))
			}
			if m := focus.Cluster.Members[0]; m.Kind != tt.kind || m.Format != tt.format {
				t.Errorf("focus member kind/format = %q/%q; want %q/%q", m.Kind, m.Format, tt.kind, tt.format)
			}
		})
	}
}

// --- new in the catalog ---------------------------------------------------------

func TestClusters_FolderFields(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "Trip")
	catalogTestSeed(t, root, func(st *store.Store) {
		a := catalogTestFile(t, st, root, "A.JPG", nil)
		b := catalogTestFile(t, st, root, "B.JPG", nil)
		c := catalogTestFile(t, st, root, "C.JPG", nil)
		failed := catalogTestFile(t, st, root, "D.JPG", nil)
		if err := st.Files().UpdateStatus(failed, "failed", "broken"); err != nil {
			t.Fatal(err)
		}
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a)
		catalogTestCluster(t, st, "", 1714185600, b)
		catalogTestCluster(t, st, "Apple iPhone 15#Z", 1714185600, c)
		viewsTestMark(t, st, c, "picked")
	})
	v := h.open(t, id)
	if v.Folder != "Trip" || v.Total != 3 || v.Picked != 1 || v.Failed != 1 {
		t.Fatalf("folder %q total %d picked %d failed %d; want Trip 3 1 1", v.Folder, v.Total, v.Picked, v.Failed)
	}
	if want := []string{"Apple iPhone 15", "Unknown device", "X-T5"}; !slices.Equal(v.Devices, want) {
		t.Fatalf("devices = %v; want %v", v.Devices, want)
	}
	if v.PicksDir != "picks" || v.PicksBlocked != "" || v.PicksMissing != 0 {
		t.Fatalf("picks dir %q blocked %q missing %d; want picks, none, 0", v.PicksDir, v.PicksBlocked, v.PicksMissing)
	}
}

func TestClusters_EmptyFolder(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, _ := h.folder(t, "empty")
	v := h.open(t, id)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"clusters", "devices"} {
		if arr, ok := m[k].([]any); !ok || len(arr) != 0 {
			t.Errorf("%s = %#v; want an empty array, not null", k, m[k])
		}
	}
}

func TestClusters_Quality(t *testing.T) {
	h := newCatalogTestHarness(t)
	var rated, unrated, video int64
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		rated = catalogTestFile(t, st, root, "A.JPG", nil)
		unrated = catalogTestFile(t, st, root, "B.JPG", nil)
		var err error
		video, err = st.Files().Insert(store.File{
			Path: "C.MOV", Size: 1, Mtime: 1, ContentHash: "h-C.MOV",
			Kind: "video", Format: "mov", ScanStatus: "analyzed",
		})
		if err != nil {
			t.Fatal(err)
		}
		catalogTestRate(t, st, rated, "h-A.JPG")
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, rated, unrated, video)
	})
	quality := func() map[int64]*float64 {
		out := map[int64]*float64{}
		for _, m := range viewsTestClusters(t, h, id).Clusters[0].Members {
			out[m.FileID] = m.Quality
		}
		return out
	}
	q := quality()
	if q[rated] == nil || q[unrated] != nil || q[video] != nil {
		t.Fatalf("quality = rated %v, unrated %v, video %v; want only the rated photo", q[rated], q[unrated], q[video])
	}
	h.settings.set(func(c *config.Config) { c.QualityRating = false })
	if q := quality(); q[rated] != nil {
		t.Fatalf("quality with the setting off = %v; want omitted", *q[rated])
	}
}

func TestCluster_FocusPayload(t *testing.T) {
	h := newCatalogTestHarness(t)
	var c1, c2, c3, a, b int64
	id := viewsTestOpen(t, h, func(st *store.Store, root string) {
		a = catalogTestFile(t, st, root, "A.JPG", nil)
		b = catalogTestFile(t, st, root, "B.JPG", nil)
		c := catalogTestFile(t, st, root, "C.JPG", nil)
		d := catalogTestFile(t, st, root, "D.JPG", nil)
		if err := st.Files().UpdateMetadata(store.File{
			ID:          a,
			DeviceKey:   sql.NullString{String: "FUJIFILM X-T5#B2", Valid: true},
			CapturedAt:  sql.NullInt64{Int64: 1714185600, Valid: true},
			Width:       sql.NullInt64{Int64: 6240, Valid: true},
			Height:      sql.NullInt64{Int64: 4160, Valid: true},
			ISO:         sql.NullInt64{Int64: 200, Valid: true},
			Aperture:    sql.NullFloat64{Float64: 2.8, Valid: true},
			Shutter:     sql.NullString{String: "1/250", Valid: true},
			FocalLength: sql.NullFloat64{Float64: 23, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(context.Background(),
			`UPDATE files SET scan_status = 'analyzed', gps_lat = 1.5, gps_lon = -2.25 WHERE id = ?`, a); err != nil {
			t.Fatal(err)
		}
		c1 = catalogTestCluster(t, st, "FUJIFILM X-T5#B2", 1714185600, c)
		c2 = catalogTestCluster(t, st, "FUJIFILM X-T5#B2", 1714189200, a, b)
		c3 = catalogTestCluster(t, st, "FUJIFILM X-T5#B2", 1714192800, d)
		viewsTestMark(t, st, b, "rejected")
	})

	f, err := h.cat.Cluster(id, c2)
	if err != nil {
		t.Fatal(err)
	}
	if f.ClusterIndex != 2 || f.ClusterCount != 3 || f.PrevClusterID != c1 || f.NextClusterID != c3 {
		t.Fatalf("index %d count %d prev %d next %d; want 2 3 %d %d",
			f.ClusterIndex, f.ClusterCount, f.PrevClusterID, f.NextClusterID, c1, c3)
	}
	if f.Cluster.ID != c2 || f.Cluster.Device != "FUJIFILM X-T5" || f.Cluster.Time != formatBucketLabel(1714189200) {
		t.Fatalf("cluster = %+v", f.Cluster)
	}
	if len(f.Cluster.Members) != 2 || !f.Cluster.Members[1].Rejected {
		t.Fatalf("members = %+v; want both, the second rejected", f.Cluster.Members)
	}
	lat, lon := 1.5, -2.25
	want := Exif{
		Camera: "FUJIFILM X-T5", Aperture: "f/2.8", Shutter: "1/250", ISO: "200", FocalLength: "23mm",
		Captured: time.Unix(1714185600, 0).Format("Jan 2 2006 · 15:04:05"), Size: "6240 × 4160",
		GPSLat: &lat, GPSLon: &lon,
	}
	if got := f.Exif[strconv.FormatInt(a, 10)]; !reflect.DeepEqual(got, want) {
		t.Fatalf("exif of %d = %+v; want %+v", a, got, want)
	}
	if got := f.Exif[strconv.FormatInt(b, 10)]; !reflect.DeepEqual(got, Exif{}) {
		t.Fatalf("exif of %d = %+v; want empty", b, got)
	}

	first, err := h.cat.Cluster(id, c1)
	if err != nil || first.PrevClusterID != 0 || first.ClusterIndex != 1 {
		t.Fatalf("first cluster = prev %d index %d, %v; want 0 1", first.PrevClusterID, first.ClusterIndex, err)
	}
	last, err := h.cat.Cluster(id, c3)
	if err != nil || last.NextClusterID != 0 || last.ClusterIndex != 3 {
		t.Fatalf("last cluster = next %d index %d, %v; want 0 3", last.NextClusterID, last.ClusterIndex, err)
	}
}

func TestCluster_NotFound(t *testing.T) {
	h := newCatalogTestHarness(t)
	var empty int64
	id := viewsTestOpen(t, h, func(st *store.Store, _ string) {
		var err error
		empty, err = st.Clusters().InsertCluster(store.Cluster{DeviceKey: "X"})
		if err != nil {
			t.Fatal(err)
		}
	})
	for _, cid := range []int64{999, empty} {
		if _, err := h.cat.Cluster(id, cid); catalogTestCode(err) != apperr.CodeNotFound {
			t.Errorf("Cluster(%d) = %v; want not_found", cid, err)
		}
	}
}

// viewsTestKeys returns the JSON keys v marshals to.
func viewsTestKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

// TestViews_JSONKeys pins the JSON of the views against the design types
// (frontend/src/design/types.ts: Member, Cluster, ExifPayload,
// FocusResponse) and v1's /api/library, /api/clusters/{id} and /api/picks.
func TestViews_JSONKeys(t *testing.T) {
	q, lat := 7.5, 1.0
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"Member", Member{Quality: &q},
			[]string{"content_hash", "file_id", "format", "kind", "path", "picked", "quality", "rejected"}},
		{"Member unrated", Member{},
			[]string{"content_hash", "file_id", "format", "kind", "path", "picked", "rejected"}},
		{"Cluster", Cluster{}, []string{"device", "id", "members", "time"}},
		{"Exif", Exif{Camera: "c", Aperture: "a", Shutter: "s", ISO: "i", FocalLength: "f", Captured: "t", Size: "z", GPSLat: &lat, GPSLon: &lat},
			[]string{"aperture", "camera", "captured", "focal_length", "gps_lat", "gps_lon", "iso", "shutter", "size"}},
		{"Exif empty", Exif{}, []string{}},
		{"FocusResponse", FocusResponse{},
			[]string{"cluster", "cluster_count", "cluster_index", "exif", "next_cluster_id", "prev_cluster_id"}},
		{"FolderView", FolderView{PicksBlocked: "has_originals"},
			[]string{"clusters", "devices", "failed", "folder", "picked", "picks_blocked", "picks_dir", "picks_missing", "total"}},
		{"FolderView unblocked", FolderView{},
			[]string{"clusters", "devices", "failed", "folder", "picked", "picks_dir", "picks_missing", "total"}},
		{"PickResult", PickResult{ExportedPath: "p"}, []string{"exported_path", "file_id", "state"}},
		{"PickResult unmarked", PickResult{}, []string{"file_id", "state"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := viewsTestKeys(t, tt.v); !slices.Equal(got, tt.want) {
				t.Errorf("keys = %v; want %v", got, tt.want)
			}
		})
	}
}
