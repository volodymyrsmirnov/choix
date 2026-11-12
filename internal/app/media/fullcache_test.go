package media

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// Content hashes of the synthetic originals below: 16 lowercase hex digits,
// the form the scanner stores.
const (
	hashA = "aaaaaaaaaaaaaaa1"
	hashB = "bbbbbbbbbbbbbbb2"
)

// photo writes data as the folder's file rel and inserts its photo row with
// the given format and content hash.
func (h *mediaHarness) photo(t *testing.T, rel string, data []byte, format, hash string) int64 {
	t.Helper()
	abs := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fi := statFile(t, abs)
	id, err := h.s.Store.Files().Insert(store.File{
		Path: rel, Size: fi.Size(), Mtime: fi.ModTime().Unix(), ContentHash: hash,
		Kind: "photo", Format: format, ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert %s: %v", rel, err)
	}
	return id
}

// rewrite replaces the original rel with data and records it the way a
// rescan does (UpsertOnRescan with the new content hash).
func (h *mediaHarness) rewrite(t *testing.T, rel string, data []byte, format, hash string) {
	t.Helper()
	abs := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Error(err) // may run on a transcode's goroutine: never Fatal
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		t.Error(err)
		return
	}
	run, err := h.s.Store.Files().NewWalkRun() // a fresh walk, as the scanner draws one
	if err != nil {
		t.Errorf("new walk run: %v", err)
		return
	}
	if _, _, err := h.s.Store.Files().UpsertOnRescan(store.File{
		Path: rel, Size: fi.Size(), Mtime: fi.ModTime().Unix(), ContentHash: hash,
		Kind: "photo", Format: format,
	}, run); err != nil {
		t.Errorf("rescan %s: %v", rel, err)
	}
}

// statFile is os.Stat that fails the test.
func statFile(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// fullSize requests rel's full-size image and returns its JPEG dimensions.
func (h *mediaHarness) fullSize(t *testing.T, rel, v string) (int, int) {
	t.Helper()
	rec := h.get(stripMedia(t, FullURL(h.id, rel, v)))
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("%s: Content-Type = %q, want image/jpeg", rel, ct)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("%s: body is not a JPEG: %v", rel, err)
	}
	return cfg.Width, cfg.Height
}

// The Stills fake sizes a decode from the original's own JPEG header
// (ReadPaths), so the full-size JPEG's dimensions tell which bytes it was
// made from: a 300×200 original gives 300×200, a 200×300 one 200×300.

// B1: once the original's bytes change (and a rescan records the new hash),
// /media/full serves a full-size JPEG of the new bytes, not the cached one
// of the old.
func TestFull_ChangedOriginalServesNewPixels(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "Day1/IMG.HEIC"
	h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)

	if w, ht := h.fullSize(t, rel, hashA); w != 300 || ht != 200 {
		t.Fatalf("first full-size JPEG = %dx%d, want 300x200", w, ht)
	}
	h.rewrite(t, rel, platformtest.MakeJPEG(t, 200, 300), "heic", hashB)
	if w, ht := h.fullSize(t, rel, hashB); w != 200 || ht != 300 {
		t.Errorf("after the original changed: full-size JPEG = %dx%d, want 200x300 (the new bytes)", w, ht)
	}
	// The superseded file of the old bytes is gone; only the new one stays.
	h.wantOnlyFull(t, id(t, h, rel), hashB)
}

// id returns rel's row id.
func id(t *testing.T, h *mediaHarness, rel string) int64 {
	t.Helper()
	rec, err := h.s.Store.Files().GetByPath(rel)
	if err != nil {
		t.Fatalf("GetByPath %s: %v", rel, err)
	}
	return rec.ID
}

// wantOnlyFull fails unless fileID's only full-size JPEG is the one of hash.
func (h *mediaHarness) wantOnlyFull(t *testing.T, fileID int64, hash string) {
	t.Helper()
	want := path.Base(thumb.FullJPEGRel(fileID, hash))
	if names := h.fullJPEGs(t, fileID); len(names) != 1 || names[0] != want {
		t.Errorf("full-size JPEGs of file %d = %v, want only %s", fileID, names, want)
	}
}

