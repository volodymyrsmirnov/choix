// Sessions: one folder's open state, its SQLite store and the pipeline that
// works on it. A Session is shared and reference-counted by the Manager: the
// open folder holds one, and the job queue may hold one for the folder it is
// working on. (The package documentation is in queue.go.)
//
// Opening a session makes the confinement checks once (.choix must be a real
// directory inside the folder, and the database files regular files), never
// re-processes a folder last scanned by v1, and never copies anything.

package session

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// ErrReadOnly reports a folder in which Choix cannot write its .choix
// folder: permission denied or a read-only volume.
var ErrReadOnly = errors.New("session: the folder is read-only")

// ReadOnlyMessage is what the Library page shows for a folder that failed to
// open with ErrReadOnly.
const ReadOnlyMessage = "Choix cannot write its .choix folder here — the volume is read-only."

// ErrUnsafeState reports a folder whose .choix is not a real directory, or
// whose database files are not regular files (a symbolic link, say).
var ErrUnsafeState = errors.New("session: .choix or its database is not a plain directory and files")

// UnsafeStateMessage is what the Library page shows for a folder that failed
// to open with ErrUnsafeState.
const UnsafeStateMessage = "Choix will not open this folder: its .choix folder or database is a symbolic link."

// What the frontend receives for the package's sentinel errors.
func init() {
	apperr.Register(ErrReadOnly, apperr.CodeReadOnly, ReadOnlyMessage)
	apperr.RegisterInvalid(ErrUnsafeState, "", "unsafe_state", UnsafeStateMessage)
	apperr.Register(ErrShuttingDown, apperr.CodeUnavailable, "")
	// Not quitting: the stale session closes within the bound, and the
	// operation can be tried again.
	apperr.Register(ErrBusy, apperr.CodeBusy, "")
}

// Deps are what sessions are built from. Settings returns the live
// machine-wide settings; Embedder and CropInferer are the app's shared ONNX
// sessions (owned and closed elsewhere) and may be nil.
type Deps struct {
	Library     *library.Library
	Settings    func() config.Config
	Stills      platform.Stills
	Video       platform.Video
	Embedder    local.Embedder
	CropInferer local.CropInferer
}

// The folder's working directory and database, relative to its root.
const (
	sessionChoixDir = ".choix"
	sessionStateDB  = "state.db"
)

// sessionKVClusterParams is the kv key holding ClusterParams of the settings
// the folder's clusters were last built with. A folder last scanned by v1
// has none.
const sessionKVClusterParams = "cluster_params"

// Events of Session.clusterHook.
const (
	sessionClusterEventWait   = "wait"   // about to wait for the clustering lock
	sessionClusterEventLocked = "locked" // holding the lock, settings not yet read
	sessionClusterEventStored = "stored" // clusters rebuilt and params stored
)

// Session is one open folder: its root, its store and its pipeline. It is
// shared by every holder of a reference from the Manager and is safe for
// concurrent use. FolderID, Root and Store never change; Store stays open
// until the last reference is released and the folder's detached native
// calls have returned.
type Session struct {
	FolderID, Root string
	Store          *store.Store

	deps Deps
	// rootInfo identifies the directory Root named when the session opened
	// (os.SameFile), so a directory that later takes its place is told
	// apart.
	rootInfo os.FileInfo
	// release stops the folder's security-scoped access (Library.Acquire).
	release func()
	// inflight counts the folder's detached native calls (see Context).
	inflight platform.Inflight
	// done is closed once the store is closed and access released.
	done chan struct{}

	// clusterLock serialises clustering (a one-slot semaphore, so a waiter
	// can give up when its context ends).
	clusterLock chan struct{}
	// clusterHook, when set by a test, observes clustering.
	clusterHook func(event, params string)

	picksMissing atomic.Int64

	scorerOnce sync.Once
	scorer     *local.QualityScorer
}

// Context returns parent carrying the session's Inflight: every native call
// made under it for this folder is counted, so the store and bookmark are
// released only after those calls have really returned.
func (s *Session) Context(parent context.Context) context.Context {
	return platform.WithInflight(parent, &s.inflight)
}

// PicksMissing returns how many picked rows had no copy on disk when the
// session last reconciled its picks (on open). Safe for concurrent use.
func (s *Session) PicksMissing() int { return int(s.picksMissing.Load()) }

