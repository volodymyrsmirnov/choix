package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/quality"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// fakeQuality stands in for the background worker.
type fakeQuality struct {
	mu      sync.Mutex
	enabled bool
	status  quality.Status
	wakes   int
	sets    []bool
}

func (f *fakeQuality) Enabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enabled
}

func (f *fakeQuality) SetEnabled(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = on
	f.sets = append(f.sets, on)
}

func (f *fakeQuality) Status() quality.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status
	st.Enabled = f.enabled
	return st
}

func (f *fakeQuality) Wake() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wakes++
}

func withQuality(env *testEnv, enabled bool) *fakeQuality {
	f := &fakeQuality{enabled: enabled, status: quality.Status{State: quality.StateIdle}}
	env.server.cfg.Quality = f
	return f
}

func rate(t *testing.T, st *store.Store, id int64, path string, raw float64, scorer string) {
	t.Helper()
	ok, err := st.Files().SetQuality(id, "h-"+path, sql.NullFloat64{Float64: raw, Valid: true}, scorer)
	if err != nil || !ok {
		t.Fatalf("SetQuality(%s) = %v, %v", path, ok, err)
	}
}

// libraryQualities returns path → quality (nil when the field is absent).
func libraryQualities(t *testing.T, env *testEnv) map[string]*float64 {
	t.Helper()
	resp := env.get("/api/library")
	defer resp.Body.Close()
	var lib libraryJSON
	if err := json.NewDecoder(resp.Body).Decode(&lib); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	out := map[string]*float64{}
	for _, c := range lib.Clusters {
		for _, m := range c.Members {
			out[m.Path] = m.Quality
		}
	}
	return out
}

func TestLibraryAndClusterExposeDisplayRating(t *testing.T) {
	env := newTestServer(t)
	withQuality(env, true)
	sharp := seedFile(t, env.store, "sharp.jpg")
	soft := seedFile(t, env.store, "soft.jpg")
	unrated := seedFile(t, env.store, "unrated.jpg")
	cid := seedCluster(t, env.store, "cam", 100, []int64{sharp, soft, unrated}, 0)
	rate(t, env.store, sharp, "sharp.jpg", 0.6552, local.QualityScorerID) // → 7.4
	rate(t, env.store, soft, "soft.jpg", 0.16, local.QualityScorerID)     // → 0.8

	got := libraryQualities(t, env)
	if got["sharp.jpg"] == nil || *got["sharp.jpg"] != 7.4 {
		t.Errorf("sharp quality = %v, want 7.4", got["sharp.jpg"])
	}
	if got["soft.jpg"] == nil || *got["soft.jpg"] != 0.8 {
		t.Errorf("soft quality = %v, want 0.8", got["soft.jpg"])
	}
	if got["unrated.jpg"] != nil {
		t.Errorf("unrated photo has quality %v, want the field omitted", *got["unrated.jpg"])
	}

	// The Focus payload carries the same numbers.
	resp := env.get("/api/clusters/" + strconv.FormatInt(cid, 10))
	defer resp.Body.Close()
	var focus focusJSON
	if err := json.NewDecoder(resp.Body).Decode(&focus); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	byPath := map[string]*float64{}
	for _, m := range focus.Cluster.Members {
		byPath[m.Path] = m.Quality
	}
	if byPath["sharp.jpg"] == nil || *byPath["sharp.jpg"] != 7.4 || byPath["unrated.jpg"] != nil {
		t.Errorf("cluster payload qualities = %v", byPath)
	}
}

func TestRatingHiddenWhenFeatureOffOrWorkerAbsent(t *testing.T) {
	env := newTestServer(t)
	id := seedFile(t, env.store, "a.jpg")
	seedCluster(t, env.store, "cam", 100, []int64{id}, 0)
	rate(t, env.store, id, "a.jpg", 0.7, local.QualityScorerID)

	// No worker wired (the feature does not exist for this server).
	if q := libraryQualities(t, env)["a.jpg"]; q != nil {
		t.Errorf("quality = %v with no worker, want none", *q)
	}
	// Worker present but the setting is off: stored ratings stay hidden.
	f := withQuality(env, false)
	if q := libraryQualities(t, env)["a.jpg"]; q != nil {
		t.Errorf("quality = %v with the setting off, want none", *q)
	}
	// Switched back on: the stored rating reappears without rescoring.
	f.SetEnabled(true)
	if q := libraryQualities(t, env)["a.jpg"]; q == nil {
		t.Error("quality missing after re-enabling")
	}
}

