package library

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// ErrNotDirectory reports that a path given to Add is not a folder.
var ErrNotDirectory = errors.New("library: not a folder")

// ErrOffline reports that a folder's bookmark does not resolve: the folder
// was deleted or its volume is not mounted.
var ErrOffline = errors.New("library: folder is offline")

// ErrAccessLost reports that a folder's bookmark no longer grants access
// (the app was re-signed, or the system refused the security scope). Only
// adding the folder again, or a later successful Acquire, clears it.
var ErrAccessLost = errors.New("library: access to the folder was lost")

// AccessLostMessage is what the Library page shows for a folder whose
// presence is access_lost.
const AccessLostMessage = "Choix lost access to this folder. Add it again."

// What the frontend receives for the package's sentinel errors.
func init() {
	apperr.Register(ErrOffline, apperr.CodeOffline, "")
	apperr.Register(ErrAccessLost, apperr.CodeAccessLost, AccessLostMessage)
	apperr.Register(ErrNotFound, apperr.CodeNotFound, "")
	apperr.Register(ErrNotDirectory, apperr.CodeInvalid, "")
}

// Folder presence values (folders.presence).
const (
	presencePresent    = "present"
	presenceAccessLost = "access_lost"
)

// Reasons of the library:changed events this file emits.
const (
	reasonAdded   = "added"
	reasonRemoved = "removed"
	reasonOffline = "offline"
	reasonOnline  = "online"
	reasonMoved   = "moved"
)

// Options configure a Library.
type Options struct {
	DB        *DB
	Bookmarks platform.Bookmarks
	Volumes   platform.Volumes
	Emit      events.Emitter // nil: no events
	Now       func() time.Time
	NewID     func() string // nil → 8 random bytes as hex
	// OnOffline, OnRemoved and OnOnline, and the library:changed events, are
	// delivered one at a time, in the order the changes they report were
	// recorded, and never while a Library lock is held, so a callback may
	// call back into the Library. The changes such a nested call records
	// are delivered after the callback returns; a call made while another
	// goroutine is delivering returns without waiting, its notifications
	// delivered by that goroutine.

	// OnOffline is called when the folder goes offline, missing, loses access,
	// or moves while in use: its jobs are suspended and the catalog lets go.
	OnOffline func(id string)
	// OnRemoved is called when the folder is removed by the user or verified
	// gone from disk: its jobs are cancelled.
	OnRemoved func(id string)
	// OnOnline is called when the folder is present and has pending work that
	// may not be queued. The queue's Resume is a no-op when the work is
	// already queued or running.
	OnOnline func(id string)
	// PreviewsDir returns the directory holding the folder's Library-page
	// previews (appdir.Previews at runtime). Remove deletes it. nil: nothing
	// is removed.
	PreviewsDir func(id string) (string, error)
}

// AddResult is the outcome of Add. Existing is true when the path was
// already covered by the folder ID and nothing was stored; Relation then
// says how: "same", "inside" (the path is inside that folder) or "contains"
// (the path contains it). A folder whose access was lost and is added again
// under the same path gets a new bookmark: Existing is false and Relation is
// "same".
type AddResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Existing bool   `json:"existing"`
	Relation string `json:"relation,omitempty"`
}

// Library is the user's set of folders: adding and removing them, and
// reference-counted security-scoped access to them. It is safe for
// concurrent use.
type Library struct {
	db          *DB
	bookmarks   platform.Bookmarks
	volumes     platform.Volumes
	emit        events.Emitter
	now         func() time.Time
	newID       func() string
	onOffline   func(string)
	onRemoved   func(string)
	onOnline    func(string)
	previewsDir func(string) (string, error)

	// changeMu serialises Add, Remove and the presence checks, so that Add's
	// check for a related folder and its insert see the same set of folders.
	changeMu sync.Mutex
	// trashWarned holds the folders whose Trash check could not be made and
	// was already logged (presence.go). Guarded by changeMu.
	trashWarned map[string]bool
	// refreshWarned holds the folders whose failing bookmark refresh was
	// already logged; an entry is cleared when a refresh succeeds. Guarded
	// by changeMu.
	refreshWarned map[string]bool
	// newTicker makes RunPresence's ticker; nil: time.NewTicker. Tests
	// replace it.
	newTicker func(every time.Duration) (ticks <-chan time.Time, stop func())
	// notifyMu guards notes and draining: the notifications recorded under
	// changeMu, in order, and whether a goroutine is delivering them.
	notifyMu sync.Mutex
	notes    []func()
	draining bool
	// afterExamine, when set, is called by a presence check after a
	// folder's outcome is recorded and before its notifications are
	// delivered, with no lock held. Tests use it to force interleavings.
	afterExamine func(id string)

	// previewLocks and statsLocks serialise RefreshPreviews and RefreshStats
	// per folder: each reads the folder's state.db and then publishes, so a
	// refresh that read older data must never publish after a newer one.
	previewLocks keyedLock
	statsLocks   keyedLock

	// mu guards leases, and the pins and refs of every entry in it.
	mu     sync.Mutex
	leases map[string]*lease
}

