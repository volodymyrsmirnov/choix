// Package catalog is the service behind the Folder, Focus and Compare
// screens: the cluster grid of the open folder, the focus payload of one
// cluster, pick / reject / unpick with an in-memory undo stack, re-cluster,
// and keeping the Library's cached counts and previews current after picks.
//
// Catalog is the core with the Go API; Service is the stateless facade
// whose exported methods are exactly the frontend bindings.
//
// Never lose a pick: the mutations of one folder (SetPick, Undo, Recluster)
// are serialised by that folder id's lock (kept across reopenings), each
// holds a session reference of its own for the whole operation, and a pick,
// reject or undo returns only after internal/picks has committed it
// durably. Close and shutdown wait for a mutation in flight.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// ErrNotOpen reports an operation on a folder the catalog does not have
// open.
var ErrNotOpen = errors.New("catalog: the folder is not open")

// ErrUnknownAction reports a SetPick action other than pick, unpick, reject
// and unreject.
var ErrUnknownAction = errors.New("catalog: unknown pick action")

// ErrNothingToUndo reports an Undo with an empty undo stack.
var ErrNothingToUndo = errors.New("nothing to undo")

// What the frontend receives for the package's sentinel errors.
func init() {
	apperr.Register(ErrNotOpen, apperr.CodeNotOpen, "")
	apperr.RegisterInvalid(ErrUnknownAction, "action", "unknown_action", "")
	apperr.Register(ErrNothingToUndo, apperr.CodeNotFound, "nothing to undo")
}

// SetPick actions.
const (
	actionPick     = "pick"
	actionUnpick   = "unpick"
	actionReject   = "reject"
	actionUnreject = "unreject"
)

// PickResult states.
const (
	statePicked   = "picked"
	stateRejected = "rejected"
	stateUnmarked = "unmarked"
)

// picksDirField is the settings key an invalid picks directory is reported
// against.
const picksDirField = "picks_dir"

// defaultRefreshDelay is RefreshDelay when Deps leaves it zero.
const defaultRefreshDelay = 500 * time.Millisecond

// Events of Catalog.hook (tests).
const (
	hookCloseWait    = "close-wait"    // about to wait for the folder's mutations
	hookMutateWait   = "mutate-wait"   // a mutation about to wait for the folder's lock
	hookCloseAllWait = "closeall-wait" // CloseAll about to wait for pending closes ("" folder)
	hookOpenAcquired = "open-acquired" // Open holds its session, not yet installed
)

// PickResult is a file's pick state after SetPick or Undo: picked (with the
// path of its copy, relative to the folder root), rejected or unmarked.
type PickResult struct {
	FileID       int64  `json:"file_id"`
	State        string `json:"state"`
	ExportedPath string `json:"exported_path,omitempty"`
}

// Deps are what the catalog works with.
type Deps struct {
	Sessions *session.Manager
	Library  *library.Library
	// Settings returns the live machine-wide settings; nil means the
	// built-in defaults.
	Settings func() config.Config
	// EnqueueFront queues a full job for the folder at the front; the
	// runtime binds it to queue.EnqueueFront(id, session.JobFull): opening a
	// folder rescans it. nil: nothing is queued.
	EnqueueFront func(folderID string)
	// Emit receives folder:changed; nil: no events.
	Emit events.Emitter
	// Stills and PreviewsDir make the Library's preview strips; either nil
	// turns preview refreshes off.
	Stills      platform.Stills
	PreviewsDir func(id string) (string, error)
	// RefreshDelay is how long the Library refresh waits for a further
	// pick, reject or undo (500 ms when zero).
	RefreshDelay time.Duration
	// Context is the runtime's root context, cancelled at shutdown; nil
	// means context.Background().
	Context context.Context
}

