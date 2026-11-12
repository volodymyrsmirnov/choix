// Package session runs the background work on the folders of the Library:
// the job queue that processes a few folders at a time, never one folder
// twice at once.
//
// The package does not import Wails.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
)

// JobKind is the kind of work a job does ([call] C17).
type JobKind int

const (
	// JobBackfill fills in what an earlier run left out (a setting was
	// switched on) without walking the folder. Stored as "backfill".
	JobBackfill JobKind = iota
	// JobFull walks the folder first, then does everything a backfill does.
	// Stored as "full".
	JobFull
)

// String returns the kind as stored in library.db.
func (k JobKind) String() string {
	switch k {
	case JobBackfill:
		return "backfill"
	case JobFull:
		return "full"
	}
	return fmt.Sprintf("JobKind(%d)", int(k))
}

// parseKind converts a stored kind. Anything but "backfill" is JobFull: a
// full job does everything a backfill does, so it is the safe reading.
func parseKind(s string) JobKind {
	if s == "backfill" {
		return JobBackfill
	}
	return JobFull
}

// mergeKind returns the kind covering both: a full job replaces a backfill,
// never the reverse.
func mergeKind(a, b JobKind) JobKind {
	if a == JobFull || b == JobFull {
		return JobFull
	}
	return JobBackfill
}

// Outcome is how a run ended.
type Outcome int

const (
	// Done: the work finished.
	Done Outcome = iota
	// Failed: the work cannot be done; the message is shown to the user.
	Failed
	// Suspended: the folder is unreachable (its volume went away, its
	// bookmark no longer resolves). The job stays pending.
	Suspended
	// Stopped: the run's context was cancelled.
	Stopped
	// Busy: a suspension whose cause is that the folder's previous session,
	// at a location the folder has left, is still closing (ErrBusy). As
	// with Suspended the job stays pending; unlike it, it is never a strike
	// towards maxSuspensions, OnSuspended is not called, and the queue does
	// not run the folder again on its own: the next Resume (the presence
	// pass) or enqueue does, by when the old session has normally closed.
	Busy
)

// couldNotReadMessage is the error of a folder that keeps suspending its job
// while the Library reports it present.
const couldNotReadMessage = "The folder could not be read."

// failedMessage is the error stored for a Failed run that gave no message.
const failedMessage = "The scan failed."

// maxSuspensions is how many suspensions in a row of a folder the Library
// reports present turn into a failure.
const maxSuspensions = 3

// presencePresent is library.Folder.Presence for a reachable folder.
const presencePresent = "present"

// Phases of scan:progress that are delivered at once instead of coalesced.
const (
	phaseStarting = "starting"
	phaseRunning  = "running"
	phaseDone     = "done"
	phaseFailed   = "failed"
)

// States of scan:state the queue emits.
const (
	stateQueued   = "queued"
	stateScanning = "scanning"
	stateReady    = "ready"
	stateError    = "error"
)

// DefaultSlots is how many folder jobs the app runs at once. Native calls
// are capped by platform.NativeSlots whatever the number of jobs, so a
// second job lets another folder scan while one is in its quality pass; a
// third would add disk and memory pressure and little throughput.
const DefaultSlots = 2

// Runner does one job: the pipeline pass over a folder. It returns how the
// run ended and, for Failed, the message to show. It must return within
// ~1 s of ctx being cancelled, reporting Stopped. report may be called from
// any goroutine until Run returns; later calls are ignored. Run is called
// concurrently for different folders (up to QueueOptions.Slots at once),
// never twice at once for the same folder. Run must not call Queue.Cancel
// (Cancel waits for Run to return); the other Queue methods may be called
// from it.
type Runner interface {
	Run(ctx context.Context, folderID string, kind JobKind, report func(p events.Progress, phase string)) (Outcome, string)
}

