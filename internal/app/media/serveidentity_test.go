package media

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// Round 1 of fix wave B: what is served, and how long it may be cached,
// follows the row's content hash at the moment of serving.

// jpegSize decodes a response body's JPEG dimensions.
func jpegSize(t *testing.T, rec *httptest.ResponseRecorder) (int, int) {
	t.Helper()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("body is not a JPEG: %v", err)
	}
	return cfg.Width, cfg.Height
}

// wantCache fails unless the response's Cache-Control is want.
func wantCache(t *testing.T, rec *httptest.ResponseRecorder, want, what string) {
	t.Helper()
	if cc := rec.Header().Get("Cache-Control"); cc != want {
		t.Errorf("%s: Cache-Control = %q, want %q", what, cc, want)
	}
}

// A rescan that lands after the full-size JPEG of the old bytes was
// published, before the request serves it, is noticed at serving: the
// response carries the new bytes' pixels, never the old ones.
func TestFull_RescanAfterPublishServesNewPixels(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	fileID := h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
	var once sync.Once
	h.h.hook = func(ev string) {
		if ev == hookEnd { // on the transcode's goroutine, after it wrote
			once.Do(func() { h.rewrite(t, rel, platformtest.MakeJPEG(t, 200, 300), "heic", hashB) })
		}
	}

	rec := h.get(stripMedia(t, FullURL(h.id, rel, hashA)))
	wantStatus(t, rec, http.StatusOK)
	if w, ht := jpegSize(t, rec); w != 200 || ht != 300 {
		t.Errorf("body = %dx%d, want the new bytes' 200x300", w, ht)
	}
	wantCache(t, rec, cacheUnversioned, "a v=A request answered with B")
	h.wantOnlyFull(t, fileID, hashB)
}

// The same on the cache-hit path: the full-size JPEG of the old bytes is
// already cached and opened when the rescan lands.
func TestFull_RescanAfterCacheHitServesNewPixels(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	fileID := h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
	if w, ht := h.fullSize(t, rel, hashA); w != 300 || ht != 200 {
		t.Fatalf("first full-size JPEG = %dx%d, want 300x200", w, ht)
	}
	var once sync.Once
	h.h.hook = func(ev string) {
		if ev == hookOpened {
			once.Do(func() { h.rewrite(t, rel, platformtest.MakeJPEG(t, 200, 300), "heic", hashB) })
		}
	}

	rec := h.get(stripMedia(t, FullURL(h.id, rel, hashA)))
	wantStatus(t, rec, http.StatusOK)
	if w, ht := jpegSize(t, rec); w != 200 || ht != 300 {
		t.Errorf("body = %dx%d, want the new bytes' 200x300 (the cached old file was served)", w, ht)
	}
	h.wantOnlyFull(t, fileID, hashB)
}