// PicksDir returns the folder's picks directory for cfg: picks.EffectiveDir
// (where earlier picks were exported, else cfg.PicksDir), then ValidateDir
// and CheckDir. A non-nil error is why this folder cannot take picks — a
// *picks.DirError, or picks.ErrDirHasOriginals — and dir is then the
// directory refused; a database error returns "".
func (s *Session) PicksDir(cfg config.Config) (dir string, err error) {
	dir, _, err = picks.EffectiveDir(s.Store, cfg.PicksDir)
	if err != nil {
		return "", fmt.Errorf("session: picks directory: %w", err)
	}
	dir, err = sessionCleanPicksDir(dir)
	if err != nil {
		return dir, err
	}
	if err := picks.CheckDir(s.Store, dir); err != nil {
		return dir, err
	}
	return dir, nil
}

// Picks returns the pick service of the folder, exporting to its effective
// picks directory (normalised by sessionCleanPicksDir when it is valid). It
// is returned whether or not that directory can take picks (Set refuses a
// pick there; unpick and reject still work); only a database error fails.
func (s *Session) Picks(cfg config.Config) (*picks.Service, error) {
	dir, _, err := picks.EffectiveDir(s.Store, cfg.PicksDir)
	if err != nil {
		return nil, fmt.Errorf("session: picks directory: %w", err)
	}
	if clean, err := sessionCleanPicksDir(dir); err == nil {
		dir = clean
	}
	return picks.New(s.Store, s.Root, dir), nil
}

// sessionCleanPicksDir is the one place a picks or export directory is
// normalised: it must pass picks.ValidateDir, and is returned
// slash-separated and path.Clean'd — relative, inside the folder, without
// "." or ".." components, never the folder itself or .choix. The scanner's
// skip list, safefs (which refuses "." and "..") and every comparison of
// directories use this form. An invalid d is returned unchanged with
// ValidateDir's *picks.DirError.
func sessionCleanPicksDir(d string) (string, error) {
	if err := picks.ValidateDir(d); err != nil {
		return d, err
	}
	clean := path.Clean(filepath.ToSlash(d))
	if err := picks.ValidateDir(clean); err != nil {
		return d, err
	}
	return clean, nil
}

// Scorer returns the folder's quality scorer, built once. It runs crops
// through Deps.CropInferer; without one, Score fails with
// local.ErrQualityUnavailable.
func (s *Session) Scorer() *local.QualityScorer {
	s.scorerOnce.Do(func() {
		var inf local.CropInferer = sessionNoInferer{}
		if s.deps.CropInferer != nil {
			inf = s.deps.CropInferer
		}
		s.scorer = local.NewQualityScorer(s.Store, inf, func(fileID int64) (string, error) {
			return sessionThumbPath(s.Store, s.Root, fileID, thumb.TierPreview)
		})
		// A preview unreadable because the folder is gone leaves the photo
		// pending instead of stamping it as never ratable.
		s.scorer.FailureGuard = s.reachable
	})
	return s.scorer
}

// reachable reports whether the folder is still there: Root is a directory.
// It guards every per-file failure the session's stages and scorer record.
func (s *Session) reachable() bool { return sessionRootReachable(s.Root) }

// sessionRootReachable reports whether root exists and is a directory.
func sessionRootReachable(root string) bool {
	fi, err := os.Lstat(root)
	return err == nil && fi.IsDir()
}

// sessionNoInferer is the CropInferer of a session built without one.
type sessionNoInferer struct{}

func (sessionNoInferer) QualityCrop([]float32) (float64, error) {
	return 0, local.ErrQualityUnavailable
}

// errSessionVisualGroupingOff is what the analyze stage's embedder answers while
// visual grouping is off: the file is analyzed without an embedding.
var errSessionVisualGroupingOff = errors.New("session: visual grouping is off")

// sessionGatedEmbedder embeds only while the live visual_grouping setting is
// on.
type sessionGatedEmbedder struct {
	inner    local.Embedder
	settings func() config.Config
}

func (e *sessionGatedEmbedder) Embed(img image.Image) ([]float32, error) {
	if !e.settings().VisualGrouping {
		return nil, errSessionVisualGroupingOff
	}
	return e.inner.Embed(img)
}

// openSession makes the checks of a session open on root, already accessed
// through the Library, and opens the store. On error nothing is left open.
func openSession(folderID, root string, d Deps) (*Session, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", folderID, err)
	}
	dir, err := safefs.OpenDir(root, sessionChoixDir, true)
	if err != nil {
		return nil, sessionOpenError(folderID, err)
	}
	verr := dir.VerifyRegular(sessionStateDB, sessionStateDB+"-wal", sessionStateDB+"-shm")
	if cerr := dir.Close(); cerr != nil {
		slog.Warn("session: close .choix", "folder", folderID, "err", cerr)
	}
	if verr != nil {
		return nil, sessionOpenError(folderID, verr)
	}

	choixAbs := filepath.Join(root, sessionChoixDir)
	st, err := store.Open(filepath.Join(choixAbs, sessionStateDB))
	if err != nil {
		if rerr := sessionWritable(choixAbs); rerr != nil {
			return nil, fmt.Errorf("session: open %s: %w: %w", folderID, ErrReadOnly, errors.Join(rerr, err))
		}
		return nil, fmt.Errorf("session: open %s: %w", folderID, err)
	}

	s := &Session{
		FolderID:    folderID,
		Root:        root,
		Store:       st,
		deps:        d,
		rootInfo:    rootInfo,
		done:        make(chan struct{}),
		clusterLock: make(chan struct{}, 1),
	}
	s.prepare(d.Settings())
	return s, nil
}