// Catalog serves the open folder. At most one folder is open at a time. It
// is safe for concurrent use.
type Catalog struct {
	d Deps
	// ctx is Deps.Context, also cancelled by CloseAll.
	ctx    context.Context
	cancel context.CancelFunc

	// afterFunc starts a debounce timer (time.AfterFunc; tests replace it).
	afterFunc func(time.Duration, func()) stopper
	// hook, when set by a test, observes Close.
	hook func(event, folderID string)

	// mu guards open, shut, locks and gens, and the Add of closing. It is never
	// held while calling out: the session's Acquire may deliver Library
	// callbacks (OnOffline) that call Close on the same goroutine.
	mu   sync.Mutex
	open *folder
	shut bool
	// locks holds one mutation lock per folder id for the catalog's
	// lifetime ([call] C4). It is never replaced when a folder is closed
	// and opened again, so a mutation of a new instance waits for one still
	// in flight on the previous instance. A holder never calls into the
	// Library, so no Library callback can arrive while one is held.
	locks map[string]*sync.Mutex
	// closing counts the closes in progress (Close's, and the background
	// closes of folders Open replaced); CloseAll waits for them.
	closing sync.WaitGroup
	// gens counts, per folder id, the invalidations (Invalidate: offline,
	// moved, removed) — also those that found nothing open. Open reads it
	// before acquiring the session and installs that session only when it
	// has not changed meanwhile.
	gens map[string]uint64
}

// folder is one open instance of a folder: the catalog's session
// reference, its undo stack and its Library refresher. closed and undo are
// guarded by the folder id's mutation lock (Catalog.lockFor).
type folder struct {
	id      string
	s       *session.Session
	release func()
	ref     *refresher

	closed bool
	undo   undoStack
}

// lockFor returns the mutation lock of folderID, made on first use.
func (c *Catalog) lockFor(folderID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	l := c.locks[folderID]
	if l == nil {
		l = &sync.Mutex{}
		c.locks[folderID] = l
	}
	return l
}

// New returns a Catalog over d.
func New(d Deps) *Catalog {
	if d.Settings == nil {
		d.Settings = config.Defaults
	}
	if d.RefreshDelay <= 0 {
		d.RefreshDelay = defaultRefreshDelay
	}
	if d.Context == nil {
		d.Context = context.Background()
	}
	ctx, cancel := context.WithCancel(d.Context)
	return &Catalog{d: d, ctx: ctx, cancel: cancel, afterFunc: timeAfterFunc}
}

// Open opens the folder and returns its cluster grid. It takes one session
// reference, held until the folder is closed, re-clusters when the folder's
// clusters were built with other settings (EnsureClustered) and queues a
// rescan at the front (EnqueueFront). The folder open before is closed in
// the background, as Close does it: once its mutation in flight has ended
// its undo stack is dropped, its owed Library refresh runs and its
// reference is released. Opening the folder already open keeps its
// reference and undo stack. Errors are the session's: library.ErrOffline,
// library.ErrAccessLost, library.ErrNotFound, session.ErrReadOnly,
// session.ErrUnsafeState, and session.ErrShuttingDown after CloseAll.
//
// No catalog lock is held while the session is acquired: that may deliver
// Library callbacks which call Close or Invalidate on this goroutine.
//
// A folder invalidated (Invalidate: offline, moved, removed) after Open
// began and before it installed the session is not installed: the session
// may be the one the folder has left. Open then releases what it acquired
// and answers library.ErrOffline.
func (c *Catalog) Open(ctx context.Context, folderID string) (FolderView, error) {
	c.mu.Lock()
	shut := c.shut
	gen := c.gens[folderID]
	c.mu.Unlock()
	if shut {
		return FolderView{}, fmt.Errorf("catalog: open %s: %w", folderID, session.ErrShuttingDown)
	}
	// Open's own reference, for the work below: a Close arriving meanwhile
	// cannot close the store under it.
	s, release, err := c.d.Sessions.Acquire(folderID)
	if err != nil {
		return FolderView{}, fmt.Errorf("catalog: open %s: %w", folderID, err)
	}
	defer release()
	c.event(hookOpenAcquired, folderID)
	if err := c.install(folderID, s, gen); err != nil {
		return FolderView{}, fmt.Errorf("catalog: open %s: %w", folderID, err)
	}
	if _, err := s.EnsureClustered(ctx, c.d.Settings); err != nil {
		// The folder keeps the clusters it has; the rescan re-clusters.
		slog.Warn("catalog: re-cluster on open", "folder", folderID, "err", err)
	}
	if c.d.EnqueueFront != nil {
		c.d.EnqueueFront(folderID)
	}
	return c.view(s)
}

