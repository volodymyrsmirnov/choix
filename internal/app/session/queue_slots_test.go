package session

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// newSlotsHarness is newHarness with a queue running up to slots jobs at
// once.
func newSlotsHarness(t *testing.T, slots int, ids ...string) *harness {
	t.Helper()
	h := newHarness(t, ids...)
	h.slots = slots
	h.q = h.newQueue()
	return h
}

// waitStartedSet waits for the runner's next len(ids) calls, in any order,
// and checks they are for ids: jobs started together begin on their own
// goroutines.
func (h *harness) waitStartedSet(ids ...string) {
	h.t.Helper()
	h.waitSet("started", h.run.started, ids)
}

// waitFinishedSet is waitStartedSet for returns; it returns the calls.
func (h *harness) waitFinishedSet(ids ...string) map[string]runCall {
	h.t.Helper()
	return h.waitSet("finished", h.run.finished, ids)
}

func (h *harness) waitSet(what string, ch <-chan runCall, ids []string) map[string]runCall {
	h.t.Helper()
	got := map[string]runCall{}
	for range ids {
		select {
		case c := <-ch:
			if !slices.Contains(ids, c.ID) {
				h.t.Fatalf("runner %s %s, want one of %v", what, c.ID, ids)
			}
			if _, dup := got[c.ID]; dup {
				h.t.Fatalf("runner %s %s twice, want each of %v once", what, c.ID, ids)
			}
			got[c.ID] = c
		case <-time.After(waitTimeout):
			h.t.Fatalf("runner %s %d of %v only", what, len(got), ids)
		}
	}
	return got
}