// QueueOptions configure a Queue.
type QueueOptions struct {
	Library *library.Library
	Runner  Runner
	// Emit receives scan:state and scan:progress. nil: no events. Its
	// Emitter must not call back into the Queue.
	Emit *events.Coalescer
	// OnSuspended is called, on the goroutine of the run and holding no
	// lock, after a run ends Suspended. The runtime runs Library.CheckFolder
	// for it at once, so the row turns offline (or the job is re-queued)
	// without waiting for the 30 s tick. nil: nothing is called.
	OnSuspended func(folderID string)
	// Slots is how many jobs may run at once, each on a different folder.
	// Less than 1 means 1. The app uses DefaultSlots.
	Slots int
}

// entry is a queued job: the folder, the kind of work and the sequence
// number library.db returned for its latest enqueue.
type entry struct {
	id   string
	kind JobKind
	seq  int64
	// prio is the stamp of the latest EnqueueFront of the folder (opening
	// it), 0 when it was only ever enqueued at the tail. When every slot is
	// busy, an open preempts the running job with the lowest prio.
	prio int64
}

// stopReason says why a running job's context was cancelled.
type stopReason int

const (
	stopNone     stopReason = iota
	stopPreempt             // another folder was put in front of it
	stopSuspend             // its folder became unreachable
	stopCancel              // its folder was removed
	stopShutdown            // the queue is stopping
)

// run is a running job. It holds its slot, and its folder, until the
// runner has returned and the run is accounted for.
type run struct {
	entry       // what is running; seq identifies the run in library.db
	next  entry // the latest persisted enqueue of the folder (entry until one arrives during the run)
	// again is set when the folder was enqueued (or resumed after a Suspend)
	// during the run; front when the latest such enqueue was EnqueueFront.
	// front matters only for a run that is stopped (preempted or suspended):
	// the open folder then goes back to the front. A finished run's rerun
	// always goes to the tail.
	again, front bool
	stop         stopReason
	behind       string // stopPreempt: the folder to re-queue it behind
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{} // closed once the run is accounted for
	progress     *events.Progress
	started      int64 // the order in which runs started, from 1
}

// Queue runs folder jobs in order ([call] C18, C19), up to Slots at once,
// each on a different folder: a folder is never run twice at once, so its
// store keeps a single writer. Every enqueue is persisted in library.db
// before it is acknowledged; a run acknowledges only its own sequence
// number, so an enqueue that arrives while the folder runs makes it run
// again. It is safe for concurrent use.
type Queue struct {
	lib         *library.Library
	runner      Runner
	emit        *events.Coalescer
	onSuspended func(string)
	slots       int

	// mu guards everything below. It is held across library.db calls so an
	// enqueue and the end of a run are ordered: FinishJob reports a newer
	// sequence exactly when an enqueue was recorded in run.next.
	mu       sync.Mutex
	queue    []entry
	runs     map[string]*run // by folder id; a run stays until it is accounted for
	starts   int64           // runs started so far
	fronts   int64           // EnqueueFront calls so far: the prio stamps
	suspends map[string]int  // suspensions in a row of a folder reported present
	started  bool
	stopped  bool
	cancel   context.CancelFunc
	wake     chan struct{}
	wg       sync.WaitGroup // the dispatcher and every run
}

// NewQueue returns a stopped Queue; Start runs it.
func NewQueue(o QueueOptions) *Queue {
	return &Queue{
		lib:         o.Library,
		runner:      o.Runner,
		emit:        o.Emit,
		onSuspended: o.OnSuspended,
		slots:       max(o.Slots, 1),
		runs:        map[string]*run{},
		suspends:    map[string]int{},
		wake:        make(chan struct{}, 1),
	}
}

// Start queues every pending job of library.db whose folder is present, in
// sequence order with its stored kind, and starts the dispatcher goroutine.
// It does nothing when the queue was already started or stopped. Cancelling
// ctx stops the dispatcher and every run like Stop, without waiting for
// them.
func (q *Queue) Start(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.started || q.stopped {
		return
	}
	q.started = true
	wctx, cancel := context.WithCancel(ctx)
	q.cancel = cancel
	q.loadPendingLocked()
	q.wg.Add(1)
	go q.work(wctx)
}

