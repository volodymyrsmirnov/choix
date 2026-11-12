package inference

import (
	"errors"
	"image"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
)

// fakeBackend stands in for *local.Sessions. Like the real one it "creates"
// a model's session on that model's first call; a test can hold that
// creation (createGate) or any inference (inferGate) open.
type fakeBackend struct {
	mu       sync.Mutex
	created  map[local.ModelKind]bool
	calls    map[local.ModelKind]int
	closed   int
	embedErr error

	// When non-nil, the first call for a model blocks on createGate (the
	// session being created) and every call blocks on inferGate (the
	// inference). entered receives one value per call that reached the gate.
	createGate chan struct{}
	inferGate  chan struct{}
	entered    chan local.ModelKind
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{created: map[local.ModelKind]bool{}, calls: map[local.ModelKind]int{}}
}

func (f *fakeBackend) run(kind local.ModelKind) {
	if f.entered != nil {
		f.entered <- kind
	}
	f.mu.Lock()
	create := !f.created[kind]
	f.mu.Unlock()
	if create && f.createGate != nil {
		<-f.createGate
	}
	if f.inferGate != nil {
		<-f.inferGate
	}
	f.mu.Lock()
	f.created[kind] = true
	f.calls[kind]++
	f.mu.Unlock()
}

func (f *fakeBackend) Embed(image.Image) ([]float32, error) {
	f.run(local.ModelCLIP)
	f.mu.Lock()
	err := f.embedErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return []float32{1, 0}, nil
}

func (f *fakeBackend) QualityCrop([]float32) (float64, error) {
	f.run(local.ModelQuality)
	return 0.5, nil
}

func (f *fakeBackend) Provider(kind local.ModelKind) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created[kind] {
		return "cpu"
	}
	return ""
}

func (f *fakeBackend) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	clear(f.created)
	return nil
}

func (f *fakeBackend) snapshot() (created map[local.ModelKind]bool, calls map[local.ModelKind]int, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.created), maps.Clone(f.calls), f.closed
}

// fakeEngine returns an Engine whose sessions come from fb, and a counter
// of how often the engine asked for them.
func fakeEngine(t *testing.T, fb *fakeBackend) (*Engine, *atomic.Int32) {
	t.Helper()
	var opened atomic.Int32
	e := newEngine(local.NewModelStore(t.TempDir()), nil,
		func(*local.ModelStore, map[local.ModelKind]bool) backend {
			opened.Add(1)
			return fb
		})
	return e, &opened
}

func img() image.Image { return image.NewRGBA(image.Rect(0, 0, 2, 2)) }

// returnsWithin reports whether ch delivers within d, and what.
func returnsWithin(ch <-chan error, d time.Duration) (bool, error) {
	select {
	case err := <-ch:
		return true, err
	case <-time.After(d):
		return false, nil
	}
}

func TestEmbedder_DisabledIsModelMissing(t *testing.T) {
	fb := newFakeBackend()
	e, opened := fakeEngine(t, fb)
	t.Cleanup(func() { _ = e.Close() })

	var on atomic.Bool
	emb := e.Embedder(on.Load)
	if _, err := emb.Embed(img()); !errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("Embed while disabled = %v, want ErrModelMissing", err)
	}
	if n := opened.Load(); n != 0 {
		t.Fatalf("a disabled Embed opened sessions (%d times)", n)
	}

	// The setting is read on every call: switching it on works at once,
	// and switching it off again is honoured at once.
	on.Store(true)
	if v, err := emb.Embed(img()); err != nil || len(v) != 2 {
		t.Fatalf("Embed while enabled = %v, %v", v, err)
	}
	on.Store(false)
	if _, err := emb.Embed(img()); !errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("Embed after switching off = %v, want ErrModelMissing", err)
	}
	if _, calls, _ := fb.snapshot(); calls[local.ModelCLIP] != 1 {
		t.Fatalf("CLIP inferences = %d, want 1", calls[local.ModelCLIP])
	}
}

// A nil enabled func means "always enabled".
func TestEmbedder_NilEnabledMeansOn(t *testing.T) {
	e, _ := fakeEngine(t, newFakeBackend())
	t.Cleanup(func() { _ = e.Close() })
	if _, err := e.Embedder(nil).Embed(img()); err != nil {
		t.Fatalf("Embed with nil enabled: %v", err)
	}
}

