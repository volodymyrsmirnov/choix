package pipeline

import (
	"context"
	"errors"
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
// cancel the pool — only cancellation does. An fn error returned once ctx
// is done, or one that wraps context.Canceled, context.DeadlineExceeded or
// ErrFolderUnavailable, is a cancellation, not a per-item failure: it is not counted, the pool
// stops, and that error (ctx's, if ctx is done) is returned. The first per-item error
// is reported via the returned err so callers can surface it; callers that
// need every error should record them inside fn.
func RunPool(ctx context.Context, workers int, ids []int64, fn func(context.Context, int64) error) (okCount, failCount int, firstErr error) {
	return RunPoolWithProgress(ctx, workers, ids, fn, nil)
}

// ErrFolderUnavailable reports that a file's work stopped because the folder
// it lives in can no longer be reached (its volume went away). It is not the
// file's failure: a processor returning it has left the row's status as it
// was, and RunPool treats it like a cancellation — the pool stops and the
// file is not counted.
var ErrFolderUnavailable = errors.New("pipeline: the folder is unavailable")

// isCancellation reports whether err is, or wraps, context.Canceled,
// context.DeadlineExceeded or ErrFolderUnavailable: an interruption of the
// run, not a failure of the file.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrFolderUnavailable)
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
						// Interrupted by cancellation, not a failure of this
						// file: neither counted nor reported. Returning the
						// error stops the whole pool.
						if cerr := gctx.Err(); cerr != nil {
							return cerr
						}
						if isCancellation(err) {
							return err
						}
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
