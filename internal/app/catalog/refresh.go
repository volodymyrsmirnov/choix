package catalog

import (
	"log/slog"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/session"
)

// stopper is a pending timer; *time.Timer is one.
type stopper interface{ Stop() bool }

// timeAfterFunc is the Catalog's default afterFunc.
func timeAfterFunc(d time.Duration, fn func()) stopper { return time.AfterFunc(d, fn) }

// refresher keeps one open folder's Library row current ([call] C21): after
// RefreshDelay without a further pick, reject, undo or re-cluster, one
// refresh runs, off the binding's path, under a session reference and the
// catalog's context — statistics and preview strip with one
// library:changed "stats" (Library.RefreshStatsAndPreviews), or statistics
// only when only re-clusters are owed. At most one refresh follows a burst,
// and it starts no earlier than the delay after the burst's last mutation.
// The refresher lives exactly as long as the catalog holds the folder
// instance open: flush ends it.
type refresher struct {
	c  *Catalog
	id string
	// s is the folder's session, referenced by the catalog until flush has
	// returned.
	s *session.Session

	mu sync.Mutex
	// timer is the pending debounce timer, gen the generation it fires for:
	// a timer whose generation is stale does nothing.
	timer stopper
	gen   int
	// owed is set by a mutation and cleared when a refresh starts reading,
	// so a refresh owed when flush runs has not started; owedPreviews says
	// whether a mutation that changes the preview strip (a pick, reject or
	// undo) is among them.
	owed, owedPreviews bool
	// running is closed when the refresh running now ends. rerun asks it to
	// run once more because a timer fired meanwhile; a later mutation
	// clears it, its own timer standing for it.
	running chan struct{}
	rerun   bool
	// closed is set by flush; nothing is scheduled or started afterwards.
	closed bool
}

func newRefresher(c *Catalog, id string, s *session.Session) *refresher {
	return &refresher{c: c, id: id, s: s}
}

// schedule (re)starts the debounce after a mutation of the folder; previews
// is false for a mutation that cannot change the preview strip.
func (r *refresher) schedule(previews bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.owed = true
	r.owedPreviews = r.owedPreviews || previews
	// A rerun requested by an earlier timer would refresh before this
	// mutation's delay has passed; this mutation's timer replaces it.
	r.rerun = false
	r.gen++
	gen := r.gen
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = r.c.afterFunc(r.c.d.RefreshDelay, func() { r.fire(gen) })
}

// fire runs on the timer's goroutine: it refreshes now, or asks the refresh
// already running to run once more when it ends.
func (r *refresher) fire(gen int) {
	r.mu.Lock()
	if r.closed || gen != r.gen {
		r.mu.Unlock()
		return
	}
	r.timer = nil
	if r.running != nil {
		r.rerun = true
		r.mu.Unlock()
		return
	}
	done := make(chan struct{})
	r.running = done
	for {
		previews := r.owedPreviews
		r.owed, r.owedPreviews, r.rerun = false, false, false
		r.mu.Unlock()
		r.runReferenced(!previews)
		r.mu.Lock()
		if !r.rerun || r.closed {
			break // flush runs what closing left owed
		}
	}
	r.running = nil
	close(done)
	r.mu.Unlock()
}

// runReferenced refreshes under a session reference of its own; it does
// nothing once the folder's session is no longer open (shutdown began).
func (r *refresher) runReferenced(statsOnly bool) {
	s, release, ok := r.c.d.Sessions.AcquireOpen(r.id)
	if !ok {
		return
	}
	defer release()
	r.c.refresh(r.id, s, statsOnly)
}

// flush ends the refresher, before the catalog releases its reference:
// a pending timer is stopped, a running refresh is waited for, and a
// refresh owed but not started runs now — statistics only when statsOnly
// (shutdown), which leaves the previous preview strip as it is.
func (r *refresher) flush(statsOnly bool) {
	r.mu.Lock()
	r.closed = true
	r.gen++
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	running := r.running
	r.mu.Unlock()
	if running != nil {
		<-running
	}
	r.mu.Lock()
	owed, previews := r.owed, r.owedPreviews
	r.owed, r.owedPreviews = false, false
	r.mu.Unlock()
	if owed {
		r.c.refresh(r.id, r.s, statsOnly || !previews)
	}
}

// refresh recomputes the folder's Library statistics and, unless statsOnly
// or the catalog's context is done, its preview strip, with one
// library:changed "stats". s must stay referenced throughout. Failures are
// logged: the Library row is a cache. A folder removed from the Library
// meanwhile is not an error (the Library drops the result).
func (c *Catalog) refresh(id string, s *session.Session, statsOnly bool) {
	if c.d.Library == nil {
		return
	}
	if statsOnly || c.d.PreviewsDir == nil || c.d.Stills == nil || c.ctx.Err() != nil {
		if err := c.d.Library.RefreshStats(id, s.Store); err != nil {
			slog.Warn("catalog: refresh library statistics", "folder", id, "err", err)
		}
		return
	}
	dir, err := c.d.PreviewsDir(id)
	if err != nil {
		slog.Warn("catalog: previews directory", "folder", id, "err", err)
		if err := c.d.Library.RefreshStats(id, s.Store); err != nil {
			slog.Warn("catalog: refresh library statistics", "folder", id, "err", err)
		}
		return
	}
	err = c.d.Library.RefreshStatsAndPreviews(s.Context(c.ctx), id, s.Store, s.Root, c.d.Stills, dir)
	switch {
	case err == nil:
	case c.ctx.Err() != nil:
		slog.Info("catalog: preview refresh stopped by shutdown", "folder", id)
	default:
		slog.Warn("catalog: refresh library statistics and previews", "folder", id, "err", err)
	}
}