// The real sessions behind New: with no model installed, CLIP reports
// ErrModelMissing and the quality model ErrQualityUnavailable — and the
// absence is not remembered (local.Sessions checks the file on each call).
func TestNew_MissingModels(t *testing.T) {
	e := New(local.NewModelStore(t.TempDir()), nil)
	t.Cleanup(func() { _ = e.Close() })

	if _, err := e.Embedder(func() bool { return false }).Embed(img()); !errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("disabled Embed = %v, want ErrModelMissing", err)
	}
	if _, err := e.Embedder(func() bool { return true }).Embed(img()); !errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("Embed without the model = %v, want ErrModelMissing", err)
	}
	_, err := e.CropInferer().QualityCrop(make([]float32, 3*384*384))
	if !errors.Is(err, local.ErrQualityUnavailable) || !errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("QualityCrop without the model = %v, want ErrQualityUnavailable and ErrModelMissing", err)
	}
	for _, k := range []local.ModelKind{local.ModelCLIP, local.ModelQuality} {
		if p := e.Provider(k); p != "" {
			t.Errorf("Provider(%s) = %q without a session, want empty", k, p)
		}
	}
}

func TestEngine_NoSessionBeforeFirstUse(t *testing.T) {
	fb := newFakeBackend()
	e, opened := fakeEngine(t, fb)
	t.Cleanup(func() { _ = e.Close() })

	// Constructing, handing out the interfaces and asking for providers
	// creates nothing.
	emb := e.Embedder(func() bool { return true })
	crop := e.CropInferer()
	for _, k := range []local.ModelKind{local.ModelCLIP, local.ModelQuality} {
		if p := e.Provider(k); p != "" {
			t.Errorf("Provider(%s) before use = %q, want empty", k, p)
		}
	}
	if n := opened.Load(); n != 0 {
		t.Fatalf("sessions opened %d times before first use, want 0", n)
	}

	// The first embedding creates the CLIP session only; the quality
	// session waits for the first crop.
	if _, err := emb.Embed(img()); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	created, _, _ := fb.snapshot()
	if !created[local.ModelCLIP] || created[local.ModelQuality] {
		t.Fatalf("after one Embed created = %v, want CLIP only", created)
	}
	if p := e.Provider(local.ModelCLIP); p != "cpu" {
		t.Errorf("Provider(clip) after Embed = %q, want cpu", p)
	}
	if p := e.Provider(local.ModelQuality); p != "" {
		t.Errorf("Provider(quality) after Embed = %q, want empty", p)
	}

	if _, err := crop.QualityCrop(nil); err != nil {
		t.Fatalf("QualityCrop: %v", err)
	}
	if p := e.Provider(local.ModelQuality); p != "cpu" {
		t.Errorf("Provider(quality) after QualityCrop = %q, want cpu", p)
	}
	if n := opened.Load(); n != 1 {
		t.Fatalf("sessions opened %d times, want exactly once", n)
	}
}

// A failure (the CLIP model not downloaded yet) is not remembered: the
// next call goes to the sessions again and succeeds once the model is there.
func TestEngine_ModelAvailableLater(t *testing.T) {
	fb := newFakeBackend()
	fb.embedErr = local.ErrModelMissing
	e, _ := fakeEngine(t, fb)
	t.Cleanup(func() { _ = e.Close() })

	emb := e.Embedder(func() bool { return true })
	if _, err := emb.Embed(img()); !errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("Embed before download = %v, want ErrModelMissing", err)
	}
	fb.mu.Lock()
	fb.embedErr = nil
	fb.mu.Unlock()
	if _, err := emb.Embed(img()); err != nil {
		t.Fatalf("Embed after download: %v", err)
	}
}

func TestEngine_Serialised(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	probe := &probeBackend{backend: newFakeBackend(), inFlight: &inFlight, max: &maxInFlight}
	e := newEngine(local.NewModelStore(t.TempDir()), nil,
		func(*local.ModelStore, map[local.ModelKind]bool) backend { return probe })
	t.Cleanup(func() { _ = e.Close() })

	emb := e.Embedder(nil)
	crop := e.CropInferer()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 10 {
				var err error
				if (g+i)%2 == 0 {
					_, err = emb.Embed(img())
				} else {
					_, err = crop.QualityCrop(nil)
				}
				if err != nil {
					t.Errorf("inference: %v", err)
				}
			}
		})
	}
	wg.Wait()
	if m := maxInFlight.Load(); m != 1 {
		t.Fatalf("up to %d inferences overlapped, want 1", m)
	}
}

type probeBackend struct {
	backend
	inFlight, max *atomic.Int32
}