// prepare does the open-time upkeep that never fails the open: re-deriving
// columns from stored metadata, sweeping interrupted cache writes and pick
// copies, and reconciling the picks (which copies nothing).
func (s *Session) prepare(cfg config.Config) {
	ctx := context.Background()
	if n, err := meta.BackfillGPS(ctx, s.Store); err != nil {
		slog.Warn("session: gps backfill", "folder", s.FolderID, "err", err)
	} else if n > 0 {
		slog.Info("session: gps backfill", "folder", s.FolderID, "rows", n)
	}
	if n, err := meta.BackfillDeviceKey(ctx, s.Store); err != nil {
		slog.Warn("session: device_key backfill", "folder", s.FolderID, "err", err)
	} else if n > 0 {
		slog.Info("session: device_key backfill", "folder", s.FolderID, "rows", n)
	}
	if _, err := thumb.SweepTemps(s.Root); err != nil {
		slog.Warn("session: sweep thumbnail temporaries", "folder", s.FolderID, "err", err)
	}

	dir, err := s.PicksDir(cfg)
	if err != nil {
		slog.Info("session: the folder cannot take picks", "folder", s.FolderID, "picks_dir", dir, "reason", err)
		return
	}
	// An export cut short (a crash, a power loss, the quit deadline) leaves
	// its temporary copy in the picks directory, which the user hands on.
	if n, err := picks.SweepTemps(s.Store, s.Root, dir); err != nil {
		slog.Warn("session: sweep interrupted pick copies", "folder", s.FolderID, "picks_dir", dir, "removed", n, "err", err)
	} else if n > 0 {
		slog.Info("session: removed interrupted pick copies", "folder", s.FolderID, "picks_dir", dir, "removed", n)
	}
	rep, err := picks.New(s.Store, s.Root, dir).Reconcile(ctx)
	if err != nil {
		slog.Warn("session: reconcile picks", "folder", s.FolderID, "err", err)
		return
	}
	s.picksMissing.Store(int64(rep.Missing))
}

// close closes the store and releases the folder's access.
func (s *Session) close() {
	if err := s.Store.Close(); err != nil {
		slog.Warn("session: close store", "folder", s.FolderID, "err", err)
	}
	if s.release != nil {
		s.release()
	}
	close(s.done)
}

// sessionOpenError classifies a failed check of .choix or its database.
func sessionOpenError(folderID string, err error) error {
	switch {
	case errors.Is(err, safefs.ErrSymlink), errors.Is(err, safefs.ErrNotDir), errors.Is(err, safefs.ErrNotRegular):
		return fmt.Errorf("session: open %s: %w: %w", folderID, ErrUnsafeState, err)
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		return fmt.Errorf("session: open %s: %w: %w", folderID, ErrReadOnly, err)
	}
	return fmt.Errorf("session: open %s: %w", folderID, err)
}

// sessionWritable returns the error that makes dir unwritable — permission
// denied or a read-only volume — or nil.
func sessionWritable(dir string) error {
	err := unix.Access(dir, unix.W_OK)
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EROFS) {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	return nil
}

// sessionThumbPath returns the absolute path of the file's cached thumbnail
// of tier: the thumbs row's path, or the deterministic cache layout when the
// row is missing and the file exists there.
func sessionThumbPath(st *store.Store, root string, fileID int64, tier string) (string, error) {
	t, err := st.Thumbs().Get(fileID, tier)
	if err == nil && t.RelPath != "" && filepath.IsLocal(filepath.FromSlash(t.RelPath)) {
		return filepath.Join(root, filepath.FromSlash(t.RelPath)), nil
	}
	abs := thumb.CachePath(root, fileID, tier)
	if _, statErr := os.Stat(abs); statErr == nil {
		return abs, nil
	}
	if err == nil {
		err = errors.New("its cache entry is unusable")
	}
	name := "thumbnail"
	if tier == thumb.TierPreview {
		name = "preview"
	}
	return "", fmt.Errorf("no cached %s image for this file (%w)", name, err)
}
