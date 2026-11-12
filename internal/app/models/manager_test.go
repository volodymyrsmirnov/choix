package models

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// waitLimit bounds every wait in these tests. It is generous on purpose:
// the tests synchronise on state, never on elapsed time, so a slow machine
// only makes them slower.
const waitLimit = 10 * time.Second

// call is one Download invocation of the fake. The test decides how it
// ends: finish delivers a result, or the manager cancels its context.
type call struct {
	kind     local.ModelKind
	ctx      context.Context
	progress func(received, total int64)
	result   chan error
}

func (c *call) finish(err error) { c.result <- err }

// fakeDownloader is a Downloader without network: every Download blocks
// until the test finishes it or its context is cancelled.
type fakeDownloader struct {
	mu    sync.Mutex
	has   map[local.ModelKind]bool
	count map[local.ModelKind]int
	calls chan *call
	// stubborn makes Download ignore cancellation and wait for finish, like
	// a download whose last byte lands just as it is cancelled.
	stubborn bool
	// hold, when set, keeps a successful Download from returning after the
	// file is in place until the channel is closed.
	hold chan struct{}
}

func newFake(present ...local.ModelKind) *fakeDownloader {
	f := &fakeDownloader{
		has:   map[local.ModelKind]bool{},
		count: map[local.ModelKind]int{},
		calls: make(chan *call, 64),
	}
	for _, k := range present {
		f.has[k] = true
	}
	return f
}

func (f *fakeDownloader) Has(kind local.ModelKind) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.has[kind]
}

func (f *fakeDownloader) Download(ctx context.Context, kind local.ModelKind, onProgress func(received, total int64)) error {
	f.mu.Lock()
	f.count[kind]++
	present, stubborn, hold := f.has[kind], f.stubborn, f.hold
	f.mu.Unlock()
	if present { // like local.Installer: an installed model is not fetched
		return nil
	}
	c := &call{kind: kind, ctx: ctx, progress: onProgress, result: make(chan error, 1)}
	f.calls <- c
	done := ctx.Done()
	if stubborn {
		done = nil
	}
	select {
	case err := <-c.result:
		if err == nil {
			f.install(kind)
			if hold != nil {
				<-hold
			}
		}
		return err
	case <-done:
		return ctx.Err()
	}
}

func (f *fakeDownloader) install(kind local.ModelKind) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.has[kind] = true
}

func (f *fakeDownloader) callCount(kind local.ModelKind) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count[kind]
}

// next returns the next Download call and checks its kind.
func (f *fakeDownloader) next(t *testing.T, want local.ModelKind) *call {
	t.Helper()
	select {
	case c := <-f.calls:
		if c.kind != want {
			t.Fatalf("Download(%s) called, want Download(%s)", c.kind, want)
		}
		return c
	case <-time.After(waitLimit):
		t.Fatalf("Download(%s) was not called", want)
		return nil
	}
}

// switches is a mutable Enabled function.
type switches struct {
	mu sync.Mutex
	on map[local.ModelKind]bool
}

func newSwitches(clip, quality bool) *switches {
	return &switches{on: map[local.ModelKind]bool{local.ModelCLIP: clip, local.ModelQuality: quality}}
}

func (s *switches) enabled(kind local.ModelKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on[kind]
}

func (s *switches) set(kind local.ModelKind, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on[kind] = on
}

type harness struct {
	m   *Manager
	dl  *fakeDownloader
	sw  *switches
	rec *events.Recorder
	co  *events.Coalescer

	mu         sync.Mutex
	downloaded []local.ModelKind
}

func (h *harness) onDownloaded() []local.ModelKind {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]local.ModelKind(nil), h.downloaded...)
}

// newHarness builds a Manager over a fake downloader. The Coalescer is never
// Run: coalesced values reach the Recorder only on an explicit Flush.
func newHarness(t *testing.T, dl *fakeDownloader, sw *switches, backoff []time.Duration) *harness {
	t.Helper()
	h := &harness{dl: dl, sw: sw, rec: &events.Recorder{}}
	h.co = events.NewCoalescer(h.rec, time.Hour)
	h.m = New(Options{
		Downloader: dl,
		Enabled:    sw.enabled,
		Emit:       h.co,
		Backoff:    backoff,
		OnDownloaded: func(kind local.ModelKind) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.downloaded = append(h.downloaded, kind)
		},
	})
	t.Cleanup(h.m.Stop)
	return h
}