// loadPendingLocked queues the pending jobs of present folders. Called
// holding mu.
func (q *Queue) loadPendingLocked() {
	jobs, err := q.lib.PendingJobs()
	if err != nil {
		slog.Warn("session: read pending jobs", "err", err)
		return
	}
	folders, err := q.lib.Folders()
	if err != nil {
		slog.Warn("session: read folders", "err", err)
		return
	}
	present := make(map[string]bool, len(folders))
	for _, f := range folders {
		present[f.ID] = f.Presence == presencePresent
	}
	for _, j := range jobs {
		if !present[j.ID] || q.isRunningLocked(j.ID) || q.indexLocked(j.ID) >= 0 {
			continue
		}
		q.queue = append(q.queue, entry{id: j.ID, kind: parseKind(j.Kind), seq: j.Seq})
		q.emitStateLocked(j.ID, stateQueued, "")
	}
	q.signal()
}

// Enqueue queues work of kind for the folder at the tail. A folder already
// queued keeps its place (full replaces backfill, never the reverse); the
// folder that is running is marked to run again afterwards. The enqueue is
// persisted before Enqueue returns. An unknown folder is library.ErrNotFound.
func (q *Queue) Enqueue(folderID string, kind JobKind) error {
	return q.enqueue(folderID, kind, false)
}

// EnqueueFront queues work of kind for the folder at the front ([call] C18):
// it starts in the next free slot. When every slot is busy and none is
// about to be free (a run winding down), the running job with the lowest
// priority is stopped and re-queued directly behind it: a job no open made
// before one an earlier open made, and among equals the one started last.
// The folder itself, when running, is not restarted — the running pass
// already serves it — but, like after Enqueue, runs once more afterwards at
// the tail. The enqueue is persisted before it returns.
func (q *Queue) EnqueueFront(folderID string, kind JobKind) error {
	return q.enqueue(folderID, kind, true)
}