// install makes folderID, whose session s the caller references, the open
// folder, unless it already is: the catalog takes a reference of its own and
// the folder open before is closed in the background. gen is the folder's
// invalidation count when the caller began; if it has changed, nothing is
// installed and the error is library.ErrOffline.
func (c *Catalog) install(folderID string, s *session.Session, gen uint64) error {
	_, keep, ok := c.d.Sessions.AcquireOpen(folderID)
	if !ok {
		return session.ErrShuttingDown // the caller's reference keeps it open otherwise
	}
	c.mu.Lock()
	if c.shut {
		c.mu.Unlock()
		keep()
		return session.ErrShuttingDown
	}
	if c.gens[folderID] != gen {
		c.mu.Unlock()
		keep()
		return fmt.Errorf("%w: it went offline or moved while it was being opened", library.ErrOffline)
	}
	prev := c.open
	if prev != nil && prev.id == folderID {
		c.mu.Unlock()
		keep() // already open: the catalog holds its reference
		return nil
	}
	f := &folder{id: folderID, s: s, release: keep}
	f.ref = newRefresher(c, folderID, s)
	c.open = f
	if prev != nil {
		c.closing.Add(1) // under mu, so CloseAll's Wait sees it
	}
	c.mu.Unlock()
	if prev != nil {
		go func() {
			defer c.closing.Done()
			c.closeFolder(prev, false)
		}()
	}
	return nil
}

// Close closes the folder when it is the one open: it waits for a mutation
// in flight, drops the undo stack, runs an owed Library refresh at once and
// waits for it, then releases the catalog's session reference. Closing a
// folder that is not open does nothing.
func (c *Catalog) Close(folderID string) {
	if closeFn := c.Detach(folderID); closeFn != nil {
		closeFn()
	}
}

// Detach lets go of the folder at once when it is the one open — a later
// Open installs a fresh instance — and returns the function that closes the
// instance it detached, as Close does (waiting for its mutation in flight,
// flushing its Library refresh, releasing its reference). That function acts
// only on that instance, never on one a later Open installed, so it may run
// later on any goroutine; it must be called once (more calls do nothing),
// and CloseAll waits for it. Detach returns nil when the folder is not open.
func (c *Catalog) Detach(folderID string) (closeFn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detachLocked(folderID)
}

// Invalidate is Detach for a folder that went offline, moved or was
// removed: it also counts an invalidation of folderID, even when the folder
// is not open, so an Open in progress — whose session was acquired before
// this — does not install it (see Open). The runtime calls it from the
// Library's OnOffline and OnRemoved.
func (c *Catalog) Invalidate(folderID string) (closeFn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gens == nil {
		c.gens = map[string]uint64{}
	}
	c.gens[folderID]++
	return c.detachLocked(folderID)
}

// detachLocked is Detach's work; the caller holds mu.
func (c *Catalog) detachLocked(folderID string) func() {
	f := c.open
	if f == nil || f.id != folderID {
		return nil
	}
	c.open = nil
	c.closing.Add(1) // under mu, so a CloseAll that follows waits for it
	return sync.OnceFunc(func() {
		defer c.closing.Done()
		c.closeFolder(f, false)
	})
}

// CloseAll is the shutdown: it refuses every later Open, cancels the
// catalog's context, waits for the open folder's mutation in flight, runs
// only RefreshStats for a refresh that had not started (a RefreshPreviews
// already running ends on the cancelled context, leaving the previous strip
// intact), releases the session references and waits for every close in
// progress (Close's and those Open started) — which, the context being
// cancelled, refresh statistics only. It may be called more than once.
func (c *Catalog) CloseAll() {
	c.mu.Lock()
	c.shut = true
	f := c.open
	c.open = nil
	c.mu.Unlock()
	c.cancel()
	if f != nil {
		c.closeFolder(f, true)
	}
	c.event(hookCloseAllWait, "")
	c.closing.Wait()
}

// OpenFolder returns the id of the open folder, or "".
func (c *Catalog) OpenFolder() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		return ""
	}
	return c.open.id
}

