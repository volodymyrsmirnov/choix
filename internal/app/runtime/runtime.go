// Package runtime is the app's composition root: New opens library.db,
// loads the settings and builds every core and binding; Start begins the
// background work; the subscriptions made in New connect the cores; and
// Shutdown tears everything down in a fixed order, within a deadline.
//
// cmd/choix only adapts it to Wails: the package does not import Wails, and
// its tests run with the platform fakes, without a window or cgo.
//
// The connections between the cores:
//
//   - settings change → quality.SettingChanged (switching the rating off
//     cancels the pass in flight at once); quality_rating or visual_grouping
//     switched on queues a backfill of every reachable folder (nothing is
//     queued at launch for finished folders); every scan:state →
//     quality.Changed;
//   - Library: a folder offline → its job suspended and the catalog lets go
//     of it; removed → its job cancelled and the catalog lets go; online →
//     its pending job resumed;
//   - a job suspended → Library.CheckFolder at once;
//   - opening a folder in the catalog → a full job at the front;
//   - the end of a job → the folder's statistics and preview strip.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/user"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/catalog"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/media"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/app/settings"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// QuitDeadline bounds Shutdown: when the drain has not finished by then,
// Shutdown returns anyway and what is still in use stays open.
const QuitDeadline = 5 * time.Second

// NativeQuitWait is how long, from the cancellation at quit, sessions wait
// for their abandoned native calls before Shutdown gives up on them. It lies
// inside QuitDeadline.
const NativeQuitWait = 2 * time.Second

const (
	// presenceEvery is the interval of the periodic presence check.
	presenceEvery = 30 * time.Second
	// coalesceEvery is the delivery interval of coalesced events.
	coalesceEvery = 100 * time.Millisecond
)

// Steps of Shutdown, in order, reported to Runtime.hook (tests) as each
// ends. stepLibraryClosed is reported only when library.db was free and was
// closed.
const (
	stepGateClosed     = "gate closed"
	stepCancelled      = "context cancelled"
	stepSilenced       = "emitter closed"
	stepQueueStopped   = "queue stopped"
	stepCatalogClosed  = "catalog closed"
	stepSessionsClosed = "sessions closed"
	stepLibraryClosed  = "library closed"
	stepDone           = "done"
)

// Deps are what the runtime is built from: the platform, the frontend's
// event sink, the native dialogs and menu and the window's title bar.
// Bookmarks, Volumes, Workspace, Stills, Video, Vision, Picker and Confirm
// are required.
type Deps struct {
	Bookmarks platform.Bookmarks
	Volumes   platform.Volumes
	Workspace platform.Workspace
	Stills    platform.Stills
	Video     platform.Video
	// Vision makes the feature prints of visual grouping.
	Vision platform.Vision

	// Emit receives every event for the frontend; nil sends none. If it also
	// has a Close method (Close() or Close() error), Shutdown calls it once
	// the runtime has stopped emitting — cmd/choix's emitter has one.
	Emit events.Emitter

	Picker    library.FolderPicker
	Confirm   library.Confirmer
	MenuState system.MenuState // nil: no native menu
	TitleBar  system.TitleBar  // nil: no window (server builds)

	// Home is the user's real home directory, shown as "~" in the Library.
	// Empty: it is read from the password database, never from $HOME, which
	// inside the App Sandbox is the app's container.
	Home string
}