func (p *probeBackend) enter() func() {
	n := p.inFlight.Add(1)
	for {
		m := p.max.Load()
		if n <= m || p.max.CompareAndSwap(m, n) {
			break
		}
	}
	return func() { p.inFlight.Add(-1) }
}

func (p *probeBackend) Embed(i image.Image) ([]float32, error) {
	defer p.enter()()
	runtimeYield()
	return p.backend.Embed(i)
}

func (p *probeBackend) QualityCrop(in []float32) (float64, error) {
	defer p.enter()()
	runtimeYield()
	return p.backend.QualityCrop(in)
}

func runtimeYield() { time.Sleep(50 * time.Microsecond) }

func TestClose_Idempotent(t *testing.T) {
	fb := newFakeBackend()
	e, opened := fakeEngine(t, fb)
	if _, err := e.CropInferer().QualityCrop(nil); err != nil {
		t.Fatalf("QualityCrop: %v", err)
	}
	for i := range 3 {
		if err := e.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	if _, _, closed := fb.snapshot(); closed != 1 {
		t.Fatalf("sessions closed %d times, want 1", closed)
	}

	// Calls after Close fail without reaching (or reopening) the sessions.
	if _, err := e.Embedder(nil).Embed(img()); err == nil {
		t.Fatal("Embed after Close = nil error")
	} else if errors.Is(err, local.ErrModelMissing) {
		t.Errorf("Embed after Close = %v, must not read as a missing model", err)
	}
	if _, err := e.CropInferer().QualityCrop(nil); !errors.Is(err, local.ErrQualityUnavailable) {
		t.Fatalf("QualityCrop after Close = %v, want ErrQualityUnavailable", err)
	}
	if p := e.Provider(local.ModelQuality); p != "" {
		t.Errorf("Provider after Close = %q, want empty", p)
	}
	if n := opened.Load(); n != 1 {
		t.Fatalf("sessions opened %d times, want 1", n)
	}
}

// Close before any use opens nothing, and nothing opens afterwards.
func TestClose_BeforeUse(t *testing.T) {
	e, opened := fakeEngine(t, newFakeBackend())
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := e.Embedder(nil).Embed(img()); err == nil {
		t.Fatal("Embed after Close = nil error")
	}
	if n := opened.Load(); n != 0 {
		t.Fatalf("sessions opened %d times, want 0", n)
	}
}

// startHeld starts one call that blocks in the backend and returns once it
// has reached the gate, with a channel that receives the call's error.
func startHeld(t *testing.T, fb *fakeBackend, call func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	<-fb.entered
	return done
}

func TestClose_WaitsForInFlight(t *testing.T) {
	fb := newFakeBackend()
	fb.inferGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 4)
	e, _ := fakeEngine(t, fb)

	inferDone := startHeld(t, fb, func() error {
		_, err := e.CropInferer().QualityCrop(nil)
		return err
	})

	closed := make(chan error, 1)
	go func() { closed <- e.Close() }()
	if ok, _ := returnsWithin(closed, 50*time.Millisecond); ok {
		t.Fatal("Close returned while an inference was running")
	}
	if _, _, n := fb.snapshot(); n != 0 {
		t.Fatal("sessions closed under a running inference")
	}

	close(fb.inferGate)
	if err := <-inferDone; err != nil {
		t.Fatalf("in-flight QualityCrop: %v", err)
	}
	if ok, err := returnsWithin(closed, 5*time.Second); !ok {
		t.Fatal("Close did not return after the inference did")
	} else if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, n := fb.snapshot(); n != 1 {
		t.Fatalf("sessions closed %d times, want 1", n)
	}
}

// A session being created (the quality model's takes seconds and cannot be
// interrupted) is awaited the same way.
func TestClose_WaitsForSessionCreation(t *testing.T) {
	fb := newFakeBackend()
	fb.createGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 4)
	e, _ := fakeEngine(t, fb)

	inferDone := startHeld(t, fb, func() error {
		_, err := e.Embedder(nil).Embed(img())
		return err
	})
	closed := make(chan error, 1)
	go func() { closed <- e.Close() }()
	if ok, _ := returnsWithin(closed, 50*time.Millisecond); ok {
		t.Fatal("Close returned while a session was being created")
	}
	close(fb.createGate)
	if err := <-inferDone; err != nil {
		t.Fatalf("Embed during creation: %v", err)
	}
	if ok, err := returnsWithin(closed, 5*time.Second); !ok || err != nil {
		t.Fatalf("Close = %v (returned %v)", err, ok)
	}
}