// closeFolder closes f, already detached from c.open: it waits for f's
// mutation in flight, marks f closed and drops its undo stack, flushes its
// refresher (statistics only for shutdown) and releases the catalog's
// reference.
func (c *Catalog) closeFolder(f *folder, shutdown bool) {
	lock := c.lockFor(f.id)
	c.event(hookCloseWait, f.id)
	lock.Lock()
	f.closed = true
	f.undo.clear()
	lock.Unlock()
	f.ref.flush(shutdown)
	f.release()
}

// Clusters returns the cluster grid of the open folder.
func (c *Catalog) Clusters(folderID string) (FolderView, error) {
	var v FolderView
	err := c.read(folderID, func(s *session.Session) error {
		var err error
		v, err = c.view(s)
		return err
	})
	if err != nil {
		return FolderView{}, fmt.Errorf("catalog: clusters of %s: %w", folderID, err)
	}
	return v, nil
}

// Cluster returns the focus payload of one cluster of the open folder; an
// unknown cluster, or one without members, is store.ErrNotFound.
func (c *Catalog) Cluster(folderID string, clusterID int64) (FocusResponse, error) {
	var r FocusResponse
	err := c.read(folderID, func(s *session.Session) error {
		var err error
		r, err = buildFocus(s.Store, clusterID, c.d.Settings().QualityRating)
		return err
	})
	if err != nil {
		return FocusResponse{}, fmt.Errorf("catalog: cluster %d of %s: %w", clusterID, folderID, err)
	}
	return r, nil
}

// SetPick applies action to fileID: pick (export a copy, then mark
// picked), reject, unpick (only a picked file becomes unmarked) or unreject
// (only a rejected file becomes unmarked); anything else is
// ErrUnknownAction. It returns once the change is durably committed. A
// change is pushed on the folder's undo stack and schedules the Library
// refresh; a no-op is neither. An unknown file is store.ErrNotFound; a
// folder whose root no longer exists is library.ErrOffline; a picks
// directory that cannot take picks refuses a pick as an invalid picks_dir
// error (reason "has_originals" or a picks.DirError reason), while reject,
// unpick and unreject still work.
func (c *Catalog) SetPick(folderID string, fileID int64, action string) (PickResult, error) {
	switch action {
	case actionPick, actionUnpick, actionReject, actionUnreject:
	default:
		return PickResult{}, fmt.Errorf("catalog: %q: %w", action, ErrUnknownAction)
	}
	var res PickResult
	err := c.mutate(folderID, true, func(f *folder, s *session.Session) error {
		cur, exported, err := pickState(s.Store, fileID)
		if err != nil {
			return err
		}
		target, change := targetOf(action, cur)
		if !change {
			res = pickResult(fileID, cur, exported)
			return nil
		}
		r, err := c.set(s, fileID, target)
		if err != nil {
			return err
		}
		res = pickResult(fileID, r.State, r.ExportedPath)
		if r.State != cur {
			f.undo.push(undoEntry{fileID: fileID, prev: cur})
			f.ref.schedule(true)
		}
		return nil
	})
	if err != nil {
		return PickResult{}, fmt.Errorf("catalog: %s file %d in %s: %w", action, fileID, folderID, err)
	}
	return res, nil
}

// Undo reverts the newest change on the folder's undo stack ([call] C22),
// committed like SetPick, and returns the file's state. An empty stack is
// ErrNothingToUndo. A change that cannot be reverted stays on the stack,
// unless its file is gone from the folder.
func (c *Catalog) Undo(folderID string) (PickResult, error) {
	var res PickResult
	err := c.mutate(folderID, true, func(f *folder, s *session.Session) error {
		e, ok := f.undo.pop()
		if !ok {
			return ErrNothingToUndo
		}
		if _, err := s.Store.Files().GetByID(e.fileID); errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("file %d: %w", e.fileID, err)
		}
		r, err := c.set(s, e.fileID, e.prev)
		if err != nil {
			f.undo.push(e) // just popped: nothing is dropped
			return err
		}
		res = pickResult(e.fileID, r.State, r.ExportedPath)
		f.ref.schedule(true)
		return nil
	})
	if err != nil {
		return PickResult{}, fmt.Errorf("catalog: undo in %s: %w", folderID, err)
	}
	return res, nil
}

