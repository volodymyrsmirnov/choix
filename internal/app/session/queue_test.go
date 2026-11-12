package session

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
)

// waitTimeout bounds every wait in these tests. It only decides how long a
// broken queue takes to fail; a correct one never comes close.
const waitTimeout = 10 * time.Second

// script is what the fake runner does for one call.
type script func(ctx context.Context, kind JobKind, report func(events.Progress, string)) (Outcome, string)

// runCall is one call of the fake runner.
type runCall struct {
	ID      string
	Kind    JobKind
	Outcome Outcome
}

// fakeRunner runs scripted calls. Each folder has a list of scripts used one
// per call; a folder without one left returns Done at once.
type fakeRunner struct {
	mu        sync.Mutex
	scripts   map[string][]script
	calls     []runCall
	ctxs      map[string][]context.Context
	active    int
	maxActive int
	activeOf  map[string]int // calls in progress per folder
	peakOf    map[string]int // most calls in progress at once per folder

	started  chan runCall // sent when a call begins
	finished chan runCall // sent just before a call returns, with its outcome
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		scripts:  map[string][]script{},
		ctxs:     map[string][]context.Context{},
		activeOf: map[string]int{},
		peakOf:   map[string]int{},
		started:  make(chan runCall, 256),
		finished: make(chan runCall, 256),
	}
}

// script appends scripts for the folder's next calls.
func (f *fakeRunner) script(id string, s ...script) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], s...)
}

func (f *fakeRunner) Run(ctx context.Context, id string, kind JobKind, report func(events.Progress, string)) (Outcome, string) {
	f.mu.Lock()
	f.calls = append(f.calls, runCall{ID: id, Kind: kind})
	f.ctxs[id] = append(f.ctxs[id], ctx)
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	f.activeOf[id]++
	f.peakOf[id] = max(f.peakOf[id], f.activeOf[id])
	var s script
	if list := f.scripts[id]; len(list) > 0 {
		s, f.scripts[id] = list[0], list[1:]
	}
	f.mu.Unlock()

	f.started <- runCall{ID: id, Kind: kind}
	o, msg := Done, ""
	if s != nil {
		o, msg = s(ctx, kind, report)
	}

	f.mu.Lock()
	f.active--
	f.activeOf[id]--
	f.mu.Unlock()
	f.finished <- runCall{ID: id, Kind: kind, Outcome: o}
	return o, msg
}

func (f *fakeRunner) callIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.ID
	}
	return out
}

func (f *fakeRunner) callsOf(id string) []runCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runCall
	for _, c := range f.calls {
		if c.ID == id {
			out = append(out, c)
		}
	}
	return out
}

// ctxOf returns the context of the folder's n-th call (0-based).
func (f *fakeRunner) ctxOf(t *testing.T, id string, n int) context.Context {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ctxs[id]) <= n {
		t.Fatalf("runner has %d calls for %s, want call %d", len(f.ctxs[id]), id, n)
	}
	return f.ctxs[id][n]
}

func (f *fakeRunner) peak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

// hold returns a script that runs until gate is closed (Done) or its context
// is cancelled (Stopped).
func hold(gate <-chan struct{}) script {
	return func(ctx context.Context, _ JobKind, _ func(events.Progress, string)) (Outcome, string) {
		select {
		case <-gate:
			return Done, ""
		case <-ctx.Done():
			return Stopped, ""
		}
	}
}

// slowStop returns a script that runs until its context is cancelled, then
// signals stopping and returns Stopped only once release is closed: a job
// that takes time to honour cancellation.
func slowStop(stopping chan<- struct{}, release <-chan struct{}) script {
	return func(ctx context.Context, _ JobKind, _ func(events.Progress, string)) (Outcome, string) {
		<-ctx.Done()
		close(stopping)
		<-release
		return Stopped, ""
	}
}

// returns is a script that returns o and msg at once.
func returns(o Outcome, msg string) script {
	return func(context.Context, JobKind, func(events.Progress, string)) (Outcome, string) {
		return o, msg
	}
}

// harness is a Queue over a real library.db in a temporary file.
type harness struct {
	t      *testing.T
	dbPath string
	db     *library.DB
	lib    *library.Library
	rec    *events.Recorder
	co     *events.Coalescer
	run    *fakeRunner
	q      *Queue
	slots  int // QueueOptions.Slots of the queue newQueue builds

	mu          sync.Mutex
	suspended   []string
	onSuspended func(id string) // extra behaviour of OnSuspended, if any
}

// newHarness opens a library.db holding one present folder per id, added in
// the order given.
func newHarness(t *testing.T, ids ...string) *harness {
	t.Helper()
	h := &harness{
		t:      t,
		dbPath: filepath.Join(t.TempDir(), "library.db"),
		rec:    &events.Recorder{},
		run:    newFakeRunner(),
	}
	db, err := library.OpenDB(h.dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	h.db = db
	t.Cleanup(func() { _ = db.Close() })
	for i, id := range ids {
		if err := db.InsertFolder(library.Folder{
			ID: id, Path: "/test/" + id, Name: id, Bookmark: []byte("bookmark-" + id),
			AddedAt: int64(i + 1), Presence: "present",
		}); err != nil {
			t.Fatalf("InsertFolder(%s): %v", id, err)
		}
	}
	h.lib = library.New(library.Options{DB: db})
	// The coalescer is never run: tests flush it themselves.
	h.co = events.NewCoalescer(h.rec, time.Hour)
	h.q = h.newQueue()
	return h
}

func (h *harness) newQueue() *Queue {
	return NewQueue(QueueOptions{
		Library: h.lib,
		Runner:  h.run,
		Emit:    h.co,
		Slots:   h.slots,
		OnSuspended: func(id string) {
			h.mu.Lock()
			h.suspended = append(h.suspended, id)
			extra := h.onSuspended
			h.mu.Unlock()
			if extra != nil {
				extra(id)
			}
		},
	})
}

// start starts the queue; it is stopped when the test ends (before the
// database is closed).
func (h *harness) start() {
	h.q.Start(context.Background())
	h.t.Cleanup(h.q.Stop)
}

func (h *harness) suspendedCalls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.suspended)
}