// lease is the security-scoped access to one folder shared by every holder.
// An entry stays in Library.leases while pins > 0, so every goroutine that
// starts or stops the folder's access works on the same entry.
type lease struct {
	// mu serialises starting and stopping the folder's access.
	mu sync.Mutex
	// pins counts Acquire calls in progress plus live leases (Library.mu).
	pins int
	// refs counts live leases; written holding both mu and Library.mu.
	refs int
	root string // guarded by mu
	stop func() // guarded by mu
}

// New returns a Library over o.
func New(o Options) *Library {
	l := &Library{
		db:          o.DB,
		bookmarks:   o.Bookmarks,
		volumes:     o.Volumes,
		emit:        o.Emit,
		now:         o.Now,
		newID:       o.NewID,
		onOffline:   o.OnOffline,
		onRemoved:   o.OnRemoved,
		onOnline:    o.OnOnline,
		previewsDir: o.PreviewsDir,
		leases:      map[string]*lease{},
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.newID == nil {
		l.newID = randomID
	}
	return l
}

// randomID returns 8 random bytes as 16 lowercase hex characters.
func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	return hex.EncodeToString(b[:])
}

// Add adds the folder at path, an absolute path. The path is cleaned and its
// symbolic links resolved; a path that is not a directory is ErrNotDirectory.
// When a folder in the Library is the same folder, contains it or lies
// inside it, Add returns that folder with Existing set and stores nothing —
// except that the same folder whose access was lost gets a new bookmark,
// becomes present again and OnOnline is called. Otherwise a bookmark is
// created, the folder is stored and library:changed "added" is emitted.
// Before comparing, every folder's bookmark is resolved: a folder whose
// bookmark resolves is compared at that location only, and a new location is
// recorded (library:changed "moved"); a folder whose bookmark does not
// resolve is compared at its stored path.
func (l *Library) Add(path string) (AddResult, error) {
	resolved, err := resolveFolder(path)
	if err != nil {
		return AddResult{}, err
	}
	res, err := l.add(resolved)
	l.drain()
	return res, err
}

// AddPaths adds each path, in order, as Add does: the Go-only entry for
// folders dropped on the window, never bound. A path that cannot be added is
// logged and left out of the results. enqueue, when not nil, is called with
// the id of every new folder — not one the Library already covered, nor one
// whose lost access was restored — once the folder is stored, holding no
// Library lock, before this call delivers the folder's notifications. The
// folder stays added whatever enqueue does: enqueue reports its own
// failures (a folder that could not be queued is started by a rescan).
func (l *Library) AddPaths(paths []string, enqueue func(id string)) []AddResult {
	var enq func(string) error
	if enqueue != nil {
		enq = func(id string) error { enqueue(id); return nil }
	}
	res, addErrs, _ := l.addPaths(paths, enq)
	for _, err := range addErrs {
		slog.Warn("library: add a dropped folder", "err", err)
	}
	return res
}

// addPaths does AddPaths' work. It returns the results (never nil), the
// errors of the paths it could not add and the errors enqueue returned for
// folders that were added, each in order. A folder whose enqueue failed is
// in the results.
func (l *Library) addPaths(paths []string, enqueue func(id string) error) (out []AddResult, addErrs, enqueueErrs []error) {
	out = make([]AddResult, 0, len(paths))
	for _, p := range paths {
		var qerr error
		res, err := l.addOne(p, func(id string) {
			if enqueue != nil {
				qerr = enqueue(id)
			}
		})
		if err != nil {
			addErrs = append(addErrs, err)
			continue
		}
		out = append(out, res)
		if qerr != nil {
			enqueueErrs = append(enqueueErrs, qerr)
		}
	}
	return out, addErrs, enqueueErrs
}

