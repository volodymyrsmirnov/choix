package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// mediaTestTimeout bounds every wait for something that must happen.
const mediaTestTimeout = 10 * time.Second

// enteredStills reports every Thumbnail call on entered before handing it
// to the fake, so a test knows a (gated) transcode is in progress.
type enteredStills struct {
	*platformtest.Stills
	entered chan struct{}
}

func (s enteredStills) Thumbnail(ctx context.Context, in platform.ImageInput, limit int, fit platform.Fit) (platform.Image, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	return s.Stills.Thumbnail(ctx, in, limit, fit)
}

// mediaHarness is a Handler over a real Library and session Manager with
// the platform fakes, and one folder whose session the "UI" holds open.
type mediaHarness struct {
	lib        *library.Library
	mgr        *session.Manager
	stills     *platformtest.Stills
	entered    chan struct{}
	gate       *opgate.Gate
	root       context.Context
	cancelRoot context.CancelFunc
	h          *Handler

	id, dir   string // the folder's id and root
	s         *session.Session
	releaseUI func()
	previews  string // the previews base directory: <previews>/<id>/<n>.jpg
}

func newMediaHarness(t *testing.T) *mediaHarness {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := library.OpenDB(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vols := &platformtest.Volumes{}
	vols.Add("/", platform.VolumeInfo{UUID: "UUID-TEST", Name: "Test HD"})
	lib := library.New(library.Options{DB: db, Bookmarks: &platformtest.Bookmarks{}, Volumes: vols})

	stills := &platformtest.Stills{Props: map[string]map[string]any{}, Fail: map[string]error{}}
	mgr := session.NewManager(session.Deps{Library: lib, Stills: stills, Video: &platformtest.Video{}})

	h := &mediaHarness{
		lib:      lib,
		mgr:      mgr,
		stills:   stills,
		entered:  make(chan struct{}, 64),
		gate:     &opgate.Gate{},
		previews: t.TempDir(),
	}
	h.root, h.cancelRoot = context.WithCancel(context.Background())
	h.h = New(h.root, mgr, enteredStills{Stills: stills, entered: h.entered}, func(id string) (string, error) {
		return filepath.Join(h.previews, id), nil
	}, h.gate)

	h.dir = filepath.Join(parent, "Trip")
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := lib.Add(h.dir)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	h.id = res.ID
	h.s, h.releaseUI, err = mgr.Acquire(h.id)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(func() {
		h.releaseUI()
		h.cancelRoot()
		ctx, cancel := context.WithTimeout(context.Background(), mediaTestTimeout)
		defer cancel()
		if n := mgr.Shutdown(ctx); n != 0 {
			t.Errorf("cleanup: %d sessions still open (a reference was not released)", n)
		}
	})
	return h
}

// original writes data as the folder's file rel and inserts its row.
func (h *mediaHarness) original(t *testing.T, rel string, data []byte, kind, format string) int64 {
	t.Helper()
	abs := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fi := statFile(t, abs)
	return h.insert(t, rel, kind, format, fi.Size(), fi.ModTime().Unix())
}

// row inserts a files row for rel without touching the disk. Its content
// hash is "h-" + rel.
func (h *mediaHarness) row(t *testing.T, rel, kind, format string) int64 {
	t.Helper()
	return h.insert(t, rel, kind, format, 1, time.Now().Unix())
}

// insert inserts an analyzed files row for rel with content hash "h-" + rel.
func (h *mediaHarness) insert(t *testing.T, rel, kind, format string, size, mtime int64) int64 {
	t.Helper()
	id, err := h.s.Store.Files().Insert(store.File{
		Path: rel, Size: size, Mtime: mtime, ContentHash: "h-" + rel,
		Kind: kind, Format: format, ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert %s: %v", rel, err)
	}
	return id
}

// heic writes a fake HEIC original whose decode the Stills fake sizes as
// 400×300, and inserts its row.
func (h *mediaHarness) heic(t *testing.T, rel string) int64 {
	t.Helper()
	id := h.original(t, rel, []byte("not-real-heic"), "photo", "heic")
	h.stills.Props[filepath.Join(h.dir, filepath.FromSlash(rel))] = map[string]any{"PixelWidth": 400, "PixelHeight": 300}
	return id
}

// cache writes data as fileID's tier cache file and records its thumbs row;
// a full-size JPEG is written under the row's current content hash and has
// no thumbs row.
func (h *mediaHarness) cache(t *testing.T, fileID int64, tier string, data []byte) string {
	t.Helper()
	if tier == thumb.TierFullJPEG {
		rel, err := thumb.WriteFullJPEG(h.dir, fileID, h.hashOf(t, fileID), data)
		if err != nil {
			t.Fatalf("WriteFullJPEG: %v", err)
		}
		return rel
	}
	rel, err := thumb.WriteCache(h.dir, fileID, tier, data)
	if err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	h.thumbRow(t, fileID, tier, rel)
	return rel
}

// hashOf returns the content hash fileID's row has now.
func (h *mediaHarness) hashOf(t *testing.T, fileID int64) string {
	t.Helper()
	rec, err := h.s.Store.Files().GetByID(fileID)
	if err != nil {
		t.Fatalf("GetByID %d: %v", fileID, err)
	}
	return rec.ContentHash
}

// fullPath is the absolute path of fileID's full-size JPEG for the row's
// current content hash.
func (h *mediaHarness) fullPath(t *testing.T, fileID int64) string {
	t.Helper()
	return filepath.Join(h.dir, filepath.FromSlash(thumb.FullJPEGRel(fileID, h.hashOf(t, fileID))))
}

// fullJPEGs lists the full-size JPEGs of fileID in its cache bucket, of any
// content hash and of the old hash-less style. It needs no session.
func (h *mediaHarness) fullJPEGs(t *testing.T, fileID int64) []string {
	t.Helper()
	bucket := filepath.Dir(thumb.CachePath(h.dir, fileID, thumb.TierFullJPEG))
	entries, err := os.ReadDir(bucket)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("list %s: %v", bucket, err)
	}
	prefix := strconv.FormatInt(fileID, 10) + "-full"
	var names []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".jpg") {
			names = append(names, n)
		}
	}
	return names
}

