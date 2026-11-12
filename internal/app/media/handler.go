// Package media serves a folder's media to the web view: cached thumbnails,
// full-size images and videos (with HTTP Range), and the Library page's
// preview strips. It is not a binding but one http.Handler, which cmd/choix
// mounts on the Wails asset server with Route "/media" (C14); Wails strips
// that prefix, so the Handler sees
//
//	/thumb/<folder-id>/<rel-path>?tier=thumb|preview&v=<hash>
//	/full/<folder-id>/<rel-path>?v=<hash>
//	/preview/<folder-id>/<n>?v=<stamp>
//
// and answers 404 for anything else, including what http.StripPrefix leaves
// of a sibling prefix such as /mediaX. URLs are built with ThumbURL, FullURL
// and PreviewURL.
//
// A file is found through the folder's store (Files().GetByPath), never by
// joining the URL to the folder: the URL's path only selects a row, and the
// row's path is joined under the folder root and checked with
// safeJoinUnderRoot. Originals are opened read-only; the one file this
// package writes, the full-size JPEG of a photo the web view cannot show, is
// written through thumb.WriteFullJPEG (safefs), named by the file's id and
// content hash.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// Route names: the first element of a path the Handler serves.
const (
	routeThumb   = "thumb"
	routeFull    = "full"
	routePreview = "preview"
)

// Cache-Control values. A URL carrying ?v= (a content hash or a strip
// stamp) changes when the bytes do, so it may be kept for a day, as v1 kept
// thumbnails; one without a version is revalidated every time.
const (
	cacheVersioned   = "public, max-age=86400"
	cacheUnversioned = "no-cache"
)

const (
	// folderIDLen is the length of a folder id: 16 lowercase hex digits.
	folderIDLen = 16
	// previewCount is how many images a preview strip holds: 0.jpg … 5.jpg.
	previewCount = 6
	// maxTranscodes bounds the full-size JPEGs made at once. Each can
	// saturate several cores; 2 lets the next photo's prefetch start while
	// the current one is still being made.
	maxTranscodes = 2
	// readChunk bounds one read of a served file, so a cancelled request is
	// noticed within one chunk whatever buffer the writer asks with.
	readChunk = 256 << 10
	// cachePrefix is the folder's thumbnail cache, which every cached file
	// served must lie in.
	cachePrefix = ".choix/thumbs/"
	// fullAttempts is how many times one /full request looks its file up
	// when the row changed under the full-size JPEG it was about to serve
	// or waited for (errStale); after the last it answers 503, never the
	// pixels of bytes the row no longer names.
	fullAttempts = 3
)

// Events reported to Handler.hook (tests).
const (
	hookJoin  = "join"            // a request waits for a full-size JPEG
	hookStart = "transcode-start" // a transcode took one of the maxTranscodes slots
	hookEnd   = "transcode-end"   // … and is about to give it back
	// hookOpened: a request opened a full-size JPEG and is about to check
	// that its row is still current before serving it.
	hookOpened = "full-opened"
	// hookThumbOpened: a request opened a thumbnail file and is about to
	// read its row again.
	hookThumbOpened = "thumb-opened"
)

// errRefused reports a file that may not be served: outside the folder, or
// a symbolic link where none may be. It is answered 403.
var errRefused = errors.New("media: refused")

// errNotRegular reports a path that is not a regular file — a directory,
// FIFO, socket or device — where one was expected. Nothing ever waits on
// such an entry; it is answered 404 and logged.
var errNotRegular = errors.New("media: not a regular file")

// errStale reports a full-size JPEG that is not written or not served
// because its row no longer describes the bytes it was made from: the
// original was rescanned with other bytes, or the row was removed and its
// id perhaps given to another file. The request looks the file up again,
// up to fullAttempts times, and then answers 503.
var errStale = errors.New("media: the file changed while its full-size JPEG was made")

// Handler serves media; see the package documentation. It is safe for
// concurrent use.
type Handler struct {
	root        context.Context
	sessions    *session.Manager
	stills      platform.Stills
	previewsDir func(folderID string) (string, error)
	gate        *opgate.Gate

	// sem holds one token per running transcode.
	sem chan struct{}

	mu sync.Mutex
	// flights are the full-size JPEGs being made, by folder id and cache
	// path (which names the file id and its content hash).
	flights map[string]*flight

	// publish makes "is the row still current?" and the write of a
	// full-size JPEG one step (makeFullJPEG). Never held with mu.
	publish sync.Mutex

	// hook, when set by a test, observes transcodes.
	hook func(event string)
}