// addOne adds path, calls enqueue for a new folder and delivers the queued
// notifications, also when enqueue panics.
func (l *Library) addOne(path string, enqueue func(id string)) (AddResult, error) {
	resolved, err := resolveFolder(path)
	if err != nil {
		return AddResult{}, err
	}
	defer l.drain()
	res, err := l.add(resolved)
	if err != nil {
		return AddResult{}, err
	}
	if !res.Existing && res.Relation == "" {
		enqueue(res.ID)
	}
	return res, nil
}

// add does Add's work under changeMu and queues its notifications.
func (l *Library) add(resolved string) (AddResult, error) {
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	res, notify, err := l.addLocked(resolved)
	l.enqueueLocked(notify)
	return res, err
}

// addLocked does Add's work. Called holding changeMu; the returned func, if
// any, notifies the outside world.
func (l *Library) addLocked(resolved string) (AddResult, func(), error) {
	folders, err := l.db.Folders()
	if err != nil {
		return AddResult{}, nil, fmt.Errorf("library: add %s: %w", resolved, err)
	}
	locs, notes := l.reconcileLocked(folders)
	if loc, rel := related(locs, resolved); rel != "" {
		f := loc.Folder
		if rel == relSame && f.Presence == presenceAccessLost {
			res, restored, err := l.restoreAccessLocked(f, resolved)
			return res, runAll(append(notes, restored)), err
		}
		return AddResult{ID: f.ID, Name: f.Name, Existing: true, Relation: rel}, runAll(notes), nil
	}

	bookmark, err := l.bookmarks.Create(resolved)
	if err != nil {
		return AddResult{}, runAll(notes), fmt.Errorf("library: add %s: bookmark: %w", resolved, err)
	}
	vol, err := l.volumes.Info(resolved)
	if err != nil {
		return AddResult{}, runAll(notes), fmt.Errorf("library: add %s: volume: %w", resolved, err)
	}
	f := Folder{
		ID:         l.newID(),
		Path:       resolved,
		Name:       filepath.Base(resolved),
		Bookmark:   bookmark,
		VolumeUUID: vol.UUID,
		VolumeName: vol.Name,
		AddedAt:    l.now().Unix(),
		Presence:   presencePresent,
	}
	if err := l.db.InsertFolder(f); err != nil {
		return AddResult{}, runAll(notes), fmt.Errorf("library: add %s: %w", resolved, err)
	}
	notes = append(notes, func() { l.emitChange(reasonAdded, f.ID, f.Name) })
	return AddResult{ID: f.ID, Name: f.Name}, runAll(notes), nil
}

// located is a folder with the path a candidate is compared against: where
// its bookmark resolves now or, when it does not resolve, its stored path.
type located struct {
	Folder
	path string
}

// reconcileLocked resolves every folder's bookmark (which never mounts a
// volume or shows UI). A folder whose bookmark resolves is compared at the
// resolved path only — after a rename or move the stored path is stale and
// may belong to a different, new folder — and a new location is recorded.
// A bookmark that does not resolve leaves the stored path. Called holding
// changeMu; the returned funcs notify the outside world.
func (l *Library) reconcileLocked(folders []Folder) ([]located, []func()) {
	locs := make([]located, len(folders))
	var notes []func()
	for i, f := range folders {
		locs[i] = located{Folder: f, path: f.Path}
		p, _, err := l.bookmarks.Resolve(f.Bookmark)
		if err != nil || p == f.Path {
			continue
		}
		locs[i].path = p
		if moved := l.relocateLocked(f, p, folders); moved != nil {
			notes = append(notes, moved)
			locs[i].Path, locs[i].Name = p, filepath.Base(p)
			folders[i].Path = p
		}
	}
	return locs, notes
}

// related returns the first folder related to the resolved candidate path,
// and the relation, preferring "same" over "inside" and "contains". The
// relation is "" when no folder is related.
func related(locs []located, candidate string) (located, string) {
	for _, loc := range locs {
		if r := relation(loc.path, candidate); r == relSame {
			return loc, r
		}
	}
	for _, loc := range locs {
		if r := relation(loc.path, candidate); r != "" {
			return loc, r
		}
	}
	return located{}, ""
}