func (h *mediaHarness) thumbRow(t *testing.T, fileID int64, tier, rel string) {
	t.Helper()
	if err := h.s.Store.Thumbs().Insert(store.Thumb{FileID: fileID, Tier: tier, RelPath: rel, Width: 1, Height: 1}); err != nil {
		t.Fatalf("thumbs insert: %v", err)
	}
}

// preview writes the folder's strip image n.
func (h *mediaHarness) preview(t *testing.T, n int, data []byte) {
	t.Helper()
	dir := filepath.Join(h.previews, h.id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.jpg", n)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// serve sends a request for target (as the handler sees it, /media
// stripped) with the given header pairs.
func (h *mediaHarness) serve(method, target string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

// get is serve("GET", …).
func (h *mediaHarness) get(target string, header ...string) *httptest.ResponseRecorder {
	return h.serve(http.MethodGet, target, header...)
}

// stripMedia turns a URL helper's result into what the handler receives.
func stripMedia(t *testing.T, u string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(u, "/media")
	if !ok {
		t.Fatalf("URL %q does not start with /media", u)
	}
	return rest
}

// wailsMount serves h the way Wails' asset server mounts a service with
// Route "/media": a strings.HasPrefix match, then http.StripPrefix.
func wailsMount(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/media") {
			http.StripPrefix("/media", h).ServeHTTP(w, r)
			return
		}
		http.Error(w, "asset", http.StatusTeapot)
	})
}

// assertReleased fails when anything still holds a reference to the
// folder's session (the UI's must have been released before).
func (h *mediaHarness) assertReleased(t *testing.T) {
	t.Helper()
	if _, release, ok := h.mgr.AcquireOpen(h.id); ok {
		release()
		t.Fatal("the session is still referenced after the request ended")
	}
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		body := rec.Body.String()
		if len(body) > 120 {
			body = body[:120] + "…"
		}
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, want, body)
	}
}

func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(mediaTestTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// ── thumb ──────────────────────────────────────────────────────────────────

func TestThumb_ServesCachedJPEG(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "Day1/A.JPG", []byte("original"), "photo", "jpeg")
	data := platformtest.MakeJPEG(t, 256, 171)
	h.cache(t, id, thumb.TierThumb, data)

	rec := h.get(stripMedia(t, ThumbURL(h.id, "Day1/A.JPG", "thumb", "h-Day1/A.JPG"))) // the row's hash
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Error("missing ETag")
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("body is not the cached thumbnail")
	}

	again := h.get(stripMedia(t, ThumbURL(h.id, "day1/a.jpg", "thumb", "h")), "If-None-Match", etag)
	wantStatus(t, again, http.StatusNotModified)

	head := h.serve(http.MethodHead, stripMedia(t, ThumbURL(h.id, "Day1/A.JPG", "thumb", "h")))
	wantStatus(t, head, http.StatusOK)
	if head.Body.Len() != 0 {
		t.Errorf("HEAD body = %d bytes", head.Body.Len())
	}
}

func TestThumb_DefaultsToThumbTier(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("original"), "photo", "jpeg")
	data := platformtest.MakeJPEG(t, 16, 16)
	h.cache(t, id, thumb.TierThumb, data)
	h.cache(t, id, thumb.TierPreview, platformtest.MakeJPEG(t, 32, 32))

	rec := h.get("/thumb/" + h.id + "/A.JPG")
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("without a tier the thumb tier must be served")
	}
}

func TestThumb_PreviewTier(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("original"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 16, 16))
	data := platformtest.MakeJPEG(t, 32, 32)
	h.cache(t, id, thumb.TierPreview, data)

	rec := h.get("/thumb/" + h.id + "/A.JPG?tier=preview")
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("the preview tier was not served")
	}
}

