package main

import (
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// fakeSink records what reaches the Wails event bus.
type fakeSink struct {
	mu    sync.Mutex
	names []string
	data  [][]any
}

func (s *fakeSink) Emit(name string, data ...any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names = append(s.names, name)
	s.data = append(s.data, data)
	return false
}

func (s *fakeSink) calls() ([]string, [][]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...), append([][]any(nil), s.data...)
}

var _ events.Emitter = (*emitter)(nil)

func TestEmitter_ForwardsOneValue(t *testing.T) {
	sink := &fakeSink{}
	e := newEmitter(sink)
	ev := events.ScanStateEvent{FolderID: "0123456789abcdef", State: "scanning"}
	e.Emit(events.ScanState, ev)
	e.Emit(events.MenuAction, nil)

	names, data := sink.calls()
	if len(names) != 2 || names[0] != events.ScanState || names[1] != events.MenuAction {
		t.Fatalf("names = %v; want [%s %s]", names, events.ScanState, events.MenuAction)
	}
	// One argument each: Wails takes a single argument as the event data
	// and would wrap several in a slice.
	if len(data[0]) != 1 || data[0][0] != ev {
		t.Errorf("data = %#v; want the event value alone", data[0])
	}
	if len(data[1]) != 1 || data[1][0] != nil {
		t.Errorf("nil data = %#v; want one nil argument", data[1])
	}
}

func TestEmitter_NoOpAfterClose(t *testing.T) {
	sink := &fakeSink{}
	e := newEmitter(sink)
	e.Emit(events.LibraryChanged, events.LibraryChangedEvent{Reason: "added"})
	e.Close()
	e.Emit(events.LibraryChanged, events.LibraryChangedEvent{Reason: "removed"})
	e.Close() // idempotent
	e.Emit(events.ScanState, events.ScanStateEvent{State: "ready"})

	names, data := sink.calls()
	if len(names) != 1 {
		t.Fatalf("%d events reached the sink (%v); want only the one before Close", len(names), names)
	}
	if got := data[0][0].(events.LibraryChangedEvent).Reason; got != "added" {
		t.Errorf("reason = %q; want added", got)
	}
}

// TestEmitter_CloseWhileEmitting: emits racing Close either reach the sink
// before Close returns or not at all.
func TestEmitter_CloseWhileEmitting(t *testing.T) {
	sink := &fakeSink{}
	e := newEmitter(sink)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				e.Emit(events.FolderChanged, events.FolderChangedEvent{Reason: "clusters"})
			}
		})
	}
	e.Close()
	names, _ := sink.calls()
	atClose := len(names)
	wg.Wait()
	names, _ = sink.calls()
	if len(names) != atClose {
		t.Fatalf("%d events reached the sink after Close returned", len(names)-atClose)
	}
}
