// Package opgate is the admission gate in front of the service bindings:
// every binding enters it before doing work and leaves when done, so that
// on quit Close can refuse new operations and wait, up to a deadline, for
// the ones already running.
package opgate

import (
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
)

// ClosedMessage is the message of the error Enter returns after Close.
const ClosedMessage = "Choix is quitting."

// Gate admits operations until it is closed. The zero value is an open
// gate; a Gate must not be copied after first use. It is safe for
// concurrent use.
type Gate struct {
	mu     sync.Mutex
	closed bool
	inside int
	// drained is made by the first Close that has to wait and closed when
	// the last entry leaves.
	drained chan struct{}
}

// Enter admits one operation and returns the func that ends it; calling
// leave more than once has no further effect. After Close, Enter admits
// nothing and returns an unavailable *apperr.Error and a nil leave.
func (g *Gate) Enter() (leave func(), err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, apperr.New(apperr.CodeUnavailable, ClosedMessage)
	}
	g.inside++
	var once sync.Once
	return func() { once.Do(g.leave) }, nil
}

func (g *Gate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inside--
	if g.inside == 0 && g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
}

// Close refuses every later Enter and waits up to wait for the operations
// already inside to leave. It reports whether none is left. It may be
// called more than once, and concurrently.
func (g *Gate) Close(wait time.Duration) bool {
	g.mu.Lock()
	g.closed = true
	if g.inside == 0 {
		g.mu.Unlock()
		return true
	}
	if g.drained == nil {
		g.drained = make(chan struct{})
	}
	drained := g.drained
	g.mu.Unlock()

	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-drained:
		return true
	case <-timer.C:
		return false
	}
}
