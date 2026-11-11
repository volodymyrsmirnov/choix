package quality

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// fakeFetcher is an in-memory model source.
type fakeFetcher struct {
	mu        sync.Mutex
	installed bool
	calls     int
	fail      int           // fail this many calls first
	block     chan struct{} // when non-nil, Download waits for it (or ctx)
	started   chan struct{} // receives when a Download call begins
}

func (f *fakeFetcher) Has() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed
}

func (f *fakeFetcher) Download(ctx context.Context, onProgress func(received, size int64)) error {
	f.mu.Lock()
	f.calls++
	fail := f.fail > 0
	if fail {
		f.fail--
	}
	block := f.block
	f.mu.Unlock()
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	onProgress(40, 100)
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail {
		return errors.New("network down")
	}
	f.mu.Lock()
	f.installed = true
	f.mu.Unlock()
	onProgress(100, 100)
	return nil
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeScorer keeps pending photos in memory. score decides each outcome.
type fakeScorer struct {
	mu      sync.Mutex
	pending map[int64]bool
	scored  []int64
	score   func(ctx context.Context, id int64) error // nil → success
}

func newFakeScorer(ids ...int64) *fakeScorer {
	s := &fakeScorer{pending: map[int64]bool{}}
	for _, id := range ids {
		s.pending[id] = true
	}
	return s
}

func (s *fakeScorer) add(id int64) {
	s.mu.Lock()
	s.pending[id] = true
	s.mu.Unlock()
}

func (s *fakeScorer) CountPending() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending), nil
}

func (s *fakeScorer) ListPending(afterID int64, limit int) ([]store.QualityItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.QualityItem
	for id := afterID + 1; id < afterID+1000 && len(out) < limit; id++ {
		if s.pending[id] {
			out = append(out, store.QualityItem{FileID: id})
		}
	}
	return out, nil
}

func (s *fakeScorer) Score(ctx context.Context, item store.QualityItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	fn := s.score
	s.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, item.FileID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.pending, item.FileID)
	s.scored = append(s.scored, item.FileID)
	s.mu.Unlock()
	return nil
}

func (s *fakeScorer) scoredIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.scored...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func startWorker(t *testing.T, opts Options) *Worker {
	t.Helper()
	if opts.Backoff == nil {
		opts.Backoff = []time.Duration{time.Millisecond, time.Millisecond}
	}
	opts.PollInterval = 2 * time.Millisecond
	w := New(opts)
	w.Start(context.Background())
	t.Cleanup(w.Stop)
	return w
}

func TestWorkerDownloadsThenRatesEverything(t *testing.T) {
	f := &fakeFetcher{}
	s := newFakeScorer(1, 2, 3)
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})

	waitFor(t, "idle after rating", func() bool { return w.Status().State == StateIdle && len(s.scoredIDs()) == 3 })
	if f.callCount() != 1 {
		t.Fatalf("downloads = %d, want 1", f.callCount())
	}
	st := w.Status()
	if st.Done != 3 || st.Total != 3 || st.Error != "" {
		t.Fatalf("status = %+v, want 3/3 without error", st)
	}
}

func TestWorkerDisabledDoesNothing(t *testing.T) {
	f := &fakeFetcher{}
	s := newFakeScorer(1, 2)
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: false})

	time.Sleep(30 * time.Millisecond)
	if f.callCount() != 0 || len(s.scoredIDs()) != 0 {
		t.Fatalf("disabled worker downloaded %d times and rated %v", f.callCount(), s.scoredIDs())
	}
	if st := w.Status(); st.State != StateDisabled || st.Enabled {
		t.Fatalf("status = %+v, want disabled", st)
	}

	// Switching it on does the work, with no restart.
	w.SetEnabled(true)
	waitFor(t, "rating after enabling", func() bool { return len(s.scoredIDs()) == 2 && w.Status().State == StateIdle })
}

func TestWorkerRetriesDownloadThenGivesUpWithoutBreakingAnything(t *testing.T) {
	f := &fakeFetcher{fail: 100}
	s := newFakeScorer(1)
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})

	waitFor(t, "download_failed", func() bool { return w.Status().State == StateDownloadFailed })
	if f.callCount() != 3 {
		t.Fatalf("download attempts = %d, want 3 (one plus two backoffs)", f.callCount())
	}
	if w.Status().Error == "" {
		t.Fatal("a failed download must carry its error for the UI")
	}
	if len(s.scoredIDs()) != 0 {
		t.Fatal("nothing can be rated without the model")
	}

	// The user presses Retry once the network is back.
	f.mu.Lock()
	f.fail = 0
	f.mu.Unlock()
	w.Wake()
	waitFor(t, "rating after retry", func() bool { return len(s.scoredIDs()) == 1 && w.Status().State == StateIdle })
	if w.Status().Error != "" {
		t.Fatalf("error should clear after a successful retry, got %q", w.Status().Error)
	}
}

func TestWorkerDisableCancelsDownloadAndEnableResumes(t *testing.T) {
	f := &fakeFetcher{block: make(chan struct{}), started: make(chan struct{}, 4)}
	s := newFakeScorer(1)
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})

	<-f.started
	waitFor(t, "downloading with progress", func() bool {
		st := w.Status()
		return st.State == StateDownloading && st.Received == 40 && st.Size == 100
	})

	w.SetEnabled(false)
	waitFor(t, "disabled", func() bool { return w.Status().State == StateDisabled })
	if f.Has() {
		t.Fatal("a cancelled download must not install the model")
	}

	f.mu.Lock()
	f.block = nil
	f.mu.Unlock()
	w.SetEnabled(true)
	waitFor(t, "rated after re-enabling", func() bool { return len(s.scoredIDs()) == 1 && w.Status().State == StateIdle })
}

