package events

import (
	"context"
	"slices"
	"sync"
	"time"
)

// coalesceKey identifies one coalesced stream: an event name plus a
// caller-chosen key (typically a folder id or a model kind).
type coalesceKey struct {
	name string
	key  string
}

// Coalescer rate-limits events: of all values emitted for one (name, key)
// between two ticks only the latest is delivered. It is safe for concurrent
// use.
type Coalescer struct {
	out      Emitter
	interval time.Duration

	// deliverMu serialises delivery so a value taken by Flush can never
	// reach out after a newer value sent by EmitNow for the same key.
	// Lock order: deliverMu, then mu.
	deliverMu sync.Mutex

	mu      sync.Mutex
	pending map[coalesceKey]any
	order   []coalesceKey // first-emit order of the keys in pending
}

// NewCoalescer returns a Coalescer that delivers to out every interval
// (100 ms in production) once Run is started. interval must be positive.
func NewCoalescer(out Emitter, interval time.Duration) *Coalescer {
	if interval <= 0 {
		panic("events: NewCoalescer interval must be positive")
	}
	return &Coalescer{
		out:      out,
		interval: interval,
		pending:  make(map[coalesceKey]any),
	}
}

// Emit records data as the pending value for (name, key), replacing any
// earlier pending value. It is delivered on the next tick or Flush.
func (c *Coalescer) Emit(name, key string, data any) {
	k := coalesceKey{name: name, key: key}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[k]; !ok {
		c.order = append(c.order, k)
	}
	c.pending[k] = data
}

// EmitNow drops any pending value for (name, key) and delivers data
// immediately. Use it for values that must not be delayed or superseded,
// such as a final "done" progress.
func (c *Coalescer) EmitNow(name, key string, data any) {
	k := coalesceKey{name: name, key: key}
	c.deliverMu.Lock()
	defer c.deliverMu.Unlock()
	c.mu.Lock()
	if _, ok := c.pending[k]; ok {
		delete(c.pending, k)
		if i := slices.Index(c.order, k); i >= 0 {
			c.order = slices.Delete(c.order, i, i+1)
		}
	}
	c.mu.Unlock()
	c.out.Emit(name, data)
}

// Flush delivers every pending value, in the order its key was first
// emitted, and clears them.
func (c *Coalescer) Flush() {
	c.deliverMu.Lock()
	defer c.deliverMu.Unlock()
	c.mu.Lock()
	order, pending := c.order, c.pending
	c.order, c.pending = nil, make(map[coalesceKey]any, len(pending))
	c.mu.Unlock()
	for _, k := range order {
		c.out.Emit(k.name, pending[k])
	}
}

// Run flushes every interval until ctx is done, then flushes once more and
// returns. Values emitted after Run returns wait for an explicit Flush.
func (c *Coalescer) Run(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Flush()
			return
		case <-t.C:
			c.Flush()
		}
	}
}