// Close waits for the one inference in flight, not for the calls queued
// behind it: a call that was already waiting for its turn when Close began
// fails without running, so quitting never waits for a second (possibly
// multi-second) session creation.
func TestClose_QueuedCallsDoNotRun(t *testing.T) {
	fb := newFakeBackend()
	fb.inferGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 8)
	e, _ := fakeEngine(t, fb)

	inferDone := startHeld(t, fb, func() error {
		_, err := e.Embedder(nil).Embed(img())
		return err
	})

	// A second caller passes the entry check (its enabled func runs there)
	// and then has to wait for the held inference.
	passed := make(chan struct{})
	var once sync.Once
	enabled := func() bool { once.Do(func() { close(passed) }); return true }
	queuedDone := make(chan error, 1)
	go func() {
		_, err := e.Embedder(enabled).Embed(img())
		queuedDone <- err
	}()
	<-passed

	closed := make(chan error, 1)
	go func() { closed <- e.Close() }()
	// Close has begun once it has marked the engine closing.
	for !e.closing.Load() {
		time.Sleep(time.Millisecond)
	}
	close(fb.inferGate)

	if err := <-inferDone; err != nil {
		t.Fatalf("in-flight Embed: %v", err)
	}
	if ok, err := returnsWithin(closed, 5*time.Second); !ok || err != nil {
		t.Fatalf("Close = %v (returned %v)", err, ok)
	}
	if ok, err := returnsWithin(queuedDone, 5*time.Second); !ok {
		t.Fatal("the queued Embed did not return")
	} else if err == nil || errors.Is(err, local.ErrModelMissing) {
		t.Fatalf("queued Embed = %v, want the closed error", err)
	}
	if _, calls, _ := fb.snapshot(); calls[local.ModelCLIP] != 1 {
		t.Fatalf("CLIP inferences = %d, want only the in-flight one", calls[local.ModelCLIP])
	}
}

// Every call that observes the engine closing fails without touching the
// sessions, even while it waited for the lock.
func TestClose_CallWaitingForLockFails(t *testing.T) {
	fb := newFakeBackend()
	fb.inferGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 8)
	e, _ := fakeEngine(t, fb)

	inferDone := startHeld(t, fb, func() error {
		_, err := e.Embedder(nil).Embed(img())
		return err
	})

	// Mark the engine closing first, then queue a caller: it cannot have
	// passed the check, so it must fail even if it wins the lock before
	// Close does.
	e.closing.Store(true)
	queuedDone := make(chan error, 1)
	go func() {
		_, err := e.CropInferer().QualityCrop(nil)
		queuedDone <- err
	}()
	if ok, err := returnsWithin(queuedDone, 5*time.Second); !ok {
		t.Fatal("a call made while closing waited for the in-flight inference")
	} else if !errors.Is(err, local.ErrQualityUnavailable) {
		t.Fatalf("QualityCrop while closing = %v, want ErrQualityUnavailable", err)
	}
	close(fb.inferGate)
	if err := <-inferDone; err != nil {
		t.Fatalf("in-flight Embed: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, calls, _ := fb.snapshot(); calls[local.ModelQuality] != 0 {
		t.Fatalf("quality inferences = %d, want 0", calls[local.ModelQuality])
	}
}

// A second, concurrent Close also waits for the in-flight inference instead
// of reporting success while the sessions are still alive.
func TestClose_ConcurrentCloseWaits(t *testing.T) {
	fb := newFakeBackend()
	fb.inferGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 4)
	e, _ := fakeEngine(t, fb)

	inferDone := startHeld(t, fb, func() error {
		_, err := e.CropInferer().QualityCrop(nil)
		return err
	})
	c1 := make(chan error, 1)
	c2 := make(chan error, 1)
	go func() { c1 <- e.Close() }()
	go func() { c2 <- e.Close() }()
	if ok, _ := returnsWithin(c2, 50*time.Millisecond); ok {
		t.Fatal("a second Close returned while an inference was running")
	}
	close(fb.inferGate)
	<-inferDone
	for _, c := range []chan error{c1, c2} {
		if ok, err := returnsWithin(c, 5*time.Second); !ok || err != nil {
			t.Fatalf("Close = %v (returned %v)", err, ok)
		}
	}
	if _, _, n := fb.snapshot(); n != 1 {
		t.Fatalf("sessions closed %d times, want 1", n)
	}
}

