package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// qualityTestEnv holds what a QualityTracker reads — the setting and whether
// a job is queued or running — and records the quality:status events it
// sends. The Coalescer never ticks: an event in the
// recorder before a Flush was sent with EmitNow.
type qualityTestEnv struct {
	mu      sync.Mutex
	enabled bool
	busy    bool
	// busyLock, when set, is taken by Busy, as the Queue's mutex is by
	// Queue.Idle.
	busyLock *sync.Mutex
	// busyScript, when not empty, is what Busy answers instead of busy, one
	// value per call: the queue's state at each moment it is read.
	busyScript []bool
	// onEmit, when set, runs inside the Coalescer's delivery of every event,
	// as a subscriber of the event bus would.
	onEmit func(name string)

	rec  *events.Recorder
	emit *events.Coalescer
	tr   *QualityTracker
}

func newQualityTestEnv(t *testing.T) *qualityTestEnv {
	t.Helper()
	e := &qualityTestEnv{
		enabled: true,
		rec:     &events.Recorder{},
	}
	e.emit = events.NewCoalescer(emitterFunc(func(name string, data any) {
		e.rec.Emit(name, data)
		e.mu.Lock()
		hook := e.onEmit
		e.mu.Unlock()
		if hook != nil {
			hook(name)
		}
	}), time.Hour)
	e.tr = NewQualityTracker(QualityOptions{
		Enabled: func() bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.enabled
		},
		Busy: func() bool {
			e.mu.Lock()
			l := e.busyLock
			busy := e.busy
			if len(e.busyScript) > 0 {
				busy, e.busyScript = e.busyScript[0], e.busyScript[1:]
			}
			e.mu.Unlock()
			if l != nil {
				l.Lock()
				defer l.Unlock()
			}
			return busy
		},
		Emit: e.emit,
	})
	t.Cleanup(e.tr.settle)
	return e
}

func (e *qualityTestEnv) set(fn func(e *qualityTestEnv)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(e)
}

// sent returns the quality:status events delivered so far.
func (e *qualityTestEnv) sent(t *testing.T) []events.Quality {
	t.Helper()
	var out []events.Quality
	for _, ev := range e.rec.Events(events.QualityStatus) {
		q, ok := ev.(events.Quality)
		if !ok {
			t.Fatalf("quality:status payload %T; want events.Quality", ev)
		}
		out = append(out, q)
	}
	return out
}

// changed calls Changed and waits for the status it triggers to be sent.
func (e *qualityTestEnv) changed() {
	e.tr.Changed()
	e.tr.settle()
}

func assertQuality(t *testing.T, got, want events.Quality) {
	t.Helper()
	if !qualityEqual(got, want) {
		t.Fatalf("status = %+v\nwant     %+v", got, want)
	}
}

func TestStatus_Disabled(t *testing.T) {
	e := newQualityTestEnv(t)
	_, progress, _ := e.tr.Begin(context.Background(), "F")
	progress(1, 3)
	e.set(func(e *qualityTestEnv) {
		e.enabled = false
		e.busy = true
	})
	// Off wins over the pass in flight and the job.
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: false, State: "disabled", Rated: 1})
}

func TestStatus_Scoring(t *testing.T) {
	e := newQualityTestEnv(t)
	e.set(func(e *qualityTestEnv) { e.busy = true }) // the pass runs inside a job
	_, progress, _ := e.tr.Begin(context.Background(), "F")
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "scoring", Active: true,
		FolderID: "F", Passes: []events.QualityPass{{FolderID: "F"}}})
	progress(1, 4)
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "scoring", Active: true, Done: 1, Total: 4, Rated: 1,
		FolderID: "F", Passes: []events.QualityPass{{FolderID: "F", Done: 1, Total: 4}}})
}

// The quality status has no download and no error state: with the setting
// on it is idle, scoring while a pass runs, idle again after a pass whatever
// error ended it, and disabled with the setting off.
func TestTracker_NoDownloadOrErrorStates(t *testing.T) {
	e := newQualityTestEnv(t)
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle"})

	_, progress, end := e.tr.Begin(context.Background(), "F")
	progress(1, 3)
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "scoring", Active: true, Done: 1, Total: 3, Rated: 1,
		FolderID: "F", Passes: []events.QualityPass{{FolderID: "F", Done: 1, Total: 3}}})

	end(1, fmt.Errorf("quality: rate file 3: %w", errors.New("SetQuality: database is locked")))
	got := e.tr.Status()
	assertQuality(t, got, events.Quality{Enabled: true, State: "idle", Rated: 1})
	if got.Error != "" {
		t.Fatalf("error text = %q; want none", got.Error)
	}

	e.set(func(e *qualityTestEnv) { e.enabled = false })
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: false, State: "disabled", Rated: 1})
}