// flight is one full-size JPEG being made for the requests waiting on it.
// It runs while at least one of them waits: the last to give up cancels it
// and waits for it to end, so its work is always covered by a request's
// session reference and gate entry.
type flight struct {
	done    chan struct{} // closed when the transcode has ended
	err     error         // its outcome, set before done is closed
	cancel  context.CancelFunc
	waiters int // guarded by Handler.mu
}

// New returns a Handler. Requests and transcodes are cancelled when root is
// done; folders are served only while sessions has them open; stills makes
// the full-size JPEGs; previewsDir names a folder's preview strip directory
// (the app's own cache, outside the folder) and must not create anything —
// it is called for every preview request, for any well-formed folder id, and
// a directory that does not exist is answered 404; every request enters g.
func New(root context.Context, sessions *session.Manager, stills platform.Stills, previewsDir func(folderID string) (string, error), g *opgate.Gate) *Handler {
	if root == nil {
		root = context.Background()
	}
	if g == nil {
		g = &opgate.Gate{}
	}
	return &Handler{
		root:        root,
		sessions:    sessions,
		stills:      stills,
		previewsDir: previewsDir,
		gate:        g,
		sem:         make(chan struct{}, maxTranscodes),
		flights:     map[string]*flight{},
	}
}

// ServeHTTP answers GET and HEAD for the three routes. A request holds its
// gate entry and, for thumb and full, a reference to the folder's session
// until its response is written; its context ends with the request or the
// root context, whichever is first, and reading a served file stops then.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	leave, err := h.gate.Enter()
	if err != nil {
		http.Error(w, opgate.ClosedMessage, http.StatusServiceUnavailable)
		return
	}
	defer leave()

	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// The request's own cancellation reaches ctx at once; the root's through
	// AfterFunc, and serveFile's reader checks the root directly as well.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(h.root, cancel)
	defer stop()
	r = r.WithContext(ctx)

	switch rt.kind {
	case routeThumb:
		h.serveThumb(w, r, rt)
	case routeFull:
		h.serveFull(w, r, rt)
	case routePreview:
		h.servePreview(w, r, rt)
	}
}

// route is a parsed request path.
type route struct {
	kind     string // routeThumb, routeFull or routePreview
	folderID string
	rest     string // the file's relative path, or the strip index
}

// parseRoute accepts exactly /<kind>/<folder-id>/<rest>: a folder id of 16
// lowercase hex digits, and a rest that is a canonical relative path
// (fs.ValidPath: no empty, "." or ".." element, no leading or trailing
// slash) or, for preview, one digit 0–5.
func parseRoute(p string) (route, bool) {
	p, ok := strings.CutPrefix(p, "/")
	if !ok {
		return route{}, false
	}
	kind, p, ok := strings.Cut(p, "/")
	if !ok {
		return route{}, false
	}
	id, rest, ok := strings.Cut(p, "/")
	if !ok || !validFolderID(id) {
		return route{}, false
	}
	switch kind {
	case routeThumb, routeFull:
		if rest == "." || !fs.ValidPath(rest) || strings.ContainsRune(rest, 0) {
			return route{}, false
		}
	case routePreview:
		if len(rest) != 1 || rest[0] < '0' || rest[0] >= '0'+previewCount {
			return route{}, false
		}
	default:
		return route{}, false
	}
	return route{kind: kind, folderID: id, rest: rest}, true
}