// B1: SQLite reuses the highest rowid, so a file added after another was
// removed (DeleteUnderPath) can get its id. It must not inherit the removed
// file's full-size JPEG.
func TestFull_ReusedIDDoesNotInheritFullJPEG(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	oldID := h.photo(t, "Old/A.HEIC", platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
	if w, ht := h.fullSize(t, "Old/A.HEIC", hashA); w != 300 || ht != 200 {
		t.Fatalf("first full-size JPEG = %dx%d, want 300x200", w, ht)
	}
	if n, err := h.s.Store.Files().DeleteUnderPath(context.Background(), "Old"); err != nil || n != 1 {
		t.Fatalf("DeleteUnderPath = %d, %v; want 1 row", n, err)
	}

	newID := h.photo(t, "New/B.HEIC", platformtest.MakeJPEG(t, 200, 300), "heic", hashB)
	if newID != oldID {
		t.Fatalf("the new row got id %d, not the reused id %d: the test's premise does not hold", newID, oldID)
	}
	if w, ht := h.fullSize(t, "New/B.HEIC", hashB); w != 200 || ht != 300 {
		t.Errorf("the new file's full-size JPEG = %dx%d, want 200x300 (its own bytes, not the removed file's)", w, ht)
	}
	h.wantOnlyFull(t, newID, hashB)
}

// B1: a full-size JPEG written under the old name, <id>-full.jpg, which
// nothing ever invalidated, is never served again.
func TestFull_OldStyleFullJPEGIsNotServed(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	id := h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
	legacy := platformtest.MakeJPEG(t, 10, 10)
	if _, err := thumb.WriteCache(h.dir, id, thumb.TierFullJPEG, legacy); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}

	if w, ht := h.fullSize(t, rel, hashA); w != 300 || ht != 200 {
		t.Errorf("full-size JPEG = %dx%d, want 300x200 (the old-style file was served)", w, ht)
	}
	// Met and replaced: the old-style file is removed with the new write.
	if _, err := os.Lstat(thumb.CachePath(h.dir, id, thumb.TierFullJPEG)); !os.IsNotExist(err) {
		t.Errorf("the old-style %d-full.jpg is still there (%v)", id, err)
	}
	h.wantOnlyFull(t, id, hashA)
}

// B2: a TIFF is stored with format "jpeg", but the web view is not handed
// its bytes: it gets a full-size JPEG, capped at 4096 on the long side.
func TestFull_TIFFIsTranscoded(t *testing.T) {
	h := newMediaHarness(t)
	for _, rel := range []string{"Scans/A.TIF", "Scans/b.tiff", "Scans/C.Tiff"} {
		tiff := []byte("II*\x00not-a-real-tiff")
		h.original(t, rel, tiff, "photo", "jpeg")
		h.stills.Props[filepath.Join(h.dir, filepath.FromSlash(rel))] = map[string]any{"PixelWidth": 8192, "PixelHeight": 256}

		rec := h.get(stripMedia(t, FullURL(h.id, rel, "h")))
		wantStatus(t, rec, http.StatusOK)
		if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("%s: Content-Type = %q, want image/jpeg", rel, ct)
		}
		if bytes.Equal(rec.Body.Bytes(), tiff) {
			t.Errorf("%s: the TIFF's own bytes were served", rel)
			continue
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
		if err != nil || cfg.Width != 4096 || cfg.Height != 128 {
			t.Errorf("%s: body = %dx%d, %v; want a 4096x128 JPEG (8192x256 capped)", rel, cfg.Width, cfg.Height, err)
		}
	}
	for _, c := range h.stills.Calls() {
		if c.Method != "Thumbnail" || c.Limit != thumb.WidthFullJPEG || c.Fit != platform.FitLongSide || c.Path == "" {
			t.Errorf("Stills call %+v; want Thumbnail(path, 4096, FitLongSide)", c)
		}
	}
	if n := len(h.stills.Calls()); n != 3 {
		t.Errorf("Stills called %d times, want 3", n)
	}
}

