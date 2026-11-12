package media

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// Round 2 of fix wave B: validators are bound to the identity of the bytes
// served, and modification-time validation is not offered on /thumb and
// /full, so a conditional request never keeps another version's bytes.

// future is an If-Modified-Since date after every file the tests write: a
// handler that validated by modification time would answer it 304.
var future = time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)

// sameSecond is the mtime every version of a file gets in these tests: a
// restore within the same second, the case modification-time validation
// cannot tell apart.
var sameSecond = time.Now().Truncate(time.Second)

// putAt writes data as rel with mtime sameSecond and records it the way a
// (re)scan does, with the given hash.
func (h *mediaHarness) putAt(t *testing.T, rel string, data []byte, format, hash string) {
	t.Helper()
	abs := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(abs, sameSecond, sameSecond); err != nil {
		t.Fatal(err)
	}
	fi := statFile(t, abs)
	run, err := h.s.Store.Files().NewWalkRun() // a fresh walk, as the scanner draws one
	if err != nil {
		t.Fatalf("new walk run: %v", err)
	}
	if _, _, err := h.s.Store.Files().UpsertOnRescan(store.File{
		Path: rel, Size: fi.Size(), Mtime: fi.ModTime().Unix(), ContentHash: hash, Kind: "photo", Format: format,
	}, run); err != nil {
		t.Fatalf("rescan %s: %v", rel, err)
	}
}

// noLastModified fails when rec offers modification-time validation.
func noLastModified(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if lm := rec.Header().Get("Last-Modified"); lm != "" {
		t.Errorf("%s: Last-Modified = %q, want none", what, lm)
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("%s: no ETag", what)
	}
}

// abaConditional runs A → B → A on one URL kind and checks that the client
// never ends up holding B under URL A. target(v) is the URL for version v,
// size(rec) names the version a body is, rescan(v) puts version v in place
// (same second) and records it, ready(v) makes whatever cache the route
// serves for v (nothing for originals and full-size JPEGs).
func abaConditional(t *testing.T, h *mediaHarness, target func(v string) string, version func(*httptest.ResponseRecorder) string, rescan, ready func(v string)) {
	t.Helper()
	rescan("A")
	ready("A")
	a := h.get(target(hashA))
	wantStatus(t, a, http.StatusOK)
	noLastModified(t, a, "A")
	if v := version(a); v != "A" {
		t.Fatalf("first response is %s, want A", v)
	}

	rescan("B")
	ready("B")
	stale := h.get(target(hashA)) // a page still holding URL A
	wantStatus(t, stale, http.StatusOK)
	noLastModified(t, stale, "stale A, row B")
	if v := version(stale); v != "B" {
		t.Fatalf("stale request got %s, want the current B", v)
	}
	wantCache(t, stale, cacheUnversioned, "stale A, row B")
	heldETag := stale.Header().Get("ETag") // the client now holds B under URL A

	rescan("A") // restored within the same second
	ready("A")
	for _, c := range []struct {
		name   string
		header []string
	}{
		{"If-None-Match of B and If-Modified-Since", []string{"If-None-Match", heldETag, "If-Modified-Since", future}},
		{"If-None-Match of B", []string{"If-None-Match", heldETag}},
		{"If-Modified-Since only", []string{"If-Modified-Since", future}},
	} {
		rec := h.get(target(hashA), c.header...)
		if rec.Code == http.StatusNotModified {
			t.Errorf("after the restore, %s: 304 — the client would keep B under URL A", c.name)
			continue
		}
		wantStatus(t, rec, http.StatusOK)
		if v := version(rec); v != "A" {
			t.Errorf("after the restore, %s: body is %s, want A", c.name, v)
		}
		wantCache(t, rec, cacheVersioned, "after the restore, "+c.name)
		if rec.Header().Get("ETag") == heldETag {
			t.Errorf("after the restore, %s: A carries B's ETag %s", c.name, heldETag)
		}
	}
	// A validator for the bytes served now still revalidates.
	cur := h.get(target(hashA))
	if rec := h.get(target(hashA), "If-None-Match", cur.Header().Get("ETag")); rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match of the current A: status %d, want 304", rec.Code)
	}
}

func TestFull_ConditionalFollowsIdentity_FullJPEG(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	data := map[string][]byte{"A": platformtest.MakeJPEG(t, 300, 200), "B": platformtest.MakeJPEG(t, 200, 300)}
	hash := map[string]string{"A": hashA, "B": hashB}
	abaConditional(t, h,
		func(v string) string { return stripMedia(t, FullURL(h.id, rel, v)) },
		func(rec *httptest.ResponseRecorder) string {
			if w, _ := jpegSize(t, rec); w == 300 {
				return "A"
			}
			return "B"
		},
		func(v string) { h.putAt(t, rel, data[v], "heic", hash[v]) },
		func(string) {})
}