// Recluster rebuilds the open folder's clusters with the current settings,
// serialised with its picks, emits folder:changed "clusters" and schedules
// a statistics-only Library refresh (the cluster count) through the same
// debounce as picks.
func (c *Catalog) Recluster(ctx context.Context, folderID string) error {
	err := c.mutate(folderID, false, func(f *folder, s *session.Session) error {
		if err := s.Recluster(ctx, c.d.Settings); err != nil {
			return err
		}
		f.ref.schedule(false)
		return nil
	})
	if err != nil {
		return fmt.Errorf("catalog: re-cluster %s: %w", folderID, err)
	}
	if c.d.Emit != nil {
		c.d.Emit.Emit(events.FolderChanged, events.FolderChangedEvent{FolderID: folderID, Reason: "clusters"})
	}
	return nil
}

// Ratings returns the open folder's current quality ratings, 0–10 by
// decimal file id, for every cluster member that has one (as Member.Quality
// shows them). The map is empty, not nil, while quality_rating is off.
func (c *Catalog) Ratings(folderID string) (map[string]float64, error) {
	out := map[string]float64{}
	err := c.read(folderID, func(s *session.Session) error {
		if !c.d.Settings().QualityRating {
			return nil
		}
		return ratingsOf(c.ctx, s.Store, out)
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: ratings of %s: %w", folderID, err)
	}
	return out, nil
}

// ratingsOf adds the current rating of every rated cluster member to out.
func ratingsOf(ctx context.Context, st *store.Store, out map[string]float64) error {
	rows, err := st.DB().QueryContext(ctx,
		`SELECT f.id, f.kind, f.quality_raw, f.quality_model
		   FROM files f
		  WHERE f.quality_raw IS NOT NULL
		    AND EXISTS (SELECT 1 FROM cluster_members cm WHERE cm.file_id = f.id)`)
	if err != nil {
		return fmt.Errorf("read ratings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m store.ClusterMemberView
		if err := rows.Scan(&m.FileID, &m.Kind, &m.QualityRaw, &m.QualityModel); err != nil {
			return fmt.Errorf("read ratings: %w", err)
		}
		if q := memberQuality(m, true); q != nil {
			out[strconv.FormatInt(m.FileID, 10)] = *q
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read ratings: %w", err)
	}
	return nil
}

// view builds the cluster grid of the open folder's session s.
func (c *Catalog) view(s *session.Session) (FolderView, error) {
	cfg := c.d.Settings()
	v, err := buildFolderView(s.Store, filepath.Base(s.Root), cfg)
	if err != nil {
		return FolderView{}, err
	}
	dir, err := s.PicksDir(cfg)
	if err != nil {
		reason := blockedReason(err)
		if reason == "" {
			return FolderView{}, err
		}
		v.PicksBlocked = reason
	}
	v.PicksDir = dir
	v.PicksMissing = s.PicksMissing()
	return v, nil
}

// blockedReason returns the reason code of a picks directory refused by
// Session.PicksDir, or "" for any other error.
func blockedReason(err error) string {
	var de *picks.DirError
	switch {
	case errors.As(err, &de):
		return de.Reason
	case errors.Is(err, picks.ErrDirHasOriginals):
		return "has_originals"
	}
	return ""
}

// lookup returns the open folder when it is folderID.
func (c *Catalog) lookup(folderID string) *folder {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open != nil && c.open.id == folderID {
		return c.open
	}
	return nil
}

// notOpen is the error for an operation on folderID when the catalog cannot
// serve it: session.ErrShuttingDown once shutdown began, else ErrNotOpen.
func (c *Catalog) notOpen(folderID string) error {
	c.mu.Lock()
	shut := c.shut
	c.mu.Unlock()
	if shut {
		return fmt.Errorf("%s: %w", folderID, session.ErrShuttingDown)
	}
	return fmt.Errorf("%s: %w", folderID, ErrNotOpen)
}

// read runs fn with a session reference of its own on the open folder.
func (c *Catalog) read(folderID string, fn func(s *session.Session) error) error {
	if c.lookup(folderID) == nil {
		return c.notOpen(folderID)
	}
	s, release, ok := c.d.Sessions.AcquireOpen(folderID)
	if !ok {
		// The catalog holds a reference, so only a manager shutdown refuses.
		return fmt.Errorf("%s: %w", folderID, session.ErrShuttingDown)
	}
	defer release()
	return fn(s)
}

// mutate runs fn under the folder id's mutation lock, on the instance open
// when the lock is held, with a session reference of its own, so Close and
// shutdown wait for it. With checkRoot, a folder whose
// root no longer exists — before fn or after fn failed — is
// library.ErrOffline. A picks.DirError from fn becomes an invalid
// picks_dir error.
func (c *Catalog) mutate(folderID string, checkRoot bool, fn func(f *folder, s *session.Session) error) error {
	if c.lookup(folderID) == nil {
		return c.notOpen(folderID)
	}
	lock := c.lockFor(folderID)
	c.event(hookMutateWait, folderID)
	lock.Lock()
	defer lock.Unlock()
	// The instance open now: the one looked up above may have been closed
	// (and the folder opened again) while this waited.
	f := c.lookup(folderID)
	if f == nil || f.closed {
		return c.notOpen(folderID)
	}
	s, release, ok := c.d.Sessions.AcquireOpen(folderID)
	if !ok {
		return fmt.Errorf("%s: %w", folderID, session.ErrShuttingDown)
	}
	defer release()
	if checkRoot && rootGone(s.Root) {
		return fmt.Errorf("%s: %w", folderID, library.ErrOffline)
	}
	if err := fn(f, s); err != nil {
		if checkRoot && rootGone(s.Root) {
			return fmt.Errorf("%w: %w", library.ErrOffline, err)
		}
		return classify(err)
	}
	return nil
}

// rootGone reports whether the folder's root no longer exists: deleted, or
// its volume unplugged.
func rootGone(root string) bool {
	_, err := os.Lstat(root)
	return errors.Is(err, fs.ErrNotExist)
}

// classify turns a picks.DirError into the invalid picks_dir error the
// frontend expects; other errors pass through (apperr maps the sentinels).
func classify(err error) error {
	var de *picks.DirError
	if errors.As(err, &de) {
		return fmt.Errorf("%w: %w", apperr.Invalid(picksDirField, de.Reason, de.Error()), err)
	}
	return err
}

// set moves fileID to target through the folder's pick service, which
// commits durably before it returns. The operation is not cancelled by
// shutdown: CloseAll waits for it.
func (c *Catalog) set(s *session.Session, fileID int64, target picks.State) (picks.Result, error) {
	svc, err := s.Picks(c.d.Settings())
	if err != nil {
		return picks.Result{}, err
	}
	return svc.Set(context.WithoutCancel(c.ctx), fileID, target)
}

// pickState returns fileID's stored state and exported path; an unknown
// file is store.ErrNotFound.
func pickState(st *store.Store, fileID int64) (picks.State, string, error) {
	if _, err := st.Files().GetByID(fileID); err != nil {
		return "", "", fmt.Errorf("file %d: %w", fileID, err)
	}
	p, err := st.Picks().Get(fileID)
	if errors.Is(err, store.ErrNotFound) {
		return picks.StateNone, "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("pick state of file %d: %w", fileID, err)
	}
	return picks.State(p.State), nullString(p.ExportedPath), nil
}

func nullString(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}

// targetOf returns the state action moves a file in state cur to, and
// whether that is a change. unpick and unreject leave a file in another
// state as it is (v1's semantics).
func targetOf(action string, cur picks.State) (picks.State, bool) {
	var target picks.State
	switch action {
	case actionPick:
		target = picks.StatePicked
	case actionReject:
		target = picks.StateRejected
	case actionUnpick:
		if cur != picks.StatePicked {
			return cur, false
		}
		target = picks.StateNone
	case actionUnreject:
		if cur != picks.StateRejected {
			return cur, false
		}
		target = picks.StateNone
	}
	return target, target != cur
}

// pickResult is the PickResult of fileID in state st.
func pickResult(fileID int64, st picks.State, exported string) PickResult {
	switch st {
	case picks.StatePicked:
		return PickResult{FileID: fileID, State: statePicked, ExportedPath: exported}
	case picks.StateRejected:
		return PickResult{FileID: fileID, State: stateRejected}
	}
	return PickResult{FileID: fileID, State: stateUnmarked}
}

func (c *Catalog) event(event, folderID string) {
	if c.hook != nil {
		c.hook(event, folderID)
	}
}