func TestThumb_MissingIs404(t *testing.T) {
	h := newMediaHarness(t)
	wantStatus(t, h.get("/thumb/"+h.id+"/Day1/Nope.JPG?tier=thumb"), http.StatusNotFound)

	h.original(t, "NoThumb.JPG", []byte("x"), "photo", "jpeg")
	wantStatus(t, h.get("/thumb/"+h.id+"/NoThumb.JPG"), http.StatusNotFound)

	id := h.original(t, "Gone.JPG", []byte("x"), "photo", "jpeg")
	rel := h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	if err := os.Remove(filepath.Join(h.dir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, h.get("/thumb/"+h.id+"/Gone.JPG"), http.StatusNotFound)
}

func TestThumb_BadTierIs400(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	for _, tier := range []string{"banana", "full", "THUMB"} {
		wantStatus(t, h.get("/thumb/"+h.id+"/A.JPG?tier="+tier), http.StatusBadRequest)
	}
}

func TestThumb_FolderNotOpenIs404(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	wantStatus(t, h.get("/thumb/"+h.id+"/A.JPG"), http.StatusOK)

	h.releaseUI()
	wantStatus(t, h.get("/thumb/"+h.id+"/A.JPG"), http.StatusNotFound)
	wantStatus(t, h.get("/full/"+h.id+"/A.JPG"), http.StatusNotFound)
	// A well-formed id the Library does not know.
	wantStatus(t, h.get("/thumb/ffffffffffffffff/A.JPG"), http.StatusNotFound)
}

func TestThumb_BadFolderID(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "x/A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	id2 := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id2, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))

	for _, target := range []string{
		"/thumb/../x/A.JPG",
		"/thumb/..%2Fx/A.JPG",
		"/thumb/" + strings.ToUpper(h.id) + "/A.JPG",
		"/thumb/ABCDEF0123456789/A.JPG",
		"/thumb/" + h.id[:15] + "/A.JPG",
		"/thumb/" + h.id + "0/A.JPG",
		"/thumb/0123456789abcdeg/A.JPG",
		"/thumb//A.JPG",
	} {
		t.Run(target, func(t *testing.T) {
			wantStatus(t, h.get(target), http.StatusNotFound)
		})
	}
}

func TestThumb_TraversalIs404(t *testing.T) {
	h := newMediaHarness(t)
	// Rows a naive handler would find: one whose path climbs out of the
	// folder and one absolute, both with a working thumbnail.
	outside := filepath.Join(filepath.Dir(h.dir), "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../secret.jpg", outside, "etc/passwd"} {
		id := h.row(t, rel, "photo", "jpeg")
		h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	}
	id := h.original(t, "Day1/A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))

	for _, route := range []string{"thumb", "full"} {
		for _, rest := range []string{
			"../secret.jpg",
			"%2e%2e/secret.jpg",
			"..%2Fsecret.jpg",
			"%2E%2E%2Fsecret.jpg",
			"Day1/../../secret.jpg",
			"../../etc/passwd",
			"%2e%2e%2f%2e%2e%2fetc%2fpasswd",
			"..%2F..%2Fetc%2Fpasswd",
			"/etc/passwd",
			"%2Fetc%2Fpasswd",
			"/" + strings.TrimPrefix(outside, "/"),
			"./Day1/A.JPG",
			"Day1//A.JPG",
			"Day1/A.JPG/",
			".",
			"",
		} {
			target := "/" + route + "/" + h.id + "/" + rest
			t.Run(target, func(t *testing.T) {
				wantStatus(t, h.get(target), http.StatusNotFound)
			})
		}
	}
}

// A thumbs row naming a path outside the cache, or a cache entry that is a
// symbolic link (to a file or as a directory), is refused.
func TestThumb_SymlinkedCacheEscapesIs403(t *testing.T) {
	h := newMediaHarness(t)
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.jpg")
	if err := os.WriteFile(outside, platformtest.MakeJPEG(t, 8, 8), 0o644); err != nil {
		t.Fatal(err)
	}

	// A symlinked cache file pointing outside the folder.
	id := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	bucket := filepath.Join(h.dir, ".choix", "thumbs", "00")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bucket, "symlinked.jpg")); err != nil {
		t.Fatal(err)
	}
	h.thumbRow(t, id, thumb.TierThumb, ".choix/thumbs/00/symlinked.jpg")
	wantStatus(t, h.get("/thumb/"+h.id+"/A.JPG"), http.StatusForbidden)

	// A symlinked bucket directory pointing outside the folder.
	id2 := h.original(t, "B.JPG", []byte("x"), "photo", "jpeg")
	if err := os.Symlink(outsideDir, filepath.Join(h.dir, ".choix", "thumbs", "ee")); err != nil {
		t.Fatal(err)
	}
	h.thumbRow(t, id2, thumb.TierThumb, ".choix/thumbs/ee/secret.jpg")
	wantStatus(t, h.get("/thumb/"+h.id+"/B.JPG"), http.StatusForbidden)

	// A corrupt row climbing out of the folder, and one outside the cache.
	id3 := h.original(t, "C.JPG", []byte("x"), "photo", "jpeg")
	h.thumbRow(t, id3, thumb.TierThumb, "../../escape")
	wantStatus(t, h.get("/thumb/"+h.id+"/C.JPG"), http.StatusForbidden)
	id4 := h.original(t, "D.JPG", platformtest.MakeJPEG(t, 8, 8), "photo", "jpeg")
	h.thumbRow(t, id4, thumb.TierThumb, "D.JPG")
	wantStatus(t, h.get("/thumb/"+h.id+"/D.JPG"), http.StatusForbidden)
}

func TestThumb_VideoPreviewFallsBackToThumb(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "clip.mov", []byte("video"), "video", "mov")
	data := platformtest.MakeJPEG(t, 16, 9)
	h.cache(t, id, thumb.TierThumb, data)

	rec := h.get("/thumb/" + h.id + "/clip.mov?tier=preview")
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("a video without a preview row must get its thumb row")
	}

	// Photos do not fall back.
	pid := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, pid, thumb.TierThumb, data)
	wantStatus(t, h.get("/thumb/"+h.id+"/A.JPG?tier=preview"), http.StatusNotFound)
}

// ── full ───────────────────────────────────────────────────────────────────

func TestFull_StreamsOriginalReadOnly(t *testing.T) {
	h := newMediaHarness(t)
	for _, c := range []struct{ rel, format, ct string }{
		{"Day1/A.JPG", "jpeg", "image/jpeg"},
		{"Day1/B.png", "png", "image/png"},
	} {
		data := platformtest.MakeJPEG(t, 40, 30)
		h.original(t, c.rel, data, "photo", c.format)
		rec := h.get(stripMedia(t, FullURL(h.id, c.rel, "h-"+c.rel))) // the row's hash
		wantStatus(t, rec, http.StatusOK)
		if ct := rec.Header().Get("Content-Type"); ct != c.ct {
			t.Errorf("%s: Content-Type = %q, want %q", c.rel, ct, c.ct)
		}
		if !bytes.Equal(rec.Body.Bytes(), data) {
			t.Errorf("%s: body is not the original", c.rel)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
			t.Errorf("%s: Cache-Control with a version = %q", c.rel, cc)
		}
	}
	rec := h.get("/full/" + h.id + "/Day1/A.JPG")
	wantStatus(t, rec, http.StatusOK)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control without a version = %q, want no-cache", cc)
	}
	wantStatus(t, h.get("/full/"+h.id+"/Day1/Nope.JPG"), http.StatusNotFound)
}