func TestStatus_PassErrorsAreNotAState(t *testing.T) {
	// The job's outcomes: a pass stopped by the setting, by the folder going
	// away (the job is suspended) or by repeated failures of single photos
	// leaves the status idle.
	for name, err := range map[string]error{
		"setting off":     fmt.Errorf("stopped: %w", context.Canceled),
		"folder gone":     context.Canceled,
		"repeated errors": errors.New("quality: rate file 3: decode failed"),
	} {
		t.Run(name, func(t *testing.T) {
			e := newQualityTestEnv(t)
			_, progress, end := e.tr.Begin(context.Background(), "F")
			progress(1, 3)
			end(1, err)
			assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle", Rated: 1})
		})
	}
}

func TestStatus_Waiting(t *testing.T) {
	e := newQualityTestEnv(t)
	e.set(func(e *qualityTestEnv) { e.busy = true })
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "waiting", Active: true})
}

func TestStatus_Idle(t *testing.T) {
	e := newQualityTestEnv(t)
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle"})
}

func TestSettingOff_CancelsPassImmediately(t *testing.T) {
	e := newQualityTestEnv(t)
	e.set(func(e *qualityTestEnv) { e.busy = true })
	passCtx, progress, _ := e.tr.Begin(context.Background(), "F")
	progress(1, 3)
	if err := passCtx.Err(); err != nil {
		t.Fatalf("passCtx.Err() = %v before the switch; want nil", err)
	}

	e.set(func(e *qualityTestEnv) { e.enabled = false })
	e.tr.SettingChanged()
	select {
	case <-passCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("the pass was not cancelled within 100 ms of SettingChanged")
	}
	if cause := context.Cause(passCtx); !errors.Is(cause, errJobQualityOff) {
		t.Fatalf("cause = %v; want errJobQualityOff", cause)
	}
	got := e.sent(t) // EmitNow: no Flush needed
	if len(got) == 0 {
		t.Fatal("no quality:status sent")
	}
	assertQuality(t, got[len(got)-1], events.Quality{Enabled: false, State: "disabled", Rated: 1})
}

func TestSettingChanged_AlwaysReEmits(t *testing.T) {
	e := newQualityTestEnv(t)
	e.tr.SettingChanged()
	e.tr.SettingChanged()
	got := e.sent(t)
	if len(got) != 2 {
		t.Fatalf("events = %+v; want two", got)
	}
	for _, s := range got {
		assertQuality(t, s, events.Quality{Enabled: true, State: "idle"})
	}
}

func TestSettingOn_KeepsPassesRunning(t *testing.T) {
	e := newQualityTestEnv(t)
	passCtx, _, _ := e.tr.Begin(context.Background(), "F")
	e.tr.SettingChanged() // still on
	if err := passCtx.Err(); err != nil {
		t.Fatalf("passCtx.Err() = %v after SettingChanged with the setting on; want nil", err)
	}
}

func TestBegin_WhenDisabledReturnsCancelledContext(t *testing.T) {
	e := newQualityTestEnv(t)
	e.set(func(e *qualityTestEnv) { e.enabled = false })
	passCtx, progress, end := e.tr.Begin(context.Background(), "F")
	if passCtx.Err() == nil {
		t.Fatal("passCtx is not cancelled")
	}
	if cause := context.Cause(passCtx); !errors.Is(cause, errJobQualityOff) {
		t.Fatalf("cause = %v; want errJobQualityOff", cause)
	}
	if progress == nil || end == nil {
		t.Fatal("progress or end is nil")
	}
	progress(1, 1)
	end(0, passCtx.Err())
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: false, State: "disabled"})
	if got := e.sent(t); len(got) != 0 {
		t.Fatalf("events = %+v; want none for a pass that never started", got)
	}

	// The setting back on: the refused pass is not shown as running.
	e.set(func(e *qualityTestEnv) { e.enabled = true })
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle"})
}

