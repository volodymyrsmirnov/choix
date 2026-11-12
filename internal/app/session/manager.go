package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/config"
)

// ErrShuttingDown reports an Acquire made after Shutdown began.
var ErrShuttingDown = errors.New("session: shutting down")

// ErrBusy reports an Acquire of a folder whose previous session is stale —
// the folder moved or its root went away — and has not finished closing
// within the bound: it still holds the folder's state.db, so no new session
// is opened. The folder job treats it as a suspension.
var ErrBusy = errors.New("session: the folder's previous session is still closing")

// defaultStaleWait bounds how long Acquire waits for a stale session to
// close before answering ErrBusy. An abandoned native call may never
// return, so the wait is never unbounded.
const defaultStaleWait = 2 * time.Second

// Stages of a session's close, reported to Manager.hook (tests).
const (
	managerStageWaiting = "waiting" // last reference released; waiting for detached calls
	managerStageKept    = "kept"    // revived (or superseded) while waiting: not closed
	managerStageClosed  = "closed"  // store closed and access released
)

// Manager hands out one shared Session per folder, reference-counted. The
// first Acquire opens it; after the last release it stops being handed out
// by AcquireOpen and, in the background, waits for the folder's detached
// native calls, closes the store and releases the folder's access. An
// Acquire arriving meanwhile revives the same session, unless the folder
// moved or went away since it was opened (see Acquire). A store is never
// closed while a reference or a detached call exists. It is safe for
// concurrent use.
type Manager struct {
	deps Deps

	mu       sync.Mutex
	entries  map[string]*managerEntry // folders opening, open or closing
	shutdown bool
	// changed is closed and replaced whenever an entry leaves entries.
	changed chan struct{}

	// hook, when set by a test, observes the stages of each close.
	hook func(folderID, stage string)
	// staleWait bounds Acquire's wait for a stale session to close
	// (defaultStaleWait; tests shorten it).
	staleWait time.Duration
}

// managerEntry is one folder's session and its references. Fields are
// guarded by Manager.mu.
type managerEntry struct {
	id string
	s  *Session // nil while opening
	// opened is closed when the open attempt ends, either way.
	opened chan struct{}
	refs   int
	// drainGen identifies the latest drain started; an older drain that
	// wakes up finds it changed and leaves the session alone.
	drainGen int
	// closing is set once the session is committed to close; closed is
	// closed when it is.
	closing bool
	closed  chan struct{}
	// stale is set once an Acquire found the session's root is no longer
	// the folder's location: it is never handed out by Acquire again and
	// closes once its references and detached calls are gone.
	stale bool
}

// NewManager returns a Manager building sessions from d. A nil d.Settings
// means the built-in defaults.
func NewManager(d Deps) *Manager {
	if d.Settings == nil {
		d.Settings = config.Defaults
	}
	return &Manager{deps: d, entries: map[string]*managerEntry{}, changed: make(chan struct{}), staleWait: defaultStaleWait}
}

// Acquire returns the folder's session, opening it when no session is open,
// and a release func (idempotent). Opening starts security-scoped access
// through the Library (its errors, library.ErrOffline and
// library.ErrAccessLost among them, are returned wrapped) and makes the
// checks of a session open: a .choix or a database file that is not a plain
// directory or regular file is ErrUnsafeState, a folder Choix cannot write
// is ErrReadOnly; access is released again on any failure. After Shutdown
// began, Acquire is ErrShuttingDown.
//
// A session already open — referenced, or draining after its last release —
// is shared (a draining one is revived) only while it is still at the
// folder's location. A session whose root is gone or is another directory
// than the one it opened, or is not the folder's current location (see
// isStale), is stale: it is never handed out again (nor by AcquireOpen) and closes
// once its references and detached calls are gone. As it holds the folder's
// state.db, Acquire waits for it to close — at most staleWait, since an
// abandoned native call may never return — and then opens a fresh session at
// the current location; when it is still open then, Acquire returns ErrBusy
// and opens nothing.
func (m *Manager) Acquire(folderID string) (*Session, func(), error) {
	var staleTimer *time.Timer // started at the first wait for a stale session
	defer func() {
		if staleTimer != nil {
			staleTimer.Stop()
		}
	}()
	m.mu.Lock()
	for {
		if m.shutdown {
			m.mu.Unlock()
			return nil, nil, fmt.Errorf("session: acquire %s: %w", folderID, ErrShuttingDown)
		}
		e := m.entries[folderID]
		if e == nil {
			break
		}
		var wait chan struct{}
		switch {
		case e.s == nil:
			wait = e.opened // another caller is opening it
		case e.closing:
			wait = e.closed
		case !e.stale:
			// The check reads the disk and library.db: not under mu.
			s := e.s
			m.mu.Unlock()
			stale := m.isStale(s)
			m.mu.Lock()
			if m.entries[folderID] != e || e.closing || e.stale {
				continue // changed meanwhile: look again
			}
			if !stale {
				e.refs++ // revives a draining session too
				m.mu.Unlock()
				return e.s, m.releaser(e), nil
			}
			e.stale = true
			slog.Info("session: the open session's folder moved or went away; it closes before a new one opens",
				"folder", folderID, "refs", e.refs)
			continue
		default:
			// Stale: wait, bounded, for it to close.
			if staleTimer == nil {
				staleTimer = time.NewTimer(m.staleWait)
			}
			closed := e.closed
			m.mu.Unlock()
			select {
			case <-closed:
			case <-staleTimer.C:
				return nil, nil, fmt.Errorf("session: acquire %s: %w", folderID, ErrBusy)
			}
			m.mu.Lock()
			continue
		}
		m.mu.Unlock()
		<-wait
		m.mu.Lock()
	}
	e := &managerEntry{id: folderID, opened: make(chan struct{}), closed: make(chan struct{})}
	m.entries[folderID] = e
	m.mu.Unlock()

	s, err := m.open(folderID)

	m.mu.Lock()
	defer m.mu.Unlock()
	close(e.opened)
	if err != nil {
		delete(m.entries, folderID)
		m.notifyLocked()
		return nil, nil, err
	}
	e.s, e.refs = s, 1
	return s, m.releaser(e), nil
}

