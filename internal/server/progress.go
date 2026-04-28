package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/r3labs/sse/v2"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
)

// progressHub fans out a single pipeline.Reporter channel to many SSE
// subscribers. It owns one SSE stream named "progress".
type progressHub struct {
	server *sse.Server
	mu     sync.Mutex
	source <-chan pipeline.Event
}

const progressStreamID = "progress"

func newProgressHub() *progressHub {
	srv := sse.New()
	// AutoReplay is enabled so subscribers that connect after the pipeline
	// finishes see the last few events (especially the cluster=done that
	// triggers the auto-reload). EventTTL keeps the buffer bounded.
	srv.AutoReplay = true
	srv.BufferSize = 64
	srv.CreateStream(progressStreamID)
	return &progressHub{server: srv}
}

// Attach binds a pipeline event channel to the hub. Events read from
// the channel are forwarded as SSE messages until the channel closes.
func (h *progressHub) Attach(events <-chan pipeline.Event) {
	h.mu.Lock()
	h.source = events
	h.mu.Unlock()
	go h.pump(events)
}

func (h *progressHub) pump(events <-chan pipeline.Event) {
	slog.Info("progress hub pump started")
	count := 0
	for ev := range events {
		body, err := json.Marshal(ev)
		if err != nil {
			slog.Error("progress hub: marshal failed", "err", err)
			continue
		}
		// Phase transitions ("starting" / "done" / "failed") are useful
		// landmarks for humans watching the console; the per-file
		// "running" updates are too noisy and already logged at INFO by
		// the pipeline at a coarser cadence.
		if ev.Phase != "" && ev.Phase != "running" {
			slog.Info("progress",
				"stage", ev.Stage,
				"phase", ev.Phase,
				"done", ev.Done,
				"total", ev.Total,
				"failed", ev.Failed,
			)
		}
		h.server.Publish(progressStreamID, &sse.Event{Data: body})
		count++
	}
	slog.Info("progress hub pump finished", "total_events", count)
}

// ServeHTTP wraps the inner SSE server. The SSE library expects the
// stream ID via the "stream" query parameter.
func (h *progressHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slog.Debug("SSE client connected")
	q := r.URL.Query()
	q.Set("stream", progressStreamID)
	r.URL.RawQuery = q.Encode()
	h.server.ServeHTTP(w, r)
	slog.Debug("SSE client disconnected")
}