func status(m *Manager, kind local.ModelKind) events.ModelStatus {
	for _, s := range m.Status() {
		if s.Kind == kind.String() {
			return s
		}
	}
	return events.ModelStatus{}
}

// waitState polls until kind reaches state.
func waitState(t *testing.T, m *Manager, kind local.ModelKind, state string) events.ModelStatus {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		s := status(m, kind)
		if s.State == state {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: state %q, want %q", kind, s.State, state)
		}
		time.Sleep(time.Millisecond)
	}
}

// recorded returns the models:progress payloads recorded for kind.
func recorded(t *testing.T, rec *events.Recorder, kind local.ModelKind) []events.ModelStatus {
	t.Helper()
	var out []events.ModelStatus
	for _, v := range rec.Events(events.ModelsProgress) {
		s, ok := v.(events.ModelStatus)
		if !ok {
			t.Fatalf("models:progress payload is %T, want events.ModelStatus", v)
		}
		if s.Kind == kind.String() {
			out = append(out, s)
		}
	}
	return out
}

func states(ss []events.ModelStatus) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.State
	}
	return out
}

func TestStart_Sequential_ClipFirst(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, true), nil)
	h.m.Start(context.Background())

	clip := h.dl.next(t, local.ModelCLIP)
	waitState(t, h.m, local.ModelCLIP, stateDownloading)
	if s := status(h.m, local.ModelQuality); s.State != stateQueued {
		t.Errorf("quality state while CLIP downloads = %q, want %q", s.State, stateQueued)
	}
	if n := h.dl.callCount(local.ModelQuality); n != 0 {
		t.Errorf("quality Download called %d times while CLIP downloads, want 0", n)
	}

	clip.finish(nil)
	quality := h.dl.next(t, local.ModelQuality)
	if s := status(h.m, local.ModelCLIP); s.State != stateReady {
		t.Errorf("CLIP state when quality starts = %q, want %q", s.State, stateReady)
	}
	quality.finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
	if !h.m.Ready(local.ModelCLIP) || !h.m.Ready(local.ModelQuality) {
		t.Errorf("Ready = %v/%v, want true/true", h.m.Ready(local.ModelCLIP), h.m.Ready(local.ModelQuality))
	}
}

func TestStatus_OrderAndInitialSize(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, true), nil)

	want := []events.ModelStatus{
		{Kind: "clip", State: stateQueued, Size: local.KnownModels[local.ModelCLIP].Size},
		{Kind: "quality", State: stateQueued, Size: local.KnownModels[local.ModelQuality].Size},
	}
	if got := h.m.Status(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Status before Start = %+v, want %+v", got, want)
	}

	h.m.Start(context.Background())
	clip := h.dl.next(t, local.ModelCLIP)

	// A progress report without a total keeps the registry size.
	clip.progress(10, 0)
	if s := status(h.m, local.ModelCLIP); s.Received != 10 || s.Size != local.KnownModels[local.ModelCLIP].Size {
		t.Errorf("after progress(10, 0): %+v, want received 10 and the registry size", s)
	}
	// The server's total replaces it.
	clip.progress(20, 1234)
	if s := status(h.m, local.ModelCLIP); s.Received != 20 || s.Size != 1234 {
		t.Errorf("after progress(20, 1234): %+v, want received 20, size 1234", s)
	}

	got := h.m.Status()
	if len(got) != 2 || got[0].Kind != "clip" || got[1].Kind != "quality" {
		t.Errorf("Status order = %+v, want [clip, quality]", got)
	}
}

func TestDisabledModelIsNeverDownloaded(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(false, false), nil)
	h.m.Start(context.Background())
	h.m.Retry(local.ModelCLIP) // a disabled model is not failed: no-op
	h.m.Refresh()              // still off
	h.m.Stop()

	for _, k := range []local.ModelKind{local.ModelCLIP, local.ModelQuality} {
		if n := h.dl.callCount(k); n != 0 {
			t.Errorf("Download(%s) called %d times, want 0", k, n)
		}
		if s := status(h.m, k); s.State != stateDisabled {
			t.Errorf("%s state = %q, want %q", k, s.State, stateDisabled)
		}
		if h.m.Ready(k) {
			t.Errorf("Ready(%s) = true for a disabled model", k)
		}
	}
}

