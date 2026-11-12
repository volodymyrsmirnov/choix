// The quality status: what the advisory quality rating is doing, as one
// snapshot (a binding source) and as quality:status events, plus the
// immediate opt-out — switching quality_rating off cancels the rating passes
// in flight.

package session

import (
	"context"
	"slices"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// States of quality:status (v1's quality.State names, frontend QualityState).
const (
	qualityDisabled = "disabled"
	qualityWaiting  = "waiting"
	qualityScoring  = "scoring"
	qualityIdle     = "idle"
)

// qualityEventKey is the coalescing key of quality:status.
const qualityEventKey = "quality"

// QualityOptions configure a QualityTracker. Enabled is required. The funcs
// are called without the tracker's locks held, except Enabled, which must
// therefore not call back into the tracker.
type QualityOptions struct {
	// Enabled reports the quality_rating setting.
	Enabled func() bool
	// Busy reports whether a job is queued or running. nil: never.
	Busy func() bool
	// Emit receives quality:status (key "quality"). nil: no events.
	Emit *events.Coalescer
}

// qualityPass is a rating pass in flight, of one folder.
type qualityPass struct {
	folderID    string
	cancel      context.CancelCauseFunc
	done, total int
}

// QualityTracker is the job's QualityRun and the one source of the quality
// status. Status computes the snapshot; quality:status sends it whenever it
// changes: every change of state at once (EmitNow), progress within scoring
// coalesced. Its methods are safe for concurrent use.
type QualityTracker struct {
	o QualityOptions

	// sendMu serialises computing a status and handing it to the Coalescer,
	// so statuses reach it in the order they were computed and a coalesced
	// progress value can never follow a newer state. Lock order: sendMu,
	// then the lock of Busy, then mu.
	sendMu  sync.Mutex
	sent    events.Quality // the last status handed to Emit; guarded by sendMu
	hasSent bool

	// mu guards the fields below. It is held while Enabled is called, so a
	// pass that begins and a switch to off are ordered.
	mu       sync.Mutex
	passes   []*qualityPass // in flight, oldest first
	rated    int            // photos dealt with by the passes that ended
	queued   int            // Changed calls not yet published, a FIFO of contentless notifications
	draining bool           // the drainer goroutine runs
	drainers sync.WaitGroup // the drainer goroutine; tests wait on it
}

var _ QualityRun = (*QualityTracker)(nil)

// NewQualityTracker returns a tracker reading o. It panics when o.Enabled is
// nil.
func NewQualityTracker(o QualityOptions) *QualityTracker {
	if o.Enabled == nil {
		panic("session: QualityOptions.Enabled is required")
	}
	return &QualityTracker{o: o}
}

// Status returns the quality status. State, first match:
//   - disabled: the setting is off;
//   - scoring: a pass is running: Passes lists every pass in flight with
//     its folder, oldest first; Done, Total and FolderID are the latest
//     pass begun's (two folder jobs run at once, so two passes may);
//   - waiting: a job is queued or running;
//   - idle otherwise, also after a pass that ended with an error (it is
//     logged; its photos stay pending).
//
// Active is true for waiting and scoring. Rated counts the photos the passes
// dealt with since the tracker was made; it only grows.
func (t *QualityTracker) Status() events.Quality {
	busy := t.o.Busy != nil && t.o.Busy()

	t.mu.Lock()
	on := t.o.Enabled()
	s := events.Quality{Enabled: on, Rated: t.ratedLocked()}
	// The passes' numbers are copied here: progress writes them under mu.
	passes := make([]events.QualityPass, len(t.passes))
	for i, p := range t.passes {
		passes[i] = events.QualityPass{FolderID: p.folderID, Done: p.done, Total: p.total}
	}
	t.mu.Unlock()

	switch {
	case !on:
		s.State = qualityDisabled
	case len(passes) > 0:
		latest := passes[len(passes)-1]
		s.State, s.Done, s.Total, s.FolderID, s.Passes = qualityScoring, latest.Done, latest.Total, latest.FolderID, passes
	case busy:
		s.State = qualityWaiting
	default:
		s.State = qualityIdle
	}
	s.Active = s.State == qualityWaiting || s.State == qualityScoring
	return s
}

// ratedLocked returns Rated: the passes that ended plus the progress of
// those in flight. Called holding mu.
func (t *QualityTracker) ratedLocked() int {
	n := t.rated
	for _, p := range t.passes {
		n += p.done
	}
	return n
}

// Begin starts tracking a rating pass (QualityRun). When the setting is off
// the returned context is already cancelled, with errJobQualityOff as its
// cause, and the pass is not tracked. end does not cancel passCtx: the job
// reads its cause after end, and it is released with ctx.
func (t *QualityTracker) Begin(ctx context.Context, folderID string) (passCtx context.Context, progress func(done, total int), end func(rated int, err error)) {
	passCtx, cancel := context.WithCancelCause(ctx)
	p := &qualityPass{folderID: folderID, cancel: cancel}
	t.mu.Lock()
	if !t.o.Enabled() {
		t.mu.Unlock()
		cancel(errJobQualityOff)
		return passCtx, func(int, int) {}, func(int, error) {}
	}
	t.passes = append(t.passes, p)
	t.mu.Unlock()
	t.publish(false)

	ended := false // guarded by mu
	progress = func(done, total int) {
		t.mu.Lock()
		if ended {
			t.mu.Unlock()
			return
		}
		p.done, p.total = max(p.done, done), total
		t.mu.Unlock()
		t.publish(false)
	}
	end = func(rated int, err error) {
		t.mu.Lock()
		if ended {
			t.mu.Unlock()
			return
		}
		ended = true
		if i := slices.Index(t.passes, p); i >= 0 {
			t.passes = slices.Delete(t.passes, i, i+1)
		}
		// Never below what the progress already counted. A pass's error
		// (the setting switched off, the folder gone, repeated failures of
		// single photos) is not a state: the job logs it.
		t.rated += max(p.done, rated)
		t.mu.Unlock()
		t.publish(false)
	}
	return passCtx, progress, end
}

// SettingChanged applies the quality_rating setting: when it reads off,
// every pass in flight is cancelled at once (cause errJobQualityOff; the job
// then stops before its next photo). The status is always sent, at once.
// Call it after the setting is stored, not from inside an Emitter.
func (t *QualityTracker) SettingChanged() {
	t.mu.Lock()
	if !t.o.Enabled() {
		for _, p := range t.passes {
			p.cancel(errJobQualityOff)
		}
	}
	t.mu.Unlock()
	t.publish(true)
}

// Changed reports that the queue's state changed: the status is computed again and sent when it differs from the
// last one sent — a new state at once, progress coalesced. It returns at
// once, so it may be called from anywhere, also from inside an Emitter or
// while holding the lock that Busy takes.
//
// The source cannot be read on the caller's goroutine: the Queue emits while
// holding the very lock Busy takes, and the Coalescer holds its delivery
// lock while its Emitter runs. So each call
// is queued and a single drainer goroutine reads the sources once per call,
// in call order: no notification is merged into another, and every state the
// drainer reads is sent, in order.
func (t *QualityTracker) Changed() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queued++
	if t.draining {
		return
	}
	t.draining = true
	t.drainers.Add(1)
	go t.drain()
}