func TestFull_VideoRange(t *testing.T) {
	h := newMediaHarness(t)
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	h.original(t, "clip.mp4", data, "video", "mp4")
	h.original(t, "clip.mov", data, "video", "mov")
	h.original(t, "clip.m4v", data, "video", "mp4")

	rec := h.get(stripMedia(t, FullURL(h.id, "clip.mp4", "h")), "Range", "bytes=10-19")
	wantStatus(t, rec, http.StatusPartialContent)
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 10-19/100" {
		t.Errorf("Content-Range = %q", cr)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), data[10:20]) {
		t.Errorf("body = %v", rec.Body.Bytes())
	}

	for rel, ct := range map[string]string{"clip.mov": "video/quicktime", "clip.m4v": "video/mp4"} {
		rec := h.get("/full/"+h.id+"/"+rel, "Range", "bytes=0-1")
		wantStatus(t, rec, http.StatusPartialContent)
		if got := rec.Header().Get("Content-Type"); got != ct {
			t.Errorf("%s: Content-Type = %q, want %q", rel, got, ct)
		}
	}

	full := h.get("/full/" + h.id + "/clip.mov")
	wantStatus(t, full, http.StatusOK)
	if full.Header().Get("Accept-Ranges") != "bytes" || !bytes.Equal(full.Body.Bytes(), data) {
		t.Errorf("whole video: Accept-Ranges %q, %d bytes", full.Header().Get("Accept-Ranges"), full.Body.Len())
	}
}

func TestContentTypeFor(t *testing.T) {
	for format, want := range map[string]string{
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"heic": "image/heic",
		"mov":  "video/quicktime",
		"mp4":  "video/mp4",
		"raf":  "application/octet-stream",
		"":     "application/octet-stream",
		"avi":  "application/octet-stream",
	} {
		if got := contentTypeFor(format); got != want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestFull_HEICServesCachedTranscode(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "Day1/IMG.HEIC", []byte("not-real-heic"), "photo", "heic")
	want := platformtest.MakeJPEG(t, 64, 48)
	h.cache(t, id, thumb.TierFullJPEG, want)

	rec := h.get(stripMedia(t, FullURL(h.id, "Day1/IMG.HEIC", "h")))
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Error("body is not the cached transcode")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if n := len(h.stills.Calls()); n != 0 {
		t.Errorf("Stills called %d times for a cached transcode", n)
	}
}

func TestFull_HEICTranscodesOnce(t *testing.T) {
	h := newMediaHarness(t)
	rel := "Day1/IMG.HEIC"
	id := h.original(t, rel, []byte("not-real-heic"), "photo", "heic")
	h.stills.Props[filepath.Join(h.dir, rel)] = map[string]any{"PixelWidth": 8000, "PixelHeight": 100}
	gate := make(chan struct{})
	h.stills.Gate = gate
	joined := make(chan struct{}, 16)
	h.h.hook = func(ev string) {
		if ev == hookJoin {
			joined <- struct{}{}
		}
	}

	const n = 5
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { recs[i] = h.get(stripMedia(t, FullURL(h.id, rel, "h"))) })
	}
	for range n {
		select {
		case <-joined:
		case <-time.After(mediaTestTimeout):
			t.Fatal("not every request joined the transcode")
		}
	}
	close(gate)
	wg.Wait()

	cached, err := os.ReadFile(h.fullPath(t, id))
	if err != nil {
		t.Fatalf("cache file: %v", err)
	}
	if names := h.fullJPEGs(t, id); len(names) != 1 {
		t.Errorf("full-size JPEGs of the file = %v, want exactly one", names)
	}
	for i, rec := range recs {
		wantStatus(t, rec, http.StatusOK)
		if !bytes.Equal(rec.Body.Bytes(), cached) {
			t.Errorf("request %d: body is not the cached transcode", i)
		}
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(cached))
	if err != nil || cfg.Width != 4096 || cfg.Height != 51 {
		t.Errorf("transcode = %dx%d, %v; want 4096x51", cfg.Width, cfg.Height, err)
	}
	calls := h.stills.Calls()
	if len(calls) != 1 || calls[0].Limit != thumb.WidthFullJPEG || calls[0].Fit != platform.FitLongSide {
		t.Fatalf("Stills calls = %+v; want one (4096, FitLongSide)", calls)
	}
}

func TestFull_RAFTranscodesByWidth(t *testing.T) {
	h := newMediaHarness(t)
	src, err := os.ReadFile(platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 5000, 100)))
	if err != nil {
		t.Fatal(err)
	}
	h.original(t, "Day1/T.RAF", src, "photo", "raf")

	rec := h.get("/full/" + h.id + "/Day1/T.RAF")
	wantStatus(t, rec, http.StatusOK)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || cfg.Width != 4096 {
		t.Errorf("transcode = %dx%d, %v; want 4096 wide", cfg.Width, cfg.Height, err)
	}
	calls := h.stills.Calls()
	if len(calls) != 1 || calls[0].Limit != thumb.WidthFullJPEG || calls[0].Fit != platform.FitWidth || calls[0].Data == nil {
		t.Fatalf("Stills calls = %+v; want one (4096, FitWidth) on the embedded JPEG", calls)
	}
}

func TestFull_RootContextCancelsTranscode(t *testing.T) {
	h := newMediaHarness(t)
	rel := "IMG.HEIC"
	id := h.heic(t, rel)
	gate := make(chan struct{})
	h.stills.Gate = gate
	defer close(gate)

	var rec *httptest.ResponseRecorder
	done := make(chan struct{})
	go func() {
		defer close(done)
		rec = h.get("/full/" + h.id + "/" + rel)
	}()
	waitDone(t, h.entered, "the transcode")
	h.cancelRoot()
	waitDone(t, done, "the request after the root context was cancelled")

	if rec.Code < 500 {
		t.Fatalf("status = %d, want an error", rec.Code)
	}
	h.h.mu.Lock()
	flights := len(h.h.flights)
	h.h.mu.Unlock()
	if flights != 0 {
		t.Errorf("%d transcodes still registered", flights)
	}
	h.releaseUI()
	h.assertReleased(t)
	if names := h.fullJPEGs(t, id); len(names) != 0 {
		t.Errorf("a cancelled transcode wrote its cache file: %v", names)
	}
}