func TestFull_ConditionalFollowsIdentity_Original(t *testing.T) {
	h := newMediaHarness(t)
	rel := "A.JPG"
	data := map[string][]byte{"A": platformtest.MakeJPEG(t, 40, 30), "B": platformtest.MakeJPEG(t, 30, 40)}
	hash := map[string]string{"A": hashA, "B": hashB}
	abaConditional(t, h,
		func(v string) string { return stripMedia(t, FullURL(h.id, rel, v)) },
		func(rec *httptest.ResponseRecorder) string {
			if bytes.Equal(rec.Body.Bytes(), data["A"]) {
				return "A"
			}
			return "B"
		},
		func(v string) { h.putAt(t, rel, data[v], "jpeg", hash[v]) },
		func(string) {})
}

func TestThumb_ConditionalFollowsIdentity(t *testing.T) {
	h := newMediaHarness(t)
	rel := "A.JPG"
	thumbs := map[string][]byte{"A": platformtest.MakeJPEG(t, 32, 24), "B": platformtest.MakeJPEG(t, 24, 32)}
	hash := map[string]string{"A": hashA, "B": hashB}
	var id int64
	abaConditional(t, h,
		func(v string) string { return stripMedia(t, ThumbURL(h.id, rel, thumb.TierThumb, v)) },
		func(rec *httptest.ResponseRecorder) string {
			if bytes.Equal(rec.Body.Bytes(), thumbs["A"]) {
				return "A"
			}
			return "B"
		},
		func(v string) {
			h.putAt(t, rel, []byte("original "+v), "jpeg", hash[v])
			id = idOf(t, h, rel)
		},
		func(v string) { h.rebuildThumb(t, id, thumbs[v]) })
}

// idOf returns rel's row id.
func idOf(t *testing.T, h *mediaHarness, rel string) int64 {
	t.Helper()
	rec, err := h.s.Store.Files().GetByPath(rel)
	if err != nil {
		t.Fatalf("GetByPath %s: %v", rel, err)
	}
	return rec.ID
}

// rebuildThumb does what the thumbnail stage does for a file: write its
// thumb tier (replacing the file) and record the row, then mark the file
// "thumbed". It may run on another goroutine: it never calls Fatal.
func (h *mediaHarness) rebuildThumb(t *testing.T, fileID int64, data []byte) {
	rel, err := thumb.WriteCache(h.dir, fileID, thumb.TierThumb, data)
	if err != nil {
		t.Errorf("WriteCache: %v", err)
		return
	}
	if err := h.s.Store.Thumbs().Upsert(store.Thumb{FileID: fileID, Tier: thumb.TierThumb, RelPath: rel, Width: 1, Height: 1}); err != nil {
		t.Errorf("thumbs upsert: %v", err)
		return
	}
	if err := h.s.Store.Files().SetScanStatus(fileID, "thumbed", ""); err != nil {
		t.Errorf("set thumbed: %v", err)
	}
}

// The interleaving of finding 3: the request reads row B while B's
// thumbnails still await their rebuild, opens the old thumbnail file (A's
// pixels), and the rebuild of B completes before the request reads the
// row again. The A pixels are not long-cached under B's hash, and their
// validator cannot match B's rebuilt thumbnail.
func TestThumb_RebuildBetweenOpenAndCheckIsNotLongCached(t *testing.T) {
	h := newMediaHarness(t)
	rel := "A.JPG"
	oldThumb, newThumb := platformtest.MakeJPEG(t, 32, 24), platformtest.MakeJPEG(t, 24, 32)
	h.putAt(t, rel, []byte("original A"), "jpeg", hashA)
	id := idOf(t, h, rel)
	h.rebuildThumb(t, id, oldThumb)
	h.putAt(t, rel, []byte("original B"), "jpeg", hashB) // row B, status discovered

	var once sync.Once
	h.h.hook = func(ev string) {
		if ev == hookThumbOpened {
			once.Do(func() { h.rebuildThumb(t, id, newThumb) })
		}
	}
	rec := h.get(stripMedia(t, ThumbURL(h.id, rel, thumb.TierThumb, hashB)))
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), oldThumb) {
		t.Fatal("the test's premise does not hold: the old thumbnail file was not the one opened")
	}
	wantCache(t, rec, cacheUnversioned, "old pixels opened before the rebuild")
	oldETag := rec.Header().Get("ETag")

	h.h.hook = nil
	cur := h.get(stripMedia(t, ThumbURL(h.id, rel, thumb.TierThumb, hashB)))
	wantStatus(t, cur, http.StatusOK)
	if !bytes.Equal(cur.Body.Bytes(), newThumb) {
		t.Fatal("the rebuilt thumbnail is not served")
	}
	wantCache(t, cur, cacheVersioned, "rebuilt B")
	if cur.Header().Get("ETag") == oldETag {
		t.Errorf("the old pixels' ETag %s is the rebuilt thumbnail's", oldETag)
	}
	again := h.get(stripMedia(t, ThumbURL(h.id, rel, thumb.TierThumb, hashB)), "If-None-Match", oldETag)
	wantStatus(t, again, http.StatusOK)
	if !bytes.Equal(again.Body.Bytes(), newThumb) {
		t.Error("revalidating the old pixels' ETag did not deliver the rebuilt thumbnail")
	}
}
