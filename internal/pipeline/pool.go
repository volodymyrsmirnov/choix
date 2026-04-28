package pipeline

import (
	"context"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// PoolProgress is invoked exactly once per processed id with a delta of
// either ok=1 or fail=1 (never both). Callers use it to surface per-file
// progress to the UI. The callback runs on a worker goroutine and must
// be cheap and goroutine-safe.
type PoolProgress func(okDelta, failDelta int)

// RunPool runs fn against every id using up to workers goroutines. It
// returns the count of successful invocations, the count of per-item
// failures, and the first error encountered (which may be ctx.Err if the
// context was cancelled, or a per-item error). Per-item errors do NOT
// cancel the pool — only ctx cancellation does. The first per-item error
// is reported via the returned err so callers can surface it; callers that
// need every error should record them inside fn.
func RunPool(ctx context.Context, workers int, ids []int64, fn func(context.Context, int64) error) (okCount, failCount int, firstErr error) {
	return RunPoolWithProgress(ctx, workers, ids, fn, nil)
}

// RunPoolWithProgress is RunPool plus a per-file progress callback.
func RunPoolWithProgress(ctx context.Context, workers int, ids []int64, fn func(context.Context, int64) error, onProgress PoolProgress) (okCount, failCount int, firstErr error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	if workers < 1 {
		workers = 1
	}

	g, gctx := errgroup.WithContext(ctx)
	// +1 for the producer goroutine.
	g.SetLimit(workers + 1)

	jobs := make(chan int64)

	// Producer: feed ids onto jobs, abort on ctx cancel.
	g.Go(func() error {
		defer close(jobs)
		for _, id := range ids {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case jobs <- id:
			}
		}
		return nil
	})

	var ok, fail int64
	var (
		firstErrMu      sync.Mutex
		firstPerItemErr error
	)
	storeFirst := func(err error) {
		firstErrMu.Lock()
		if firstPerItemErr == nil {
			firstPerItemErr = err
		}
		firstErrMu.Unlock()
	}

	// Consumers.
	for i := 0; i < workers; i++ {
		g.Go(func() error {
			for {
				select {
				case <-gctx.Done():
					return gctx.Err()
				case id, more := <-jobs:
					if !more {
						return nil
					}
					if err := fn(gctx, id); err != nil {
						atomic.AddInt64(&fail, 1)
						storeFirst(err)
						if onProgress != nil {
							onProgress(0, 1)
						}
						continue
					}
					atomic.AddInt64(&ok, 1)
					if onProgress != nil {
						onProgress(1, 0)
					}
				}
			}
		})
	}

	groupErr := g.Wait()
	okCount = int(ok)
	failCount = int(fail)

	switch {
	case groupErr != nil:
		firstErr = groupErr
	default:
		firstErrMu.Lock()
		firstErr = firstPerItemErr
		firstErrMu.Unlock()
	}
	return okCount, failCount, firstErr
}