// B1: a transcode is keyed by the content hash as well as the id. While a
// transcode of the old bytes is held, a request after the original changed
// starts its own instead of joining it, and once both end only the new
// bytes' full-size JPEG exists: the stale transcode writes nothing (and so
// removes nothing), and its request is answered with the new bytes.
func TestFull_TranscodeForOtherBytesIsNotJoined(t *testing.T) {
	h := newMediaHarness(t)
	h.stills.ReadPaths = true
	rel := "IMG.HEIC"
	fileID := h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
	gate := make(chan struct{})
	h.stills.Gate = gate
	released := false
	defer func() {
		if !released {
			close(gate)
		}
	}()

	recA, doneA := h.getAsync(context.Background(), stripMedia(t, FullURL(h.id, rel, hashA)))
	waitDone(t, h.entered, "the transcode of the old bytes")
	h.rewrite(t, rel, platformtest.MakeJPEG(t, 200, 300), "heic", hashB)
	recB, doneB := h.getAsync(context.Background(), stripMedia(t, FullURL(h.id, rel, hashB)))
	waitDone(t, h.entered, "a second transcode, for the new bytes")
	close(gate)
	released = true
	waitDone(t, doneA, "the request for the old bytes")
	waitDone(t, doneB, "the request for the new bytes")

	for name, rec := range map[string]*httptest.ResponseRecorder{"old": recA, "new": recB} {
		wantStatus(t, rec, http.StatusOK)
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
		if err != nil || cfg.Width != 200 || cfg.Height != 300 {
			t.Errorf("%s request: body = %dx%d, %v; want the new bytes' 200x300", name, cfg.Width, cfg.Height, err)
		}
	}
	if n := len(h.stills.Calls()); n != 2 {
		t.Errorf("Stills called %d times, want 2 (one per content hash)", n)
	}
	h.wantOnlyFull(t, fileID, hashB)
}

// B1: a transcode whose row stops describing its source while it runs —
// the original was rewritten and rescanned, or the row was removed and its
// id given to another file — writes nothing. Its request looks the file up
// again: the rewritten file is served from its new bytes, the removed one
// is not found.
func TestFull_StaleTranscodeWritesNothing(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(t *testing.T, h *mediaHarness, rel string)
		status int
	}{
		{"rewritten", func(t *testing.T, h *mediaHarness, rel string) {
			h.rewrite(t, rel, platformtest.MakeJPEG(t, 200, 300), "heic", hashB)
		}, http.StatusOK},
		{"id reused", func(t *testing.T, h *mediaHarness, rel string) {
			if _, err := h.s.Store.Files().DeleteUnderPath(context.Background(), rel); err != nil {
				t.Errorf("DeleteUnderPath: %v", err)
			}
			h.photo(t, "Other.HEIC", platformtest.MakeJPEG(t, 200, 300), "heic", hashB)
		}, http.StatusNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newMediaHarness(t)
			h.stills.ReadPaths = true
			rel := "IMG.HEIC"
			fileID := h.photo(t, rel, platformtest.MakeJPEG(t, 300, 200), "heic", hashA)
			var once sync.Once
			h.h.hook = func(ev string) {
				if ev == hookStart {
					// On the transcode's goroutine, before it decodes.
					once.Do(func() { c.change(t, h, rel) })
				}
			}

			rec := h.get(stripMedia(t, FullURL(h.id, rel, hashA)))
			wantStatus(t, rec, c.status)
			if c.status == http.StatusOK {
				cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
				if err != nil || cfg.Width != 200 || cfg.Height != 300 {
					t.Errorf("body = %dx%d, %v; want the new bytes' 200x300", cfg.Width, cfg.Height, err)
				}
				h.wantOnlyFull(t, fileID, hashB)
				return
			}
			if names := h.fullJPEGs(t, fileID); len(names) != 0 {
				t.Errorf("full-size JPEGs = %v; the stale transcode wrote one", names)
			}
		})
	}
}