func (q *Queue) enqueue(id string, kind JobKind, front bool) error {
	if kind != JobBackfill && kind != JobFull {
		return fmt.Errorf("session: enqueue %s: unknown job kind %v", id, kind)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	seq, err := q.lib.EnqueueJob(id, kind.String())
	if err != nil {
		return fmt.Errorf("session: enqueue %s: %w", id, err)
	}
	// library.db never downgrades a pending full job: run what it stored.
	kind = q.storedKindLocked(id, kind)
	var prio int64
	if front {
		q.fronts++
		prio = q.fronts
	}

	if r := q.runs[id]; r != nil {
		r.next = entry{id: id, kind: mergeKind(r.next.kind, kind), seq: seq, prio: max(r.next.prio, prio)}
		r.again = true
		if front {
			r.front = true
			// The open folder's running pass is the last a later open
			// preempts.
			r.prio = prio
		}
		return nil
	}

	e := entry{id: id, kind: kind, seq: seq, prio: prio}
	i := q.indexLocked(id)
	if i >= 0 {
		e.kind = mergeKind(q.queue[i].kind, kind)
		e.prio = max(q.queue[i].prio, prio)
		q.queue[i] = e
	}
	switch {
	case front:
		if i >= 0 {
			q.queue = slices.Delete(q.queue, i, i+1)
		}
		q.queue = slices.Insert(q.queue, 0, e)
		q.preemptLocked(id)
	case i < 0:
		q.queue = append(q.queue, e)
	}
	if i < 0 {
		q.emitStateLocked(id, stateQueued, "")
	}
	q.signal()
	return nil
}

// storedKindLocked returns kind merged with the kind library.db holds for the
// folder's pending job: full absorbs backfill, also when the full job was
// enqueued before a suspension or a restart. When the row cannot be read,
// kind is returned. Called holding mu.
func (q *Queue) storedKindLocked(id string, kind JobKind) JobKind {
	f, err := q.lib.Folder(id)
	if err != nil {
		slog.Warn("session: read the stored job kind", "folder", id, "err", err)
		return kind
	}
	if f.JobPending && f.JobKind == JobFull.String() {
		return JobFull
	}
	return kind
}

// preemptLocked makes sure the folder id, just put at the front of the
// queue, gets the next slot: unless id is running, a slot is free, or a run
// is winding down (its slot is about to be free), the running job with the
// lowest priority is stopped. A stopped job is re-queued directly behind
// the latest folder put at the front. Called holding mu.
func (q *Queue) preemptLocked(id string) {
	if q.runs[id] != nil {
		return
	}
	freeing := false
	var victim *run
	for _, r := range q.runs {
		switch r.stop {
		case stopNone:
			if victim == nil || lowerPriority(r, victim) {
				victim = r
			}
		case stopPreempt:
			r.behind, r.front = id, false
			freeing = true
		default:
			freeing = true
		}
	}
	if freeing || len(q.runs) < q.slots || victim == nil {
		return
	}
	victim.stop, victim.behind, victim.front = stopPreempt, id, false
	victim.cancel()
}

// lowerPriority reports whether running job a yields before b to an open:
// a has the older open (or none), or the same and it started later, having
// done less work.
func lowerPriority(a, b *run) bool {
	if a.prio != b.prio {
		return a.prio < b.prio
	}
	return a.started > b.started
}

// EnqueueAllReachable enqueues work of kind for every folder whose presence
// is "present". Failures are logged.
func (q *Queue) EnqueueAllReachable(kind JobKind) {
	folders, err := q.lib.Folders()
	if err != nil {
		slog.Warn("session: enqueue reachable folders", "err", err)
		return
	}
	for _, f := range folders {
		if f.Presence != presencePresent {
			continue
		}
		if err := q.Enqueue(f.ID, kind); err != nil {
			slog.Warn("session: enqueue a reachable folder", "folder", f.ID, "err", err)
		}
	}
}

// Suspend drops the folder from the queue or stops its running job, without
// waiting for it to return. The job stays pending in library.db; Resume
// queues it again. Called when the folder becomes unreachable.
func (q *Queue) Suspend(folderID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// The Library found the folder unreachable: its suspensions were real.
	delete(q.suspends, folderID)
	q.dropLocked(folderID)
	r := q.runs[folderID]
	if r == nil {
		return
	}
	switch r.stop {
	case stopCancel, stopShutdown:
		return
	}
	r.stop, r.again, r.front = stopSuspend, false, false
	r.cancel()
}

// Resume queues the folder's job, with its stored kind and sequence number,
// when library.db says it is pending and it is neither queued nor running;
// otherwise it does nothing. A job still winding down after Suspend is
// queued again once it returns.
func (q *Queue) Resume(folderID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if r := q.runs[folderID]; r != nil {
		if r.stop == stopSuspend {
			r.again = true
		}
		return
	}
	if q.indexLocked(folderID) >= 0 {
		return
	}
	jobs, err := q.lib.PendingJobs()
	if err != nil {
		slog.Warn("session: resume", "folder", folderID, "err", err)
		return
	}
	i := slices.IndexFunc(jobs, func(j library.PendingJob) bool { return j.ID == folderID })
	if i < 0 {
		return
	}
	q.queue = append(q.queue, entry{id: folderID, kind: parseKind(jobs[i].Kind), seq: jobs[i].Seq})
	q.emitStateLocked(folderID, stateQueued, "")
	q.signal()
}

// Cancel drops the folder from the queue or stops its running job, and
// clears its pending job in library.db. It returns once the runner has
// returned. Called when the folder is removed; it must not be called from
// the Runner.
func (q *Queue) Cancel(folderID string) {
	q.mu.Lock()
	delete(q.suspends, folderID)
	q.dropLocked(folderID)
	var done chan struct{}
	if r := q.runs[folderID]; r != nil {
		r.stop, r.again, r.front = stopCancel, false, false
		r.cancel()
		done = r.done
	}
	q.mu.Unlock()

	if done != nil {
		<-done
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.dropLocked(folderID) // an enqueue that raced the wait loses to the removal
	if err := q.lib.ClearJob(folderID); err != nil && !errors.Is(err, library.ErrNotFound) {
		slog.Warn("session: clear the job of a cancelled folder", "folder", folderID, "err", err)
	}
}

// State returns "scanning" and the latest progress (nil before the first
// report) for a running folder, "queued" for a queued one — including a job
// stopped to be re-queued — and "" otherwise. Several folders may be
// scanning at once.
func (q *Queue) State(folderID string) (state string, p *events.Progress) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if r := q.runs[folderID]; r != nil {
		switch {
		case r.stop == stopNone:
			if r.progress != nil {
				cp := *r.progress
				p = &cp
			}
			return stateScanning, p
		case r.stop == stopPreempt, r.again && r.stop == stopSuspend:
			return stateQueued, nil
		}
		return "", nil
	}
	if q.indexLocked(folderID) >= 0 {
		return stateQueued, nil
	}
	return "", nil
}

// Idle reports whether no job is queued or running.
func (q *Queue) Idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.runs) == 0 && len(q.queue) == 0
}

