package quality

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// passScorer is an in-memory Scorer for Pass tests. Photos that score
// successfully leave the pending set; outcome decides each photo's result
// (nil → success).
type passScorer struct {
	mu      sync.Mutex
	pending map[int64]bool
	calls   []int64
	outcome func(ctx context.Context, id int64) error
}

func newPassScorer(ids ...int64) *passScorer {
	s := &passScorer{pending: map[int64]bool{}}
	for _, id := range ids {
		s.pending[id] = true
	}
	return s
}

func (s *passScorer) CountPending() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending), nil
}

func (s *passScorer) ListPending(afterID int64, limit int) ([]store.QualityItem, error) {
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

func (s *passScorer) Score(ctx context.Context, item store.QualityItem) error {
	s.mu.Lock()
	s.calls = append(s.calls, item.FileID)
	fn := s.outcome
	s.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, item.FileID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.pending, item.FileID)
	s.mu.Unlock()
	return nil
}

func (s *passScorer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type progressCall struct{ done, total int }

func TestPass_RatesAllPending(t *testing.T) {
	sc := newPassScorer(1, 2, 3, 4, 5)
	var calls []progressCall
	p := &Pass{Scorer: sc, OnProgress: func(done, total int) {
		calls = append(calls, progressCall{done, total})
	}}

	rated, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rated != 5 {
		t.Fatalf("rated = %d, want 5", rated)
	}
	if len(calls) == 0 {
		t.Fatal("OnProgress never called")
	}
	if last := calls[len(calls)-1]; last != (progressCall{5, 5}) {
		t.Fatalf("last OnProgress = %+v, want {5 5}", last)
	}
	if n, _ := sc.CountPending(); n != 0 {
		t.Fatalf("pending after pass = %d, want 0", n)
	}
}

func TestPass_StopsAfterThreeConsecutiveFailures(t *testing.T) {
	boom := errors.New("inference failed")
	sc := newPassScorer(1, 2, 3, 4, 5, 6)
	// Photo 1 fails, 2 succeeds (resets the streak), then 3, 4, 5 fail in a
	// row: the pass stops there and never reaches 6.
	sc.outcome = func(_ context.Context, id int64) error {
		if id == 2 || id == 6 {
			return nil
		}
		return boom
	}
	p := &Pass{Scorer: sc}

	rated, err := p.Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if rated != 1 {
		t.Fatalf("rated = %d, want 1", rated)
	}
	if got := sc.callCount(); got != 5 {
		t.Fatalf("Score called %d times, want 5 (stop after the third failure in a row)", got)
	}
}

func TestPass_UnavailableStopsImmediately(t *testing.T) {
	unavailable := errors.New("onnx runtime missing")
	sc := newPassScorer(1, 2, 3)
	sc.outcome = func(_ context.Context, id int64) error {
		if id == 2 {
			return unavailable
		}
		return nil
	}
	p := &Pass{Scorer: sc, IsUnavailable: func(err error) bool { return errors.Is(err, unavailable) }}

	rated, err := p.Run(context.Background())
	if !errors.Is(err, unavailable) {
		t.Fatalf("err = %v, want %v", err, unavailable)
	}
	if rated != 1 {
		t.Fatalf("rated = %d, want 1", rated)
	}
	if got := sc.callCount(); got != 2 {
		t.Fatalf("Score called %d times, want 2 (stop at the unavailable error)", got)
	}
}

func TestPass_CancelBetweenPhotos(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc := newPassScorer(1, 2, 3, 4, 5)
	// The second photo is rated successfully, then the context is
	// cancelled: the pass must not start the third.
	sc.outcome = func(_ context.Context, id int64) error {
		if id == 2 {
			cancel()
		}
		return nil
	}
	p := &Pass{Scorer: sc}

	rated, err := p.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rated != 2 {
		t.Fatalf("rated = %d, want 2", rated)
	}
	if got := sc.callCount(); got != 2 {
		t.Fatalf("Score called %d times, want 2", got)
	}
}