// The last request waiting for a transcode that gives up cancels it: the
// request returns without waiting for the native call, nothing is cached
// and nothing keeps the session.
func TestFull_LastRequestGivingUpCancelsTranscode(t *testing.T) {
	h := newMediaHarness(t)
	id := h.heic(t, "IMG.HEIC")
	gate := make(chan struct{})
	h.stills.Gate = gate
	defer close(gate)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/full/"+h.id+"/IMG.HEIC", nil).WithContext(ctx))
	}()
	waitDone(t, h.entered, "the transcode")
	cancel()
	waitDone(t, done, "the cancelled request")

	h.h.mu.Lock()
	flights := len(h.h.flights)
	h.h.mu.Unlock()
	if flights != 0 {
		t.Errorf("%d transcodes still registered", flights)
	}
	if names := h.fullJPEGs(t, id); len(names) != 0 {
		t.Errorf("an abandoned transcode wrote its cache file: %v", names)
	}
	h.releaseUI()
	h.assertReleased(t)
}

// A request that gives up leaves a shared transcode to the others.
func TestFull_TranscodeSurvivesOneRequestGivingUp(t *testing.T) {
	h := newMediaHarness(t)
	h.heic(t, "IMG.HEIC")
	gate := make(chan struct{})
	h.stills.Gate = gate
	joined := make(chan struct{}, 4)
	h.h.hook = func(ev string) {
		if ev == hookJoin {
			joined <- struct{}{}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	quitter := make(chan struct{})
	go func() {
		defer close(quitter)
		h.h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/full/"+h.id+"/IMG.HEIC", nil).WithContext(ctx))
	}()
	waitDone(t, joined, "the first request")
	var rec *httptest.ResponseRecorder
	stayer := make(chan struct{})
	go func() {
		defer close(stayer)
		rec = h.get("/full/" + h.id + "/IMG.HEIC")
	}()
	waitDone(t, joined, "the second request")
	cancel()
	waitDone(t, quitter, "the request that gave up")
	close(gate)
	waitDone(t, stayer, "the request that stayed")

	wantStatus(t, rec, http.StatusOK)
	if _, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Errorf("body is not a JPEG: %v", err)
	}
	if n := len(h.stills.Calls()); n != 1 {
		t.Errorf("Stills called %d times, want 1", n)
	}
}

func TestFull_TranscodeFailureFallsBackToPreview(t *testing.T) {
	h := newMediaHarness(t)
	rel := "IMG.HEIC"
	id := h.original(t, rel, []byte("not-real-heic"), "photo", "heic")
	h.stills.Fail[filepath.Join(h.dir, rel)] = errors.New("decode failed")
	preview := platformtest.MakeJPEG(t, 32, 24)
	h.cache(t, id, thumb.TierPreview, preview)

	rec := h.get("/full/" + h.id + "/" + rel)
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), preview) {
		t.Error("body is not the preview tier")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}

	// Without a preview it is an error.
	rel2 := "IMG2.HEIC"
	h.original(t, rel2, []byte("not-real-heic"), "photo", "heic")
	h.stills.Fail[filepath.Join(h.dir, rel2)] = errors.New("decode failed")
	wantStatus(t, h.get("/full/"+h.id+"/"+rel2), http.StatusInternalServerError)
}

// probeWriter runs probe once, when the response starts being written.
type probeWriter struct {
	*httptest.ResponseRecorder
	once  sync.Once
	probe func()
}

func (p *probeWriter) WriteHeader(code int) {
	p.once.Do(p.probe)
	p.ResponseRecorder.WriteHeader(code)
}

func (p *probeWriter) Write(b []byte) (int, error) {
	p.once.Do(p.probe)
	return p.ResponseRecorder.Write(b)
}

func TestFull_TranscodeHoldsSession(t *testing.T) {
	h := newMediaHarness(t)
	rel := "IMG.HEIC"
	id := h.heic(t, rel)
	gate := make(chan struct{})
	h.stills.Gate = gate

	var heldWhileWriting bool
	w := &probeWriter{ResponseRecorder: httptest.NewRecorder(), probe: func() {
		s, release, ok := h.mgr.AcquireOpen(h.id)
		if ok {
			_, err := s.Store.Files().GetByID(id)
			heldWhileWriting = err == nil
			release()
		}
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/full/"+h.id+"/"+rel, nil))
	}()
	waitDone(t, h.entered, "the transcode")
	h.releaseUI() // the UI closes the folder meanwhile
	if _, release, ok := h.mgr.AcquireOpen(h.id); !ok {
		t.Error("the transcoding request holds no session reference")
	} else {
		release()
	}
	close(gate)
	waitDone(t, done, "the request")

	wantStatus(t, w.ResponseRecorder, http.StatusOK)
	if !heldWhileWriting {
		t.Error("the store was not open, or not referenced, while the response was written")
	}
	h.assertReleased(t)
}

// Originals are only ever read: served from a read-only directory, they are
// unchanged, and the package's code has no way of opening a file to write.
func TestFull_NeverOpensOriginalForWrite(t *testing.T) {
	h := newMediaHarness(t)
	jpg := platformtest.MakeJPEG(t, 40, 30)
	video := bytes.Repeat([]byte("v"), 1000)
	h.original(t, "Day1/A.JPG", jpg, "photo", "jpeg")
	h.original(t, "Day1/clip.mov", video, "video", "mov")
	h.heic(t, "Day1/IMG.HEIC")

	day := filepath.Join(h.dir, "Day1")
	before := map[string]os.FileInfo{}
	for _, name := range []string{"A.JPG", "clip.mov", "IMG.HEIC"} {
		p := filepath.Join(day, name)
		if err := os.Chmod(p, 0o444); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = fi
	}
	if err := os.Chmod(day, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(day, 0o755) })

	wantStatus(t, h.get("/full/"+h.id+"/Day1/A.JPG"), http.StatusOK)
	wantStatus(t, h.get("/full/"+h.id+"/Day1/clip.mov", "Range", "bytes=0-9"), http.StatusPartialContent)
	wantStatus(t, h.get("/full/"+h.id+"/Day1/IMG.HEIC"), http.StatusOK)

	for name, fi := range before {
		after, err := os.Stat(filepath.Join(day, name))
		if err != nil {
			t.Fatal(err)
		}
		if after.Size() != fi.Size() || !after.ModTime().Equal(fi.ModTime()) || after.Mode() != fi.Mode() {
			t.Errorf("%s changed: %v %v %v → %v %v %v", name, fi.Size(), fi.ModTime(), fi.Mode(), after.Size(), after.ModTime(), after.Mode())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(day, "A.JPG")); !bytes.Equal(got, jpg) {
		t.Error("A.JPG's bytes changed")
	}
	entries, err := os.ReadDir(day)
	if err != nil || len(entries) != 3 {
		t.Errorf("Day1 holds %d entries (%v), want the 3 originals", len(entries), err)
	}

	assertNoWriteOpens(t)
}

// assertNoWriteOpens parses the package's non-test files and fails on any
// call or flag that can create, truncate or write a file by path.
func assertNoWriteOpens(t *testing.T) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"Create": true, "WriteFile": true, "OpenFile": true, "Truncate": true, "Rename": true,
		"O_WRONLY": true, "O_RDWR": true, "O_CREAT": true, "O_CREATE": true, "O_TRUNC": true, "O_APPEND": true,
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && (pkg.Name == "os" || pkg.Name == "unix" || pkg.Name == "syscall") && forbidden[sel.Sel.Name] {
				t.Errorf("%s uses %s.%s", name, pkg.Name, sel.Sel.Name)
			}
			return true
		})
	}
}

