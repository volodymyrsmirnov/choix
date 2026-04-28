package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSettingsPostRoundTrip(t *testing.T) {
	env := newTestServer(t)
	bucket := 300
	similarity := 0.85
	resp := postJSON(t, env, "/api/settings", map[string]any{
		"bucket_size": bucket,
		"similarity":  similarity,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings post status %d", resp.StatusCode)
	}

	resp2 := env.get("/api/settings")
	defer resp2.Body.Close()
	var body settingsBody
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode settings json: %v", err)
	}
	if body.BucketSize != bucket {
		t.Errorf("bucket_size = %d, want %d", body.BucketSize, bucket)
	}
	if body.Similarity != similarity {
		t.Errorf("similarity = %f, want %f", body.Similarity, similarity)
	}
}

func TestSettingsPostRejectsBadBucket(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/settings", map[string]any{"bucket_size": 30})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400 for bucket_size < 60", resp.StatusCode)
	}
}

func TestSettingsPostRejectsBadSimilarity(t *testing.T) {
	env := newTestServer(t)
	for _, bad := range []float64{0, 1.1, -0.5} {
		resp := postJSON(t, env, "/api/settings", map[string]any{"similarity": bad})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("similarity=%v: status %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestSettingsPicksDirPersistsToConfig(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/settings", map[string]any{"picks_dir": "exported"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings post status %d", resp.StatusCode)
	}
	resp2 := env.get("/api/settings")
	defer resp2.Body.Close()
	var body settingsBody
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PicksDir != "exported" {
		t.Errorf("picks_dir = %q, want exported (machine-wide TOML)", body.PicksDir)
	}
}

func TestSettingsGetReturnsJSON(t *testing.T) {
	env := newTestServer(t)
	resp := env.get("/api/settings")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := body["bucket_size"]; !ok {
		t.Error("missing bucket_size in settings response")
	}
}

func TestSettingsHideRejectedDefaultFalse(t *testing.T) {
	env := newTestServer(t)
	resp := env.get("/api/settings")
	defer resp.Body.Close()
	var body settingsBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.HideRejectedPhotos {
		t.Errorf("hide_rejected_photos default = true, want false")
	}
}

func TestSettingsHideRejectedPersistsToConfig(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/settings", map[string]any{"hide_rejected_photos": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings post status %d", resp.StatusCode)
	}
	resp2 := env.get("/api/settings")
	defer resp2.Body.Close()
	var body settingsBody
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.HideRejectedPhotos {
		t.Errorf("hide_rejected_photos = false, want true after save")
	}
}

func TestValidatePicksDirTraversal(t *testing.T) {
	bad := []string{"../out", "a/../../b", "../", "a/../../../etc"}
	for _, p := range bad {
		if err := validatePicksDir(p); err == nil {
			t.Errorf("validatePicksDir(%q) should return error for traversal", p)
		}
	}
	good := []string{"picks", "exported", "nested/dir"}
	for _, p := range good {
		if err := validatePicksDir(p); err != nil {
			t.Errorf("validatePicksDir(%q) should accept local path, got: %v", p, err)
		}
	}
}

func TestSettingsCrossDeviceMergingRoundTrip(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/settings", map[string]any{"cross_device_merging": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings post status %d", resp.StatusCode)
	}
	var saveResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reclustered, _ := saveResp["reclustered"].(bool); !reclustered {
		t.Errorf("flipping cross_device_merging should trigger recluster; got %v", saveResp)
	}

	r2 := env.get("/api/settings")
	defer r2.Body.Close()
	var body settingsBody
	if err := json.NewDecoder(r2.Body).Decode(&body); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if !body.CrossDeviceMerging {
		t.Errorf("cross_device_merging = false, want true after save")
	}
}
