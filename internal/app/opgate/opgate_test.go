package opgate

import (
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
)

// longWait is far longer than any test runs: a Close given it returns only
// because nothing is left inside.
const longWait = time.Hour

func TestEnter_BeforeCloseSucceeds(t *testing.T) {
	var g Gate
	leave, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	leave()
	if !g.Close(0) {
		t.Error("Close after every entry left = false, want true")
	}
}

func TestEnter_AfterCloseIsUnavailable(t *testing.T) {
	var g Gate
	if !g.Close(0) {
		t.Fatal("Close of an empty gate = false, want true")
	}
	leave, err := g.Enter()
	if err == nil {
		t.Fatal("Enter after Close succeeded, want an error")
	}
	if leave != nil {
		t.Error("Enter after Close returned a leave func, want nil")
	}
	if e := apperr.From(err); e.Code != apperr.CodeUnavailable {
		t.Errorf("code = %q, want %q", e.Code, apperr.CodeUnavailable)
	}
}

func TestClose_WaitsForInFlight(t *testing.T) {
	var g Gate
	leave, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}

	done := make(chan bool, 1)
	go func() { done <- g.Close(longWait) }()

	// Once Enter is refused, Close has begun; it must still be waiting.
	for {
		l, err := g.Enter()
		if err != nil {
			break
		}
		l()
		time.Sleep(time.Millisecond)
	}
	select {
	case r := <-done:
		t.Fatalf("Close returned %v while an entry was still inside", r)
	default:
	}

	leave()
	if r := <-done; !r {
		t.Error("Close = false after the entry left, want true")
	}
}

func TestClose_TimesOut(t *testing.T) {
	var g Gate
	if _, err := g.Enter(); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if g.Close(5 * time.Millisecond) {
		t.Error("Close = true with an entry that never leaves, want false")
	}
	// A later Close still reports the entry.
	if g.Close(0) {
		t.Error("second Close = true, want false")
	}
}

func TestLeave_IsIdempotent(t *testing.T) {
	var g Gate
	first, _ := g.Enter()
	second, _ := g.Enter()
	first()
	first() // must not count as second leaving
	if g.Close(0) {
		t.Fatal("Close = true while the second entry is inside")
	}
	second()
	if !g.Close(0) {
		t.Error("Close = false after both entries left")
	}
}

func TestClose_ConcurrentClosersAllSeeTheDrain(t *testing.T) {
	var g Gate
	leave, _ := g.Enter()
	results := make(chan bool, 3)
	for range 3 {
		go func() { results <- g.Close(longWait) }()
	}
	for {
		l, err := g.Enter()
		if err != nil {
			break
		}
		l()
		time.Sleep(time.Millisecond)
	}
	leave()
	for range 3 {
		if !<-results {
			t.Error("a concurrent Close = false, want true")
		}
	}
}