// Stop stops every running job and waits for them and the dispatcher to
// return. Pending jobs stay pending in library.db. The queue cannot be
// started again.
func (q *Queue) Stop() {
	q.mu.Lock()
	q.stopped = true
	for _, r := range q.runs {
		if r.stop == stopNone || r.stop == stopPreempt {
			r.stop = stopShutdown
			r.cancel()
		}
	}
	cancel := q.cancel
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	q.wg.Wait()
}

// work is the dispatcher goroutine: until ctx is done it starts queued jobs
// while a slot is free, each run on its own goroutine, and waits for an
// enqueue or the end of a run.
func (q *Queue) work(ctx context.Context) {
	defer q.wg.Done()
	for {
		q.mu.Lock()
		if q.stopped || ctx.Err() != nil {
			q.mu.Unlock()
			return
		}
		for len(q.runs) < q.slots {
			r := q.startLocked(ctx)
			if r == nil {
				break
			}
			// The dispatcher is counted in wg until it returns, so this Add
			// never races Stop's Wait from zero.
			q.wg.Add(1)
			go q.execute(ctx, r)
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		}
	}
}

// startLocked takes the first queued job whose folder is not running —
// a folder is never run twice at once — and makes it a run. It returns nil
// when there is none. Called holding mu.
func (q *Queue) startLocked(ctx context.Context) *run {
	i := slices.IndexFunc(q.queue, func(e entry) bool { return q.runs[e.id] == nil })
	if i < 0 {
		return nil
	}
	e := q.queue[i]
	q.queue = slices.Delete(q.queue, i, i+1)
	q.starts++
	rctx, cancel := context.WithCancel(ctx)
	r := &run{entry: e, next: e, ctx: rctx, cancel: cancel, done: make(chan struct{}), started: q.starts}
	q.runs[e.id] = r
	q.emitStateLocked(e.id, stateScanning, "")
	return r
}

// execute runs r on its own goroutine, accounts for its outcome and frees
// its slot.
func (q *Queue) execute(ctx context.Context, r *run) {
	defer q.wg.Done()
	o, msg := q.runner.Run(r.ctx, r.id, r.kind, q.reporter(r))
	r.cancel()

	q.mu.Lock()
	notify := q.finishLocked(ctx, r, o, msg)
	delete(q.runs, r.id)
	q.signal()
	q.mu.Unlock()
	close(r.done)

	if notify && q.onSuspended != nil {
		q.onSuspended(r.id)
	}
}

