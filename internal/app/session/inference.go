// The inference turn: the folder jobs running at once take turns, one photo
// at a time, before they call the shared inference engine.

package session

import (
	"context"
	"fmt"
	"sync/atomic"
)

// inferenceTurn is the process-wide turn of the jobs' inference work: the
// analyze stage holds it for one file (its thumbnail and CLIP embedding), a
// rating pass for one photo (its preview and crops). The engine
// (internal/app/inference) serialises every inference with a mutex that
// cannot be cancelled; with jobs running at once, a cancelled job would
// otherwise wait in that mutex for the other job's inference — or its
// creation of a session, seconds long — before its own. Waiting for the turn
// is cancellable, so a cancelled job waits at most for its own inference in
// flight (hard rule 4): only the holder of the turn is ever inside the
// engine.
var inferenceTurn = make(chan struct{}, 1)

// inferenceWaiting counts the callers waiting for the turn. For tests.
var inferenceWaiting atomic.Int64

// acquireInference waits for the inference turn and returns the func that
// gives it back. It returns ctx's error, wrapped, when ctx ends first.
func acquireInference(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session: wait for the inference turn: %w", err)
	}
	inferenceWaiting.Add(1)
	defer inferenceWaiting.Add(-1)
	select {
	case inferenceTurn <- struct{}{}:
		return func() { <-inferenceTurn }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("session: wait for the inference turn: %w", ctx.Err())
	}
}

// inferenceWaiters returns how many callers wait for the turn. For tests.
func inferenceWaiters() int { return int(inferenceWaiting.Load()) }
