package pipeline

import (
	"context"
	"errors"
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

func TestPoolEmptyInput(t *testing.T) {
	ok, fail, err := RunPool(context.Background(), 4, nil, func(context.Context, int64) error {
		t.Fatal("must not be called")
		return nil
	})
	if err != nil || ok != 0 || fail != 0 {
		t.Errorf("RunPool nil = %d/%d/%v, want 0/0/nil", ok, fail, err)
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