func TestRatingFromAnotherScorerOrForVideoIsNotShown(t *testing.T) {
	env := newTestServer(t)
	withQuality(env, true)
	old := seedFile(t, env.store, "old.jpg")
	rate(t, env.store, old, "old.jpg", 0.7, "some-older-model/v0")
	vid, err := env.store.Files().Insert(store.File{Path: "clip.mov", Size: 1, Mtime: 1, ContentHash: "h-clip.mov", Kind: "video", Format: "mov", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	rate(t, env.store, vid, "clip.mov", 0.7, local.QualityScorerID)
	seedCluster(t, env.store, "cam", 100, []int64{old, vid}, 0)

	got := libraryQualities(t, env)
	if got["old.jpg"] != nil {
		t.Error("a rating from a different scorer must be hidden until the photo is re-rated")
	}
	if got["clip.mov"] != nil {
		t.Error("videos never carry a rating")
	}
}

func TestRatingSurvivesRecluster(t *testing.T) {
	env := newTestServer(t)
	withQuality(env, true)
	id := seedFile(t, env.store, "a.jpg")
	seedCluster(t, env.store, "cam", 100, []int64{id}, 0)
	rate(t, env.store, id, "a.jpg", 0.6552, local.QualityScorerID)

	resp := postJSON(t, env, "/api/recluster", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recluster status %d", resp.StatusCode)
	}
	if q := libraryQualities(t, env)["a.jpg"]; q == nil || *q != 7.4 {
		t.Fatalf("quality after recluster = %v, want 7.4", q)
	}
}

func TestQualityStatusEndpoint(t *testing.T) {
	env := newTestServer(t)

	// Without a worker the endpoint still answers, as disabled.
	resp := env.get("/api/quality")
	var st quality.Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if st.Enabled || st.State != quality.StateDisabled {
		t.Fatalf("status without worker = %+v, want disabled", st)
	}

	f := withQuality(env, true)
	f.status = quality.Status{State: quality.StateDownloading, Received: 40, Size: 100}
	resp = env.get("/api/quality")
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if raw["enabled"] != true || raw["state"] != "downloading" || raw["received"] != float64(40) || raw["size"] != float64(100) {
		t.Fatalf("status JSON = %v", raw)
	}
}

func TestQualityRetryWakesWorker(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/quality/retry", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("retry without worker = %d, want 404", resp.StatusCode)
	}

	f := withQuality(env, true)
	resp = postJSON(t, env, "/api/quality/retry", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || f.wakes != 1 {
		t.Fatalf("retry status %d, wakes %d; want 200 and one wake", resp.StatusCode, f.wakes)
	}
}

func TestSettingsQualityRatingDefaultsOnAndTogglesWorker(t *testing.T) {
	env := newTestServer(t)
	f := withQuality(env, true)

	get := func() settingsBody {
		t.Helper()
		resp := env.get("/api/settings")
		defer resp.Body.Close()
		var b settingsBody
		if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !get().QualityRating {
		t.Fatal("quality_rating must default to true")
	}

	// A save that does not mention the key leaves the worker alone.
	resp := postJSON(t, env, "/api/settings", map[string]any{"advance_on_action": true})
	resp.Body.Close()
	if len(f.sets) != 0 {
		t.Fatalf("worker toggled %v by an unrelated save", f.sets)
	}

	resp = postJSON(t, env, "/api/settings", map[string]any{"quality_rating": false})
	var saved map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status %d", resp.StatusCode)
	}
	if saved["reclustered"] != false {
		t.Error("switching the quality rating must not recluster")
	}
	if get().QualityRating {
		t.Error("quality_rating=false did not persist")
	}
	if len(f.sets) != 1 || f.sets[0] != false {
		t.Fatalf("worker SetEnabled calls = %v, want [false]", f.sets)
	}

	resp = postJSON(t, env, "/api/settings", map[string]any{"quality_rating": true})
	resp.Body.Close()
	if len(f.sets) != 2 || f.sets[1] != true || !get().QualityRating {
		t.Fatalf("re-enabling: sets=%v persisted=%v", f.sets, get().QualityRating)
	}
}