// validFolderID reports whether id is 16 lowercase hex digits.
func validFolderID(id string) bool {
	if len(id) != folderIDLen {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// serveThumb serves a cached thumbnail tier from the file's thumbs row; a
// video without a preview row gets its thumb row.
func (h *Handler) serveThumb(w http.ResponseWriter, r *http.Request, rt route) {
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = thumb.TierThumb
	}
	if tier != thumb.TierThumb && tier != thumb.TierPreview {
		http.Error(w, "bad tier", http.StatusBadRequest)
		return
	}
	s, release, ok := h.sessions.AcquireOpen(rt.folderID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer release()

	rec, err := s.Store.Files().GetByPath(rt.rest)
	if err != nil {
		h.fail(w, r, fmt.Errorf("look up file: %w", err))
		return
	}
	t, err := s.Store.Thumbs().Get(rec.ID, tier)
	if errors.Is(err, store.ErrNotFound) && tier == thumb.TierPreview && rec.Kind == "video" {
		t, err = s.Store.Thumbs().Get(rec.ID, thumb.TierThumb)
	}
	if err != nil {
		h.fail(w, r, fmt.Errorf("look up thumbnail: %w", err))
		return
	}
	f, fi, err := openCache(s.Root, t.RelPath)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer closeFile(f)
	h.report(hookThumbOpened)

	// A thumbnail file is not keyed by hash: it shows the row's bytes only
	// once the thumbnail stage has run for them, and the stage replaces the
	// file before it marks the row. So the opened file is known to show
	// the row's hash only when the row was in a built state with that same
	// hash both before the open (rec) and after it (cur): a rebuild that
	// completes between the two leaves the opened (old) file unverified.
	served := ""
	if thumbsBuiltFor(rec.ScanStatus) {
		if cur, err := s.Store.Files().GetByID(rec.ID); err == nil &&
			cur.ContentHash == rec.ContentHash && thumbsBuiltFor(cur.ScanStatus) {
			served = cur.ContentHash
		}
	}
	w.Header().Set("ETag", etagFor("thumb-"+t.Tier, served, fi))
	w.Header().Set("Cache-Control", cacheFor(r, served))
	h.serveFile(w, r, f, "image/jpeg", time.Time{})
}

// thumbsBuiltFor reports whether a row in this scan_status has had its
// thumbnails made from its current bytes: a rescan that changes the bytes
// resets the row to "discovered", and the thumbnail stage moves it on to
// "thumbed" only after writing them. "failed" and "missing" are unknown.
func thumbsBuiltFor(status string) bool {
	return status == "thumbed" || status == "analyzed"
}

// serveFull serves the original through http.ServeContent (Range), or for a
// photo the web view cannot show its full-size JPEG.
func (h *Handler) serveFull(w http.ResponseWriter, r *http.Request, rt route) {
	s, release, ok := h.sessions.AcquireOpen(rt.folderID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer release()

	// A full-size JPEG whose row changed while it was being made, or before
	// it was served, is neither written nor served (errStale); the file is
	// then looked up again, up to fullAttempts times in all.
	for attempt := 1; ; attempt++ {
		rec, err := s.Store.Files().GetByPath(rt.rest)
		if err != nil {
			h.fail(w, r, fmt.Errorf("look up file: %w", err))
			return
		}
		if servedAsIs(rec) {
			h.serveOriginal(w, r, s, rec)
			return
		}
		if !h.serveFullJPEG(w, r, s, rec, attempt < fullAttempts) {
			return
		}
	}
}

// serveOriginal streams the file's own bytes. They are known to be those of
// the row's content hash only while the opened file has the size and mtime
// the row recorded (the scanner's own "unchanged" test); the row is read
// again after the open, so a rescan before it is seen.
func (h *Handler) serveOriginal(w http.ResponseWriter, r *http.Request, s *session.Session, rec store.File) {
	src, err := originalPath(s.Root, rec.Path)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f, fi, err := openRegular(src)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer closeFile(f)
	served := ""
	if cur, err := s.Store.Files().GetByID(rec.ID); err == nil && strings.EqualFold(cur.Path, rec.Path) &&
		cur.Size == fi.Size() && cur.Mtime == fi.ModTime().Unix() {
		served = cur.ContentHash
	}
	w.Header().Set("ETag", etagFor("original", served, fi))
	w.Header().Set("Cache-Control", cacheFor(r, served))
	h.serveFile(w, r, f, contentTypeFor(rec.Format), time.Time{})
}

// serveFullJPEG serves the photo's cached full-size JPEG, making it first
// when there is none; if it cannot be made, the preview tier. The cache file
// is the one of the row's current content hash (thumb.FullJPEGRel): one made
// from other bytes — before the original changed, for a removed file whose
// id was reused, or under the old hash-less name — is never served.
//
// Whether it was cached or just made, the full-size JPEG is served only
// after its row was read again with the file already open (serveFullCached):
// the bytes sent are always those of the hash that was current then.
//
// It reports again, having written nothing to w, when mayRetry is set and
// the row no longer describes rec (errStale): the caller looks the file up
// again. Without mayRetry a stale row is answered 503. Otherwise it has
// answered the request.
func (h *Handler) serveFullJPEG(w http.ResponseWriter, r *http.Request, s *session.Session, rec store.File, mayRetry bool) (again bool) {
	rel := thumb.FullJPEGRel(rec.ID, rec.ContentHash)
	served, err := h.serveFullCached(w, r, s, rec, rel, true)
	if served {
		return false
	}
	if err == nil {
		src, perr := originalPath(s.Root, rec.Path)
		if perr != nil {
			h.fail(w, r, perr)
			return false
		}
		// The decoders open the source by path with a blocking open: a FIFO
		// there must be refused before anything waits on it.
		if err := checkRegular(src); err != nil {
			h.fail(w, r, err)
			return false
		}
		err = h.transcode(r.Context(), s, rec, src, rel)
		if err == nil {
			if served, err = h.serveFullCached(w, r, s, rec, rel, false); served {
				return false
			}
			if err == nil {
				err = errors.New("the full-size JPEG cannot be read back")
			}
		}
	}
	if r.Context().Err() != nil || h.root.Err() != nil {
		http.Error(w, "request cancelled", http.StatusServiceUnavailable)
		return false
	}
	if errors.Is(err, errStale) {
		if mayRetry {
			return true
		}
		slog.Warn("media: the file kept changing; its full-size JPEG is not served", "folder", s.FolderID, "file", rec.ID, "err", err)
		h.fail(w, r, fmt.Errorf("full-size jpeg: %w", err)) // 503: never the pixels of other bytes
		return false
	}
	if errors.Is(err, errNotRegular) || errors.Is(err, fs.ErrNotExist) {
		h.fail(w, r, err) // the source changed while the transcode waited
		return false
	}
	slog.Warn("media: full-size JPEG failed; serving the preview", "folder", s.FolderID, "file", rec.ID, "err", err)
	if t, terr := s.Store.Thumbs().Get(rec.ID, thumb.TierPreview); terr == nil && h.serveStandIn(w, r, s.Root, t.RelPath) {
		return false
	}
	h.fail(w, r, fmt.Errorf("full-size jpeg: %w", err))
	return false
}

// serveFullCached serves rec's full-size JPEG at rel and reports whether it
// did. A missing file (or an empty one, with skipEmpty) is false, nil. The
// file is opened first and rec's row read again after: if the row no longer
// names rec's path and hash the result is errStale and nothing is written.
// Otherwise the open file's bytes, made from the bytes of rec.ContentHash,
// are served, cached long only when the request's v is that hash.
func (h *Handler) serveFullCached(w http.ResponseWriter, r *http.Request, s *session.Session, rec store.File, rel string, skipEmpty bool) (bool, error) {
	f, fi, err := openCache(s.Root, rel)
	if err != nil {
		return false, nil
	}
	defer closeFile(f)
	if skipEmpty && fi.Size() == 0 {
		return false, nil
	}
	h.report(hookOpened)
	if err := stillCurrent(s.Store, rec); err != nil {
		return false, err
	}
	w.Header().Set("ETag", etagFor("full", rec.ContentHash, fi))
	w.Header().Set("Cache-Control", cacheFor(r, rec.ContentHash))
	h.serveFile(w, r, f, "image/jpeg", time.Time{})
	return true, nil
}

// serveStandIn serves the cache file rel, the preview tier, in place of a
// full-size JPEG that could not be made, and reports whether it did. It is
// not the file the URL names, so it is never kept (no-cache) and its
// validator names no hash.
func (h *Handler) serveStandIn(w http.ResponseWriter, r *http.Request, root, rel string) bool {
	f, fi, err := openCache(root, rel)
	if err != nil {
		return false
	}
	defer closeFile(f)
	w.Header().Set("ETag", etagFor("stand-in", "", fi))
	w.Header().Set("Cache-Control", cacheUnversioned)
	h.serveFile(w, r, f, "image/jpeg", time.Time{})
	return true
}

// transcode waits for the file's full-size JPEG to be made at rel, joining
// the transcode already running for it or starting one. Transcodes are keyed
// by folder and rel, which names the file id and its content hash, so a
// request for other bytes of the same id never joins one. The transcode runs
// under the root context and the session's Inflight, not under any one
// request: a request that gives up leaves it to the others, and the last
// one to give up cancels it and waits for it to end, so no transcode
// outlives the requests (and session references) it serves.
func (h *Handler) transcode(ctx context.Context, s *session.Session, rec store.File, src, rel string) error {
	key := s.FolderID + "/" + rel
	h.mu.Lock()
	f := h.flights[key]
	if f == nil {
		fctx, cancel := context.WithCancel(s.Context(h.root))
		f = &flight{done: make(chan struct{}), cancel: cancel}
		h.flights[key] = f
		go h.run(fctx, key, f, func(ctx context.Context) error {
			return h.makeFullJPEG(ctx, s, rec, src, rel)
		})
	}
	f.waiters++
	h.mu.Unlock()
	h.report(hookJoin)

	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		h.mu.Lock()
		f.waiters--
		last := f.waiters == 0
		if last && h.flights[key] == f {
			delete(h.flights, key)
		}
		h.mu.Unlock()
		if last {
			f.cancel()
			<-f.done
		}
		return ctx.Err()
	}
}

// run runs one flight's work and publishes its outcome.
func (h *Handler) run(ctx context.Context, key string, f *flight, work func(context.Context) error) {
	defer func() {
		f.cancel()
		h.mu.Lock()
		if h.flights[key] == f {
			delete(h.flights, key)
		}
		h.mu.Unlock()
		close(f.done)
	}()
	f.err = work(ctx)
}

// makeFullJPEG makes the full-size JPEG of the photo at src, sized by the
// source kind's rule (thumb.Photo at 4096), and writes it to the cache at
// rel through thumb.WriteFullJPEG, which removes the file's superseded
// full-size JPEGs. It waits for one of the maxTranscodes slots and writes
// nothing once ctx is done, nor when rec's row stopped describing the file
// meanwhile (errStale): a transcode of old bytes never writes under their
// hash after the row moved on, and so never removes the current file's
// full-size JPEG as superseded.
func (h *Handler) makeFullJPEG(ctx context.Context, s *session.Session, rec store.File, src, rel string) error {
	root := s.Root
	select {
	case h.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	h.report(hookStart)
	defer func() {
		h.report(hookEnd)
		<-h.sem
	}()

	// A transcode that ended while this one waited may have made it.
	if f, fi, err := openCache(root, rel); err == nil {
		closeFile(f)
		if fi.Size() > 0 {
			return nil
		}
	}
	// thumb.Photo opens src by path, blocking; check its type right before
	// (time has passed waiting for a slot). The window left is outside the
	// threat model (no concurrent local attacker).
	if err := checkRegular(src); err != nil {
		return err
	}
	img, err := thumb.Photo(ctx, h.stills, src, thumb.WidthFullJPEG)
	if err != nil {
		return fmt.Errorf("make full-size jpeg: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The check and the write are one step for every transcode: one that
	// finds its row current writes before any later one of newer bytes can
	// check, so that one removes it, never the other way round.
	h.publish.Lock()
	defer h.publish.Unlock()
	if err := stillCurrent(s.Store, rec); err != nil {
		return err
	}
	if _, err := thumb.WriteFullJPEG(root, rec.ID, rec.ContentHash, img.JPEG); err != nil {
		return fmt.Errorf("write full-size jpeg: %w", err)
	}
	return nil
}

// stillCurrent returns nil when the row of rec's id still names the same
// file (path, case-insensitively) with the same content hash, and errStale
// when it was removed, its id reused, or its bytes rescanned.
func stillCurrent(st *store.Store, rec store.File) error {
	cur, err := st.Files().GetByID(rec.ID)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: file %d was removed", errStale, rec.ID)
	}
	if err != nil {
		return fmt.Errorf("look up file %d: %w", rec.ID, err)
	}
	if cur.ContentHash != rec.ContentHash || !strings.EqualFold(cur.Path, rec.Path) {
		return fmt.Errorf("%w: file %d", errStale, rec.ID)
	}
	return nil
}

// servePreview serves image n of the folder's preview strip from the app's
// own cache. It needs no session: the strip is shown for folders that are
// closed or offline too.
func (h *Handler) servePreview(w http.ResponseWriter, r *http.Request, rt route) {
	if h.previewsDir == nil {
		http.NotFound(w, r)
		return
	}
	dir, err := h.previewsDir(rt.folderID)
	if err != nil {
		h.fail(w, r, fmt.Errorf("previews directory: %w", err))
		return
	}
	// A folder without a strip has no directory; nothing is created for it.
	if di, err := os.Lstat(dir); err != nil || !di.IsDir() {
		http.NotFound(w, r)
		return
	}
	f, fi, err := openRegular(filepath.Join(dir, rt.rest+".jpg"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer closeFile(f)
	w.Header().Set("Cache-Control", cacheControl(r))
	// The strip's stamp only grows, so modification-time validation cannot
	// confuse two strips under one URL: it stays on this route.
	h.serveFile(w, r, f, "image/jpeg", fi.ModTime())
}

// serveFile streams f with http.ServeContent (Range, conditional requests)
// and an explicit Content-Type, so nothing is ever sniffed. Reading stops
// as soon as the request or the root context is done.
//
// Validation is what the caller makes it. /thumb and /full pass a zero
// modtime and set an ETag (etagFor): http.ServeContent then sends no
// Last-Modified and ignores If-Modified-Since, If-Unmodified-Since and a
// date in If-Range (it evaluates them only for a non-zero modtime), so a
// conditional request is answered 304 only when If-None-Match carries the
// ETag of the bytes served now, and a Range is resumed only on an If-Range
// naming it. Modification time cannot tell two versions written within
// one second apart, nor two files of the same mtime.
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, f *os.File, contentType string, modtime time.Time) {
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "", modtime, &ctxReadSeeker{ctx: r.Context(), root: h.root, f: f})
}

// etagFor is the strong ETag of the open file fi served as kind. It is
// bound both to the content hash the bytes are known to be of (served; ""
// when not verified, which gives a distinct "u" form) and to the file
// itself (inode, mtime in nanoseconds, size), so the validator of one
// version never matches another: not a rebuild or re-transcode of the same
// hash, not a restore within the same second, not unverified bytes later
// verified, and not another file. Only the bytes served now can be
// answered 304.
func etagFor(kind, served string, fi fs.FileInfo) string {
	id := "u"
	if served != "" {
		sum := sha256.Sum256([]byte(served))
		id = hex.EncodeToString(sum[:8])
	}
	var ino uint64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
	}
	return fmt.Sprintf(`"%s-%s-%x-%x-%x"`, kind, id, ino, fi.ModTime().UnixNano(), fi.Size())
}

// ctxReadSeeker reads f until ctx or root is done. http.ServeContent copies
// through it, so a request the web view cancelled — whose later writes
// Wails discards while still reporting success — stops reading the file.
type ctxReadSeeker struct {
	ctx, root context.Context
	f         *os.File
}

func (c *ctxReadSeeker) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	if err := c.root.Err(); err != nil {
		return 0, err
	}
	if len(p) > readChunk {
		p = p[:readChunk]
	}
	return c.f.Read(p)
}

func (c *ctxReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return c.f.Seek(offset, whence)
}

var _ io.ReadSeeker = (*ctxReadSeeker)(nil)

// fail answers with the status err maps to and logs what is not a plain
// miss.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	code := statusOf(err)
	if code == http.StatusForbidden || code >= http.StatusInternalServerError && code != http.StatusServiceUnavailable ||
		errors.Is(err, errNotRegular) || errors.Is(err, safefs.ErrNotRegular) {
		slog.Warn("media: request failed", "path", r.URL.Path, "status", code, "err", err)
	}
	http.Error(w, http.StatusText(code), code)
}

// statusOf maps an error to its HTTP status.
func statusOf(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, fs.ErrNotExist),
		errors.Is(err, errNotRegular), errors.Is(err, safefs.ErrNotRegular):
		return http.StatusNotFound
	case errors.Is(err, errRefused), errors.Is(err, safefs.ErrSymlink), errors.Is(err, safefs.ErrNotDir):
		return http.StatusForbidden
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, errStale):
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// cacheFor is the Cache-Control of a response to r whose bytes are those of
// the content hash served ("" when that is not known): it may be kept for a
// day only when the request's v names exactly those bytes. A stale or
// missing v is revalidated, so a URL is never left holding other bytes than
// its hash — not even after the original is restored to that hash.
func cacheFor(r *http.Request, served string) string {
	if v := r.URL.Query().Get("v"); v != "" && v == served {
		return cacheVersioned
	}
	return cacheUnversioned
}