func TestDisabledModelOnDisk_IsDisabledNotReady(t *testing.T) {
	h := newHarness(t, newFake(local.ModelCLIP), newSwitches(false, true), nil)
	h.m.Start(context.Background())
	if s := status(h.m, local.ModelCLIP); s.State != stateDisabled {
		t.Errorf("CLIP state = %q, want %q (setting off wins over a file on disk)", s.State, stateDisabled)
	}
	if h.m.Ready(local.ModelCLIP) {
		t.Error("Ready(clip) = true while its setting is off")
	}
}

func TestClipDisabled_QualityDownloadsAlone(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(false, true), nil)
	h.m.Start(context.Background())

	h.dl.next(t, local.ModelQuality).finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
	h.m.Stop()

	if n := h.dl.callCount(local.ModelCLIP); n != 0 {
		t.Errorf("Download(clip) called %d times, want 0", n)
	}
	if s := status(h.m, local.ModelCLIP); s.State != stateDisabled {
		t.Errorf("CLIP state = %q, want %q", s.State, stateDisabled)
	}
}

func TestFailure_RetriesTwiceThenFailed(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, true), []time.Duration{time.Millisecond, time.Millisecond})
	h.m.Start(context.Background())

	for i := 1; i <= 3; i++ {
		h.dl.next(t, local.ModelCLIP).finish(errors.New("boom " + string(rune('0'+i))))
	}
	// The other model gets its turn after the third failure.
	quality := h.dl.next(t, local.ModelQuality)

	s := status(h.m, local.ModelCLIP)
	if s.State != stateFailed {
		t.Fatalf("CLIP state = %q, want %q", s.State, stateFailed)
	}
	if !strings.Contains(s.Error, "boom 3") {
		t.Errorf("CLIP error = %q, want the last attempt's error", s.Error)
	}
	if n := h.dl.callCount(local.ModelCLIP); n != 3 {
		t.Errorf("Download(clip) called %d times, want 3", n)
	}
	if h.m.Ready(local.ModelCLIP) {
		t.Error("Ready(clip) = true after failure")
	}

	quality.finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
	h.m.Stop()
	if n := h.dl.callCount(local.ModelCLIP); n != 3 {
		t.Errorf("Download(clip) called %d times after failure, want 3 (no attempt without Retry)", n)
	}

	// Between attempts the model is reported queued (waiting for its pause).
	var seq []string
	for _, st := range states(recorded(t, h.rec, local.ModelCLIP)) {
		if st != stateDownloading { // coalesced, never flushed here
			seq = append(seq, st)
		}
	}
	if want := []string{stateQueued, stateQueued, stateQueued, stateFailed}; !reflect.DeepEqual(seq, want) {
		t.Errorf("CLIP immediate events = %v, want %v", seq, want)
	}
}

func TestBackoff_DefaultPausesAreTwoAndTenSeconds(t *testing.T) {
	var (
		mu     sync.Mutex
		pauses []time.Duration
	)
	dl := newFake()
	m := New(Options{
		Downloader: dl,
		Enabled:    newSwitches(true, false).enabled,
		Sleep: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			pauses = append(pauses, d)
			return ctx.Err()
		},
	})
	t.Cleanup(m.Stop)
	m.Start(context.Background())
	for range 3 {
		dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
	}
	waitState(t, m, local.ModelCLIP, stateFailed)

	mu.Lock()
	defer mu.Unlock()
	if want := []time.Duration{2 * time.Second, 10 * time.Second}; !reflect.DeepEqual(pauses, want) {
		t.Errorf("pauses = %v, want %v", pauses, want)
	}
}

func TestRetry_AfterFailure(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, false), []time.Duration{time.Millisecond, time.Millisecond})
	h.m.Start(context.Background())
	for range 3 {
		h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
	}
	waitState(t, h.m, local.ModelCLIP, stateFailed)

	h.m.Retry(local.ModelCLIP)
	h.dl.next(t, local.ModelCLIP).finish(nil)
	s := waitState(t, h.m, local.ModelCLIP, stateReady)
	if s.Error != "" {
		t.Errorf("error after a successful retry = %q, want empty", s.Error)
	}
	if !h.m.Ready(local.ModelCLIP) {
		t.Error("Ready(clip) = false after a successful retry")
	}
}