// AcquireOpen returns the folder's session and a release func (idempotent)
// only when a session is open and referenced; it never opens one, nor
// revives one whose last reference was released, nor hands out one an
// Acquire found stale (its folder moved or went away). ok is false otherwise
// (the session is nil and release a no-op), and after Shutdown began.
func (m *Manager) AcquireOpen(folderID string) (s *Session, release func(), ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[folderID]
	if m.shutdown || e == nil || e.s == nil || e.closing || e.stale || e.refs == 0 {
		return nil, func() {}, false
	}
	e.refs++
	return e.s, m.releaser(e), true
}

// Shutdown refuses new Acquires and waits until every session is closed —
// each closes as soon as it has no reference and no detached call — or ctx
// is done. It returns how many sessions are still open then. Those are not
// closed: their store and bookmark stay open because something still uses
// them, and the process is about to exit.
func (m *Manager) Shutdown(ctx context.Context) (stillOpen int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shutdown = true
	for len(m.entries) > 0 {
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		m.mu.Lock()
		if ctx.Err() != nil {
			break
		}
	}
	return len(m.entries)
}

// isStale reports whether s no longer serves its folder:
//   - its Root is gone, or is no longer the directory it opened (another
//     directory took its place: os.SameFile against the identity taken at
//     open) — this catches a move the Library has not recorded yet;
//   - or the folder's current location differs from Root, whether or not
//     that location is available now. The current location is where the
//     folder's bookmark resolves (Library.Location, which neither records
//     nor starts access), or, when the bookmark does not resolve, the path
//     the Library has recorded.
//
// Library.Acquire cannot tell: while the old session's lease is held it
// shares it and returns the old root.
func (m *Manager) isStale(s *Session) bool {
	fi, err := os.Stat(s.Root)
	if err != nil || !fi.IsDir() || s.rootInfo == nil || !os.SameFile(fi, s.rootInfo) {
		return true
	}
	if m.deps.Library == nil {
		return false
	}
	loc, err := m.deps.Library.Location(s.FolderID)
	if err != nil {
		f, ferr := m.deps.Library.Folder(s.FolderID)
		if ferr != nil {
			return false // removed: whoever acquires finds out
		}
		loc = f.Path
	}
	return loc != s.Root
}

// open starts access to the folder and opens its session.
func (m *Manager) open(folderID string) (*Session, error) {
	root, release, err := m.deps.Library.Acquire(folderID)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", folderID, err)
	}
	s, err := openSession(folderID, root, m.deps)
	if err != nil {
		release()
		return nil, err
	}
	s.release = release
	return s, nil
}

// releaser returns e's idempotent release func.
func (m *Manager) releaser(e *managerEntry) func() {
	var once sync.Once
	return func() { once.Do(func() { m.release(e) }) }
}

// release drops one reference; the last one starts a drain.
func (m *Manager) release(e *managerEntry) {
	m.mu.Lock()
	e.refs--
	if e.refs > 0 {
		m.mu.Unlock()
		return
	}
	e.drainGen++
	gen := e.drainGen
	m.mu.Unlock()
	go m.drain(e, gen)
}

// drain waits for the session's detached calls and closes it, unless it was
// revived (or a newer drain took over) meanwhile.
func (m *Manager) drain(e *managerEntry, gen int) {
	m.report(e.id, managerStageWaiting)
	e.s.inflight.Wait(-1)

	m.mu.Lock()
	if e.refs > 0 || e.drainGen != gen {
		m.mu.Unlock()
		m.report(e.id, managerStageKept)
		return
	}
	e.closing = true
	m.mu.Unlock()

	e.s.close()

	m.mu.Lock()
	delete(m.entries, e.id)
	close(e.closed)
	m.notifyLocked()
	m.mu.Unlock()
	m.report(e.id, managerStageClosed)
}

// notifyLocked wakes Shutdown and anything else waiting on changed.
func (m *Manager) notifyLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Manager) report(folderID, stage string) {
	if m.hook != nil {
		m.hook(folderID, stage)
	}
}