func (h *harness) enqueue(id string, kind JobKind) {
	h.t.Helper()
	if err := h.q.Enqueue(id, kind); err != nil {
		h.t.Fatalf("Enqueue(%s): %v", id, err)
	}
}

func (h *harness) enqueueFront(id string, kind JobKind) {
	h.t.Helper()
	if err := h.q.EnqueueFront(id, kind); err != nil {
		h.t.Fatalf("EnqueueFront(%s): %v", id, err)
	}
}

// waitStarted waits for the runner's next call and checks it is for id.
func (h *harness) waitStarted(id string) runCall {
	h.t.Helper()
	select {
	case c := <-h.run.started:
		if c.ID != id {
			h.t.Fatalf("runner started %s, want %s", c.ID, id)
		}
		return c
	case <-time.After(waitTimeout):
		h.t.Fatalf("runner never started %s", id)
	}
	return runCall{}
}

// waitFinished waits for the runner's next return and checks it is for id.
func (h *harness) waitFinished(id string) runCall {
	h.t.Helper()
	select {
	case c := <-h.run.finished:
		if c.ID != id {
			h.t.Fatalf("runner finished %s, want %s", c.ID, id)
		}
		return c
	case <-time.After(waitTimeout):
		h.t.Fatalf("runner never finished %s", id)
	}
	return runCall{}
}

// waitIdle waits until the queue has nothing queued or running.
func (h *harness) waitIdle() {
	h.t.Helper()
	waitFor(h.t, "queue idle", h.q.Idle)
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *harness) folder(id string) library.Folder {
	h.t.Helper()
	f, err := h.lib.Folder(id)
	if err != nil {
		h.t.Fatalf("Folder(%s): %v", id, err)
	}
	return f
}

func (h *harness) pending() []library.PendingJob {
	h.t.Helper()
	p, err := h.lib.PendingJobs()
	if err != nil {
		h.t.Fatalf("PendingJobs: %v", err)
	}
	return p
}

func (h *harness) pendingSeq(id string) int64 {
	h.t.Helper()
	for _, p := range h.pending() {
		if p.ID == id {
			return p.Seq
		}
	}
	h.t.Fatalf("%s is not pending", id)
	return 0
}

// states returns the scan:state events of the folder, as "state" or
// "state: error".
func (h *harness) states(id string) []string {
	h.t.Helper()
	var out []string
	for _, e := range h.rec.Events(events.ScanState) {
		ev, ok := e.(events.ScanStateEvent)
		if !ok {
			h.t.Fatalf("scan:state payload is %T", e)
		}
		if ev.FolderID != id {
			continue
		}
		s := ev.State
		if ev.Error != "" {
			s += ": " + ev.Error
		}
		out = append(out, s)
	}
	return out
}

// progress returns the scan:progress events of the folder.
func (h *harness) progress(id string) []events.ScanProgressEvent {
	h.t.Helper()
	var out []events.ScanProgressEvent
	for _, e := range h.rec.Events(events.ScanProgress) {
		ev, ok := e.(events.ScanProgressEvent)
		if !ok {
			h.t.Fatalf("scan:progress payload is %T", e)
		}
		if ev.FolderID == id {
			out = append(out, ev)
		}
	}
	return out
}

func lastOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

func TestQueue_OneAtATimeFIFO(t *testing.T) {
	h := newHarness(t, "A", "B", "C")
	gates := map[string]chan struct{}{}
	for _, id := range []string{"A", "B", "C"} {
		gates[id] = make(chan struct{})
		h.run.script(id, hold(gates[id]))
	}
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.enqueue("C", JobFull)

	for _, id := range []string{"A", "B", "C"} {
		h.waitStarted(id)
		if got := h.run.peak(); got != 1 {
			t.Fatalf("%d jobs ran at once, want 1", got)
		}
		close(gates[id])
		h.waitFinished(id)
	}
	h.waitIdle()

	if got, want := h.run.callIDs(), []string{"A", "B", "C"}; !slices.Equal(got, want) {
		t.Errorf("run order %v, want %v", got, want)
	}
	if got := h.run.peak(); got != 1 {
		t.Errorf("%d jobs ran at once, want 1", got)
	}
	for _, id := range []string{"A", "B", "C"} {
		if f := h.folder(id); f.JobPending {
			t.Errorf("%s still pending after Done", id)
		}
		if got, want := h.states(id), []string{"queued", "scanning", "ready"}; !slices.Equal(got, want) {
			t.Errorf("scan:state of %s = %v, want %v", id, got, want)
		}
	}
}

func TestQueue_DedupesQueuedFolder(t *testing.T) {
	h := newHarness(t, "A", "B", "C")
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)
	h.enqueue("C", JobFull)
	h.enqueue("B", JobFull)
	if got, _ := h.q.State("B"); got != "queued" {
		t.Fatalf("State(B) = %q, want queued", got)
	}
	close(gate)
	h.waitIdle()

	if got, want := h.run.callIDs(), []string{"A", "B", "C"}; !slices.Equal(got, want) {
		t.Errorf("run order %v, want %v (B once, in its first place)", got, want)
	}
}

