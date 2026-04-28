package pipeline

import (
	"sync"
	"testing"
	"time"
)

func TestReporterUpdateBuffered(t *testing.T) {
	ch := make(chan Progress, 16)
	r := NewReporter(ch)

	for i := 0; i < 16; i++ {
		r.Update(Progress{Stage: "metadata", Done: i, Total: 100, Phase: "running"})
	}
	if got := len(ch); got != 16 {
		t.Errorf("len(ch) = %d, want 16", got)
	}
}

func TestReporterUpdateNonBlockingDropsOldest(t *testing.T) {
	ch := make(chan Progress, 4)
	r := NewReporter(ch)

	// Push more than capacity. The reporter must NOT block — it drops the
	// oldest event when the channel is full.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			r.Update(Progress{Stage: "thumb", Done: i, Total: 100})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reporter blocked under backpressure")
	}
	// Channel should still hold capacity events (most recent ones).
	if got := len(ch); got != 4 {
		t.Errorf("len(ch) = %d, want 4", got)
	}
	// Drain and verify the events we kept are recent (Done > some threshold).
	min := -1
	for len(ch) > 0 {
		ev := <-ch
		if min == -1 || ev.Done < min {
			min = ev.Done
		}
	}
	if min < 90 {
		t.Errorf("oldest retained event Done = %d, want >= 90 (oldest dropped)", min)
	}
}

func TestReporterNilChannelIsNoop(t *testing.T) {
	// Nil channel must not panic — used when Reporter is unset.
	r := NewReporter(nil)
	r.Update(Progress{Stage: "x"}) // must not panic
}

func TestReporterConcurrentSafe(t *testing.T) {
	ch := make(chan Progress, 16)
	r := NewReporter(ch)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.Update(Progress{Stage: "metadata", Done: j})
			}
		}()
	}
	wg.Wait()
}
