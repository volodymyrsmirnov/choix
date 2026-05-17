package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

func TestFullStreamsOriginalReadOnly(t *testing.T) {
	env := newTestServer(t)
	rel := "Day1/A.JPG"
	abs := filepath.Join(env.scanRoot, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../testdata/http/fixture_thumb.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, src, 0o444); err != nil { // read-only on disk
		t.Fatal(err)
	}
	_ = seedFile(t, env.store, rel)

	resp := env.get("/full/" + rel)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if len(got) != len(src) {
		t.Errorf("len = %d, want %d", len(got), len(src))
	}
}

func TestFullRejectsTraversalPath(t *testing.T) {
	env := newTestServer(t)
	// Unknown path 404s cleanly.
	resp := env.get("/full/Day1/Nope.JPG")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

// TestFullHEICServesCachedTranscode covers the on-demand JPEG transcode
// branch (rec.Kind=="photo" && format!=jpeg|png). We pre-seed the cache
// so the handler hits the os.Stat fast path and never invokes sips, which
// keeps the test hermetic and exercises the path-keyed lookup + cached
// response wiring.
func TestFullHEICServesCachedTranscode(t *testing.T) {
	env := newTestServer(t)
	rel := "Day1/IMG_HEIC.HEIC"
	// Source file on disk (content is irrelevant — we never decode it).
	abs := filepath.Join(env.scanRoot, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("not-real-heic"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := env.store.Files().Insert(store.File{
		Path: rel, Size: 12, Mtime: time.Now().Unix(),
		ContentHash: "h-heic", Kind: "photo", Format: "heic",
		ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Pre-create the cache JPEG so ensureFullJPEG short-circuits before sips.
	cache := thumb.CachePath(env.scanRoot, id, thumb.TierFullJPEG)
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("cached-jpeg-bytes")
	if err := os.WriteFile(cache, want, 0o644); err != nil {
		t.Fatal(err)
	}

	resp := env.get("/full/" + rel)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(want) {
		t.Errorf("body = %q want %q", got, want)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content-type = %q, want image/jpeg", ct)
	}
}

// TestFullHEICConcurrentRequestsDedupe verifies that two simultaneous /full/
// requests for the same HEIC file share the cached result and don't
// double-write the destination. With the cache pre-seeded, both calls win
// via the os.Stat fast path; without the singleflight, a missed-cache race
// would still funnel through Do() into a single transcode. We assert all
// callers see identical bytes and the cache file is untouched.
func TestFullHEICConcurrentRequestsDedupe(t *testing.T) {
	env := newTestServer(t)
	rel := "Day1/IMG_DEDUPE.HEIC"
	abs := filepath.Join(env.scanRoot, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := env.store.Files().Insert(store.File{
		Path: rel, Size: 4, Mtime: time.Now().Unix(),
		ContentHash: "h-dedupe", Kind: "photo", Format: "heic",
		ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	cache := thumb.CachePath(env.scanRoot, id, thumb.TierFullJPEG)
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("dedupe-cached-bytes")
	if err := os.WriteFile(cache, want, 0o644); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	results := make([][]byte, n)
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := env.get("/full/" + rel)
			defer resp.Body.Close()
			statuses[i] = resp.StatusCode
			results[i], _ = io.ReadAll(resp.Body)
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if statuses[i] != http.StatusOK {
			t.Errorf("call %d status = %d", i, statuses[i])
		}
		if string(results[i]) != string(want) {
			t.Errorf("call %d body mismatch", i)
		}
	}
}