// restoreAccessLocked replaces the bookmark of f, a folder whose access was
// lost, with a new one made from the access the caller of Add was just
// granted, and marks it present. Called holding changeMu.
func (l *Library) restoreAccessLocked(f Folder, resolved string) (AddResult, func(), error) {
	bookmark, err := l.bookmarks.Create(resolved)
	if err != nil {
		return AddResult{}, nil, fmt.Errorf("library: add %s again: bookmark: %w", resolved, err)
	}
	if err := l.db.SetBookmark(f.ID, bookmark); err != nil {
		return AddResult{}, nil, fmt.Errorf("library: add %s again: %w", resolved, err)
	}
	if err := l.db.SetPresence(f.ID, presencePresent); err != nil {
		return AddResult{}, nil, fmt.Errorf("library: add %s again: %w", resolved, err)
	}
	after := func() {
		if l.onOnline != nil {
			l.onOnline(f.ID)
		}
		l.emitChange(reasonOnline, f.ID, f.Name)
	}
	return AddResult{ID: f.ID, Name: f.Name, Relation: relSame}, after, nil
}

// Remove forgets the folder: its row (with its job state) and its previews
// directory. Nothing inside the folder itself is touched. OnRemoved is
// called and library:changed "removed" is emitted. Leases already taken stay
// valid until released. An unknown id is ErrNotFound.
func (l *Library) Remove(id string) error {
	if err := l.remove(id); err != nil {
		return err
	}
	l.drain()
	return nil
}

// remove deletes the folder's row under changeMu and queues OnRemoved, the
// removal of its previews directory and the event.
func (l *Library) remove(id string) error {
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	f, resolved, err := l.removeLocked(id)
	if err != nil {
		return err
	}
	l.enqueueLocked(func() {
		if l.onRemoved != nil {
			l.onRemoved(id)
		}
		l.removePreviews(f, resolved)
		l.emitChange(reasonRemoved, id, f.Name)
	})
	return nil
}

// removeLocked deletes the folder's row. It returns the row and where the
// folder's bookmark resolved just before ("" when it did not), so the
// previews cleanup can protect the folder's current location too. Called
// holding changeMu.
func (l *Library) removeLocked(id string) (Folder, string, error) {
	f, err := l.db.Folder(id)
	if err != nil {
		return Folder{}, "", fmt.Errorf("library: remove: %w", err)
	}
	resolved, _, err := l.bookmarks.Resolve(f.Bookmark)
	if err != nil {
		resolved = ""
	}
	if err := l.db.DeleteFolder(id); err != nil {
		return Folder{}, "", fmt.Errorf("library: remove: %w", err)
	}
	return f, resolved, nil
}

// removePreviews deletes the previews directory of removed, a folder just
// removed, whose bookmark resolved to removedAt ("" when it did not). That
// directory is the app's own cache and must never overlap a user folder, so
// it is removed only when PreviewsDir names an absolute directory whose last
// element is the id and whose canonical form (symbolic links resolved,
// through its nearest existing ancestor when it does not exist) still ends
// in the id and is not the same as, inside, or containing any protected
// path: for every registered folder and the removed one, its stored path
// and the path its bookmark resolves to, each also canonicalised. Anything
// else is refused and logged; failures are logged too, because the folder is
// already gone from the Library.
func (l *Library) removePreviews(removed Folder, removedAt string) {
	id := removed.ID
	if l.previewsDir == nil {
		return
	}
	dir, err := l.previewsDir(id)
	if err != nil {
		slog.Warn("library: previews directory of a removed folder", "folder", id, "err", err)
		return
	}
	if !validPreviewsDir(dir, id) {
		slog.Warn("library: refusing to remove an unexpected previews directory", "folder", id, "dir", dir)
		return
	}
	canon, err := canonicalPath(dir)
	if err != nil {
		slog.Warn("library: refusing to remove an unresolvable previews directory", "folder", id, "dir", dir, "err", err)
		return
	}
	if filepath.Base(canon) != id {
		slog.Warn("library: refusing to remove an unexpected previews directory", "folder", id, "dir", dir, "resolved", canon)
		return
	}

	// changeMu keeps the set of folders fixed until the directory is gone.
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	folders, err := l.db.Folders()
	if err != nil {
		slog.Warn("library: refusing to remove a previews directory", "folder", id, "dir", dir, "err", err)
		return
	}
	type guarded struct {
		id    string
		paths []string
	}
	guards := make([]guarded, 0, len(folders)+1)
	for _, f := range folders {
		at, _, err := l.bookmarks.Resolve(f.Bookmark)
		if err != nil {
			at = ""
		}
		guards = append(guards, guarded{f.ID, protectedPaths(f.Path, at)})
	}
	guards = append(guards, guarded{id, protectedPaths(removed.Path, removedAt)})
	for _, g := range guards {
		for _, p := range g.paths {
			if rel := relation(p, canon); rel != "" {
				slog.Warn("library: refusing to remove a previews directory that overlaps a folder",
					"folder", id, "dir", dir, "resolved", canon, "overlaps", g.id, "relation", rel)
				return
			}
		}
	}
	if err := os.RemoveAll(canon); err != nil {
		slog.Warn("library: remove previews directory", "folder", id, "dir", canon, "err", err)
	}
}

