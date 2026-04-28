package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ErrPipelineAlreadyRunning is returned by PipelineRunner.Start when a
// pipeline is already in progress. Handlers map this to 409 Conflict.
var ErrPipelineAlreadyRunning = errors.New("pipeline already running")

func (s *Server) handleScanPost(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		http.Error(w, "pipeline not configured", http.StatusServiceUnavailable)
		return
	}
	if s.pipe.Running() {
		http.Error(w, "scan already running", http.StatusConflict)
		return
	}
	events, err := s.pipe.Start(context.Background())
	if err != nil {
		if errors.Is(err, ErrPipelineAlreadyRunning) {
			http.Error(w, "scan already running", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.progress.Attach(events)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}