func TestQueue_FullReplacesBackfillNotReverse(t *testing.T) {
	h := newHarness(t, "A", "B", "C")
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")

	h.enqueue("B", JobBackfill)
	h.enqueue("B", JobFull)
	h.enqueue("C", JobFull)
	h.enqueue("C", JobBackfill)
	for _, id := range []string{"B", "C"} {
		if f := h.folder(id); f.JobKind != "full" {
			t.Errorf("stored kind of %s = %q, want full", id, f.JobKind)
		}
	}
	close(gate)
	h.waitIdle()

	for _, id := range []string{"B", "C"} {
		calls := h.run.callsOf(id)
		if len(calls) != 1 || calls[0].Kind != JobFull {
			t.Errorf("calls for %s = %+v, want one JobFull", id, calls)
		}
	}
}

func TestEnqueue_UnknownFolderIsNotFound(t *testing.T) {
	h := newHarness(t, "A")
	h.start()
	if err := h.q.Enqueue("nope", JobFull); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("Enqueue(unknown) = %v, want ErrNotFound", err)
	}
	if err := h.q.EnqueueFront("nope", JobFull); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("EnqueueFront(unknown) = %v, want ErrNotFound", err)
	}
	if got, _ := h.q.State("nope"); got != "" {
		t.Errorf("State(unknown) = %q, want empty", got)
	}
	if !h.q.Idle() {
		t.Error("an unknown folder was queued")
	}
}

func TestEnqueue_PersistsBeforeRunning(t *testing.T) {
	h := newHarness(t, "A", "B")
	gate := make(chan struct{})
	defer close(gate)
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)
	h.q.Stop()
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err := library.OpenDB(h.dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	jobs, err := db.PendingJobs()
	if err != nil {
		t.Fatalf("PendingJobs: %v", err)
	}
	i := slices.IndexFunc(jobs, func(j library.PendingJob) bool { return j.ID == "B" })
	if i < 0 {
		t.Fatalf("B is not pending after a restart: %+v", jobs)
	}
	if jobs[i].Kind != "full" {
		t.Errorf("B's kind = %q, want full", jobs[i].Kind)
	}
	if got := h.run.callsOf("B"); len(got) != 0 {
		t.Errorf("B ran: %+v", got)
	}
}

func TestStart_RequeuesPendingWithStoredKind(t *testing.T) {
	h := newHarness(t, "A", "B")
	if _, err := h.db.EnqueueJob("A", "backfill"); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	if got, _ := h.q.State("A"); got == "" {
		t.Errorf("State(A) right after Start = %q, want queued or scanning", got)
	}
	c := h.waitStarted("A")
	if c.Kind != JobBackfill {
		t.Errorf("A ran with kind %v, want JobBackfill", c.Kind)
	}
	close(gate)
	h.waitIdle()
	if got := h.run.callsOf("B"); len(got) != 0 {
		t.Errorf("B (not pending) ran: %+v", got)
	}
	if f := h.folder("A"); f.JobPending {
		t.Error("A still pending after Done")
	}
}

func TestStart_RequeuesInEnqueueOrder(t *testing.T) {
	h := newHarness(t, "A", "B") // A was added first
	for _, id := range []string{"B", "A"} {
		if _, err := h.db.EnqueueJob(id, "full"); err != nil {
			t.Fatalf("EnqueueJob(%s): %v", id, err)
		}
	}
	h.start()
	h.waitIdle()
	if got, want := h.run.callIDs(), []string{"B", "A"}; !slices.Equal(got, want) {
		t.Errorf("run order after restart %v, want %v", got, want)
	}
}

func TestStart_SkipsOfflinePending(t *testing.T) {
	h := newHarness(t, "A", "B")
	if _, err := h.db.EnqueueJob("A", "full"); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := h.db.EnqueueJob("B", "full"); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := h.db.SetPresence("A", "offline"); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}
	h.start()
	if got, _ := h.q.State("A"); got != "" {
		t.Errorf("State(offline A) = %q, want empty", got)
	}
	h.waitStarted("B")
	h.waitIdle()
	if got := h.run.callsOf("A"); len(got) != 0 {
		t.Errorf("offline A ran: %+v", got)
	}
	if f := h.folder("A"); !f.JobPending {
		t.Error("offline A is no longer pending")
	}
}

func TestEnqueueDuringRun_RunsAgain(t *testing.T) {
	h := newHarness(t, "A")
	first, second := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(first), hold(second))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("A", JobFull)
	if got, _ := h.q.State("A"); got != "scanning" {
		t.Errorf("State(A) after enqueueing it while it runs = %q, want scanning", got)
	}

	close(first)
	if c := h.waitFinished("A"); c.Outcome != Done {
		t.Fatalf("first run outcome %v, want Done", c.Outcome)
	}
	h.waitStarted("A")
	if f := h.folder("A"); !f.JobPending {
		t.Error("A is not pending between its two runs")
	}
	if slices.Contains(h.states("A"), "ready") {
		t.Errorf("A was marked ready before its second run: %v", h.states("A"))
	}

	close(second)
	h.waitIdle()
	if got := len(h.run.callsOf("A")); got != 2 {
		t.Errorf("A ran %d times, want 2", got)
	}
	if f := h.folder("A"); f.JobPending {
		t.Error("A still pending after its second run")
	}
	if got := lastOf(h.states("A")); got != "ready" {
		t.Errorf("last scan:state of A = %q, want ready", got)
	}
}

func TestDone_DoesNotEraseNewerEnqueue(t *testing.T) {
	h := newHarness(t, "A")
	first, second := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(first), hold(second))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	older := h.pendingSeq("A")
	h.enqueue("A", JobBackfill)
	newer := h.pendingSeq("A")
	if newer <= older {
		t.Fatalf("enqueue during the run did not bump the sequence: %d then %d", older, newer)
	}

	close(first)
	c := h.waitStarted("A")
	if got := h.pendingSeq("A"); got != newer {
		t.Errorf("after the older run finished, A's sequence is %d, want %d", got, newer)
	}
	if f := h.folder("A"); f.JobKind != "full" {
		t.Errorf("A's stored kind = %q, want full (a pending full job is not downgraded)", f.JobKind)
	}
	if c.Kind != JobFull {
		t.Errorf("second run kind %v, want JobFull (as stored)", c.Kind)
	}
	if cleared, err := h.lib.FinishJob("A", older, ""); err != nil || cleared {
		t.Errorf("FinishJob(older) = %v, %v; want false, nil", cleared, err)
	}

	close(second)
	h.waitIdle()
	if got := h.pending(); len(got) != 0 {
		t.Errorf("pending after the newer run = %+v, want none", got)
	}
}