// noStart fails when the runner starts a call within a short while.
func (h *harness) noStart(why string) {
	h.t.Helper()
	select {
	case c := <-h.run.started:
		h.t.Fatalf("runner started %s; %s", c.ID, why)
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *harness) peakOf(id string) int {
	h.run.mu.Lock()
	defer h.run.mu.Unlock()
	return h.run.peakOf[id]
}

func TestSlots_ZeroMeansOne(t *testing.T) {
	h := newSlotsHarness(t, 0, "A", "B")
	gate := make(chan struct{})
	h.run.script("A", hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.waitStarted("A")
	h.noStart("one slot is busy")
	close(gate)
	h.waitIdle()
	if got := h.run.peak(); got != 1 {
		t.Errorf("%d jobs ran at once, want 1", got)
	}
}

func TestSlots_TwoFoldersRunAtOnce(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B", "C")
	gates := map[string]chan struct{}{}
	for _, id := range []string{"A", "B", "C"} {
		gates[id] = make(chan struct{})
		h.run.script(id, hold(gates[id]))
	}
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.enqueue("C", JobFull)

	h.waitStartedSet("A", "B")
	for _, id := range []string{"A", "B"} {
		if got, _ := h.q.State(id); got != "scanning" {
			t.Errorf("State(%s) = %q, want scanning", id, got)
		}
	}
	if got, _ := h.q.State("C"); got != "queued" {
		t.Errorf("State(C) = %q, want queued", got)
	}
	h.noStart("both slots are busy")
	if h.q.Idle() {
		t.Error("Idle with two jobs running")
	}

	// The second folder finishes first: its slot goes to C at once, while A
	// keeps running.
	close(gates["B"])
	h.waitFinished("B")
	h.waitStarted("C")
	if err := h.run.ctxOf(t, "A", 0).Err(); err != nil {
		t.Errorf("A was cancelled when B finished: %v", err)
	}
	close(gates["A"])
	close(gates["C"])
	h.waitIdle()

	if got := h.run.peak(); got != 2 {
		t.Errorf("%d jobs ran at once, want 2", got)
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

func TestSlots_OpenFolderTakesAFreeSlotWithoutPreempting(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B", "C")
	gateA, gateB := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(gateA))
	h.run.script("B", hold(gateB))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("C", JobFull) // takes the second slot
	h.waitStarted("C")
	h.waitFinished("C")
	h.enqueueFront("B", JobFull)
	h.waitStarted("B")
	if err := h.run.ctxOf(t, "A", 0).Err(); err != nil {
		t.Fatalf("A was preempted although a slot was free: %v", err)
	}
	close(gateA)
	close(gateB)
	h.waitIdle()
	if got := len(h.run.callsOf("A")); got != 1 {
		t.Errorf("A ran %d times, want 1", got)
	}
}

func TestSlots_OpenFolderPreemptsTheLatestTailJob(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B", "C", "D")
	gates := map[string]chan struct{}{}
	for _, id := range []string{"A", "B", "C", "D"} {
		gates[id] = make(chan struct{})
		h.run.script(id, hold(gates[id]))
	}
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)
	h.waitStarted("B")
	seqB := h.pendingSeq("B")
	h.enqueue("C", JobFull)

	h.enqueueFront("D", JobFull)
	// The cancellation happens inside EnqueueFront: well within 1 s.
	if err := h.run.ctxOf(t, "B", 0).Err(); err == nil {
		t.Fatal("B, the job started last, is not cancelled when EnqueueFront returns")
	}
	if err := h.run.ctxOf(t, "A", 0).Err(); err != nil {
		t.Fatalf("A was preempted too: %v", err)
	}
	if c := h.waitFinished("B"); c.Outcome != Stopped {
		t.Fatalf("B's outcome %v, want Stopped", c.Outcome)
	}
	h.waitStarted("D")
	if got, _ := h.q.State("B"); got != "queued" {
		t.Errorf("State(preempted B) = %q, want queued", got)
	}
	if got := h.pendingSeq("B"); got != seqB {
		t.Errorf("preempted B's sequence %d, want %d (kept)", got, seqB)
	}
	// B went back directly behind D, ahead of C: it gets the next slot.
	close(gates["A"])
	h.waitFinished("A")
	h.waitStarted("B")
	h.waitFinished("B") // its second run has no script: Done at once
	close(gates["D"])
	h.waitFinished("D")
	h.waitStarted("C")
	close(gates["C"])
	h.waitIdle()
	close(gates["B"])

	if got, want := h.run.callIDs(), []string{"A", "B", "D", "B", "C"}; !slices.Equal(got, want) {
		t.Errorf("run order %v, want %v", got, want)
	}
	if got := h.run.peak(); got > 2 {
		t.Errorf("%d jobs ran at once, want at most 2", got)
	}
	for _, id := range []string{"A", "B", "C", "D"} {
		if got := lastOf(h.states(id)); got != "ready" {
			t.Errorf("last scan:state of %s = %q, want ready", id, got)
		}
	}
}

func TestSlots_OpenFolderPreemptsATailJobBeforeAnEarlierOpen(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "O1", "O2")
	gates := map[string]chan struct{}{}
	for _, id := range []string{"A", "O1", "O2"} {
		gates[id] = make(chan struct{})
		h.run.script(id, hold(gates[id]), hold(gates[id]))
	}
	h.start()
	h.enqueueFront("O1", JobFull)
	h.waitStarted("O1")
	h.enqueue("A", JobFull) // started after O1, but from the tail
	h.waitStarted("A")

	h.enqueueFront("O2", JobFull)
	if err := h.run.ctxOf(t, "A", 0).Err(); err == nil {
		t.Fatal("the tail job A is not preempted")
	}
	if err := h.run.ctxOf(t, "O1", 0).Err(); err != nil {
		t.Fatalf("O1, opened earlier, was preempted instead of the tail job: %v", err)
	}
	h.waitFinished("A")
	h.waitStarted("O2")
	for _, g := range gates {
		close(g)
	}
	h.waitIdle()
}

func TestSlots_StoppingJobKeepsItsSlot(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B", "C", "D")
	gateA := make(chan struct{})
	stopping, release := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(gateA))
	h.run.script("B", slowStop(stopping, release))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")
	h.enqueue("B", JobFull)
	h.waitStarted("B")

	h.enqueueFront("C", JobFull)
	<-stopping
	h.noStart("B is still winding down in its slot")
	if got := h.run.peak(); got > 2 {
		t.Fatalf("%d jobs ran at once, want at most 2", got)
	}
	// A second open while B winds down preempts nothing more: B's slot is
	// about to be free, and the new open goes first. B goes back directly
	// behind the latest open, as with one slot.
	h.enqueueFront("D", JobFull)
	if err := h.run.ctxOf(t, "A", 0).Err(); err != nil {
		t.Fatalf("A was preempted although B's slot is about to be free: %v", err)
	}
	close(release)
	h.waitFinished("B")
	h.waitStarted("D")
	h.waitFinished("D")
	h.waitStarted("B")
	h.waitFinished("B")
	h.waitStarted("C")
	h.waitFinished("C")
	close(gateA)
	h.waitIdle()
	if got, want := h.run.callIDs(), []string{"A", "B", "D", "B", "C"}; !slices.Equal(got, want) {
		t.Errorf("run order %v, want %v", got, want)
	}
	if got := h.run.peak(); got > 2 {
		t.Errorf("%d jobs ran at once, want at most 2", got)
	}
}