// Provider never waits behind an inference or a session being created.
func TestProvider_DoesNotWait(t *testing.T) {
	fb := newFakeBackend()
	fb.inferGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 4)
	e, _ := fakeEngine(t, fb)

	inferDone := startHeld(t, fb, func() error {
		_, err := e.Embedder(nil).Embed(img())
		return err
	})
	got := make(chan error, 1)
	go func() { _ = e.Provider(local.ModelCLIP); got <- nil }()
	if ok, _ := returnsWithin(got, 5*time.Second); !ok {
		t.Fatal("Provider waited behind an inference")
	}
	close(fb.inferGate)
	<-inferDone
	_ = e.Close()
}

// New hands the caller's CPU-only choices to the sessions unchanged and
// later edits of the caller's map do not leak in.
func TestNew_PassesCPUOnly(t *testing.T) {
	var got map[local.ModelKind]bool
	in := map[local.ModelKind]bool{local.ModelCLIP: true}
	e := newEngine(local.NewModelStore(t.TempDir()), in,
		func(_ *local.ModelStore, cpuOnly map[local.ModelKind]bool) backend {
			got = cpuOnly
			return newFakeBackend()
		})
	in[local.ModelQuality] = true
	if _, err := e.Embedder(nil).Embed(img()); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	_ = e.Close()
	if !got[local.ModelCLIP] || got[local.ModelQuality] {
		t.Fatalf("cpuOnly given to the sessions = %v, want {clip: true}", got)
	}
}

// TryClose never waits: with an inference in flight it closes nothing and
// leaves the engine usable; once the engine is free it closes it as Close
// does.
func TestTryClose_DoesNotWaitForInFlight(t *testing.T) {
	fb := newFakeBackend()
	fb.inferGate = make(chan struct{})
	fb.entered = make(chan local.ModelKind, 4)
	e, _ := fakeEngine(t, fb)
	t.Cleanup(func() { _ = e.Close() })

	inferDone := startHeld(t, fb, func() error {
		_, err := e.CropInferer().QualityCrop(nil)
		return err
	})

	type outcome struct {
		closed bool
		err    error
	}
	tried := make(chan outcome, 1)
	go func() {
		closed, err := e.TryClose()
		tried <- outcome{closed, err}
	}()
	select {
	case o := <-tried:
		if o.closed || o.err != nil {
			t.Fatalf("TryClose under an inference = %v, %v; want false, nil", o.closed, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TryClose waited for the inference in flight")
	}
	if _, _, n := fb.snapshot(); n != 0 {
		t.Fatal("sessions closed under a running inference")
	}

	close(fb.inferGate)
	if err := <-inferDone; err != nil {
		t.Fatalf("in-flight QualityCrop: %v", err)
	}
	// A refused TryClose changed nothing: the engine still serves calls.
	if _, err := e.CropInferer().QualityCrop(nil); err != nil {
		t.Fatalf("QualityCrop after a refused TryClose: %v", err)
	}

	if closed, err := e.TryClose(); !closed || err != nil {
		t.Fatalf("TryClose once free = %v, %v; want true, nil", closed, err)
	}
	if _, _, n := fb.snapshot(); n != 1 {
		t.Fatalf("sessions closed %d times, want 1", n)
	}
	if _, err := e.CropInferer().QualityCrop(nil); !errors.Is(err, local.ErrQualityUnavailable) {
		t.Fatalf("QualityCrop after TryClose = %v, want ErrQualityUnavailable", err)
	}
	// Already closed: TryClose and Close report success and close nothing.
	if closed, err := e.TryClose(); !closed || err != nil {
		t.Fatalf("second TryClose = %v, %v; want true, nil", closed, err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close after TryClose: %v", err)
	}
	if _, _, n := fb.snapshot(); n != 1 {
		t.Fatalf("sessions closed %d times, want 1", n)
	}
}

// TryClose before any use opens nothing, and nothing opens afterwards.
func TestTryClose_BeforeUse(t *testing.T) {
	e, opened := fakeEngine(t, newFakeBackend())
	if closed, err := e.TryClose(); !closed || err != nil {
		t.Fatalf("TryClose = %v, %v; want true, nil", closed, err)
	}
	if _, err := e.Embedder(nil).Embed(img()); err == nil {
		t.Fatal("Embed after TryClose = nil error")
	}
	if n := opened.Load(); n != 0 {
		t.Fatalf("sessions opened %d times, want 0", n)
	}
}