// Runtime holds the app's cores and the bindings over them. The exported
// fields are the frontend bindings and the media handler; they are set by
// New and never change.
type Runtime struct {
	Library  *library.Service
	Catalog  *catalog.Service
	Settings *settings.Service
	System   *system.Service
	Media    *media.Handler

	d    Deps
	gate *opgate.Gate
	// ctx is the root context: every background operation, request and job
	// runs under it, and Shutdown cancels it.
	ctx    context.Context
	cancel context.CancelFunc

	out       *gatedEmitter
	coalescer *events.Coalescer
	db        *library.DB
	lib       *library.Library
	settings  *settings.Store
	sessions  *session.Manager
	quality   *session.QualityTracker
	queue     *session.Queue
	catalog   *catalog.Catalog

	visible atomic.Bool
	// queueStarted is set just before the queue starts. Until then OnOnline
	// does not resume anything: the queue's start loads every pending job
	// of a present folder, in the order they were enqueued.
	queueStarted atomic.Bool

	// presenceEvery is the presence interval; tests lengthen it before
	// Start so no tick runs.
	presenceEvery time.Duration
	// hook, when set by a test before Shutdown, observes the shutdown steps.
	hook func(step string)
	// closeLater runs the background close of a folder instance the catalog
	// detached (goClose); tests replace it, before Start, to choose when the
	// close runs.
	closeLater func(closeFn func())
	// scoreHook, when set by a test before Start, runs before each photo a
	// rating pass scores.
	scoreHook func()

	// lifeMu orders Start against Shutdown: once shut is set nothing more
	// is started.
	lifeMu        sync.Mutex
	started, shut bool
	coalesced     chan struct{} // closed when the coalescer loop returns; nil before Start

	// bg tracks the runtime's own goroutines (presence, callbacks handed
	// off); library.db is closed only after they have returned. bgMu guards
	// bgClosed and the Add of bg.
	bgMu     sync.Mutex
	bgClosed bool
	bg       sync.WaitGroup

	shutdownOnce sync.Once
	quitAt       time.Time     // set by the first Shutdown
	drained      chan struct{} // closed when the drain has finished
	reportOnce   sync.Once

	// stateMu guards stage and inUse, what the deadline log reports.
	stateMu sync.Mutex
	stage   string
	inUse   []string
}

// New opens library.db, loads the settings and builds every core and
// binding. It starts nothing: no goroutine runs and no event is emitted
// before Start.
func New(d Deps) (*Runtime, error) {
	if err := checkDeps(d); err != nil {
		return nil, err
	}
	store, err := settings.NewStore()
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	dbPath, err := appdir.LibraryDB()
	if err != nil {
		return nil, fmt.Errorf("runtime: library database path: %w", err)
	}
	db, err := library.OpenDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{
		d:             d,
		gate:          &opgate.Gate{},
		ctx:           ctx,
		cancel:        cancel,
		out:           &gatedEmitter{inner: d.Emit},
		db:            db,
		settings:      store,
		presenceEvery: presenceEvery,
	}
	r.visible.Store(true)
	r.closeLater = r.goClose
	r.coalescer = events.NewCoalescer(tapEmitter{r: r}, coalesceEvery)

	r.lib = library.New(library.Options{
		DB:          db,
		Bookmarks:   d.Bookmarks,
		Volumes:     d.Volumes,
		Emit:        r.out,
		OnOffline:   r.onOffline,
		OnRemoved:   r.onRemoved,
		OnOnline:    r.onOnline,
		PreviewsDir: appdir.PreviewsPath,
	})

	r.sessions = session.NewManager(session.Deps{
		Library:  r.lib,
		Settings: r.settings.Get,
		Stills:   d.Stills,
		Video:    d.Video,
		Vision:   d.Vision,
	})

	r.quality = session.NewQualityTracker(session.QualityOptions{
		Enabled: func() bool { return r.settings.Get().QualityRating },
		Busy:    func() bool { return !r.queue.Idle() },
		Emit:    r.coalescer,
	})

	job := session.NewJob(session.JobOptions{
		Sessions: r.sessions,
		Settings: r.settings.Get,
		Quality:  r.quality,
		Emit:     r.coalescer,
		AfterJob: r.afterJob,
		BeforeScore: func() {
			if r.scoreHook != nil {
				r.scoreHook()
			}
		},
	})
	r.queue = session.NewQueue(session.QueueOptions{
		Library:     r.lib,
		Runner:      job,
		Emit:        r.coalescer,
		OnSuspended: r.onSuspended,
		Slots:       session.DefaultSlots,
	})

	r.catalog = catalog.New(catalog.Deps{
		Sessions:     r.sessions,
		Library:      r.lib,
		Settings:     r.settings.Get,
		EnqueueFront: r.enqueueFront,
		Emit:         r.out,
		Stills:       d.Stills,
		PreviewsDir:  r.previewsDir,
		Context:      ctx,
	})

	r.settings.OnChange(r.onSettingsChanged)

	r.Library = library.NewService(library.ServiceDeps{
		Library:    r.lib,
		Jobs:       r.queue,
		Enqueue:    r.enqueue,
		Picker:     d.Picker,
		Confirm:    d.Confirm,
		Home:       homeDir(d.Home),
		PreviewURL: media.PreviewURL,
		Gate:       r.gate,
	})
	r.Catalog = catalog.NewService(r.catalog, r.quality.Status, r.gate)
	r.Settings = settings.NewService(r.settings, settings.Hooks{Recluster: r.reclusterOpen}, r.gate)
	r.System = system.NewService(system.Deps{
		Library:     r.lib,
		Sessions:    r.sessions,
		Settings:    r.settings.Get,
		FolderState: r.folderState,
		Workspace:   d.Workspace,
		MenuState:   d.MenuState,
		TitleBar:    d.TitleBar,
		Gate:        r.gate,
	})
	r.Media = media.New(ctx, r.sessions, d.Stills, appdir.PreviewsPath, r.gate)
	return r, nil
}