func TestEnqueueFront_PreemptsRunningJob(t *testing.T) {
	h := newHarness(t, "A", "B", "C")
	gateA, gateB := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(gateA))
	h.run.script("B", hold(gateB))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	seqA := h.pendingSeq("A")
	h.enqueue("C", JobFull)
	h.enqueueFront("B", JobFull)

	// The cancellation happens inside EnqueueFront: well within 1 s.
	if err := h.run.ctxOf(t, "A", 0).Err(); err == nil {
		t.Fatal("A's context is not cancelled when EnqueueFront returns")
	}
	if c := h.waitFinished("A"); c.Outcome != Stopped {
		t.Fatalf("A's outcome %v, want Stopped", c.Outcome)
	}
	h.waitStarted("B")
	if got, _ := h.q.State("A"); got != "queued" {
		t.Errorf("State(preempted A) = %q, want queued", got)
	}
	if got := h.pendingSeq("A"); got != seqA {
		t.Errorf("preempted A's sequence %d, want %d (kept)", got, seqA)
	}
	close(gateB)
	h.waitFinished("B")
	h.waitStarted("A")
	h.waitFinished("A")
	h.waitStarted("C")
	h.waitIdle()
	close(gateA)

	if got, want := h.run.callIDs(), []string{"A", "B", "A", "C"}; !slices.Equal(got, want) {
		t.Errorf("run order %v, want %v", got, want)
	}
	for _, id := range []string{"A", "B", "C"} {
		if got := lastOf(h.states(id)); got != "ready" {
			t.Errorf("last scan:state of %s = %q, want ready", id, got)
		}
	}
}

func TestEnqueueFront_SameFolderDoesNotRestart(t *testing.T) {
	h := newHarness(t, "A", "B")
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)
	h.enqueueFront("A", JobFull)

	if err := h.run.ctxOf(t, "A", 0).Err(); err != nil {
		t.Fatalf("EnqueueFront of the running folder cancelled it: %v", err)
	}
	if got, _ := h.q.State("A"); got != "scanning" {
		t.Errorf("State(A) = %q, want scanning", got)
	}
	close(gate)
	if c := h.waitFinished("A"); c.Outcome != Done {
		t.Fatalf("first run outcome %v, want Done", c.Outcome)
	}
	// The open persisted a newer enqueue, so A runs once more afterwards —
	// at the tail, behind B, which was waiting ([call] C19).
	h.waitIdle()
	if got, want := h.run.callIDs(), []string{"A", "B", "A"}; !slices.Equal(got, want) {
		t.Errorf("runs %v, want %v", got, want)
	}
	if f := h.folder("A"); f.JobPending {
		t.Error("A still pending")
	}
}

func TestEnqueue_BackfillDoesNotDowngradePendingFull(t *testing.T) {
	t.Run("after a suspension", func(t *testing.T) {
		h := newHarness(t, "A")
		first, second := make(chan struct{}), make(chan struct{})
		defer close(first)
		h.run.script("A", hold(first), hold(second))
		h.start()
		h.enqueue("A", JobFull)
		h.waitStarted("A")
		h.q.Suspend("A")
		h.waitFinished("A")
		h.waitIdle()

		h.enqueue("A", JobBackfill)
		if c := h.waitStarted("A"); c.Kind != JobFull {
			t.Errorf("A ran as %v, want JobFull (the suspended full job is still pending)", c.Kind)
		}
		if f := h.folder("A"); f.JobKind != "full" {
			t.Errorf("stored kind %q, want full", f.JobKind)
		}
		close(second)
		h.waitIdle()
		if f := h.folder("A"); f.JobPending {
			t.Error("A still pending")
		}
	})

	t.Run("after a restart", func(t *testing.T) {
		h := newHarness(t, "A")
		if _, err := h.db.EnqueueJob("A", "full"); err != nil {
			t.Fatalf("EnqueueJob: %v", err)
		}
		h.enqueue("A", JobBackfill) // before Start: the full job from the last launch is pending
		h.start()
		if c := h.waitStarted("A"); c.Kind != JobFull {
			t.Errorf("A ran as %v, want JobFull", c.Kind)
		}
		h.waitIdle()
		if got := len(h.run.callsOf("A")); got != 1 {
			t.Errorf("A ran %d times, want 1", got)
		}
	})
}

