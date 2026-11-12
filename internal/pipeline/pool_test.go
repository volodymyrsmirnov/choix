package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolRunsAllTasksConcurrently(t *testing.T) {
	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	var done int32
	fn := func(ctx context.Context, id int64) error {
		atomic.AddInt32(&done, 1)
		return nil
	}
	ok, fail, err := RunPool(context.Background(), 4, ids, fn)
	if err != nil {
		t.Fatalf("RunPool err = %v", err)
	}
	if ok != len(ids) || fail != 0 {
		t.Errorf("ok=%d fail=%d, want %d/0", ok, fail, len(ids))
	}
	if atomic.LoadInt32(&done) != int32(len(ids)) {
		t.Errorf("done = %d, want %d", done, len(ids))
	}
}

func TestPoolIsolatesPerItemFailures(t *testing.T) {
	ids := []int64{1, 2, 3, 4, 5}
	fn := func(ctx context.Context, id int64) error {
		if id%2 == 0 {
			return errors.New("even failed")
		}
		return nil
	}
	ok, fail, err := RunPool(context.Background(), 2, ids, fn)
	if err == nil {
		t.Fatal("expected first error to be returned")
	}
	if ok != 3 || fail != 2 {
		t.Errorf("ok=%d fail=%d, want 3/2", ok, fail)
	}
}

func TestPoolCancellationStopsWorkers(t *testing.T) {
	ids := make([]int64, 200)
	for i := range ids {
		ids[i] = int64(i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var processed int32
	fn := func(ctx context.Context, id int64) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
			atomic.AddInt32(&processed, 1)
			return nil
		}
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	_, _, err := RunPool(ctx, 4, ids, fn)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	// Should have stopped well short of all 200 items.
	if atomic.LoadInt32(&processed) >= int32(len(ids)) {
		t.Errorf("processed = %d, expected far fewer than %d (cancellation didn't stop)", processed, len(ids))
	}
}

func TestPoolFolderUnavailableStopsWithoutCounting(t *testing.T) {
	ids := make([]int64, 50)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	var calls int32
	fn := func(_ context.Context, id int64) error {
		atomic.AddInt32(&calls, 1)
		if id == 1 {
			return fmt.Errorf("thumb: %w", ErrFolderUnavailable)
		}
		return nil
	}
	var fails int32
	_, fail, err := RunPoolWithProgress(context.Background(), 1, ids, fn, func(_, f int) {
		atomic.AddInt32(&fails, int32(f)) //nolint:gosec // 0 or 1
	})
	if !errors.Is(err, ErrFolderUnavailable) {
		t.Fatalf("err = %v, want ErrFolderUnavailable", err)
	}
	if fail != 0 || atomic.LoadInt32(&fails) != 0 {
		t.Errorf("fail = %d (progress %d); a file interrupted by an outage is not a failure", fail, fails)
	}
	if n := atomic.LoadInt32(&calls); n >= int32(len(ids)) {
		t.Errorf("calls = %d; the pool did not stop", n)
	}
}

func TestPoolEmptyInput(t *testing.T) {
	ok, fail, err := RunPool(context.Background(), 4, nil, func(context.Context, int64) error {
		t.Fatal("must not be called")
		return nil
	})
	if err != nil || ok != 0 || fail != 0 {
		t.Errorf("RunPool nil = %d/%d/%v, want 0/0/nil", ok, fail, err)
	}
}

// A processor can report a (wrapped) context error while the pool's own
// context is still live — e.g. from a context it derived itself. That is an
// interruption, not a failure of the file: it is not counted, the pool
// aborts, and the error is returned.
func TestPoolContextErrorWithLiveParentAbortsWithoutFailure(t *testing.T) {
	for _, ctxErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(ctxErr.Error(), func(t *testing.T) {
			ids := make([]int64, 50)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			var processed, failDeltas int32
			fn := func(_ context.Context, id int64) error {
				atomic.AddInt32(&processed, 1)
				if id == 3 {
					return fmt.Errorf("thumb: tier1: %w", ctxErr)
				}
				return nil
			}
			onProgress := func(_, failDelta int) { atomic.AddInt32(&failDeltas, int32(failDelta)) }

			_, fail, err := RunPoolWithProgress(context.Background(), 1, ids, fn, onProgress)
			if !errors.Is(err, ctxErr) {
				t.Fatalf("err = %v, want %v", err, ctxErr)
			}
			if fail != 0 || atomic.LoadInt32(&failDeltas) != 0 {
				t.Fatalf("fail = %d, progress fail deltas = %d; want 0/0", fail, failDeltas)
			}
			if n := atomic.LoadInt32(&processed); n >= int32(len(ids)) {
				t.Fatalf("processed = %d, want the pool to abort well short of %d", n, len(ids))
			}
		})
	}
}

func TestPoolRespectsConcurrencyFloor(t *testing.T) {
	// workers <= 0 must coerce to 1, not panic, not run zero workers.
	ids := []int64{1, 2}
	var n int32
	ok, _, err := RunPool(context.Background(), 0, ids, func(context.Context, int64) error {
		atomic.AddInt32(&n, 1)
		return nil
	})
	if err != nil || ok != 2 {
		t.Errorf("RunPool err=%v ok=%d, want nil/2", err, ok)
	}
}