func TestRefresh_OffCancelsInFlight(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, true), nil)
	h.m.Start(context.Background())
	clip := h.dl.next(t, local.ModelCLIP)
	clip.progress(5, 0)

	h.sw.set(local.ModelCLIP, false)
	h.m.Refresh()

	select {
	case <-clip.ctx.Done():
	case <-time.After(waitLimit):
		t.Fatal("the in-flight CLIP download's context was not cancelled")
	}
	if s := status(h.m, local.ModelCLIP); s.State != stateDisabled {
		t.Errorf("CLIP state after Refresh = %q, want %q", s.State, stateDisabled)
	}

	// The next model starts, and the cancelled download leaves CLIP disabled.
	quality := h.dl.next(t, local.ModelQuality)
	if s := status(h.m, local.ModelCLIP); s.State != stateDisabled {
		t.Errorf("CLIP state after its download returned = %q, want %q", s.State, stateDisabled)
	}
	quality.finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
	h.m.Stop() // joins the worker, so its callback has returned
	if got := h.onDownloaded(); !reflect.DeepEqual(got, []local.ModelKind{local.ModelQuality}) {
		t.Errorf("OnDownloaded calls = %v, want [quality]", got)
	}
}

func TestRefresh_OffDuringRetryPause(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, false), []time.Duration{time.Hour, time.Hour})
	h.m.Start(context.Background())
	h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
	waitState(t, h.m, local.ModelCLIP, stateQueued) // pausing an hour

	h.sw.set(local.ModelCLIP, false)
	h.m.Refresh()
	if s := status(h.m, local.ModelCLIP); s.State != stateDisabled {
		t.Errorf("CLIP state = %q, want %q", s.State, stateDisabled)
	}
	h.m.Stop() // joins: the pause ended with the cancellation
	if n := h.dl.callCount(local.ModelCLIP); n != 1 {
		t.Errorf("Download(clip) called %d times, want 1", n)
	}
}

func TestRefresh_OnQueues(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, false), nil)
	h.m.Start(context.Background())
	clip := h.dl.next(t, local.ModelCLIP) // keeps the worker busy

	if s := status(h.m, local.ModelQuality); s.State != stateDisabled {
		t.Fatalf("quality state = %q, want %q", s.State, stateDisabled)
	}
	h.sw.set(local.ModelQuality, true)
	h.m.Refresh()
	if s := status(h.m, local.ModelQuality); s.State != stateQueued {
		t.Errorf("quality state after Refresh = %q, want %q", s.State, stateQueued)
	}

	clip.finish(nil)
	h.dl.next(t, local.ModelQuality).finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
}

func TestRefresh_OnWithFileOnDisk_IsReady(t *testing.T) {
	h := newHarness(t, newFake(local.ModelQuality), newSwitches(false, false), nil)
	h.m.Start(context.Background())
	h.sw.set(local.ModelQuality, true)
	h.m.Refresh()
	if s := status(h.m, local.ModelQuality); s.State != stateReady {
		t.Errorf("quality state = %q, want %q", s.State, stateReady)
	}
	h.m.Stop()
	if n := h.dl.callCount(local.ModelQuality); n != 0 {
		t.Errorf("Download(quality) called %d times, want 0", n)
	}
	if got := h.onDownloaded(); len(got) != 0 {
		t.Errorf("OnDownloaded calls = %v, want none", got)
	}
}

func TestOnDownloaded_OnlyForCompletedDownloads(t *testing.T) {
	h := newHarness(t, newFake(local.ModelCLIP), newSwitches(true, true), nil)
	h.m.Start(context.Background())
	if s := status(h.m, local.ModelCLIP); s.State != stateReady {
		t.Errorf("CLIP state at Start = %q, want %q", s.State, stateReady)
	}

	h.dl.next(t, local.ModelQuality).finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
	h.m.Stop() // joins the worker, so its callback has returned

	if got := h.onDownloaded(); !reflect.DeepEqual(got, []local.ModelKind{local.ModelQuality}) {
		t.Errorf("OnDownloaded calls = %v, want [quality]", got)
	}
}

func TestOnDownloaded_NotForFailure(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, false), []time.Duration{})
	h.m.Start(context.Background())
	h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
	waitState(t, h.m, local.ModelCLIP, stateFailed)
	h.m.Stop()
	if got := h.onDownloaded(); len(got) != 0 {
		t.Errorf("OnDownloaded calls = %v, want none", got)
	}
}