func TestFinishJobError_KeepsPendingAndRerun(t *testing.T) {
	h := newHarness(t, "A")
	gate, release := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(gate), hold(release))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("A", JobFull) // a rerun is requested
	newer := h.pendingSeq("A")

	// The database fails from now on: no run can be acknowledged.
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(gate)
	if c := h.waitFinished("A"); c.Outcome != Done {
		t.Fatalf("first run outcome %v, want Done", c.Outcome)
	}
	h.waitStarted("A") // the requested rerun is kept
	close(release)
	h.waitFinished("A")
	h.waitIdle()
	if got := len(h.run.callsOf("A")); got != 2 {
		t.Errorf("A ran %d times, want 2 (the rerun, and no retry loop on a failing database)", got)
	}
	for _, s := range h.states("A") {
		if s == "ready" || strings.HasPrefix(s, "error") {
			t.Errorf("A was marked %q although no run was acknowledged", s)
		}
	}

	db, err := library.OpenDB(h.dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	jobs, err := db.PendingJobs()
	if err != nil {
		t.Fatalf("PendingJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "A" || jobs[0].Seq != newer {
		t.Errorf("pending after the failed acknowledgements = %+v, want A at sequence %d", jobs, newer)
	}
}

func TestSuspend_KeepsPending(t *testing.T) {
	h := newHarness(t, "A", "B")
	gateA, gateB := make(chan struct{}), make(chan struct{})
	defer close(gateA)
	h.run.script("A", hold(gateA))
	h.run.script("B", hold(gateB))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)

	h.q.Suspend("A")
	if err := h.run.ctxOf(t, "A", 0).Err(); err == nil {
		t.Fatal("Suspend did not cancel A")
	}
	if got, p := h.q.State("A"); got != "" || p != nil {
		t.Errorf("State(suspended A) = %q, %v; want empty, nil", got, p)
	}
	if c := h.waitFinished("A"); c.Outcome != Stopped {
		t.Errorf("A's outcome %v, want Stopped", c.Outcome)
	}
	h.waitStarted("B")
	f := h.folder("A")
	if !f.JobPending || f.JobError != "" {
		t.Errorf("suspended A: pending %v, error %q; want pending, no error", f.JobPending, f.JobError)
	}
	if got, _ := h.q.State("A"); got != "" {
		t.Errorf("State(suspended A) = %q, want empty", got)
	}
	close(gateB)
	h.waitIdle()
	if got := len(h.run.callsOf("A")); got != 1 {
		t.Errorf("suspended A ran %d times, want 1", got)
	}
	if got, want := h.states("A"), []string{"queued", "scanning"}; !slices.Equal(got, want) {
		t.Errorf("scan:state of A = %v, want %v", got, want)
	}
	if got := h.suspendedCalls(); len(got) != 0 {
		t.Errorf("OnSuspended called for a Suspend from outside: %v", got)
	}
}

func TestSuspend_NewerEnqueueWaitsForResume(t *testing.T) {
	h := newHarness(t, "A")
	// The job finishes its work although it was asked to stop.
	h.run.script("A", func(ctx context.Context, _ JobKind, _ func(events.Progress, string)) (Outcome, string) {
		<-ctx.Done()
		return Done, ""
	})
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("A", JobFull) // a newer enqueue: A would run again
	h.q.Suspend("A")
	h.waitFinished("A")
	h.waitIdle()
	if got := len(h.run.callsOf("A")); got != 1 {
		t.Fatalf("suspended A ran %d times, want 1", got)
	}
	if f := h.folder("A"); !f.JobPending {
		t.Fatal("the newer enqueue of A was lost")
	}
	h.q.Resume("A")
	h.waitStarted("A")
	h.waitIdle()
	if f := h.folder("A"); f.JobPending {
		t.Error("A still pending after its resumed run")
	}
}

func TestSuspend_DropsQueuedFolder(t *testing.T) {
	h := newHarness(t, "A", "B")
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobBackfill)
	h.q.Suspend("B")
	if got, _ := h.q.State("B"); got != "" {
		t.Errorf("State(suspended B) = %q, want empty", got)
	}
	close(gate)
	h.waitIdle()
	if got := h.run.callsOf("B"); len(got) != 0 {
		t.Errorf("suspended B ran: %+v", got)
	}
	if f := h.folder("B"); !f.JobPending || f.JobKind != "backfill" {
		t.Errorf("suspended B: pending %v kind %q; want pending backfill", f.JobPending, f.JobKind)
	}
}

func TestResume_RequeuesSuspended(t *testing.T) {
	t.Run("after it stopped", func(t *testing.T) {
		h := newHarness(t, "A")
		first, second := make(chan struct{}), make(chan struct{})
		defer close(first)
		h.run.script("A", hold(first), hold(second))
		h.start()
		h.enqueue("A", JobBackfill)
		h.waitStarted("A")
		h.q.Suspend("A")
		h.waitFinished("A")
		h.waitIdle()

		h.q.Resume("A")
		if got, _ := h.q.State("A"); got == "" {
			t.Errorf("State(resumed A) = %q, want queued or scanning", got)
		}
		if c := h.waitStarted("A"); c.Kind != JobBackfill {
			t.Errorf("resumed A ran with %v, want JobBackfill (stored kind)", c.Kind)
		}
		close(second)
		h.waitIdle()
		if f := h.folder("A"); f.JobPending {
			t.Error("resumed A still pending after Done")
		}
		if got := lastOf(h.states("A")); got != "ready" {
			t.Errorf("last scan:state of A = %q, want ready", got)
		}
	})

	t.Run("while it is still stopping", func(t *testing.T) {
		h := newHarness(t, "A")
		stopping, release := make(chan struct{}), make(chan struct{})
		h.run.script("A", slowStop(stopping, release))
		h.start()
		h.enqueue("A", JobFull)
		h.waitStarted("A")
		h.q.Suspend("A")
		<-stopping
		h.q.Resume("A") // the drive came back before the job wound down
		close(release)
		h.waitFinished("A")
		h.waitStarted("A")
		h.waitIdle()
		if f := h.folder("A"); f.JobPending {
			t.Error("A still pending")
		}
	})

	t.Run("not pending", func(t *testing.T) {
		h := newHarness(t, "A")
		h.start()
		h.q.Resume("A")
		h.q.Resume("unknown")
		if !h.q.Idle() {
			t.Error("Resume queued a folder with nothing pending")
		}
	})
}

func TestResume_NoOpWhenQueuedOrRunning(t *testing.T) {
	h := newHarness(t, "A", "B")
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)
	for range 10 {
		h.q.Resume("A")
		h.q.Resume("B")
	}
	close(gate)
	h.waitIdle()
	if got, want := h.run.callIDs(), []string{"A", "B"}; !slices.Equal(got, want) {
		t.Errorf("runs %v, want %v", got, want)
	}
}