func TestTransitions_AllEmitted(t *testing.T) {
	e := newQualityTestEnv(t)

	e.set(func(e *qualityTestEnv) { e.busy = true }) // a job is queued
	e.changed()
	_, progress, end := e.tr.Begin(context.Background(), "F")
	progress(1, 2)
	progress(2, 2)
	e.set(func(e *qualityTestEnv) { e.busy = false })
	end(2, nil)
	e.changed() // the queue reports the job's end: nothing new

	got := e.sent(t) // no Flush: these were all sent at once
	want := []events.Quality{
		{Enabled: true, State: "waiting", Active: true},
		{Enabled: true, State: "scoring", Active: true, FolderID: "F", Passes: []events.QualityPass{{FolderID: "F"}}},
		{Enabled: true, State: "idle", Rated: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v\nwant     %+v", got, want)
	}
	for i := range want {
		assertQuality(t, got[i], want[i])
	}
	e.emit.Flush() // the coalesced progress was dropped by the final state
	if n := len(e.sent(t)); n != len(want) {
		t.Fatalf("events after Flush = %+v; want the final state last", e.sent(t))
	}
}

func TestRated_Monotonic(t *testing.T) {
	e := newQualityTestEnv(t)
	last := 0
	check := func(when string) {
		t.Helper()
		r := e.tr.Status().Rated
		if r < last {
			t.Fatalf("%s: Rated %d < %d", when, r, last)
		}
		last = r
	}
	for _, n := range []int{3, 2} {
		_, progress, end := e.tr.Begin(context.Background(), "F")
		check("begin")
		for i := 1; i <= n; i++ {
			progress(i, n)
			check("progress")
		}
		end(n, nil)
		check("end")
	}
	if r := e.tr.Status().Rated; r != 5 {
		t.Fatalf("Rated = %d; want 5", r)
	}

	// A pass whose photos failed counts them as its progress did; ending it
	// never lowers the counter.
	_, progress, end := e.tr.Begin(context.Background(), "F")
	progress(1, 2)
	progress(2, 2)
	end(0, errors.New("quality: rate file 2: decode failed"))
	check("a failed pass")
	if r := e.tr.Status().Rated; r != 7 {
		t.Fatalf("Rated = %d; want 7", r)
	}
	end(5, nil) // a second end is ignored
	if r := e.tr.Status().Rated; r != 7 {
		t.Fatalf("Rated after a second end = %d; want 7", r)
	}
}

func TestScoringProgress_Coalesced(t *testing.T) {
	e := newQualityTestEnv(t)
	_, progress, end := e.tr.Begin(context.Background(), "F")
	if n := len(e.sent(t)); n != 1 {
		t.Fatalf("events after Begin = %d; want 1 (scoring)", n)
	}
	for i := 1; i <= 3; i++ {
		progress(i, 4)
	}
	if n := len(e.sent(t)); n != 1 {
		t.Fatalf("events after progress = %+v; want progress coalesced", e.sent(t))
	}
	e.emit.Flush()
	got := e.sent(t)
	if len(got) != 2 {
		t.Fatalf("events after Flush = %+v; want scoring + the latest progress", got)
	}
	assertQuality(t, got[1], events.Quality{Enabled: true, State: "scoring", Active: true, Done: 3, Total: 4, Rated: 3,
		FolderID: "F", Passes: []events.QualityPass{{FolderID: "F", Done: 3, Total: 4}}})

	progress(4, 4)
	end(4, nil)
	e.emit.Flush()
	got = e.sent(t)
	if len(got) != 3 {
		t.Fatalf("events = %+v; want the progress before the end dropped", got)
	}
	assertQuality(t, got[2], events.Quality{Enabled: true, State: "idle", Rated: 4})
}

// Changed is called from the Queue's event path, which holds its own lock
// (and the Coalescer's) while it emits: it must return without reading Busy,
// and without emitting, on the caller's goroutine.
func TestChanged_SafeFromInsideAnEmitterHoldingLocks(t *testing.T) {
	e := newQualityTestEnv(t)
	var queueMu sync.Mutex
	e.set(func(e *qualityTestEnv) {
		e.busyLock = &queueMu
		e.onEmit = func(name string) {
			if name == events.ScanState {
				e.tr.Changed()
			}
		}
	})

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		queueMu.Lock() // the Queue emits scan:state holding its mutex...
		defer queueMu.Unlock()
		e.set(func(e *qualityTestEnv) { e.busy = true })
		// ...through the same Coalescer, whose delivery lock is held while
		// the subscriber runs.
		e.emit.EmitNow(events.ScanState, "folder", events.ScanStateEvent{FolderID: "folder", State: "queued"})
	}()
	select {
	case <-returned:
	case <-time.After(sessionTestTimeout):
		t.Fatal("Changed blocked its caller")
	}
	e.tr.settle()
	got := e.sent(t)
	if len(got) != 1 || got[0].State != "waiting" {
		t.Fatalf("events = %+v; want waiting", got)
	}
}

