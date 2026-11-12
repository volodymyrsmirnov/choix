package session

import (
	"context"
	"errors"
	"image"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// holdInference takes the inference turn for the test, as another job's
// photo would, and gives it back when the test ends (or on release).
func holdInference(t *testing.T) (release func()) {
	t.Helper()
	r, err := acquireInference(context.Background())
	if err != nil {
		t.Fatalf("acquireInference: %v", err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			r()
		}
	}
	t.Cleanup(release)
	return release
}

func TestInferenceTurn_OneHolderAndCancellableWait(t *testing.T) {
	release := holdInference(t)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		r, err := acquireInference(ctx)
		if err == nil {
			r()
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		t.Fatalf("a second holder got the turn while it was held (err %v)", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled wait = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the wait for the turn did not end within 1 s of its cancellation")
	}
	release()
	r, err := acquireInference(context.Background())
	if err != nil {
		t.Fatalf("acquireInference after release: %v", err)
	}
	r()
}

// turnTestEmbedder stands for the inference engine while another job's
// inference holds it: Embed blocks, uncancellable, until busy is closed.
type turnTestEmbedder struct {
	sessionTestEmbedder
	busy    chan struct{}
	entered atomic.Int64 // calls that reached the engine
}

func (e *turnTestEmbedder) Embed(img image.Image) ([]float32, error) {
	e.entered.Add(1)
	<-e.busy
	return e.sessionTestEmbedder.Embed(img)
}

// A job whose analyze stage waits for the inference turn — another job's
// photo holds it — stops within ~1 s of its cancellation, without waiting
// for that photo, and records nothing for its files.
func TestNewPipeline_AnalyzeWaitForTheInferenceTurnIsCancellable(t *testing.T) {
	emb := &turnTestEmbedder{busy: make(chan struct{})}
	t.Cleanup(func() { close(emb.busy) })
	h := newSessionTestHarness(t, sessionTestOptions{embedder: emb})
	id, root := h.folder(t, "turn")
	sessionTestWriteJPEG(t, root, "a.jpg")
	sessionTestWriteJPEG(t, root, "b.jpg")
	s, release := h.acquire(t, id)
	defer release()

	releaseTurn := holdInference(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := s.NewPipeline(h.settings.get)
	errc := make(chan error, 1)
	go func() { errc <- p.Run(ctx) }()

	// The analyze stage is waiting for its turn (or, without one, inside the
	// busy engine).
	waitFor(t, "the analyze stage waiting for inference", func() bool {
		return inferenceWaiters() > 0 || emb.entered.Load() > 0
	})
	cancel()
	start := time.Now()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v; want a cancellation", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Run never returned")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Run took %v to stop while waiting for the inference turn; want within 1 s", took)
	}
	releaseTurn()
	if n := emb.calls.Load(); n != 0 {
		t.Errorf("embedder called %d times; want 0", n)
	}
	if n, err := s.Store.Files().CountByStatus("analyzed"); err != nil || n != 0 {
		t.Errorf("analyzed = %d, %v; want 0", n, err)
	}
}

// Fix round 1: whether a file is embedded is decided by the live
// visual_grouping setting when the engine is about to be called, so the turn
// cannot depend on an earlier read of it. With the setting off when the
// stage starts and switched on while it waits, the stage still waits in the
// turn — never in the engine — and stops within ~1 s of its cancellation.
func TestNewPipeline_AnalyzeTakesTheTurnWhateverTheSetting(t *testing.T) {
	emb := &turnTestEmbedder{busy: make(chan struct{})}
	t.Cleanup(func() { close(emb.busy) })
	h := newSessionTestHarness(t, sessionTestOptions{embedder: emb})
	h.settings.set(func(c *config.Config) { c.VisualGrouping = false })
	id, root := h.folder(t, "turn-off-on")
	sessionTestWriteJPEG(t, root, "a.jpg")
	sessionTestWriteJPEG(t, root, "b.jpg")
	s, release := h.acquire(t, id)
	defer release()

	releaseTurn := holdInference(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := s.NewPipeline(h.settings.get)
	errc := make(chan error, 1)
	go func() { errc <- p.Run(ctx) }()

	waitFor(t, "the analyze stage waiting in the inference turn", func() bool {
		return inferenceWaiters() > 0
	})
	// Switched on while the stage waits: the file it holds would now be
	// embedded.
	h.settings.set(func(c *config.Config) { c.VisualGrouping = true })
	cancel()
	start := time.Now()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v; want a cancellation", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Run never returned")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Run took %v to stop; want within 1 s", took)
	}
	releaseTurn()
	if n := emb.entered.Load(); n != 0 {
		t.Errorf("the engine was called %d times without the turn; want 0", n)
	}
}

// turnTestScorer counts its calls.
type turnTestScorer struct{ scored int }

func (*turnTestScorer) CountPending() (int, error) { return 0, nil }
func (*turnTestScorer) ListPending(int64, int) ([]store.QualityItem, error) {
	return nil, nil
}

func (s *turnTestScorer) Score(context.Context, store.QualityItem) error {
	s.scored++
	return nil
}

// A rating pass takes no inference turn: it scores with the built-in
// formula while another job holds the turn.
func TestJobScorer_DoesNotTakeTheInferenceTurn(t *testing.T) {
	inner := &turnTestScorer{}
	sc := &jobScorer{
		Scorer:  inner,
		enabled: func() bool { return true },
		root:    t.TempDir(),
		stop:    func(error) {},
		rated:   func() {},
	}
	releaseTurn := holdInference(t)
	defer releaseTurn()
	errc := make(chan error, 1)
	go func() { errc <- sc.Score(context.Background(), store.QualityItem{FileID: 1}) }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Score: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Score waited for the inference turn")
	}
	if inner.scored != 1 {
		t.Errorf("scored %d photos; want 1", inner.scored)
	}
}