func TestCancel_ClearsPendingAndWaits(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		h := newHarness(t, "A", "B")
		stopping, release := make(chan struct{}), make(chan struct{})
		h.run.script("A", slowStop(stopping, release))
		h.start()
		h.enqueue("A", JobFull)
		h.waitStarted("A")
		h.enqueue("B", JobFull)

		cancelled := make(chan struct{})
		go func() {
			h.q.Cancel("A")
			close(cancelled)
		}()
		<-stopping
		select {
		case <-cancelled:
			t.Fatal("Cancel returned before the runner returned")
		default:
		}
		close(release)
		select {
		case <-cancelled:
		case <-time.After(waitTimeout):
			t.Fatal("Cancel never returned")
		}
		// The runner sends finished just before it returns: it must be there.
		select {
		case c := <-h.run.finished:
			if c.ID != "A" {
				t.Fatalf("finished %s, want A", c.ID)
			}
		default:
			t.Fatal("Cancel returned before the runner returned")
		}
		if f := h.folder("A"); f.JobPending {
			t.Error("cancelled A is still pending")
		}
		h.waitIdle()
		if got, want := h.run.callIDs(), []string{"A", "B"}; !slices.Equal(got, want) {
			t.Errorf("runs %v, want %v", got, want)
		}
		if got, want := h.states("A"), []string{"queued", "scanning"}; !slices.Equal(got, want) {
			t.Errorf("scan:state of cancelled A = %v, want %v", got, want)
		}
	})

	t.Run("queued", func(t *testing.T) {
		h := newHarness(t, "A", "B")
		gate := make(chan struct{})
		h.run.script("A", hold(gate))
		h.start()
		h.enqueue("A", JobFull)
		h.waitStarted("A")
		h.enqueue("B", JobFull)
		h.q.Cancel("B")
		if got, _ := h.q.State("B"); got != "" {
			t.Errorf("State(cancelled B) = %q, want empty", got)
		}
		if f := h.folder("B"); f.JobPending {
			t.Error("cancelled B is still pending")
		}
		close(gate)
		h.waitIdle()
		if got := h.run.callsOf("B"); len(got) != 0 {
			t.Errorf("cancelled B ran: %+v", got)
		}
	})

	t.Run("removed folder", func(t *testing.T) {
		h := newHarness(t, "A")
		h.start()
		if err := h.db.DeleteFolder("A"); err != nil {
			t.Fatalf("DeleteFolder: %v", err)
		}
		h.q.Cancel("A") // must not block or panic
		if !h.q.Idle() {
			t.Error("queue not idle")
		}
	})
}

func TestOutcome_FailedStoresMessage(t *testing.T) {
	h := newHarness(t, "A", "B")
	h.run.script("A", func(_ context.Context, _ JobKind, report func(events.Progress, string)) (Outcome, string) {
		report(events.Progress{Stage: "thumb", Done: 1, Total: 4, State: "running"}, "running")
		return Failed, "boom"
	})
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.waitIdle()

	f := h.folder("A")
	if f.JobPending || f.JobError != "boom" {
		t.Errorf("failed A: pending %v, error %q; want not pending, %q", f.JobPending, f.JobError, "boom")
	}
	if got, want := h.states("A"), []string{"queued", "scanning", "error: boom"}; !slices.Equal(got, want) {
		t.Errorf("scan:state of A = %v, want %v", got, want)
	}
	h.co.Flush()
	p := h.progress("A")
	if len(p) == 0 || p[len(p)-1].Phase != "failed" || p[len(p)-1].Progress.State != "stopped" {
		t.Errorf("last scan:progress of A = %+v, want phase failed, state stopped", p)
	}
	if got := len(h.run.callsOf("B")); got != 1 {
		t.Errorf("B ran %d times after A failed, want 1", got)
	}
}

func TestOutcome_SuspendedKeepsPendingAndContinues(t *testing.T) {
	h := newHarness(t, "A", "B")
	h.run.script("A", returns(Suspended, "the volume went away"))
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.waitStarted("A")
	h.waitStarted("B")
	h.waitIdle()

	f := h.folder("A")
	if !f.JobPending || f.JobError != "" || f.JobKind != "full" {
		t.Errorf("suspended A: pending %v, kind %q, error %q; want pending full, no error",
			f.JobPending, f.JobKind, f.JobError)
	}
	if got := h.suspendedCalls(); !slices.Equal(got, []string{"A"}) {
		t.Errorf("OnSuspended calls %v, want [A]", got)
	}
	if got, want := h.states("A"), []string{"queued", "scanning"}; !slices.Equal(got, want) {
		t.Errorf("scan:state of A = %v, want %v (no state event for a suspension)", got, want)
	}
	if got, _ := h.q.State("A"); got != "" {
		t.Errorf("State(suspended A) = %q, want empty", got)
	}
	if got := lastOf(h.states("B")); got != "ready" {
		t.Errorf("B: last scan:state %q, want ready", got)
	}
}

func TestOutcome_ThreeSuspensionsOfPresentFolderFail(t *testing.T) {
	h := newHarness(t, "A")
	for range 5 {
		h.run.script("A", returns(Suspended, ""))
	}
	// The runtime's presence check finds the folder present and resumes it.
	h.onSuspended = h.q.Resume
	h.start()
	h.enqueue("A", JobFull)
	for range 3 {
		h.waitStarted("A")
		h.waitFinished("A")
	}
	h.waitIdle()

	if got := len(h.run.callsOf("A")); got != 3 {
		t.Errorf("A ran %d times, want 3", got)
	}
	f := h.folder("A")
	if f.JobPending || f.JobError != couldNotReadMessage {
		t.Errorf("A: pending %v, error %q; want not pending, %q", f.JobPending, f.JobError, couldNotReadMessage)
	}
	if got := lastOf(h.states("A")); got != "error: "+couldNotReadMessage {
		t.Errorf("last scan:state of A = %q, want error", got)
	}
	if got := h.suspendedCalls(); len(got) != 2 {
		t.Errorf("OnSuspended called %d times, want 2 (not for the suspension that failed it)", len(got))
	}
}