// protectedPaths returns every non-empty path given and, where it differs,
// its canonical form.
func protectedPaths(paths ...string) []string {
	out := make([]string, 0, 2*len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		out = append(out, p)
		if c, err := canonicalPath(p); err == nil && c != p {
			out = append(out, c)
		}
	}
	return out
}

// validPreviewsDir reports whether dir is a non-empty absolute path whose
// last element is id, id being a single path element.
func validPreviewsDir(dir, id string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	if id == "" || id == "." || id == ".." || strings.ContainsRune(id, filepath.Separator) {
		return false
	}
	return filepath.Base(dir) == id
}

// Folders returns every folder, ordered by when it was added.
func (l *Library) Folders() ([]Folder, error) { return l.db.Folders() }

// Folder returns the folder with this id, or ErrNotFound.
func (l *Library) Folder(id string) (Folder, error) { return l.db.Folder(id) }

// Location returns where the folder's bookmark resolves now. It is
// read-only: it records nothing (a move is recorded by Add, the presence
// checks and Acquire), starts no access and never mounts a volume, so it
// answers independently of any access lease held on the folder. An unknown
// id is ErrNotFound; a bookmark that does not resolve is the bookmark's error
// (platform.ErrGone, platform.ErrUnusable).
func (l *Library) Location(id string) (string, error) {
	f, err := l.db.Folder(id)
	if err != nil {
		return "", fmt.Errorf("library: location: %w", err)
	}
	p, _, err := l.bookmarks.Resolve(f.Bookmark)
	if err != nil {
		return "", fmt.Errorf("library: location of %s: %w", id, err)
	}
	return p, nil
}

// Acquire starts security-scoped access to the folder and returns its
// current root and a release func. Access is reference-counted: the first
// Acquire starts it, the others share it, and the last release stops it.
// release is idempotent.
//
// A bookmark that does not resolve is ErrOffline. A bookmark that no longer
// grants access is ErrAccessLost: the folder's presence becomes access_lost
// and, on that transition, library:changed "offline" is emitted and
// OnOffline is called. A successful start clears access_lost (OnOnline is
// called and library:changed "online" emitted) and records a new location
// when the bookmark resolves elsewhere (library:changed "moved"); both run
// under the same lock as Add and Remove. Callbacks and events are delivered
// in order, after every Library lock is released (see Options). An unknown
// id is ErrNotFound.
func (l *Library) Acquire(id string) (root string, release func(), err error) {
	ls := l.pin(id)
	ls.mu.Lock()
	root, err = l.startLocked(id, ls)
	ls.mu.Unlock()
	l.drain()
	if err != nil {
		l.unpin(id, ls)
		return "", nil, err
	}
	var once sync.Once
	return root, func() { once.Do(func() { l.release(id, ls) }) }, nil
}

// startLocked adds one reference to ls, starting access when it is the
// first. Called holding ls.mu. The notifications it records are queued; the
// caller delivers them with drain after ls.mu is released.
func (l *Library) startLocked(id string, ls *lease) (string, error) {
	f, err := l.db.Folder(id)
	if err != nil {
		return "", fmt.Errorf("library: access: %w", err)
	}
	if ls.refs > 0 {
		l.mu.Lock()
		ls.refs++
		l.mu.Unlock()
		return ls.root, nil
	}

	path, stop, err := l.bookmarks.StartAccess(f.Bookmark)
	switch {
	case errors.Is(err, platform.ErrGone):
		return "", fmt.Errorf("library: access %s: %w: %w", id, ErrOffline, err)
	case errors.Is(err, platform.ErrUnusable):
		l.accessLost(f)
		return "", fmt.Errorf("library: access %s: %w: %w", id, ErrAccessLost, err)
	case err != nil:
		return "", fmt.Errorf("library: access %s: %w", id, err)
	}

	if path != f.Path || f.Presence == presenceAccessLost {
		l.settle(id, path)
	}

	l.mu.Lock()
	ls.refs = 1
	l.mu.Unlock()
	ls.root, ls.stop = path, stop
	return path, nil
}