// Whatever the interleaving of passes and queue changes, the last
// status delivered is the current one: no older coalesced value overtakes
// it.
func TestQualityTracker_LastEventIsCurrent(t *testing.T) {
	e := newQualityTestEnv(t)
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				switch g {
				case 0:
					_, progress, end := e.tr.Begin(context.Background(), "F")
					progress(1, 2)
					progress(2, 2)
					end(2, nil)
				case 1:
					_, progress, end := e.tr.Begin(context.Background(), "G")
					progress(1, 1)
					end(1, nil)
				case 2:
					e.set(func(e *qualityTestEnv) { e.busy = i%2 == 0 })
					e.tr.Changed()
				case 3:
					e.emit.Flush()
				}
			}
		}()
	}
	wg.Wait()
	e.set(func(e *qualityTestEnv) { e.busy = false })
	e.changed()
	e.emit.Flush()

	got := e.sent(t)
	if len(got) == 0 {
		t.Fatal("no quality:status sent")
	}
	assertQuality(t, got[len(got)-1], e.tr.Status())
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle", Rated: 150})
}

// Back-to-back notifications, none waited for: each is read on its own, in
// order, and every state read is sent once, at once — none is merged into the
// next.
func TestChanged_BackToBackTransitionsAllEmitted(t *testing.T) {
	e := newQualityTestEnv(t)
	var queueMu sync.Mutex
	script := []bool{true, false, true, false} // queued, done, queued, done
	e.set(func(e *qualityTestEnv) {
		e.busyLock = &queueMu
		e.busyScript = slices.Clone(script)
	})

	queueMu.Lock() // the drainer's first read waits: the calls pile up
	for range script {
		e.tr.Changed()
	}
	queueMu.Unlock()
	e.tr.settle()

	var got []string
	for _, s := range e.sent(t) { // no Flush: all sent at once
		got = append(got, s.State)
	}
	if want := []string{"waiting", "idle", "waiting", "idle"}; !slices.Equal(got, want) {
		t.Fatalf("states sent = %v; want %v", got, want)
	}
}

// Status reads a pass's numbers while progress writes them: under -race the
// snapshot is never torn (progress(i, i) ⇒ Done == Total).
func TestStatus_ConcurrentWithProgress(t *testing.T) {
	e := newQualityTestEnv(t)
	_, progress, end := e.tr.Begin(context.Background(), "F")
	const n = 500
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if s := e.tr.Status(); s.State == "scoring" && s.Done != s.Total {
				t.Errorf("torn snapshot: Done %d, Total %d", s.Done, s.Total)
				return
			}
		}
	}()
	for i := 1; i <= n; i++ {
		progress(i, i)
	}
	close(stop)
	<-readerDone
	end(n, nil)
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle", Rated: n})
}

type emitterFunc func(name string, data any)

func (f emitterFunc) Emit(name string, data any) { f(name, data) }

// The tracker as the job's QualityRun: the pass is reported and its ratings
// counted.
func TestQualityTracker_TracksTheJobsPass(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills}, nil)
	id, root := h.folder(t, "tracked")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	tr := NewQualityTracker(QualityOptions{
		Enabled: func() bool { return h.settings.get().QualityRating },
		Emit:    h.emit,
	})

	if o, msg := h.run(context.Background(), h.job(tr), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	tr.settle()
	assertQuality(t, tr.Status(), events.Quality{Enabled: true, State: "idle", Rated: 3})
	var states []string
	for _, ev := range h.rec.Events(events.QualityStatus) {
		states = append(states, ev.(events.Quality).State)
	}
	if len(states) != 2 || states[0] != "scoring" || states[1] != "idle" {
		t.Fatalf("quality:status states = %v; want [scoring idle]", states)
	}
}

// Switching the setting off mid-pass stops the job's pass before the next
// photo; the job still finishes.
func TestQualityTracker_SettingOffStopsTheJobsPass(t *testing.T) {
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills}, nil)
	id, root := h.folder(t, "opt-out")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg", "b.jpg", "c.jpg")
	tr := NewQualityTracker(QualityOptions{
		Enabled: func() bool { return h.settings.get().QualityRating },
		Emit:    h.emit,
	})
	var once sync.Once
	j := h.job(trackerHook{inner: tr, onProgress: func() {
		once.Do(func() {
			h.settings.set(func(c *config.Config) { c.QualityRating = false })
			tr.SettingChanged()
		})
	}})

	if o, msg := h.run(context.Background(), j, id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	tr.settle()
	assertQuality(t, tr.Status(), events.Quality{Enabled: false, State: "disabled", Rated: 1})
	s, release := h.acquire(t, id)
	defer release()
	if n, err := s.Scorer().CountPending(); err != nil || n != 2 {
		t.Fatalf("photos pending a rating = %d, %v; want 2 (none rated after the switch)", n, err)
	}
}