// A Busy run is a suspension that is never a strike: the folder stays
// pending without an error however often it happens, OnSuspended is not
// called (so nothing re-queues it at once), the queue does not run it again
// on its own, and a Resume runs it.
func TestOutcome_BusyIsNotAStrike(t *testing.T) {
	h := newHarness(t, "A")
	const rounds = maxSuspensions + 2
	for range rounds {
		h.run.script("A", returns(Busy, ""))
	}
	h.start()
	h.enqueue("A", JobFull)
	for i := range rounds {
		h.waitStarted("A")
		if c := h.waitFinished("A"); c.Outcome != Busy {
			t.Fatalf("run %d = %v; want Busy", i+1, c.Outcome)
		}
		h.waitIdle()
		if n := len(h.run.started); n != 0 {
			t.Fatalf("after busy run %d the queue started %d more runs on its own", i+1, n)
		}
		if f := h.folder("A"); !f.JobPending || f.JobError != "" || f.JobKind != "full" {
			t.Fatalf("after busy run %d: pending %v, kind %q, error %q; want pending full, no error",
				i+1, f.JobPending, f.JobKind, f.JobError)
		}
		h.q.Resume("A") // the presence pass
	}
	h.waitStarted("A") // unscripted: Done
	h.waitIdle()
	if f := h.folder("A"); f.JobPending || f.JobError != "" {
		t.Errorf("A: pending %v, error %q; want done without error", f.JobPending, f.JobError)
	}
	if got := h.suspendedCalls(); len(got) != 0 {
		t.Errorf("OnSuspended calls %v; want none for busy runs", got)
	}
	if got := lastOf(h.states("A")); got != "ready" {
		t.Errorf("last scan:state of A = %q; want ready", got)
	}
}

// A busy run does not reset the strikes of real suspensions either.
func TestOutcome_BusyLeavesSuspensionCount(t *testing.T) {
	h := newHarness(t, "A")
	h.run.script("A", returns(Suspended, ""), returns(Suspended, ""), returns(Busy, ""), returns(Suspended, ""))
	h.onSuspended = h.q.Resume
	h.start()
	h.enqueue("A", JobFull)
	for range 3 {
		h.waitStarted("A")
		h.waitFinished("A")
	}
	h.waitIdle() // the Busy run: nothing re-queued it
	h.q.Resume("A")
	h.waitStarted("A")
	h.waitFinished("A")
	h.waitIdle()
	if f := h.folder("A"); f.JobPending || f.JobError != couldNotReadMessage {
		t.Errorf("A: pending %v, error %q; want the third real suspension to fail it", f.JobPending, f.JobError)
	}
}

func TestOutcome_SuspensionsOfOfflineFolderNeverFail(t *testing.T) {
	h := newHarness(t, "A")
	const rounds = 5
	for range rounds {
		h.run.script("A", returns(Suspended, ""))
	}
	// The presence check finds the drive gone: the row turns offline and the
	// library suspends the job (OnOffline → Suspend).
	handled := make(chan struct{}, rounds)
	h.onSuspended = func(id string) {
		if err := h.db.SetPresence(id, "offline"); err != nil {
			t.Errorf("SetPresence: %v", err)
		}
		h.q.Suspend(id)
		handled <- struct{}{}
	}
	h.start()
	h.enqueue("A", JobFull)
	for range rounds {
		h.waitStarted("A")
		h.waitFinished("A")
		select {
		case <-handled:
		case <-time.After(waitTimeout):
			t.Fatal("OnSuspended never ran")
		}
		if !h.q.Idle() {
			t.Fatal("an offline folder is still queued or running")
		}
		// The drive comes back: present again, OnOnline → Resume.
		if err := h.db.SetPresence("A", "present"); err != nil {
			t.Fatalf("SetPresence: %v", err)
		}
		h.q.Resume("A")
	}
	h.waitStarted("A") // the sixth run is unscripted: Done
	h.waitIdle()
	f := h.folder("A")
	if f.JobPending || f.JobError != "" {
		t.Errorf("A: pending %v, error %q; want done without error", f.JobPending, f.JobError)
	}
}

func TestStop_JoinsAndLeavesPending(t *testing.T) {
	h := newHarness(t, "A", "B")
	gate := make(chan struct{})
	defer close(gate)
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobBackfill)
	h.q.Stop()

	select {
	case c := <-h.run.finished:
		if c.ID != "A" || c.Outcome != Stopped {
			t.Errorf("finished %+v, want A Stopped", c)
		}
	default:
		t.Fatal("Stop returned before the running job returned")
	}
	for _, id := range []string{"A", "B"} {
		if f := h.folder(id); !f.JobPending {
			t.Errorf("%s not pending after Stop", id)
		}
	}
	if got := h.run.callsOf("B"); len(got) != 0 {
		t.Errorf("B ran: %+v", got)
	}
	if got := lastOf(h.states("A")); got != "scanning" {
		t.Errorf("last scan:state of A = %q; Stop must not mark it", got)
	}
	h.q.Stop() // a second Stop is harmless
}

func TestStop_WithoutStart(t *testing.T) {
	h := newHarness(t, "A")
	h.enqueue("A", JobFull)
	h.q.Stop()
	if f := h.folder("A"); !f.JobPending {
		t.Error("A not pending")
	}
}