// settle records, after a successful start of access, that the folder now
// lives at path and that its access is no longer lost. It runs under
// changeMu, re-reading the row, so it cannot interleave with Add (which may
// have recorded the same move already) or Remove. Its notifications are
// queued under that lock.
func (l *Library) settle(id, path string) {
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	l.enqueueLocked(l.settleLocked(id, path))
}

// settleLocked does settle's work. Called holding changeMu; the returned
// func, if any, notifies the outside world.
func (l *Library) settleLocked(id, path string) func() {
	folders, err := l.db.Folders()
	if err != nil {
		slog.Warn("library: record the state of an acquired folder", "folder", id, "err", err)
		return nil
	}
	i := slices.IndexFunc(folders, func(f Folder) bool { return f.ID == id })
	if i < 0 {
		return nil // removed meanwhile
	}
	cur := folders[i]
	var notes []func()
	if cur.Path != path {
		if moved := l.relocateLocked(cur, path, folders); moved != nil {
			notes = append(notes, moved)
			cur.Name = filepath.Base(path)
		}
	}
	if cur.Presence == presenceAccessLost {
		if err := l.db.SetPresence(id, presencePresent); err != nil {
			slog.Warn("library: clear access_lost", "folder", id, "err", err)
		} else {
			name := cur.Name
			notes = append(notes, func() {
				// Delivered after every Library lock is released, so the
				// callback may call back into the Library. The queue's
				// Resume is a no-op when the folder is already queued or
				// running, so a job's own Acquire re-entering it is safe.
				if l.onOnline != nil {
					l.onOnline(id)
				}
				l.emitChange(reasonOnline, id, name)
			})
		}
	}
	return runAll(notes)
}

// accessLost records that f's bookmark no longer grants access and reports
// whether that was a transition to access_lost, whose notification it
// queues. Nothing is recorded when the folder was added again (its bookmark
// replaced) or removed since f was read: changeMu orders this against Add
// and Remove.
func (l *Library) accessLost(f Folder) bool {
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	notify := l.accessLostLocked(f)
	l.enqueueLocked(notify)
	return notify != nil
}

// accessLostLocked does accessLost's work. Called holding changeMu; on the
// transition it returns the notification to send.
func (l *Library) accessLostLocked(f Folder) func() {
	cur, err := l.db.Folder(f.ID)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			slog.Warn("library: mark access lost", "folder", f.ID, "err", err)
		}
		return nil
	}
	if cur.Presence == presenceAccessLost || !bytes.Equal(cur.Bookmark, f.Bookmark) {
		return nil
	}
	if err := l.db.SetPresence(f.ID, presenceAccessLost); err != nil {
		slog.Warn("library: mark access lost", "folder", f.ID, "err", err)
		return nil
	}
	slog.Warn("library: access to a folder was lost", "folder", f.ID)
	return func() {
		l.emitChange(reasonOffline, f.ID, f.Name)
		if l.onOffline != nil {
			l.onOffline(f.ID)
		}
	}
}

// relocateLocked records that f, one of folders, now lives at path: a
// renamed or moved folder is followed (spec §4.1). If the new location
// overlaps another registered folder — the user moved one Library folder
// inside another — both rows stay and a warning is logged. On success it
// returns the notification to send; a failure is logged, and the next
// Acquire or Add tries again. Called holding changeMu.
func (l *Library) relocateLocked(f Folder, path string, folders []Folder) func() {
	vol, err := l.volumes.Info(path)
	if err != nil {
		slog.Warn("library: volume of a moved folder", "folder", f.ID, "err", err)
		vol = platform.VolumeInfo{UUID: f.VolumeUUID, Name: f.VolumeName}
	}
	name := filepath.Base(path)
	if err := l.db.UpdateLocation(f.ID, path, name, vol); err != nil {
		slog.Warn("library: record a moved folder", "folder", f.ID, "err", err)
		return nil
	}
	for _, o := range folders {
		if o.ID == f.ID {
			continue
		}
		if rel := relation(o.Path, path); rel != "" {
			slog.Warn("library: a moved folder now overlaps another folder",
				"folder", f.ID, "other", o.ID, "relation", rel)
		}
	}
	return func() { l.emitChange(reasonMoved, f.ID, name) }
}