// checkDeps reports the first required dependency that is missing.
func checkDeps(d Deps) error {
	missing := ""
	switch {
	case d.Bookmarks == nil:
		missing = "Bookmarks"
	case d.Volumes == nil:
		missing = "Volumes"
	case d.Workspace == nil:
		missing = "Workspace"
	case d.Stills == nil:
		missing = "Stills"
	case d.Video == nil:
		missing = "Video"
	case d.Vision == nil:
		missing = "Vision"
	case d.Picker == nil:
		missing = "Picker"
	case d.Confirm == nil:
		missing = "Confirm"
	}
	if missing != "" {
		return fmt.Errorf("runtime: Deps.%s is required", missing)
	}
	return nil
}

// homeDir returns home, or when it is empty the current user's home
// directory from the password database ("" when that cannot be read).
func homeDir(home string) string {
	if home != "" {
		return home
	}
	u, err := user.Current()
	if err != nil {
		slog.Warn("runtime: home directory unknown; paths are shown in full", "err", err)
		return ""
	}
	return u.HomeDir
}

// Start begins the background work: the event coalescer, the removal of an
// earlier build's model files, a presence check of every folder followed by
// the periodic one (every 30 s while visible) and the job queue. It returns once the queue has
// started; the work goes on in the background until Shutdown, or until ctx
// is done. Start does nothing when called again or after Shutdown began.
func (r *Runtime) Start(ctx context.Context) {
	r.lifeMu.Lock()
	if r.started || r.shut {
		r.lifeMu.Unlock()
		return
	}
	r.started = true
	r.coalesced = make(chan struct{})
	go func() {
		defer close(r.coalesced)
		r.coalescer.Run(r.ctx)
	}()
	if ctx != nil {
		context.AfterFunc(ctx, r.cancel)
	}
	r.lifeMu.Unlock()

	r.goBackground(cleanLegacyModels)

	// The launch check, before the queue loads the pending jobs, so that
	// the folders it loads are the ones present now.
	if r.track() {
		if _, err := r.lib.CheckPresence(r.ctx); err != nil && r.ctx.Err() == nil {
			slog.Warn("runtime: presence check at launch", "err", err)
		}
		r.bg.Done()
	}

	r.lifeMu.Lock()
	defer r.lifeMu.Unlock()
	if r.shut {
		return
	}
	r.goBackground(func() { r.lib.RunPresence(r.ctx, r.presenceEvery, r.visible.Load) })
	r.queueStarted.Store(true)
	r.queue.Start(r.ctx)
}

// SetVisible tells the runtime whether the window is visible; the periodic
// presence check skips its ticks while it is not.
func (r *Runtime) SetVisible(v bool) { r.visible.Store(v) }

// AddDropped adds the folders dropped on the window, each new one queued for
// a full job (Library.AddPaths). Paths that cannot be added are logged and
// left out. Once Shutdown has begun it adds nothing and returns nil.
func (r *Runtime) AddDropped(paths []string) []library.AddResult {
	leave, err := r.gate.Enter()
	if err != nil {
		return nil
	}
	defer leave()
	return r.lib.AddPaths(paths, func(id string) {
		if err := r.queue.Enqueue(id, session.JobFull); err != nil {
			slog.Warn("runtime: queue a dropped folder; Rescan starts it", "folder", id, "err", err)
		}
	})
}

// Idle reports whether no job is queued or running.
func (r *Runtime) Idle() bool {
	return r.queue.Idle()
}