func TestProgress_Coalesced(t *testing.T) {
	h := newHarness(t, "A")
	const reports, flushes = 3000, 10
	h.run.script("A", func(_ context.Context, _ JobKind, report func(events.Progress, string)) (Outcome, string) {
		report(events.Progress{Stage: "thumb", Total: reports, State: "running"}, "starting")
		for i := 1; i <= reports; i++ {
			report(events.Progress{Stage: "thumb", Done: i, Total: reports, State: "running"}, "running")
			// The coalescer ticks ten times in this second's worth of reports.
			if i%(reports/flushes) == 0 {
				h.co.Flush()
			}
		}
		return Done, ""
	})
	h.start()
	h.enqueue("A", JobFull)
	h.waitIdle()
	h.co.Flush()

	p := h.progress("A")
	if len(p) >= 15 {
		t.Errorf("%d scan:progress events for %d reports, want fewer than 15", len(p), reports)
	}
	if len(p) < 3 {
		t.Fatalf("scan:progress events %+v: want starting, some running, done", p)
	}
	if p[0].Phase != "starting" {
		t.Errorf("first scan:progress phase %q, want starting", p[0].Phase)
	}
	last := p[len(p)-1]
	if last.Phase != "done" || last.Progress.Done != reports {
		t.Errorf("last scan:progress %+v, want phase done with done=%d", last, reports)
	}
	if got, want := h.states("A"), []string{"queued", "scanning", "ready"}; !slices.Equal(got, want) {
		t.Errorf("scan:state of A = %v, want %v", got, want)
	}
}

func TestProgress_StaleReportIgnored(t *testing.T) {
	h := newHarness(t, "A")
	var late func(events.Progress, string)
	h.run.script("A", func(_ context.Context, _ JobKind, report func(events.Progress, string)) (Outcome, string) {
		late = report
		return Done, ""
	})
	h.start()
	h.enqueue("A", JobFull)
	h.waitFinished("A")
	h.waitIdle()
	before := len(h.progress("A"))
	late(events.Progress{Stage: "thumb", Done: 1, State: "running"}, "running")
	late(events.Progress{Stage: "thumb", Done: 1, State: "running"}, "done")
	h.co.Flush()
	if got := len(h.progress("A")); got != before {
		t.Errorf("a report after the run returned was emitted (%d events, want %d)", got, before)
	}
}

func TestState_QueuedAndScanning(t *testing.T) {
	h := newHarness(t, "A", "B", "C")
	gate, reported := make(chan struct{}), make(chan struct{})
	h.run.script("A", func(ctx context.Context, _ JobKind, report func(events.Progress, string)) (Outcome, string) {
		report(events.Progress{Stage: "metadata", Done: 2, Total: 5, State: "running"}, "running")
		close(reported)
		select {
		case <-gate:
			return Done, ""
		case <-ctx.Done():
			return Stopped, ""
		}
	})
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.waitStarted("A")
	<-reported

	state, p := h.q.State("A")
	if state != "scanning" || p == nil || p.Stage != "metadata" || p.Done != 2 || p.Total != 5 {
		t.Errorf("State(A) = %q, %+v; want scanning with metadata 2/5", state, p)
	}
	if state, p := h.q.State("B"); state != "queued" || p != nil {
		t.Errorf("State(B) = %q, %v; want queued, nil", state, p)
	}
	if state, p := h.q.State("C"); state != "" || p != nil {
		t.Errorf("State(C) = %q, %v; want empty, nil", state, p)
	}
	close(gate)
	h.waitIdle()
	if state, p := h.q.State("A"); state != "" || p != nil {
		t.Errorf("State(A) after Done = %q, %v; want empty, nil", state, p)
	}
}

func TestIdle(t *testing.T) {
	h := newHarness(t, "A")
	if !h.q.Idle() {
		t.Error("a new queue is not idle")
	}
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	if !h.q.Idle() {
		t.Error("a started queue with nothing pending is not idle")
	}
	h.enqueue("A", JobFull)
	if h.q.Idle() {
		t.Error("idle with A queued")
	}
	h.waitStarted("A")
	if h.q.Idle() {
		t.Error("idle with A running")
	}
	close(gate)
	h.waitIdle()
}

func TestEnqueueAllReachable(t *testing.T) {
	h := newHarness(t, "A", "B", "C")
	if err := h.db.SetPresence("B", "offline"); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}
	h.start()
	h.q.EnqueueAllReachable(JobBackfill)
	h.waitIdle()
	if got, want := h.run.callIDs(), []string{"A", "C"}; !slices.Equal(got, want) {
		t.Errorf("runs %v, want %v", got, want)
	}
	for _, c := range h.run.callsOf("A") {
		if c.Kind != JobBackfill {
			t.Errorf("A ran with %v, want JobBackfill", c.Kind)
		}
	}
	if f := h.folder("B"); f.JobPending {
		t.Error("offline B was enqueued")
	}
}

func TestJobKind_String(t *testing.T) {
	for _, tc := range []struct {
		k    JobKind
		want string
	}{{JobBackfill, "backfill"}, {JobFull, "full"}, {JobKind(7), "JobKind(7)"}} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("%d.String() = %q, want %q", int(tc.k), got, tc.want)
		}
	}
	if err := newHarness(t, "A").q.Enqueue("A", JobKind(7)); err == nil {
		t.Error("Enqueue accepted an unknown kind")
	}
}

func TestOutcome_FailedWithoutMessageIsStillAnError(t *testing.T) {
	h := newHarness(t, "A")
	h.run.script("A", returns(Failed, ""))
	h.start()
	h.enqueue("A", JobFull)
	h.waitIdle()
	if f := h.folder("A"); f.JobPending || f.JobError != failedMessage {
		t.Errorf("A: pending %v, error %q; want not pending, %q", f.JobPending, f.JobError, failedMessage)
	}
	if got := lastOf(h.states("A")); got != "error: "+failedMessage {
		t.Errorf("last scan:state of A = %q, want error", got)
	}
}
