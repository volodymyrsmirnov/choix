package platform

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// NativeSlots is the process-wide number of native calls that may exist at
// once, abandoned ones included. It is read once, at the first Acquire;
// later changes have no effect.
//
// Because a slot is held until the native call returns, an abandoned call
// keeps its slot: after a preemption the next job simply runs with fewer
// slots until the abandoned calls finish.
var NativeSlots = runtime.NumCPU() + 4

var (
	slotsOnce sync.Once
	slots     chan struct{}

	// nativeRunning counts the slots held right now; nativeHigh is the
	// largest value nativeRunning has had in this process.
	nativeRunning atomic.Int64
	nativeHigh    atomic.Int64
)

// NativeRunning reports how many native calls hold a slot right now,
// abandoned ones included.
func NativeRunning() int { return int(nativeRunning.Load()) }

// NativeHighWater reports the largest number of native calls that have held
// a slot at the same time in this process. It never exceeds NativeSlots.
func NativeHighWater() int { return int(nativeHigh.Load()) }

// noteRunning counts one more slot holder and raises the high-water mark.
func noteRunning() {
	n := nativeRunning.Add(1)
	for {
		high := nativeHigh.Load()
		if n <= high || nativeHigh.CompareAndSwap(high, n) {
			return
		}
	}
}

// slotPool returns the process-wide semaphore, sized from NativeSlots the
// first time it is needed.
func slotPool() chan struct{} {
	slotsOnce.Do(func() {
		slots = make(chan struct{}, max(NativeSlots, 1))
	})
	return slots
}

// Inflight counts running detached calls so their owner can wait for them
// to really finish — for example before releasing a folder's store and
// bookmark. A call is counted from the moment Acquire is entered until its
// release, so a call that has not yet obtained a slot is not missed. The
// zero value is ready to use.
type Inflight struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed when n drops to zero; nil while n is zero
}

func (i *Inflight) add() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.n == 0 {
		i.idle = make(chan struct{})
	}
	i.n++
}

func (i *Inflight) done() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n--
	if i.n == 0 {
		close(i.idle)
		i.idle = nil
	}
}

// Wait reports true once no counted call is running, waiting at most
// timeout. A negative timeout waits without limit; a zero timeout only
// checks.
func (i *Inflight) Wait(timeout time.Duration) bool {
	i.mu.Lock()
	idle := i.idle
	i.mu.Unlock()
	if idle == nil {
		return true
	}
	if timeout < 0 {
		<-idle
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-idle:
		return true
	case <-timer.C:
		return false
	}
}

type inflightKey struct{}

// WithInflight returns a context whose detached calls are counted in i.
func WithInflight(ctx context.Context, i *Inflight) context.Context {
	return context.WithValue(ctx, inflightKey{}, i)
}

func inflightFrom(ctx context.Context) *Inflight {
	i, _ := ctx.Value(inflightKey{}).(*Inflight)
	return i
}

// Acquire takes one native slot, waiting for a free one while ctx allows,
// and counts the call in the context's Inflight. release — idempotent — must
// be called when the native work has really finished, however late. On error
// (ctx.Err()) no slot is held and release is a no-op.
func Acquire(ctx context.Context) (release func(), err error) {
	in := inflightFrom(ctx)
	if in != nil {
		in.add()
	}
	fail := func(err error) (func(), error) {
		if in != nil {
			in.done()
		}
		return func() {}, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	pool := slotPool()
	select {
	case pool <- struct{}{}:
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	// A slot and a cancellation can be ready together; never start native
	// work for a context that is already done.
	if err := ctx.Err(); err != nil {
		<-pool
		return fail(err)
	}
	noteRunning()
	var once sync.Once
	return func() {
		once.Do(func() {
			// Uncounted before the slot is freed, so the count never exceeds
			// the slots held.
			nativeRunning.Add(-1)
			<-pool
			if in != nil {
				in.done()
			}
		})
	}, nil
}

// Detach runs fn — an uninterruptible native call — on its own goroutine
// after taking a slot with Acquire. It returns fn's result, or ctx.Err() as
// soon as ctx is done, in which case fn's later result is discarded. The
// slot and the Inflight count are released only when fn returns.
func Detach[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	release, err := Acquire(ctx)
	if err != nil {
		return zero, err
	}
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var r result
		func() {
			defer release()
			r.v, r.err = fn()
		}()
		ch <- r
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}