// track counts one of the runtime's own goroutines (or a synchronous piece
// of background work), unless the drain has begun; the caller calls bg.Done
// when it ends.
func (r *Runtime) track() bool {
	r.bgMu.Lock()
	defer r.bgMu.Unlock()
	if r.bgClosed {
		return false
	}
	r.bg.Add(1)
	return true
}

// goBackground runs fn on a goroutine library.db outlives, unless the drain
// has begun (then fn does not run).
func (r *Runtime) goBackground(fn func()) {
	if !r.track() {
		return
	}
	go func() {
		defer r.bg.Done()
		fn()
	}()
}

// --- subscriptions --------------------------------------------------------

// onSettingsChanged runs while saves are serialised: it must be quick and
// must not save. Queueing the backfill is handed to a goroutine.
// Switching quality_rating or visual_grouping from off to on queues a
// backfill of every reachable folder: the photos rated or printed while it
// was off are pending.
func (r *Runtime) onSettingsChanged(old, cur config.Config) {
	r.quality.SettingChanged()
	if (cur.QualityRating && !old.QualityRating) || (cur.VisualGrouping && !old.VisualGrouping) {
		r.goBackground(func() {
			if r.ctx.Err() == nil {
				r.queue.EnqueueAllReachable(session.JobBackfill)
			}
		})
	}
}

// onOffline: the folder went offline, missing, lost its access or moved
// while in use. Delivered by the Library's notification drainer, possibly
// on a job's goroutine: the job is suspended without waiting, and the
// catalog lets go of the folder at once (Invalidate, which also keeps an
// Open in progress from installing the session it acquired before) and
// closes that instance — which waits for a pick in flight and a refresh — in
// the background. The folder may come back and be opened again before that
// close runs; the close never touches the instance that Open installs.
func (r *Runtime) onOffline(id string) {
	r.queue.Suspend(id)
	if closeFn := r.catalog.Invalidate(id); closeFn != nil {
		r.closeLater(closeFn)
	}
}

// onRemoved: the folder was removed or found gone. Its job is stopped at
// once and the catalog lets go of it at once; Queue.Cancel, which waits for
// the job to return, and the close of the detached instance run in the
// background — this may be the job's own goroutine.
func (r *Runtime) onRemoved(id string) {
	r.queue.Suspend(id)
	closeFn := r.catalog.Invalidate(id)
	r.goBackground(func() { r.queue.Cancel(id) })
	if closeFn != nil {
		r.closeLater(closeFn)
	}
}

// goClose runs closeFn, the close of an instance the catalog detached, on a
// goroutine library.db outlives — or, once the drain has begun, on an
// untracked one: closeFn must run, since CloseAll waits for it, and the
// drain closes library.db only after CloseAll has returned.
func (r *Runtime) goClose(closeFn func()) {
	if !r.track() {
		go closeFn()
		return
	}
	go func() {
		defer r.bg.Done()
		closeFn()
	}()
}

// onOnline: the folder is present with a pending job.
func (r *Runtime) onOnline(id string) {
	if !r.queueStarted.Load() {
		return // the queue's start loads it, in enqueue order
	}
	r.queue.Resume(id)
}

// onSuspended runs on the job's goroutine after a job was suspended: the
// folder is checked at once, so the row turns offline or missing — or, when
// the folder is in fact present, OnOnline queues the job again — without
// waiting for the next presence tick.
func (r *Runtime) onSuspended(id string) {
	if r.ctx.Err() != nil {
		return
	}
	if _, err := r.lib.CheckFolder(r.ctx, id); err != nil && !errors.Is(err, library.ErrNotFound) && r.ctx.Err() == nil {
		slog.Warn("runtime: presence check of a suspended folder", "folder", id, "err", err)
	}
}

// enqueue is the Library binding's: a full or backfill job at the tail.
func (r *Runtime) enqueue(id string, full bool) error {
	kind := session.JobBackfill
	if full {
		kind = session.JobFull
	}
	return r.queue.Enqueue(id, kind)
}

// enqueueFront is the catalog's: opening a folder rescans it first.
func (r *Runtime) enqueueFront(id string) {
	if err := r.queue.EnqueueFront(id, session.JobFull); err != nil {
		slog.Warn("runtime: queue the rescan of an opened folder", "folder", id, "err", err)
	}
}