// TestOnDownloaded_CompletionAcrossToggle covers a download whose last byte
// lands as its setting is switched off: OnDownloaded fires exactly once, when
// the download returns, whatever the setting is then. In each case quality's
// turn starting proves CLIP's turn — including its callback — has ended.
func TestOnDownloaded_CompletionAcrossToggle(t *testing.T) {
	clipOnly := []local.ModelKind{local.ModelCLIP}

	t.Run("finished while off", func(t *testing.T) {
		dl := newFake()
		dl.stubborn = true
		h := newHarness(t, dl, newSwitches(true, true), nil)
		h.m.Start(context.Background())
		clip := h.dl.next(t, local.ModelCLIP)

		h.sw.set(local.ModelCLIP, false)
		h.m.Refresh()
		clip.finish(nil) // completes despite the cancellation
		quality := h.dl.next(t, local.ModelQuality)

		if got := h.onDownloaded(); !reflect.DeepEqual(got, clipOnly) {
			t.Errorf("OnDownloaded calls = %v, want [clip]", got)
		}
		if s := status(h.m, local.ModelCLIP); s.State != stateDisabled {
			t.Errorf("CLIP state = %q, want %q", s.State, stateDisabled)
		}
		if h.m.Ready(local.ModelCLIP) {
			t.Error("Ready(clip) = true while its setting is off")
		}

		// Switching on finds the file; no second call.
		h.sw.set(local.ModelCLIP, true)
		h.m.Refresh()
		if s := status(h.m, local.ModelCLIP); s.State != stateReady {
			t.Errorf("CLIP state after switching on = %q, want %q", s.State, stateReady)
		}
		if got := h.onDownloaded(); !reflect.DeepEqual(got, clipOnly) {
			t.Errorf("OnDownloaded calls after switching on = %v, want [clip]", got)
		}
		quality.finish(nil)
	})

	t.Run("switched back on after the file landed", func(t *testing.T) {
		dl := newFake()
		dl.stubborn = true
		dl.hold = make(chan struct{})
		h := newHarness(t, dl, newSwitches(true, true), nil)
		h.m.Start(context.Background())
		clip := h.dl.next(t, local.ModelCLIP)

		h.sw.set(local.ModelCLIP, false)
		h.m.Refresh()
		clip.finish(nil)
		// The file is in place, but Download has not returned yet.
		deadline := time.Now().Add(waitLimit)
		for !dl.Has(local.ModelCLIP) {
			if time.Now().After(deadline) {
				t.Fatal("the fake never installed CLIP")
			}
			time.Sleep(time.Millisecond)
		}
		h.sw.set(local.ModelCLIP, true)
		h.m.Refresh()
		if s := status(h.m, local.ModelCLIP); s.State != stateReady {
			t.Errorf("CLIP state after switching on = %q, want %q", s.State, stateReady)
		}
		if got := h.onDownloaded(); len(got) != 0 {
			t.Errorf("OnDownloaded calls before the download returned = %v, want none", got)
		}

		close(dl.hold)
		quality := h.dl.next(t, local.ModelQuality)
		if got := h.onDownloaded(); !reflect.DeepEqual(got, clipOnly) {
			t.Errorf("OnDownloaded calls = %v, want [clip]", got)
		}
		if s := status(h.m, local.ModelCLIP); s.State != stateReady {
			t.Errorf("CLIP state = %q, want %q", s.State, stateReady)
		}
		quality.finish(nil)
	})

	t.Run("switched back on before the file landed", func(t *testing.T) {
		dl := newFake()
		dl.stubborn = true
		h := newHarness(t, dl, newSwitches(true, true), nil)
		h.m.Start(context.Background())
		clip := h.dl.next(t, local.ModelCLIP)

		h.sw.set(local.ModelCLIP, false)
		h.m.Refresh()
		h.sw.set(local.ModelCLIP, true)
		h.m.Refresh()
		if s := status(h.m, local.ModelCLIP); s.State != stateQueued {
			t.Errorf("CLIP state after switching back on = %q, want %q", s.State, stateQueued)
		}

		clip.finish(nil)
		quality := h.dl.next(t, local.ModelQuality)
		if got := h.onDownloaded(); !reflect.DeepEqual(got, clipOnly) {
			t.Errorf("OnDownloaded calls = %v, want [clip]", got)
		}
		if s := status(h.m, local.ModelCLIP); s.State != stateReady {
			t.Errorf("CLIP state = %q, want %q", s.State, stateReady)
		}
		if n := h.dl.callCount(local.ModelCLIP); n != 1 {
			t.Errorf("Download(clip) called %d times, want 1", n)
		}
		quality.finish(nil)
	})
}