// enqueueLocked appends the non-nil fns to the notifications to deliver.
// Called holding changeMu, so the queue's order is the order in which the
// changes were recorded.
func (l *Library) enqueueLocked(fns ...func()) {
	l.notifyMu.Lock()
	defer l.notifyMu.Unlock()
	for _, fn := range fns {
		if fn != nil {
			l.notes = append(l.notes, fn)
		}
	}
}

// drain delivers the queued notifications one at a time, in order, holding
// no Library lock while one runs. It must be called with no Library lock
// held, after recording changes. When another goroutine is already
// delivering — or this one is, and a callback called back into the Library —
// it returns at once: that delivery picks up the new entries.
func (l *Library) drain() {
	l.notifyMu.Lock()
	if l.draining {
		l.notifyMu.Unlock()
		return
	}
	l.draining = true
	l.notifyMu.Unlock()
	finished := false
	defer func() {
		if !finished { // a callback panicked: let the next drain deliver the rest
			l.notifyMu.Lock()
			l.draining = false
			l.notifyMu.Unlock()
		}
	}()
	for {
		l.notifyMu.Lock()
		if len(l.notes) == 0 {
			l.draining = false
			l.notifyMu.Unlock()
			finished = true
			return
		}
		fn := l.notes[0]
		l.notes[0] = nil
		l.notes = l.notes[1:]
		l.notifyMu.Unlock()
		fn()
	}
}

// runAll returns a func calling every non-nil fn in order, or nil when there
// is none.
func runAll(fns []func()) func() {
	fns = slices.DeleteFunc(fns, func(fn func()) bool { return fn == nil })
	if len(fns) == 0 {
		return nil
	}
	return func() {
		for _, fn := range fns {
			fn()
		}
	}
}

// release drops one reference to ls and stops access with the last one.
func (l *Library) release(id string, ls *lease) {
	ls.mu.Lock()
	l.mu.Lock()
	ls.refs--
	last := ls.refs == 0
	l.mu.Unlock()
	if last {
		stop := ls.stop
		ls.root, ls.stop = "", nil
		if stop != nil {
			stop()
		}
	}
	ls.mu.Unlock()
	l.unpin(id, ls)
}

// pin returns the folder's lease entry, creating it, and counts the caller.
func (l *Library) pin(id string) *lease {
	l.mu.Lock()
	defer l.mu.Unlock()
	ls := l.leases[id]
	if ls == nil {
		ls = &lease{}
		l.leases[id] = ls
	}
	ls.pins++
	return ls
}

// unpin undoes one pin, dropping the entry when nothing pins it.
func (l *Library) unpin(id string, ls *lease) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ls.pins--
	if ls.pins == 0 {
		delete(l.leases, id)
	}
}

// InUse reports whether any lease on the folder is held.
func (l *Library) InUse(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	ls := l.leases[id]
	return ls != nil && ls.refs > 0
}

// EnqueueJob queues work of kind for the folder; see DB.EnqueueJob.
func (l *Library) EnqueueJob(id, kind string) (int64, error) { return l.db.EnqueueJob(id, kind) }

// FinishJob records the end of run seq; see DB.FinishJob.
func (l *Library) FinishJob(id string, seq int64, errText string) (bool, error) {
	return l.db.FinishJob(id, seq, errText)
}

// ClearJob clears the folder's pending job; see DB.ClearJob.
func (l *Library) ClearJob(id string) error { return l.db.ClearJob(id) }

// PendingJobs returns the pending jobs in enqueue order; see DB.PendingJobs.
func (l *Library) PendingJobs() ([]PendingJob, error) { return l.db.PendingJobs() }

// emitChange emits library:changed with reason for the folder.
func (l *Library) emitChange(reason, id, name string) {
	if l.emit == nil {
		return
	}
	l.emit.Emit(events.LibraryChanged, events.LibraryChangedEvent{Reason: reason, FolderID: id, Name: name})
}