// previewsDir returns the folder's previews directory, creating it, for a
// refresh that writes the strip. A folder no longer in the Library gets none,
// so a refresh racing its removal does not recreate the directory.
func (r *Runtime) previewsDir(id string) (string, error) {
	if _, err := r.lib.Folder(id); err != nil {
		return "", err
	}
	return appdir.Previews(id)
}

// afterJob refreshes the folder's statistics and preview strip with one
// library:changed "stats". Once quit has begun only the statistics are
// stored; the next job or open redoes the strip.
func (r *Runtime) afterJob(ctx context.Context, s *session.Session) {
	dir, err := r.previewsDir(s.FolderID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			return
		}
		slog.Warn("runtime: previews directory", "folder", s.FolderID, "err", err)
		if err := r.lib.RefreshStats(s.FolderID, s.Store); err != nil {
			slog.Warn("runtime: refresh library statistics", "folder", s.FolderID, "err", err)
		}
		return
	}
	err = r.lib.RefreshStatsAndPreviews(ctx, s.FolderID, s.Store, s.Root, r.d.Stills, dir)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		slog.Info("runtime: preview refresh stopped", "folder", s.FolderID)
	default:
		slog.Warn("runtime: refresh library statistics and previews", "folder", s.FolderID, "err", err)
	}
}

// reclusterOpen is the settings binding's re-cluster hook: the open folder,
// if any, is re-clustered with the settings just saved.
func (r *Runtime) reclusterOpen() (bool, error) {
	id := r.catalog.OpenFolder()
	if id == "" {
		return false, nil
	}
	err := r.catalog.Recluster(r.ctx, id)
	switch {
	case errors.Is(err, catalog.ErrNotOpen):
		return false, nil // closed meanwhile; it re-clusters when opened
	case err != nil:
		return false, err
	}
	return true, nil
}

// folderState is the folder's state as the Library page shows it.
func (r *Runtime) folderState(id string) (string, error) {
	f, err := r.lib.Folder(id)
	if err != nil {
		return "", err
	}
	state, p := r.queue.State(id)
	return library.Summarise(f, state, p, "", nil).State, nil
}

// --- shutdown -------------------------------------------------------------

// Shutdown stops the runtime (R17). It is called on Wails' main thread, so
// the work runs on a drain goroutine that never needs that thread, and
// Shutdown only waits for it — at most until QuitDeadline after the first
// call. The drain:
//
//  1. refuses new bindings, media requests and AddDropped (the gate);
//  2. cancels the root context: the running jobs, the quality passes, the
//     presence checks, media requests and transcodes and catalog refreshes
//     stop;
//  3. closes the emitter: nothing calls into the frontend afterwards;
//  4. stops the queue, closes the catalog (a pick in flight is awaited,
//     never cut) and shuts the sessions down: each closes once unreferenced
//     and without detached native calls, which get NativeQuitWait;
//  5. closes library.db once no binding and no background work uses it.
//
// When the deadline passes first, Shutdown returns and logs what is still in
// use. Nothing still referenced is closed: such stores and bookmarks stay
// open as the process exits (SQLite's WAL is crash-safe,
// originals are only read, and every pick that returned is durable).
// Shutdown is idempotent; a later call waits for the same deadline.
func (r *Runtime) Shutdown() {
	r.shutdownOnce.Do(func() {
		r.quitAt = time.Now().Add(QuitDeadline)
		r.drained = make(chan struct{})
		go r.drain()
	})
	timer := time.NewTimer(time.Until(r.quitAt))
	defer timer.Stop()
	select {
	case <-r.drained:
	case <-timer.C:
		r.reportOnce.Do(r.reportInUse)
	}
}

