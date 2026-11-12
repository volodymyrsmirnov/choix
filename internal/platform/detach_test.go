package platform_test

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// testSlots is the process-wide slot count these tests run with. NativeSlots
// is read once, at the first Acquire, so it is set before any test runs.
const testSlots = 2

func TestMain(m *testing.M) {
	platform.NativeSlots = testSlots
	os.Exit(m.Run())
}

// detachAsync starts Detach on its own goroutine and returns a channel that
// receives its error.
func detachAsync(ctx context.Context, fn func() (int, error)) <-chan error {
	errc := make(chan error, 1)
	go func() {
		_, err := platform.Detach(ctx, fn)
		errc <- err
	}()
	return errc
}

// waitErr waits for an error from errc, failing the test after a second.
func waitErr(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(time.Second):
		t.Fatal("Detach did not return within 1s")
		return nil
	}
}

// waitIdle fails the test unless every call counted in in has finished
// within a second. Tests share the process-wide slots, so each one leaves
// none of them held.
func waitIdle(t *testing.T, in *platform.Inflight) {
	t.Helper()
	if !in.Wait(time.Second) {
		t.Fatal("detached calls still running after 1s")
	}
}

func TestDetach_ReturnsResult(t *testing.T) {
	in := &platform.Inflight{}
	ctx := platform.WithInflight(context.Background(), in)

	got, err := platform.Detach(ctx, func() (string, error) { return "ok", nil })
	if err != nil || got != "ok" {
		t.Fatalf("Detach = %q, %v; want \"ok\", nil", got, err)
	}

	wantErr := errors.New("boom")
	if _, err := platform.Detach(ctx, func() (int, error) { return 0, wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("Detach error = %v; want %v", err, wantErr)
	}
	if !in.Wait(0) {
		t.Fatal("Inflight not idle after Detach returned its result")
	}
}

func TestDetach_SlotsBoundAbandonedCalls(t *testing.T) {
	in := &platform.Inflight{}
	base := platform.WithInflight(context.Background(), in)

	// Two calls whose fn blocks are abandoned: their contexts are cancelled
	// and Detach returns, but the native call keeps its slot.
	releases := make([]chan struct{}, testSlots)
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	t.Cleanup(func() {
		for _, r := range releases {
			select {
			case <-r:
			default:
				close(r)
			}
		}
		waitIdle(t, in)
	})
	for i := range testSlots {
		ctx, cancel := context.WithCancel(base)
		started := make(chan struct{})
		rel := releases[i]
		errc := detachAsync(ctx, func() (int, error) {
			close(started)
			<-rel
			return i, nil
		})
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("call %d did not start", i)
		}
		cancel()
		if err := waitErr(t, errc); !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned call %d: err = %v; want context.Canceled", i, err)
		}
	}

	// A third call finds no free slot: its fn does not start, and cancelling
	// it returns without ever running fn.
	var ran atomic.Bool
	ctx3, cancel3 := context.WithCancel(base)
	errc3 := detachAsync(ctx3, func() (int, error) {
		ran.Store(true)
		return 3, nil
	})
	select {
	case err := <-errc3:
		t.Fatalf("third call returned %v while every slot was held", err)
	case <-time.After(50 * time.Millisecond):
	}
	if ran.Load() {
		t.Fatal("third call ran fn while every slot was held")
	}
	cancel3()
	if err := waitErr(t, errc3); !errors.Is(err, context.Canceled) {
		t.Fatalf("third call: err = %v; want context.Canceled", err)
	}
	if ran.Load() {
		t.Fatal("cancelled third call ran fn")
	}

	// When one abandoned call returns, its slot is free and a fourth call runs.
	close(releases[0])
	ctx4, cancel4 := context.WithTimeout(base, time.Second)
	defer cancel4()
	got, err := platform.Detach(ctx4, func() (int, error) { return 4, nil })
	if err != nil || got != 4 {
		t.Fatalf("fourth call = %d, %v; want 4, nil", got, err)
	}
}

func TestAcquire_ReleaseIsIdempotent(t *testing.T) {
	in := &platform.Inflight{}
	ctx := platform.WithInflight(context.Background(), in)

	release, err := platform.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if in.Wait(0) {
		t.Fatal("Inflight idle while a slot is held")
	}
	release()
	release()
	if !in.Wait(0) {
		t.Fatal("Inflight not idle after release")
	}

	// A double release must not free a second slot: exactly testSlots more
	// acquisitions succeed, the next one waits.
	var held []func()
	t.Cleanup(func() {
		for _, r := range held {
			r()
		}
		waitIdle(t, in)
	})
	for i := range testSlots {
		r, err := platform.Acquire(ctx)
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		held = append(held, r)
	}
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := platform.Acquire(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire with every slot held: err = %v; want context.DeadlineExceeded", err)
	}
	// Nor must it have pushed the Inflight count below the held calls.
	if in.Wait(10 * time.Millisecond) {
		t.Fatal("Inflight idle while slots are held")
	}
}