// finishLocked records how r ended, re-queues it when it must run again and
// emits its final events. It reports whether OnSuspended is to be called.
// Called holding mu.
func (q *Queue) finishLocked(ctx context.Context, r *run, o Outcome, msg string) bool {
	if r.stop == stopCancel {
		q.emitStoppedLocked(r)
		return false
	}
	shutdown := r.stop == stopShutdown || ctx.Err() != nil

	switch o {
	case Done:
		delete(q.suspends, r.id)
		q.finishJobLocked(r, "")
		return false
	case Failed:
		delete(q.suspends, r.id)
		if msg == "" {
			msg = failedMessage
		}
		slog.Warn("session: job failed", "folder", r.id, "kind", r.kind.String(), "err", msg)
		q.finishJobLocked(r, msg)
		return false
	case Suspended:
		return q.suspendedLocked(r, shutdown)
	}

	// Stopped, Busy (or an outcome the queue does not know): the row stays
	// pending.
	q.emitStoppedLocked(r)
	if shutdown {
		return false
	}
	if o == Busy {
		// Not a strike, and suspends is left as it is.
		slog.Info("session: job deferred, the folder's previous session is still closing", "folder", r.id)
	}
	switch {
	case r.again && r.front:
		q.requeueLocked(r.next, 0)
	case r.stop == stopPreempt:
		at := 0
		if i := q.indexLocked(r.behind); i >= 0 {
			at = i + 1
		}
		q.requeueLocked(r.next, at)
	case r.again:
		q.requeueLocked(r.next, len(q.queue))
	case r.stop == stopSuspend:
		// Resume queues it again when the folder is back.
	case o == Busy:
		// Not run again at once — that would only wait for the same old
		// session: the next Resume (presence pass) or enqueue runs it.
	default:
		slog.Warn("session: a job stopped without being asked to; it stays pending",
			"folder", r.id, "outcome", int(o))
	}
	return false
}

// suspendedLocked handles a run that ended Suspended: the row stays pending
// and no state event is sent, unless the folder keeps suspending while the
// Library reports it present, which fails the job. It reports whether
// OnSuspended is to be called. Called holding mu.
func (q *Queue) suspendedLocked(r *run, shutdown bool) bool {
	q.emitStoppedLocked(r)
	if shutdown {
		return false
	}
	slog.Info("session: job suspended, the folder is unreachable", "folder", r.id)
	if r.stop == stopSuspend {
		// The Library has already found the folder unreachable.
		delete(q.suspends, r.id)
		return true
	}
	f, err := q.lib.Folder(r.id)
	switch {
	case errors.Is(err, library.ErrNotFound):
		delete(q.suspends, r.id)
		return false // removed meanwhile; Cancel clears the rest
	case err != nil:
		slog.Warn("session: presence of a suspended folder", "folder", r.id, "err", err)
		return true
	case f.Presence != presencePresent:
		delete(q.suspends, r.id)
		return true
	}
	q.suspends[r.id]++
	if q.suspends[r.id] < maxSuspensions {
		return true
	}
	delete(q.suspends, r.id)
	slog.Warn("session: job failed, the folder keeps suspending while it is present",
		"folder", r.id, "suspensions", maxSuspensions)
	q.finishJobLocked(r, couldNotReadMessage)
	return false
}

// finishJobLocked acknowledges run r in library.db with errText ("" for
// Done). When a newer enqueue arrived during the run, the folder stays
// pending and is queued again; otherwise it is marked ready or error.
// Called holding mu.
func (q *Queue) finishJobLocked(r *run, errText string) {
	// A newer enqueue arrived during the run: the folder runs again, at the
	// tail ([call] C19), unless it was suspended meanwhile (Resume queues it
	// when it is back).
	rerun := r.next.seq != r.seq && (r.stop != stopSuspend || r.again)

	cleared, err := q.lib.FinishJob(r.id, r.seq, errText)
	switch {
	case errors.Is(err, library.ErrNotFound):
		q.emitStoppedLocked(r) // the folder was removed
		return
	case err != nil:
		// Not acknowledged: the row stays pending, so the next presence pass
		// or launch runs the job again. No retry from here, so a failing
		// database is not hammered.
		slog.Warn("session: record the end of a job; it stays pending", "folder", r.id, "err", err)
		q.emitStoppedLocked(r)
		if rerun {
			q.requeueLocked(r.next, len(q.queue))
		}
		return
	}

	phase := phaseDone
	if errText != "" {
		phase = phaseFailed
	}
	q.emitFinalLocked(r, phase)
	if !cleared {
		if rerun {
			q.requeueLocked(r.next, len(q.queue))
		}
		// Otherwise the row is gone (the folder was removed): nothing to do.
		return
	}
	if errText != "" {
		q.emitStateLocked(r.id, stateError, errText)
		return
	}
	q.emitStateLocked(r.id, stateReady, "")
}