// A symbolic link inside the folder that leads outside it is refused; one
// that stays inside is followed.
func TestFull_SymlinkOutsideFolderIs403(t *testing.T) {
	h := newMediaHarness(t)
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.dir, "link.jpg")); err != nil {
		t.Fatal(err)
	}
	h.row(t, "link.jpg", "photo", "jpeg")
	wantStatus(t, h.get("/full/"+h.id+"/link.jpg"), http.StatusForbidden)

	data := []byte("inside")
	h.original(t, "real.jpg", data, "photo", "jpeg")
	if err := os.Symlink(filepath.Join(h.dir, "real.jpg"), filepath.Join(h.dir, "alias.jpg")); err != nil {
		t.Fatal(err)
	}
	h.row(t, "alias.jpg", "photo", "jpeg")
	rec := h.get("/full/" + h.id + "/alias.jpg")
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("a symlink inside the folder was not followed")
	}

	// A directory or a FIFO under a file's name is refused, never waited on.
	if err := os.Mkdir(filepath.Join(h.dir, "dir.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.row(t, "dir.jpg", "photo", "jpeg")
	wantStatus(t, h.get("/full/"+h.id+"/dir.jpg"), http.StatusNotFound)
}

// mkfifo replaces the folder's file rel with a FIFO, which a blocking open
// would wait on forever (no writer ever comes).
func (h *mediaHarness) mkfifo(t *testing.T, rel string) {
	t.Helper()
	p := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
}

// getAsync serves target on its own goroutine with ctx and returns a channel
// closed once the response is complete.
func (h *mediaHarness) getAsync(ctx context.Context, target string) (*httptest.ResponseRecorder, <-chan struct{}) {
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx))
	}()
	return rec, done
}

// A stored original that is a FIFO — served directly or as a transcode
// source (RAF, DNG and HEIC are opened by path) — is answered 404 at once
// and never waited on.
func TestFull_FIFOOriginalIs404(t *testing.T) {
	h := newMediaHarness(t)
	var mu sync.Mutex
	var events []string
	h.h.hook = func(ev string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if len(events) != 0 {
			t.Errorf("a transcode was started for a FIFO source: %v", events)
		}
	})
	for _, c := range []struct{ rel, kind, format string }{
		{"Day1/T.RAF", "photo", "raf"},
		{"Day1/T.DNG", "photo", "dng"},
		{"Day1/IMG.HEIC", "photo", "heic"},
		{"Day1/A.JPG", "photo", "jpeg"},
		{"Day1/clip.mov", "video", "mov"},
	} {
		t.Run(c.rel, func(t *testing.T) {
			h.mkfifo(t, c.rel)
			h.row(t, c.rel, c.kind, c.format)
			rec, done := h.getAsync(context.Background(), "/full/"+h.id+"/"+c.rel)
			waitDone(t, done, "the request for a FIFO")
			wantStatus(t, rec, http.StatusNotFound)
		})
	}
	if n := len(h.stills.Calls()); n != 0 {
		t.Errorf("Stills called %d times for FIFO sources", n)
	}
	if !h.gate.Close(0) {
		t.Error("a request is still inside the gate")
	}
	h.releaseUI()
	h.assertReleased(t)
}

// A transcode source that turns into a FIFO while the transcode waits for a
// slot is refused right before the decoder opens it, so neither the request
// nor a cancellation (of the request or of the root context) ever waits on
// it, and the gate entry and the session reference are released.
func TestFull_FIFOSwappedInDuringTranscode(t *testing.T) {
	for _, cancelBy := range []string{"none", "request", "root"} {
		t.Run(cancelBy, func(t *testing.T) {
			h := newMediaHarness(t)
			src, err := os.ReadFile(platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 64, 48)))
			if err != nil {
				t.Fatal(err)
			}
			id := h.original(t, "Day1/T.RAF", src, "photo", "raf")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.h.hook = func(ev string) {
				if ev != hookStart {
					return
				}
				// On the transcode's goroutine: report, never Fatal.
				p := filepath.Join(h.dir, "Day1", "T.RAF")
				if err := os.Remove(p); err != nil {
					t.Errorf("remove: %v", err)
				}
				if err := syscall.Mkfifo(p, 0o644); err != nil {
					t.Errorf("mkfifo: %v", err)
				}
				switch cancelBy {
				case "request":
					cancel()
				case "root":
					h.cancelRoot()
				}
			}

			rec, done := h.getAsync(ctx, "/full/"+h.id+"/Day1/T.RAF")
			waitDone(t, done, "the request whose source became a FIFO")
			want := http.StatusNotFound
			if cancelBy != "none" && rec.Code == http.StatusServiceUnavailable {
				want = http.StatusServiceUnavailable
			}
			wantStatus(t, rec, want)

			h.h.mu.Lock()
			flights := len(h.h.flights)
			h.h.mu.Unlock()
			if flights != 0 {
				t.Errorf("%d transcodes still registered", flights)
			}
			if names := h.fullJPEGs(t, id); len(names) != 0 {
				t.Errorf("a full-size JPEG was written: %v", names)
			}
			if !h.gate.Close(0) {
				t.Error("the request is still inside the gate")
			}
			h.releaseUI()
			h.assertReleased(t)
		})
	}
}