// drain does the shutdown steps; see Shutdown.
func (r *Runtime) drain() {
	defer close(r.drained)
	// When quit began; the context is cancelled right after the gate closes,
	// which abandons the native calls in flight.
	began := r.quitAt.Add(-QuitDeadline)

	// 1. Refuse.
	r.setStage("refusing new operations")
	r.lifeMu.Lock()
	r.shut = true
	coalesced := r.coalesced
	r.lifeMu.Unlock()
	r.gate.Close(0)
	r.step(stepGateClosed)

	// 2. Cancel.
	r.setStage("cancelling")
	r.bgMu.Lock()
	r.bgClosed = true
	r.bgMu.Unlock()
	r.cancel()
	r.step(stepCancelled)

	// 3. Silence. The coalescer delivers what it holds when its context
	// ends; that goes out before the emitter closes.
	r.setStage("closing the event emitter")
	if coalesced != nil {
		<-coalesced
	}
	r.out.close()
	r.step(stepSilenced)

	// 4. Drain.
	r.setStage("stopping the job queue")
	r.queue.Stop()
	r.step(stepQueueStopped)
	r.setStage("closing the open folder")
	r.catalog.CloseAll()
	r.step(stepCatalogClosed)

	r.setStage("closing folder sessions")
	until := began.Add(NativeQuitWait)
	if r.quitAt.Before(until) {
		until = r.quitAt
	}
	sctx, scancel := context.WithDeadline(context.Background(), until)
	open := r.sessions.Shutdown(sctx)
	scancel()
	if open > 0 {
		r.noteInUse(fmt.Sprintf("%d folder session(s) with a native call or a reference still running", open))
		slog.Warn("runtime: folder sessions left open at quit", "sessions", open)
	}
	r.step(stepSessionsClosed)

	// 5. Close what is free.
	r.setStage("closing the library database")
	if r.waitUnused() {
		if err := r.db.Close(); err != nil {
			slog.Warn("runtime: close the library database", "err", err)
		}
		r.step(stepLibraryClosed)
	} else {
		r.noteInUse("the library database is still in use")
		slog.Warn("runtime: the library database is still in use; leaving it open")
	}
	r.setStage(stepDone)
	r.step(stepDone)
}

// waitUnused waits, until the quit deadline, for every binding to leave the
// gate and every background goroutine of the runtime to return; it reports
// whether all did.
func (r *Runtime) waitUnused() bool {
	if !r.gate.Close(time.Until(r.quitAt)) {
		return false
	}
	done := make(chan struct{})
	go func() {
		r.bg.Wait()
		close(done)
	}()
	timer := time.NewTimer(time.Until(r.quitAt))
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// step reports the end of a shutdown step to the test hook.
func (r *Runtime) step(name string) {
	if r.hook != nil {
		r.hook(name)
	}
}

func (r *Runtime) setStage(s string) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.stage = s
}

func (r *Runtime) noteInUse(s string) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.inUse = append(r.inUse, s)
}

// reportInUse logs, once the deadline has passed, where the drain is and
// what it found still in use.
func (r *Runtime) reportInUse() {
	r.stateMu.Lock()
	stage, inUse := r.stage, strings.Join(r.inUse, "; ")
	r.stateMu.Unlock()
	slog.Warn("runtime: quit deadline passed; exiting with what is still in use left open",
		"deadline", QuitDeadline, "stage", stage, "in_use", inUse)
}

// --- events ---------------------------------------------------------------

// gatedEmitter is the runtime's one way to the frontend. close stops it: it
// waits for an Emit in flight, after which nothing reaches the inner
// emitter, then closes the inner emitter when it has a Close method.
type gatedEmitter struct {
	mu     sync.RWMutex
	closed bool
	inner  events.Emitter
}

func (g *gatedEmitter) Emit(name string, data any) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed || g.inner == nil {
		return
	}
	g.inner.Emit(name, data)
}

func (g *gatedEmitter) close() {
	g.mu.Lock()
	already := g.closed
	g.closed = true
	g.mu.Unlock()
	if already || g.inner == nil {
		return
	}
	switch c := g.inner.(type) {
	case io.Closer:
		if err := c.Close(); err != nil {
			slog.Warn("runtime: close the event emitter", "err", err)
		}
	case interface{ Close() }:
		c.Close()
	}
}

// tapEmitter is the coalescer's output: it forwards to the gated emitter and
// tells the quality tracker when the queue changed — a scan state, or the
// last progress of a run that stopped — since it feeds the quality status.
// QualityTracker.Changed returns at once, so it is safe here, under the
// queue's lock.
type tapEmitter struct{ r *Runtime }

func (t tapEmitter) Emit(name string, data any) {
	t.r.out.Emit(name, data)
	switch name {
	case events.ScanState, events.ScanProgress:
		if q := t.r.quality; q != nil {
			q.Changed()
		}
	}
}