// requeueLocked puts e back in the queue at index at (clamped), unless the
// folder is queued already. Called holding mu.
func (q *Queue) requeueLocked(e entry, at int) {
	if i := q.indexLocked(e.id); i >= 0 {
		q.queue[i].kind = mergeKind(q.queue[i].kind, e.kind)
		q.queue[i].seq = max(q.queue[i].seq, e.seq)
		q.queue[i].prio = max(q.queue[i].prio, e.prio)
		return
	}
	at = min(max(at, 0), len(q.queue))
	q.queue = slices.Insert(q.queue, at, e)
	q.emitStateLocked(e.id, stateQueued, "")
	q.signal()
}

// reporter returns the report func of run r: progress is recorded for State
// and sent as scan:progress, coalesced except for the starting, done and
// failed phases. Calls after r has ended are ignored.
func (q *Queue) reporter(r *run) func(events.Progress, string) {
	return func(p events.Progress, phase string) {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.runs[r.id] != r {
			return
		}
		cp := p
		r.progress = &cp
		if q.emit == nil {
			return
		}
		ev := events.ScanProgressEvent{FolderID: r.id, Progress: p, Phase: phase}
		switch phase {
		case phaseStarting, phaseDone, phaseFailed:
			q.emit.EmitNow(events.ScanProgress, r.id, ev)
		default:
			q.emit.Emit(events.ScanProgress, r.id, ev)
		}
	}
}

// emitFinalLocked sends the last scan:progress of a run that ended Done
// (phase "done") or Failed (phase "failed", state "stopped"), at once, so
// no coalesced value of the run can arrive after it. Called holding mu.
func (q *Queue) emitFinalLocked(r *run, phase string) {
	if q.emit == nil {
		return
	}
	var p events.Progress
	if r.progress != nil {
		p = *r.progress
	}
	p.State = "running"
	if phase == phaseFailed {
		p.State = "stopped"
	}
	q.emit.EmitNow(events.ScanProgress, r.id, events.ScanProgressEvent{FolderID: r.id, Progress: p, Phase: phase})
}

// emitStoppedLocked sends, for a run that was stopped or suspended after
// reporting progress, its last progress with state "stopped", at once, so
// that no coalesced "running" value arrives after the run. Called holding
// mu.
func (q *Queue) emitStoppedLocked(r *run) {
	if q.emit == nil || r.progress == nil {
		return
	}
	p := *r.progress
	p.State = "stopped"
	q.emit.EmitNow(events.ScanProgress, r.id, events.ScanProgressEvent{FolderID: r.id, Progress: p, Phase: phaseRunning})
}

// emitStateLocked sends scan:state for the folder at once. Called holding mu
// so that the states of one folder are delivered in the order they happen.
func (q *Queue) emitStateLocked(id, state, errText string) {
	if q.emit == nil {
		return
	}
	q.emit.EmitNow(events.ScanState, id, events.ScanStateEvent{FolderID: id, State: state, Error: errText})
}

// indexLocked returns the folder's index in the queue, or -1. Called
// holding mu.
func (q *Queue) indexLocked(id string) int {
	return slices.IndexFunc(q.queue, func(e entry) bool { return e.id == id })
}

// isRunningLocked reports whether the folder's job is running. Called
// holding mu.
func (q *Queue) isRunningLocked(id string) bool {
	return q.runs[id] != nil
}

// dropLocked removes the folder from the queue. Called holding mu.
func (q *Queue) dropLocked(id string) {
	if i := q.indexLocked(id); i >= 0 {
		q.queue = slices.Delete(q.queue, i, i+1)
	}
}

// signal wakes the dispatcher if it is waiting for work or a free slot.
func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