// cancelOnWrite is a ResponseWriter that, like Wails after the web view
// stops a request, cancels the request on its first write and then keeps
// reporting success while discarding the data.
type cancelOnWrite struct {
	header  http.Header
	cancel  context.CancelFunc
	code    int
	written int64
}

func (c *cancelOnWrite) Header() http.Header { return c.header }

func (c *cancelOnWrite) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *cancelOnWrite) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	c.written += int64(len(b))
	c.cancel()
	return len(b), nil
}

func TestFull_CancelledRequestStopsReading(t *testing.T) {
	h := newMediaHarness(t)
	const size = 8 << 20
	h.original(t, "big.mov", bytes.Repeat([]byte{7}, size), "video", "mov")

	for _, rng := range []string{"", "bytes=0-" + strconv.Itoa(size-1)} {
		ctx, cancel := context.WithCancel(context.Background())
		w := &cancelOnWrite{header: http.Header{}, cancel: cancel}
		req := httptest.NewRequest(http.MethodGet, "/full/"+h.id+"/big.mov", nil).WithContext(ctx)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		h.h.ServeHTTP(w, req)
		cancel()
		if w.written >= size/8 {
			t.Errorf("range %q: %d of %d bytes written after the request was cancelled", rng, w.written, size)
		}
	}

	// A request cancelled before it is served reads nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &cancelOnWrite{header: http.Header{}, cancel: func() {}}
	h.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/full/"+h.id+"/big.mov", nil).WithContext(ctx))
	if w.written != 0 {
		t.Errorf("%d bytes written for a request cancelled up front", w.written)
	}

	h.releaseUI()
	h.assertReleased(t)
}

// At most two transcodes run at once.
func TestFull_TranscodesAtMostTwoAtOnce(t *testing.T) {
	h := newMediaHarness(t)
	gate := make(chan struct{})
	h.stills.Gate = gate
	var mu sync.Mutex
	running, peak := 0, 0
	started := make(chan struct{}, 8)
	h.h.hook = func(ev string) {
		mu.Lock()
		defer mu.Unlock()
		switch ev {
		case hookStart:
			running++
			peak = max(peak, running)
			started <- struct{}{}
		case hookEnd:
			running--
		}
	}
	for i := range 3 {
		h.heic(t, fmt.Sprintf("IMG%d.HEIC", i))
	}
	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 3)
	for i := range 3 {
		wg.Go(func() { recs[i] = h.get(fmt.Sprintf("/full/%s/IMG%d.HEIC", h.id, i)) })
	}
	waitDone(t, started, "the first transcode")
	waitDone(t, started, "the second transcode")
	gate <- struct{}{} // let one finish
	waitDone(t, started, "the third transcode")
	close(gate)
	wg.Wait()

	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: status %d", i, rec.Code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Errorf("%d transcodes ran at once, want at most 2", peak)
	}
}

// ── preview ────────────────────────────────────────────────────────────────

func TestPreview_Served(t *testing.T) {
	h := newMediaHarness(t)
	data := platformtest.MakeJPEG(t, 32, 16)
	h.preview(t, 0, data)

	rec := h.get(stripMedia(t, PreviewURL(h.id, 0, 42)))
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("body is not the strip image")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", cc)
	}
	rec = h.get("/preview/" + h.id + "/0")
	wantStatus(t, rec, http.StatusOK)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control without a version = %q, want no-cache", cc)
	}
	wantStatus(t, h.get("/preview/"+h.id+"/1"), http.StatusNotFound)
}

func TestPreview_ServedWhileFolderClosed(t *testing.T) {
	h := newMediaHarness(t)
	data := platformtest.MakeJPEG(t, 32, 16)
	h.preview(t, 5, data)
	h.releaseUI()
	h.assertReleased(t)

	rec := h.get("/preview/" + h.id + "/5?v=1")
	wantStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("body is not the strip image")
	}
	h.assertReleased(t) // and serving it opened no session
}

func TestPreview_OutOfRange(t *testing.T) {
	h := newMediaHarness(t)
	for n := range 6 {
		h.preview(t, n, platformtest.MakeJPEG(t, 8, 8))
	}
	dir := filepath.Join(h.previews, h.id)
	for _, name := range []string{"6.jpg", "-1.jpg", "x.jpg", "01.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"6", "-1", "x", "01", "+1", "0.jpg", "", "0/", "0/x", "%30"} {
		t.Run(n, func(t *testing.T) {
			target := "/preview/" + h.id + "/" + n
			if n == "%30" { // "0" percent-encoded is still "0"
				wantStatus(t, h.get(target), http.StatusOK)
				return
			}
			wantStatus(t, h.get(target), http.StatusNotFound)
		})
	}
}