// A file that keeps changing under every lookup is retried a bounded
// number of times and then answered with an error, never with stale
// pixels.
func TestFull_StaleRetriesAreBounded(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
	var mu sync.Mutex
	n := 0
	h.h.hook = func(ev string) {
		if ev != hookOpened {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		n++
		h.rewrite(t, rel, platformtest.MakeJPEG(t, 100+n, 100), "heic", fmt.Sprintf("%016x", n))
	}

	rec := h.get(stripMedia(t, FullURL(h.id, rel, hashA)))
	wantStatus(t, rec, http.StatusServiceUnavailable)
	mu.Lock()
	defer mu.Unlock()
	if n != fullAttempts {
		t.Errorf("the file was checked %d times, want fullAttempts = %d", n, fullAttempts)
	}
}

// Long-lived caching only when the requested v is the hash of the bytes
// served. A → B → A: a stale v=A while the row is B gets B's pixels with
// no-cache, so after the original is restored the A URL shows A.
func TestFull_CacheControlFollowsServedHash(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	a, b := platformtest.MakeJPEG(t, 300, 200), platformtest.MakeJPEG(t, 200, 300)
	h.photo(t, rel, a, "heic", hashA)

	get := func(v string) *httptest.ResponseRecorder {
		t.Helper()
		rec := h.get(stripMedia(t, FullURL(h.id, rel, v)))
		wantStatus(t, rec, http.StatusOK)
		return rec
	}
	size := func(rec *httptest.ResponseRecorder) string {
		w, ht := jpegSize(t, rec)
		return fmt.Sprintf("%dx%d", w, ht)
	}

	rec := get(hashA)
	wantCache(t, rec, cacheVersioned, "A, row A")
	if s := size(rec); s != "300x200" {
		t.Errorf("A, row A: %s", s)
	}

	h.rewrite(t, rel, b, "heic", hashB)
	rec = get(hashB)
	wantCache(t, rec, cacheVersioned, "B, row B")
	if s := size(rec); s != "200x300" {
		t.Errorf("B, row B: %s", s)
	}
	rec = get(hashA)
	wantCache(t, rec, cacheUnversioned, "stale A, row B")
	if s := size(rec); s != "200x300" {
		t.Errorf("stale A, row B: %s, want the current 200x300", s)
	}
	wantCache(t, h.get("/full/"+h.id+"/"+rel), cacheUnversioned, "no v")

	h.rewrite(t, rel, a, "heic", hashA)
	rec = get(hashA)
	wantCache(t, rec, cacheVersioned, "A, row A again")
	if s := size(rec); s != "300x200" {
		t.Errorf("A after the restore: %s, want 300x200", s)
	}
}

// An original served as it is (JPEG) is cached long only when v is the
// row's hash and the file on disk is still the one the row describes (size
// and mtime, the scanner's own "unchanged" test).
func TestFull_OriginalCacheControlFollowsHash(t *testing.T) {
	h := newMediaHarness(t)
	rel := "A.JPG"
	h.photo(t, rel, platformtest.MakeJPEG(t, 40, 30), "jpeg", hashA)

	wantCache(t, h.get(stripMedia(t, FullURL(h.id, rel, hashA))), cacheVersioned, "v = row hash")
	wantCache(t, h.get(stripMedia(t, FullURL(h.id, rel, hashB))), cacheUnversioned, "v = another hash")
	wantCache(t, h.get("/full/"+h.id+"/"+rel), cacheUnversioned, "no v")

	// Changed on disk, not rescanned yet: the row's hash no longer names
	// these bytes.
	if err := os.WriteFile(filepath.Join(h.dir, rel), platformtest.MakeJPEG(t, 50, 30), 0o644); err != nil {
		t.Fatal(err)
	}
	wantCache(t, h.get(stripMedia(t, FullURL(h.id, rel, hashA))), cacheUnversioned, "changed on disk, v = row hash")
}

// Thumbnails follow the same rule. Their file is not keyed by hash, so it
// is known to show the row's bytes only once the thumbnail stage has run
// for them (scan_status thumbed or analyzed); a rescanned row waiting for
// new thumbnails is served no-cache.
func TestThumb_CacheControlFollowsHash(t *testing.T) {
	h := newMediaHarness(t)
	rel := "A.JPG"
	id := h.photo(t, rel, platformtest.MakeJPEG(t, 40, 30), "jpeg", hashA)
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 32, 24))

	thumbGet := func(v string) *httptest.ResponseRecorder {
		t.Helper()
		rec := h.get(stripMedia(t, ThumbURL(h.id, rel, thumb.TierThumb, v)))
		wantStatus(t, rec, http.StatusOK)
		return rec
	}
	wantCache(t, thumbGet(hashA), cacheVersioned, "v = row hash, analyzed")
	wantCache(t, thumbGet(hashB), cacheUnversioned, "v = another hash")
	wantCache(t, h.get("/thumb/"+h.id+"/"+rel), cacheUnversioned, "no v")

	// Rescanned with new bytes: the thumbnail on disk is still the old one.
	h.rewrite(t, rel, platformtest.MakeJPEG(t, 50, 30), "jpeg", hashB)
	wantCache(t, thumbGet(hashB), cacheUnversioned, "row B, thumbnails not rebuilt")
	wantCache(t, thumbGet(hashA), cacheUnversioned, "stale A, row B")
}