func TestSlots_SameFolderNeverRunsTwiceAtOnce(t *testing.T) {
	h := newSlotsHarness(t, 2, "A")
	stopping, release := make(chan struct{}), make(chan struct{})
	gate := make(chan struct{})
	h.run.script("A", slowStop(stopping, release), hold(gate))
	h.start()
	h.enqueue("A", JobFull)
	h.waitStarted("A")

	// Every way of asking for A again while its run winds down, with a
	// slot free.
	h.q.Suspend("A")
	<-stopping
	h.q.Resume("A")
	h.enqueue("A", JobBackfill)
	h.enqueueFront("A", JobFull)
	h.noStart("A's first run has not returned")
	close(release)
	h.waitFinished("A")
	h.waitStarted("A")
	h.enqueue("A", JobFull)
	h.enqueueFront("A", JobFull)
	close(gate)
	h.waitIdle()

	if got := h.peakOf("A"); got != 1 {
		t.Errorf("A ran %d times at once, want 1", got)
	}
	if f := h.folder("A"); f.JobPending {
		t.Error("A still pending")
	}
}

func TestSlots_InterleavedCompletionsAcknowledgeTheirOwnSequence(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B")
	gateA, gateB := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(gateA))
	h.run.script("B", hold(gateB))
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.waitStartedSet("A", "B")
	seqB := h.pendingSeq("B")

	// A newer enqueue of B arrives while both run; A finishes first, then B.
	h.enqueue("B", JobBackfill)
	newerB := h.pendingSeq("B")
	if newerB == seqB {
		t.Fatalf("the newer enqueue of B kept sequence %d", seqB)
	}
	close(gateA)
	h.waitFinished("A")
	// The runner reports finished just before it returns: wait for the run
	// to be accounted for.
	waitFor(t, "A's run acknowledged", func() bool { return !h.folder("A").JobPending })
	if got := h.pendingSeq("B"); got != newerB {
		t.Errorf("B's sequence after A finished = %d, want %d", got, newerB)
	}
	close(gateB)
	h.waitFinished("B")
	// B's run acknowledged only its own sequence: B runs once more.
	if c := h.waitStarted("B"); c.Kind != JobFull {
		t.Errorf("B's second run kind %v, want JobFull (the pending full job is never downgraded)", c.Kind)
	}
	h.waitIdle()
	if got := len(h.run.callsOf("B")); got != 2 {
		t.Errorf("B ran %d times, want 2", got)
	}
	if got := h.pending(); len(got) != 0 {
		t.Errorf("pending = %+v, want none", got)
	}
}

func TestSlots_SuspendAndCancelTouchOnlyTheirFolder(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B", "C")
	gates := map[string]chan struct{}{}
	for _, id := range []string{"A", "B", "C"} {
		gates[id] = make(chan struct{})
		h.run.script(id, hold(gates[id]), hold(gates[id]))
	}
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	h.enqueue("C", JobFull)
	h.waitStartedSet("A", "B")

	h.q.Suspend("A")
	if c := h.waitFinished("A"); c.Outcome != Stopped {
		t.Fatalf("A's outcome %v, want Stopped", c.Outcome)
	}
	h.waitStarted("C")
	if err := h.run.ctxOf(t, "B", 0).Err(); err != nil {
		t.Fatalf("suspending A cancelled B: %v", err)
	}
	if f := h.folder("A"); !f.JobPending {
		t.Error("suspended A is not pending")
	}

	h.q.Cancel("C")
	if c := h.waitFinished("C"); c.Outcome != Stopped {
		t.Fatalf("C's outcome %v, want Stopped", c.Outcome)
	}
	if f := h.folder("C"); f.JobPending {
		t.Error("cancelled C is still pending")
	}
	if err := h.run.ctxOf(t, "B", 0).Err(); err != nil {
		t.Fatalf("cancelling C cancelled B: %v", err)
	}

	// A is back: it takes the free slot next to B.
	h.q.Resume("A")
	h.waitStarted("A")
	if got, _ := h.q.State("B"); got != "scanning" {
		t.Errorf("State(B) = %q, want scanning", got)
	}
	for _, g := range gates {
		close(g)
	}
	h.waitIdle()
	for _, id := range []string{"A", "B"} {
		if got := lastOf(h.states(id)); got != "ready" {
			t.Errorf("last scan:state of %s = %q, want ready", id, got)
		}
	}
	if got, want := h.states("C"), []string{"queued", "scanning"}; !slices.Equal(got, want) {
		t.Errorf("scan:state of cancelled C = %v, want %v", got, want)
	}
}