// TestFailure_ExhaustedSurvivesRestartAndToggle: once every attempt has
// failed, only Retry (or the file appearing) starts another one.
func TestFailure_ExhaustedSurvivesRestartAndToggle(t *testing.T) {
	backoff := []time.Duration{time.Millisecond, time.Millisecond}

	t.Run("Stop and Start", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(true, false), backoff)
		h.m.Start(context.Background())
		for range 3 {
			h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
		}
		waitState(t, h.m, local.ModelCLIP, stateFailed)

		h.m.Stop()
		h.m.Start(context.Background())
		s := status(h.m, local.ModelCLIP)
		if s.State != stateFailed || s.Error != "boom" {
			t.Errorf("CLIP after restart = %+v, want failed with %q", s, "boom")
		}
		h.m.Stop() // joins: anything the worker would do has happened
		if n := h.dl.callCount(local.ModelCLIP); n != 3 {
			t.Errorf("Download(clip) called %d times, want 3 (no fourth attempt)", n)
		}

		h.m.Start(context.Background())
		h.m.Retry(local.ModelCLIP)
		h.dl.next(t, local.ModelCLIP).finish(nil)
		waitState(t, h.m, local.ModelCLIP, stateReady)
	})

	t.Run("setting toggled", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(true, false), backoff)
		h.m.Start(context.Background())
		for range 3 {
			h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
		}
		waitState(t, h.m, local.ModelCLIP, stateFailed)

		h.sw.set(local.ModelCLIP, false)
		h.m.Refresh()
		if s := status(h.m, local.ModelCLIP); s.State != stateDisabled || s.Error != "" {
			t.Errorf("CLIP while off = %+v, want disabled without error", s)
		}
		h.sw.set(local.ModelCLIP, true)
		h.m.Refresh()
		if s := status(h.m, local.ModelCLIP); s.State != stateFailed || s.Error != "boom" {
			t.Errorf("CLIP after switching on = %+v, want failed with %q", s, "boom")
		}
		h.m.Stop()
		if n := h.dl.callCount(local.ModelCLIP); n != 3 {
			t.Errorf("Download(clip) called %d times, want 3 (no fourth attempt)", n)
		}
	})

	t.Run("file appears", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(true, false), backoff)
		h.m.Start(context.Background())
		for range 3 {
			h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
		}
		waitState(t, h.m, local.ModelCLIP, stateFailed)

		h.m.Stop()
		h.dl.install(local.ModelCLIP) // e.g. by another choix process
		h.m.Start(context.Background())
		if s := status(h.m, local.ModelCLIP); s.State != stateReady || s.Error != "" {
			t.Errorf("CLIP after the file appeared = %+v, want ready without error", s)
		}
		h.m.Stop()
		if got := h.onDownloaded(); len(got) != 0 {
			t.Errorf("OnDownloaded calls = %v, want none", got)
		}
	})
}

func TestInstalledElsewhere_ReadyWithoutDownload(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, true), nil)
	h.m.Start(context.Background())
	clip := h.dl.next(t, local.ModelCLIP)

	h.dl.install(local.ModelQuality) // e.g. by another choix process
	clip.finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)
	h.m.Stop()

	if n := h.dl.callCount(local.ModelQuality); n != 0 {
		t.Errorf("Download(quality) called %d times, want 0", n)
	}
	if got := h.onDownloaded(); !reflect.DeepEqual(got, []local.ModelKind{local.ModelCLIP}) {
		t.Errorf("OnDownloaded calls = %v, want [clip]", got)
	}
}

