package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/group"
)

// recluster rebuilds every (device, time-bucket) cluster from the current
// analyzed files. It's safe to call at any time: per-cluster state (picks,
// exported_path) lives in separate tables and is unaffected. Bucket width,
// CLIP similarity threshold, and the cross-device merge toggle are read
// from the live config so the operation reflects whatever the user just
// changed in Settings.
func (s *Server) recluster(ctx context.Context) error {
	cfg := s.liveCfg.Load()
	bucketSec := 600
	threshold := 0.92
	crossDevice := false
	if cfg != nil {
		if cfg.BucketSizeSec > 0 {
			bucketSec = cfg.BucketSizeSec
		}
		if cfg.VisualClusterThreshold > 0 {
			threshold = cfg.VisualClusterThreshold
		}
		crossDevice = cfg.CrossDeviceMerging
	}
	g := group.NewGrouper(s.cfg.Store, int64(bucketSec), threshold, crossDevice)
	if err := g.RebuildAll(ctx); err != nil {
		return fmt.Errorf("rebuild: %w", err)
	}
	return nil
}

func (s *Server) handleReclusterPost(w http.ResponseWriter, r *http.Request) {
	slog.Info("recluster requested")
	start := time.Now()
	if err := s.recluster(r.Context()); err != nil {
		slog.Error("recluster failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("recluster done", "elapsed_ms", time.Since(start).Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"reclustered":true}`))
}