func TestSlots_StopJoinsEveryRunningJob(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B", "C")
	gate := make(chan struct{})
	defer close(gate)
	stoppingB, releaseB := make(chan struct{}), make(chan struct{})
	h.run.script("A", hold(gate))
	h.run.script("B", slowStop(stoppingB, releaseB))
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobBackfill)
	h.enqueue("C", JobFull)
	h.waitStartedSet("A", "B")

	stopped := make(chan struct{})
	start := time.Now()
	go func() {
		h.q.Stop()
		close(stopped)
	}()
	<-stoppingB
	select {
	case <-stopped:
		t.Fatal("Stop returned before B returned")
	default:
	}
	close(releaseB)
	select {
	case <-stopped:
	case <-time.After(waitTimeout):
		t.Fatal("Stop never returned")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Stop took %v with two jobs running, want well under 1 s", took)
	}
	got := h.waitFinishedSet("A", "B")
	for id, c := range got {
		if c.Outcome != Stopped {
			t.Errorf("%s's outcome %v, want Stopped", id, c.Outcome)
		}
	}
	for _, id := range []string{"A", "B", "C"} {
		if f := h.folder(id); !f.JobPending {
			t.Errorf("%s not pending after Stop", id)
		}
		if got := lastOf(h.states(id)); got == "ready" || got == "error" {
			t.Errorf("last scan:state of %s = %q; Stop must not mark it", id, got)
		}
	}
	if got := h.run.callsOf("C"); len(got) != 0 {
		t.Errorf("C ran: %+v", got)
	}
}

func TestSlots_ProgressAndStatePerFolder(t *testing.T) {
	h := newSlotsHarness(t, 2, "A", "B")
	reported := make(chan string, 2)
	gate := make(chan struct{})
	both := func(n int) script {
		return func(ctx context.Context, _ JobKind, report func(events.Progress, string)) (Outcome, string) {
			report(events.Progress{Stage: "metadata", Done: n, Total: 10, State: "running"}, phaseRunning)
			reported <- "x"
			select {
			case <-gate:
				return Done, ""
			case <-ctx.Done():
				return Stopped, ""
			}
		}
	}
	h.run.script("A", both(3))
	h.run.script("B", both(7))
	h.start()
	h.enqueue("A", JobFull)
	h.enqueue("B", JobFull)
	for range 2 {
		select {
		case <-reported:
		case <-time.After(waitTimeout):
			t.Fatal("both jobs never reported progress at once")
		}
	}
	for id, want := range map[string]int{"A": 3, "B": 7} {
		state, p := h.q.State(id)
		if state != "scanning" || p == nil || p.Done != want {
			t.Errorf("State(%s) = %q, %+v; want scanning with done %d", id, state, p, want)
		}
	}
	close(gate)
	h.waitIdle()
	h.co.Flush()
	for id, want := range map[string]int{"A": 3, "B": 7} {
		var last events.ScanProgressEvent
		for _, ev := range h.progress(id) {
			if ev.FolderID != id {
				t.Fatalf("progress of %s carries folder %s", id, ev.FolderID)
			}
			last = ev
		}
		if last.Phase != phaseDone || last.Progress.Done != want {
			t.Errorf("last scan:progress of %s = %+v, want phase done with done %d", id, last, want)
		}
	}
}