func TestStop_JoinsWithinOneSecond(t *testing.T) {
	t.Run("during a download", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(true, true), nil)
		h.m.Start(context.Background())
		c := h.dl.next(t, local.ModelCLIP)

		start := time.Now()
		h.m.Stop()
		if d := time.Since(start); d > time.Second {
			t.Errorf("Stop took %v, want ≤ 1s", d)
		}
		select {
		case <-c.ctx.Done():
		default:
			t.Error("Stop returned before cancelling the download")
		}
		// The download resumes on the next Start, so it is queued, not failed.
		if s := status(h.m, local.ModelCLIP); s.State != stateQueued {
			t.Errorf("CLIP state after Stop = %q, want %q", s.State, stateQueued)
		}
		if n := h.dl.callCount(local.ModelQuality); n != 0 {
			t.Errorf("Download(quality) called %d times, want 0", n)
		}
	})

	t.Run("during a retry pause", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(true, false), []time.Duration{time.Hour, time.Hour})
		h.m.Start(context.Background())
		h.dl.next(t, local.ModelCLIP).finish(errors.New("boom"))
		waitState(t, h.m, local.ModelCLIP, stateQueued)

		start := time.Now()
		h.m.Stop()
		if d := time.Since(start); d > time.Second {
			t.Errorf("Stop took %v, want ≤ 1s", d)
		}
	})

	t.Run("idle, twice, and without Start", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(false, false), nil)
		h.m.Stop()
		h.m.Start(context.Background())
		h.m.Stop()
		h.m.Stop()
	})

	t.Run("parent context cancelled", func(t *testing.T) {
		h := newHarness(t, newFake(), newSwitches(true, false), nil)
		ctx, cancel := context.WithCancel(context.Background())
		h.m.Start(ctx)
		c := h.dl.next(t, local.ModelCLIP)
		cancel()
		select {
		case <-c.ctx.Done():
		case <-time.After(waitLimit):
			t.Fatal("download not cancelled with the parent context")
		}
		h.m.Stop()
	})
}

func TestRestart_ResumesQueuedDownload(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, false), nil)
	h.m.Start(context.Background())
	h.dl.next(t, local.ModelCLIP)
	h.m.Stop()

	h.m.Start(context.Background())
	h.dl.next(t, local.ModelCLIP).finish(nil)
	waitState(t, h.m, local.ModelCLIP, stateReady)
}

func TestEmits_ModelsProgress(t *testing.T) {
	h := newHarness(t, newFake(), newSwitches(true, false), nil)
	h.m.Start(context.Background())

	// Start reports every model at once.
	if got := states(recorded(t, h.rec, local.ModelCLIP)); !reflect.DeepEqual(got, []string{stateQueued}) {
		t.Errorf("CLIP events after Start = %v, want [queued]", got)
	}
	if got := states(recorded(t, h.rec, local.ModelQuality)); !reflect.DeepEqual(got, []string{stateDisabled}) {
		t.Errorf("quality events after Start = %v, want [disabled]", got)
	}

	clip := h.dl.next(t, local.ModelCLIP)
	clip.progress(10, 0)
	clip.progress(50, 0)
	// Progress is coalesced: nothing is delivered until a flush, then only
	// the latest value.
	if got := len(recorded(t, h.rec, local.ModelCLIP)); got != 1 {
		t.Errorf("CLIP events before Flush = %d, want 1", got)
	}
	h.co.Flush()
	size := local.KnownModels[local.ModelCLIP].Size
	evs := recorded(t, h.rec, local.ModelCLIP)
	if want := (events.ModelStatus{Kind: "clip", State: stateDownloading, Received: 50, Size: size}); evs[len(evs)-1] != want {
		t.Errorf("flushed event = %+v, want %+v", evs[len(evs)-1], want)
	}

	clip.finish(nil)
	waitState(t, h.m, local.ModelCLIP, stateReady)
	evs = recorded(t, h.rec, local.ModelCLIP)
	if got := states(evs); !reflect.DeepEqual(got, []string{stateQueued, stateDownloading, stateReady}) {
		t.Errorf("CLIP events = %v, want [queued downloading ready]", got)
	}
	if last := evs[len(evs)-1]; last.Size != size || last.Received != size {
		t.Errorf("ready event = %+v, want received = size = %d", last, size)
	}

	// Switching CLIP off is reported immediately.
	h.sw.set(local.ModelCLIP, false)
	h.m.Refresh()
	if got := states(recorded(t, h.rec, local.ModelCLIP)); got[len(got)-1] != stateDisabled {
		t.Errorf("CLIP events after Refresh = %v, want a final disabled", got)
	}
}

func TestNew_PanicsWithoutRequiredOptions(t *testing.T) {
	for name, o := range map[string]Options{
		"no downloader": {Enabled: func(local.ModelKind) bool { return true }},
		"no enabled":    {Downloader: newFake()},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("New did not panic")
				}
			}()
			New(o)
		})
	}
}

var _ Downloader = (*local.Installer)(nil)