// A folder whose previews directory does not exist gets 404, and serving
// the request creates nothing.
func TestPreview_MissingDirIs404AndCreatesNothing(t *testing.T) {
	h := newMediaHarness(t)
	dir := filepath.Join(h.previews, h.id)
	wantStatus(t, h.get("/preview/"+h.id+"/0?v=1"), http.StatusNotFound)
	wantStatus(t, h.get("/preview/ffffffffffffffff/0"), http.StatusNotFound)
	entries, err := os.ReadDir(h.previews)
	if err != nil || len(entries) != 0 {
		t.Errorf("previews base holds %d entries (%v), want none", len(entries), err)
	}
	// A file where the directory should be is not a strip either.
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, h.get("/preview/"+h.id+"/0"), http.StatusNotFound)
}

func TestPreview_RefusesSymlinkAndDirErrors(t *testing.T) {
	h := newMediaHarness(t)
	dir := filepath.Join(h.previews, h.id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "x.jpg")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "2.jpg")); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, h.get("/preview/"+h.id+"/2"), http.StatusForbidden)

	failing := New(h.root, h.mgr, h.stills, func(string) (string, error) {
		return "", errors.New("no data directory")
	}, h.gate)
	rec := httptest.NewRecorder()
	failing.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/preview/"+h.id+"/0", nil))
	wantStatus(t, rec, http.StatusInternalServerError)
}

// ── routing, methods, gate ─────────────────────────────────────────────────

func TestMethod_PostIs405(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	h.preview(t, 0, platformtest.MakeJPEG(t, 8, 8))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		for _, target := range []string{"/thumb/" + h.id + "/A.JPG", "/full/" + h.id + "/A.JPG", "/preview/" + h.id + "/0"} {
			rec := h.serve(method, target)
			wantStatus(t, rec, http.StatusMethodNotAllowed)
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s: Allow = %q", method, target, allow)
			}
		}
	}
	original, err := os.ReadFile(filepath.Join(h.dir, "A.JPG"))
	if err != nil || string(original) != "x" {
		t.Errorf("original = %q, %v", original, err)
	}
	wantStatus(t, h.serve(http.MethodHead, "/full/"+h.id+"/A.JPG"), http.StatusOK)
}

func TestAfterGateClosedIs503(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	h.preview(t, 0, platformtest.MakeJPEG(t, 8, 8))
	wantStatus(t, h.get("/thumb/"+h.id+"/A.JPG"), http.StatusOK)

	if !h.gate.Close(0) {
		t.Fatal("a finished request is still inside the gate")
	}
	for _, target := range []string{"/thumb/" + h.id + "/A.JPG", "/full/" + h.id + "/A.JPG", "/preview/" + h.id + "/0"} {
		wantStatus(t, h.get(target), http.StatusServiceUnavailable)
	}
}

// A request stays inside the gate until its response is written.
func TestGate_HeldDuringRequest(t *testing.T) {
	h := newMediaHarness(t)
	h.heic(t, "IMG.HEIC")
	gate := make(chan struct{})
	h.stills.Gate = gate
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.get("/full/" + h.id + "/IMG.HEIC")
	}()
	waitDone(t, h.entered, "the transcode")
	if h.gate.Close(0) {
		t.Error("the gate closed with a request inside")
	}
	close(gate)
	waitDone(t, done, "the request")
	if !h.gate.Close(0) {
		t.Error("the request did not leave the gate")
	}
}

func TestRouting_UnknownIs404(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "A.JPG", []byte("x"), "photo", "jpeg")
	h.cache(t, id, thumb.TierThumb, platformtest.MakeJPEG(t, 8, 8))
	for _, target := range []string{
		"/", "/thumb", "/thumb/", "/thumb/" + h.id, "/thumb/" + h.id + "/",
		"/THUMB/" + h.id + "/A.JPG", "/other/" + h.id + "/A.JPG", "//thumb/" + h.id + "/A.JPG",
		"/preview/" + h.id, "/preview/" + h.id + "/",
	} {
		t.Run(target, func(t *testing.T) {
			wantStatus(t, h.get(target), http.StatusNotFound)
		})
	}
	// Without the leading slash (what http.StripPrefix leaves of a sibling
	// prefix) nothing is served.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.URL.Path = "thumb/" + h.id + "/A.JPG"
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusNotFound)
}

// Mounted like Wails mounts it, the handler answers under /media and only
// there: not for a sibling prefix the HasPrefix match also catches.
func TestMount_SiblingPrefixIs404(t *testing.T) {
	h := newMediaHarness(t)
	id := h.original(t, "Day1/A.JPG", []byte("x"), "photo", "jpeg")
	data := platformtest.MakeJPEG(t, 8, 8)
	h.cache(t, id, thumb.TierThumb, data)
	h.preview(t, 0, data)
	mux := wailsMount(h.h)
	serve := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	ok := serve(ThumbURL(h.id, "Day1/A.JPG", "thumb", "h"))
	wantStatus(t, ok, http.StatusOK)
	if !bytes.Equal(ok.Body.Bytes(), data) {
		t.Error("the mounted handler did not serve the thumbnail")
	}
	wantStatus(t, serve(FullURL(h.id, "Day1/A.JPG", "h")), http.StatusOK)
	wantStatus(t, serve(PreviewURL(h.id, 0, 1)), http.StatusOK)

	for _, target := range []string{
		"/mediaX/thumb/" + h.id + "/Day1/A.JPG",
		"/media-x/thumb/" + h.id + "/Day1/A.JPG",
		"/mediathumb/" + h.id + "/Day1/A.JPG",
		"/mediafull/" + h.id + "/Day1/A.JPG",
		"/media.x/preview/" + h.id + "/0",
		"/media",
		"/media/",
		"/media/media/thumb/" + h.id + "/Day1/A.JPG",
	} {
		t.Run(target, func(t *testing.T) {
			wantStatus(t, serve(target), http.StatusNotFound)
		})
	}
}

// Every response the handler sends with a body carries an explicit
// Content-Type, so nothing is ever sniffed.
func TestContentTypeAlwaysSet(t *testing.T) {
	h := newMediaHarness(t)
	h.original(t, "x.avi", []byte("RIFF....AVI "), "video", "avi")
	rec := h.get("/full/" + h.id + "/x.avi")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
}
