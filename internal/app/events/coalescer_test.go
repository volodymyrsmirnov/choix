package events

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestCoalescer_LatestWins(t *testing.T) {
	var r Recorder
	c := NewCoalescer(&r, time.Hour)
	for i := range 100 {
		c.Emit(ScanProgress, "f1", i)
	}
	if got := r.Events(ScanProgress); len(got) != 0 {
		t.Fatalf("delivered before Flush: %v", got)
	}
	c.Flush()
	if got := r.Events(ScanProgress); !reflect.DeepEqual(got, []any{99}) {
		t.Fatalf("events = %v, want [99]", got)
	}
	c.Flush()
	if got := r.Events(ScanProgress); len(got) != 1 {
		t.Fatalf("second Flush re-delivered: %v", got)
	}
}

func TestCoalescer_KeysAreIndependent(t *testing.T) {
	var r Recorder
	c := NewCoalescer(&r, time.Hour)
	c.Emit(ScanProgress, "f1", "a1")
	c.Emit(ScanProgress, "f2", "b1")
	c.Emit(ScanProgress, "f1", "a2")
	c.Emit(ModelsProgress, "f1", "m1") // same key, other name
	c.Flush()

	if got := r.Events(ScanProgress); !reflect.DeepEqual(got, []any{"a2", "b1"}) {
		t.Errorf("scan events = %v, want [a2 b1]", got)
	}
	if got := r.Events(ModelsProgress); !reflect.DeepEqual(got, []any{"m1"}) {
		t.Errorf("model events = %v, want [m1]", got)
	}
}

func TestCoalescer_EmitNowBypassesAndClearsPending(t *testing.T) {
	var r Recorder
	c := NewCoalescer(&r, time.Hour)
	c.Emit(ScanProgress, "f1", "pending")
	c.Emit(ScanProgress, "f2", "other")
	c.EmitNow(ScanProgress, "f1", "final")

	if got := r.Events(ScanProgress); !reflect.DeepEqual(got, []any{"final"}) {
		t.Fatalf("after EmitNow events = %v, want [final]", got)
	}
	c.Flush()
	if got := r.Events(ScanProgress); !reflect.DeepEqual(got, []any{"final", "other"}) {
		t.Fatalf("after Flush events = %v, want [final other] (stale f1 must be dropped)", got)
	}
}

func TestCoalescer_RateBound(t *testing.T) {
	var r Recorder
	c := NewCoalescer(&r, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	// One key emitted every millisecond for one second.
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.After(time.Second)
	for i, emitting := 0, true; emitting; i++ {
		select {
		case <-tick.C:
			c.Emit(ScanProgress, "f1", i)
		case <-deadline:
			emitting = false
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if n := len(r.Events(ScanProgress)); n < 8 || n > 12 {
		t.Fatalf("delivered %d events in 1 s at 100 ms, want 8..12", n)
	}
}

func TestCoalescer_FlushesOnStop(t *testing.T) {
	var r Recorder
	c := NewCoalescer(&r, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	c.Emit(ScanProgress, "f1", "last")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if got := r.Events(ScanProgress); !reflect.DeepEqual(got, []any{"last"}) {
		t.Fatalf("events = %v, want [last]", got)
	}
}