// drain publishes the status once per queued Changed call, then exits.
func (t *QualityTracker) drain() {
	defer t.drainers.Done()
	for {
		t.mu.Lock()
		if t.queued == 0 {
			t.draining = false
			t.mu.Unlock()
			return
		}
		t.queued--
		t.mu.Unlock()
		t.publish(false)
	}
}

// settle waits until every Changed call so far has been published. For
// tests.
func (t *QualityTracker) settle() {
	t.drainers.Wait()
}

// publish computes the status and sends it when it differs from the last
// one sent, or always when force is set: at once when anything but progress
// changed (or force), coalesced otherwise.
func (t *QualityTracker) publish(force bool) {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	s := t.Status()
	if t.hasSent && qualityEqual(s, t.sent) && !force {
		return
	}
	last, had := t.sent, t.hasSent
	t.sent, t.hasSent = s, true
	if t.o.Emit == nil {
		return
	}
	if force || !had || !qualityProgressOnly(last, s) {
		t.o.Emit.EmitNow(events.QualityStatus, qualityEventKey, s)
		return
	}
	t.o.Emit.Emit(events.QualityStatus, qualityEventKey, s)
}

// qualityEqual reports whether a and b are the same status.
func qualityEqual(a, b events.Quality) bool {
	return a.Enabled == b.Enabled && a.State == b.State && a.Active == b.Active &&
		a.Done == b.Done && a.Total == b.Total && a.Rated == b.Rated && a.FolderID == b.FolderID &&
		slices.Equal(a.Passes, b.Passes)
}

// qualityProgressOnly reports whether a and b differ only in progress:
// photos done and rated, within the same state and the same passes (the
// same folders, in the same order).
func qualityProgressOnly(a, b events.Quality) bool {
	if len(a.Passes) != len(b.Passes) {
		return false
	}
	for i := range a.Passes {
		if a.Passes[i].FolderID != b.Passes[i].FolderID {
			return false
		}
	}
	a.Done, a.Total, a.Rated = b.Done, b.Total, b.Rated
	a.Passes = b.Passes
	return qualityEqual(a, b)
}