// cacheControl is the Cache-Control of a preview-strip response: versioned
// URLs may be kept for a day, the others are revalidated. A strip's stamp
// only grows (library's publishLocked: max(now, previous+1)), so a URL never
// names a strip again after it was rebuilt and cannot be left holding
// another strip's images.
func cacheControl(r *http.Request) string {
	if r.URL.Query().Get("v") != "" {
		return cacheVersioned
	}
	return cacheUnversioned
}

// servedAsIs reports whether /media/full streams the file's own bytes: a
// video, or a photo the web view can show (JPEG, PNG). Any other photo
// (HEIC, RAF, DNG, TIFF…) gets a full-size JPEG.
func servedAsIs(rec store.File) bool {
	return rec.Kind != "photo" || browserRenderableFormat(rec.Format) && !isTIFFPath(rec.Path)
}

// browserRenderableFormat reports whether a photo of this stored format may
// be served as it is.
func browserRenderableFormat(format string) bool {
	switch format {
	case "jpeg", "png":
		return true
	}
	return false
}

// isTIFFPath reports whether p names a TIFF (.tif or .tiff, any case). The
// scanner stores TIFFs with format "jpeg" (internal/scanner/format.go), so
// the stored format alone would hand their bytes to the web view labelled
// image/jpeg and without the full-size cap; the extension tells them apart
// without a schema change or a rescan.
func isTIFFPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".tif", ".tiff":
		return true
	}
	return false
}

