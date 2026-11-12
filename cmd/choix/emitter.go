package main

import (
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

var _ events.Emitter = (*emitter)(nil)

// eventSink is the Wails event bus the emitter writes to: the application's
// *application.EventManager (app.Event). Emit sends data as the event's
// payload when it is given one argument, which is how emitter calls it.
type eventSink interface {
	Emit(name string, data ...any) bool
}

// emitter is the runtime's events.Emitter over the Wails event bus. The
// runtime hands it Deps.Emit and calls Close at shutdown step 2, once it has
// stopped emitting; every Emit after Close is a no-op, so nothing reaches
// the frontend while the application tears down.
type emitter struct {
	// mu is held for reading across a sink call, so Close waits for an Emit
	// in flight and none reaches the sink after Close returns.
	mu     sync.RWMutex
	closed bool
	sink   eventSink
}

// newEmitter returns an emitter writing to sink.
func newEmitter(sink eventSink) *emitter {
	return &emitter{sink: sink}
}

// Emit sends one event with data as its payload. data must be a value of
// the type the name was registered with (events.go), never a pointer to it:
// Wails matches the type exactly and drops a mismatch.
func (e *emitter) Emit(name string, data any) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return
	}
	e.sink.Emit(name, data)
}

// Close makes every later Emit a no-op. It waits for an Emit in flight and
// may be called more than once.
func (e *emitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}