func TestWorkerYieldsToRunningPipeline(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2)
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true, Busy: busy.Load})

	waitFor(t, "waiting", func() bool { return w.Status().State == StateWaiting })
	time.Sleep(20 * time.Millisecond)
	if n := len(s.scoredIDs()); n != 0 {
		t.Fatalf("rated %d photos while the pipeline was running", n)
	}
	busy.Store(false)
	waitFor(t, "rated once the pipeline is idle", func() bool { return len(s.scoredIDs()) == 2 })
}

func TestWorkerPausesMidPassWhenAScanStarts(t *testing.T) {
	var busy atomic.Bool
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2, 3)
	s.score = func(_ context.Context, id int64) error {
		if id == 1 {
			busy.Store(true) // a scan starts while photo 1 is being rated
		}
		return nil
	}
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true, Busy: busy.Load})

	waitFor(t, "paused after the first photo", func() bool { return w.Status().State == StateWaiting })
	if got := s.scoredIDs(); len(got) != 1 {
		t.Fatalf("rated %v before pausing, want just photo 1", got)
	}
	busy.Store(false)
	waitFor(t, "rest rated", func() bool { return len(s.scoredIDs()) == 3 })
}

func TestWorkerWakePicksUpNewPhotos(t *testing.T) {
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1)
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})
	waitFor(t, "first pass", func() bool { return len(s.scoredIDs()) == 1 && w.Status().State == StateIdle })

	s.add(2) // a later scan analyzed a new photo
	w.Wake() // ...and the pipeline adapter wakes the worker when it ends
	waitFor(t, "second pass", func() bool { return len(s.scoredIDs()) == 2 })
}

func TestWorkerSkipsAFailingPhotoAndKeepsGoing(t *testing.T) {
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2, 3)
	s.score = func(_ context.Context, id int64) error {
		if id == 2 {
			return errors.New("inference hiccup")
		}
		return nil
	}
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})

	waitFor(t, "pass end", func() bool { return len(s.scoredIDs()) == 2 && w.Status().State == StateIdle })
	if got := s.scoredIDs(); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("rated %v, want photos 1 and 3 (2 failed and is stepped over, not retried in a loop)", got)
	}
	if n, _ := s.CountPending(); n != 1 {
		t.Fatalf("pending = %d, want the failed photo left for a later pass", n)
	}
}

func TestWorkerStopsPassAfterRepeatedFailures(t *testing.T) {
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2, 3, 4, 5, 6)
	var attempts atomic.Int32
	s.score = func(context.Context, int64) error {
		attempts.Add(1)
		return errors.New("coreml broke")
	}
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})

	waitFor(t, "error state", func() bool { return w.Status().State == StateError })
	if attempts.Load() != maxConsecutiveFailures {
		t.Fatalf("attempts = %d, want the pass to stop after %d failures in a row", attempts.Load(), maxConsecutiveFailures)
	}
	if w.Status().Error == "" {
		t.Fatal("error state must explain itself")
	}
}

func TestWorkerStopsAtOnceWhenScorerIsUnavailable(t *testing.T) {
	unavailable := errors.New("onnx runtime missing")
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2, 3)
	var attempts atomic.Int32
	s.score = func(context.Context, int64) error {
		attempts.Add(1)
		return unavailable
	}
	w := startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true,
		IsUnavailable: func(err error) bool { return errors.Is(err, unavailable) }})

	waitFor(t, "error state", func() bool { return w.Status().State == StateError })
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1 (no point trying further photos)", attempts.Load())
	}
}

func TestWorkerDisableStopsScoringBetweenPhotos(t *testing.T) {
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2, 3, 4)
	var w *Worker
	s.score = func(_ context.Context, id int64) error {
		if id == 2 {
			w.SetEnabled(false) // the user switches the setting off mid-pass
		}
		return nil
	}
	w = startWorker(t, Options{Fetcher: f, Scorer: s, Enabled: true})

	waitFor(t, "disabled", func() bool { return w.Status().State == StateDisabled })
	time.Sleep(20 * time.Millisecond)
	if got := s.scoredIDs(); len(got) > 2 {
		t.Fatalf("rated %v; scoring must stop once the setting is off", got)
	}
}

func TestWorkerStopCancelsAndJoins(t *testing.T) {
	f := &fakeFetcher{installed: true}
	s := newFakeScorer(1, 2)
	inScore := make(chan struct{})
	var once sync.Once
	s.score = func(ctx context.Context, _ int64) error {
		once.Do(func() { close(inScore) })
		<-ctx.Done() // a long inference that only ends on cancellation
		return ctx.Err()
	}
	w := New(Options{Fetcher: f, Scorer: s, Enabled: true, PollInterval: time.Millisecond})
	w.Start(context.Background())
	<-inScore

	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return within 1s")
	}
	// After Stop the worker must be completely inert.
	before := len(s.scoredIDs())
	w.Wake()
	w.SetEnabled(true)
	time.Sleep(20 * time.Millisecond)
	if len(s.scoredIDs()) != before {
		t.Fatal("worker touched the scorer after Stop")
	}
	w.Stop() // idempotent
}

func TestStopBeforeStartDoesNotHang(t *testing.T) {
	w := New(Options{Fetcher: &fakeFetcher{}, Scorer: newFakeScorer(), Enabled: true})
	done := make(chan struct{})
	go func() { w.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop before Start hung")
	}
}