// contentTypeFor is the Content-Type of an original of this format. It is
// explicit because inside the sandbox Go's MIME table knows no .mov and the
// system MIME files cannot be read (spike S3).
func contentTypeFor(format string) string {
	switch format {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "heic":
		return "image/heic"
	case "mov":
		return "video/quicktime"
	case "mp4":
		return "video/mp4"
	}
	return "application/octet-stream"
}

// originalPath joins a file row's path under root and checks, symbolic links
// resolved, that it stays there; it returns the resolved path.
func originalPath(root, rel string) (string, error) {
	p, err := safeJoinUnderRoot(root, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", errRefused, rel, err)
	}
	return p, nil
}

// openCache opens rel, a cache file under root's .choix/thumbs, read-only
// through safefs: every directory on the way must be a real directory inside
// root and the file a regular file, no symbolic link followed.
func openCache(root, rel string) (*os.File, fs.FileInfo, error) {
	rel = filepath.ToSlash(rel)
	if !fs.ValidPath(rel) || !strings.HasPrefix(rel, cachePrefix) {
		return nil, nil, fmt.Errorf("%w: cache path %q is not under %s", errRefused, rel, cachePrefix)
	}
	d, err := safefs.OpenDir(root, path.Dir(rel), false)
	if err != nil {
		return nil, nil, fmt.Errorf("open cache: %w", err)
	}
	defer func() { _ = d.Close() }()
	f, err := d.OpenRead(path.Base(rel))
	if err != nil {
		return nil, nil, fmt.Errorf("open cache: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		closeFile(f)
		return nil, nil, fmt.Errorf("open cache: %w", err)
	}
	return f, fi, nil
}

// openRegular opens the regular file p read-only. The last element must not
// be a symbolic link (callers pass a path safeJoinUnderRoot resolved, or the
// app's own cache). It never blocks on a FIFO or device: the open is
// non-blocking and anything but a regular file is refused before the file
// is put back in blocking mode.
func openRegular(p string) (*os.File, fs.FileInfo, error) {
	var fd int
	err := retryEINTR(func() error {
		var err error
		fd, err = unix.Open(p, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		return err
	})
	if errors.Is(err, unix.ELOOP) {
		return nil, nil, fmt.Errorf("%w: %s is a symbolic link", errRefused, p)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", p, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("fstat %s: %w", p, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("%w: %s", errNotRegular, p)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("fcntl %s: %w", p, err)
	}
	f := os.NewFile(uintptr(fd), p)
	fi, err := f.Stat()
	if err != nil {
		closeFile(f)
		return nil, nil, fmt.Errorf("stat %s: %w", p, err)
	}
	return f, fi, nil
}

// checkRegular reports whether p is a regular file, by opening it with
// openRegular (read-only, non-blocking) and closing it again. It is made
// right before p is handed to code that opens it by path with a blocking
// open.
func checkRegular(p string) error {
	f, _, err := openRegular(p)
	if err != nil {
		return err
	}
	closeFile(f)
	return nil
}

// retryEINTR calls fn until it fails with something other than EINTR.
func retryEINTR(fn func() error) error {
	for {
		if err := fn(); !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

// closeFile closes a file that was only read.
func closeFile(f *os.File) { _ = f.Close() }

// report passes an event to the test hook.
func (h *Handler) report(event string) {
	if h.hook != nil {
		h.hook(event)
	}
}