func TestDetach_ReturnsOnCancelWhileRunning(t *testing.T) {
	in := &platform.Inflight{}
	ctx, cancel := context.WithCancel(platform.WithInflight(context.Background(), in))
	block := make(chan struct{})
	started := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-block:
		default:
			close(block)
		}
		waitIdle(t, in)
	})

	errc := detachAsync(ctx, func() (int, error) {
		close(started)
		<-block
		return 1, nil
	})
	<-started
	begin := time.Now()
	cancel()
	err := waitErr(t, errc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if d := time.Since(begin); d >= 100*time.Millisecond {
		t.Fatalf("Detach returned %v after cancel; want < 100ms", d)
	}
	if in.Wait(10 * time.Millisecond) {
		t.Fatal("Inflight idle while the abandoned fn still runs")
	}
	close(block)
	if !in.Wait(time.Second) {
		t.Fatal("Inflight not idle after the abandoned fn returned")
	}
}

func TestDetach_WithoutInflightStillCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	block := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-block:
		default:
			close(block)
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("abandoned fn did not return")
		}
	})

	errc := detachAsync(ctx, func() (int, error) {
		defer close(finished)
		close(started)
		<-block
		return 1, nil
	})
	<-started
	cancel()
	if err := waitErr(t, errc); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	close(block)
	<-finished
	// The slot is released once fn has returned; a new call gets one.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	for range testSlots {
		release, err := platform.Acquire(ctx2)
		if err != nil {
			t.Fatalf("Acquire after the abandoned fn returned: %v", err)
		}
		defer release()
	}
}

func TestInflight_WaitNegativeBlocksUntilDone(t *testing.T) {
	in := &platform.Inflight{}
	if !in.Wait(-1) {
		t.Fatal("Wait(-1) on an idle Inflight = false")
	}

	release, err := platform.Acquire(platform.WithInflight(context.Background(), in))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(release)
	done := make(chan bool, 1)
	go func() { done <- in.Wait(-1) }()
	select {
	case <-done:
		t.Fatal("Wait(-1) returned while a call was running")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Wait(-1) = false")
		}
	case <-time.After(time.Second):
		t.Fatal("Wait(-1) did not return after release")
	}
}

func TestNativeCounters_TrackHeldSlots(t *testing.T) {
	in := &platform.Inflight{}
	ctx := platform.WithInflight(context.Background(), in)
	if got := platform.NativeRunning(); got != 0 {
		t.Fatalf("NativeRunning before any call = %d; want 0 (tests leave no slot held)", got)
	}

	var held []func()
	t.Cleanup(func() {
		for _, r := range held {
			r()
		}
		waitIdle(t, in)
	})
	for i := range testSlots {
		r, err := platform.Acquire(ctx)
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		held = append(held, r)
		if got := platform.NativeRunning(); got != i+1 {
			t.Fatalf("NativeRunning with %d slots held = %d", i+1, got)
		}
	}
	if got := platform.NativeHighWater(); got != testSlots {
		t.Fatalf("NativeHighWater with every slot held = %d; want %d", got, testSlots)
	}

	// A refused acquisition is not counted.
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := platform.Acquire(short); err == nil {
		t.Fatal("Acquire succeeded with every slot held")
	}
	if got := platform.NativeRunning(); got != testSlots {
		t.Fatalf("NativeRunning after a refused Acquire = %d; want %d", got, testSlots)
	}

	// Releases — a double one included — bring the count back to zero, never
	// below; the high-water mark stays.
	for _, r := range held {
		r()
		r()
	}
	held = nil
	if got := platform.NativeRunning(); got != 0 {
		t.Fatalf("NativeRunning after every release = %d; want 0", got)
	}
	if got := platform.NativeHighWater(); got != testSlots {
		t.Fatalf("NativeHighWater after the releases = %d; want %d", got, testSlots)
	}

	// A call whose context is already done takes no slot and is not counted.
	done, cancelDone := context.WithCancel(ctx)
	cancelDone()
	if _, err := platform.Acquire(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire with a cancelled context: err = %v; want context.Canceled", err)
	}
	if got := platform.NativeRunning(); got != 0 {
		t.Fatalf("NativeRunning after a cancelled Acquire = %d; want 0", got)
	}
}

func TestNativeCounters_NeverAboveSlotsUnderContention(t *testing.T) {
	in := &platform.Inflight{}
	ctx := platform.WithInflight(context.Background(), in)

	const calls = 32
	var outside atomic.Int32
	errc := make(chan error, calls)
	for range calls {
		go func() {
			_, err := platform.Detach(ctx, func() (int, error) {
				if n := platform.NativeRunning(); n < 1 || n > testSlots {
					outside.Add(1)
				}
				time.Sleep(time.Millisecond)
				return 0, nil
			})
			errc <- err
		}()
	}
	for range calls {
		if err := waitErr(t, errc); err != nil {
			t.Fatalf("Detach: %v", err)
		}
	}
	waitIdle(t, in)
	if n := outside.Load(); n != 0 {
		t.Fatalf("%d calls saw NativeRunning outside 1..%d", n, testSlots)
	}
	if got := platform.NativeHighWater(); got < 1 || got > testSlots {
		t.Fatalf("NativeHighWater = %d; want 1..%d", got, testSlots)
	}
	if got := platform.NativeRunning(); got != 0 {
		t.Fatalf("NativeRunning after every call returned = %d; want 0", got)
	}
}
