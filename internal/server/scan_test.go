package server

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
)

type fakePipeline struct {
	starts   atomic.Int64
	stops    atomic.Int64
	running  atomic.Bool
	emitChan chan pipeline.Event
}

func newFakePipeline() *fakePipeline {
	return &fakePipeline{emitChan: make(chan pipeline.Event, 4)}
}

func (f *fakePipeline) Start(ctx context.Context) (<-chan pipeline.Event, error) {
	f.starts.Add(1)
	f.running.Store(true)
	return f.emitChan, nil
}

func (f *fakePipeline) Stop() {
	f.stops.Add(1)
	f.running.Store(false)
}

func (f *fakePipeline) Running() bool { return f.running.Load() }

func TestScanPostStartsPipeline(t *testing.T) {
	env := newTestServer(t)
	fp := newFakePipeline()
	env.server.pipe = fp

	resp := postJSON(t, env, "/api/scan", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d, want 202", resp.StatusCode)
	}
	if fp.starts.Load() != 1 {
		t.Errorf("starts = %d, want 1", fp.starts.Load())
	}
}

func TestScanPostIsIdempotentWhenRunning(t *testing.T) {
	env := newTestServer(t)
	fp := newFakePipeline()
	env.server.pipe = fp

	postJSON(t, env, "/api/scan", map[string]any{}).Body.Close()
	resp := postJSON(t, env, "/api/scan", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second start status = %d, want 409", resp.StatusCode)
	}
	if fp.starts.Load() != 1 {
		t.Errorf("starts = %d, want 1 (second call should be rejected)", fp.starts.Load())
	}
}

func TestScanReturns503WhenNoPipelineConfigured(t *testing.T) {
	env := newTestServer(t)
	env.server.pipe = nil
	resp := postJSON(t, env, "/api/scan", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", resp.StatusCode)
	}
	_ = time.Second
}

// racyPipeline simulates the race where Running() returns false but Start()
// returns ErrPipelineAlreadyRunning because another goroutine won the lock.
type racyPipeline struct {
	starts atomic.Int64
}

func (r *racyPipeline) Start(_ context.Context) (<-chan pipeline.Event, error) {
	r.starts.Add(1)
	return nil, fmt.Errorf("lost race: %w", ErrPipelineAlreadyRunning)
}
func (r *racyPipeline) Stop()         {}
func (r *racyPipeline) Running() bool { return false }

func TestScanPostRacingStartReturns409(t *testing.T) {
	env := newTestServer(t)
	rp := &racyPipeline{}
	env.server.pipe = rp

	resp := postJSON(t, env, "/api/scan", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status %d, want 409 when Start returns ErrPipelineAlreadyRunning", resp.StatusCode)
	}
}
