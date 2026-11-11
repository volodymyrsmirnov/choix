package server

import (
	"log/slog"
	"net/http"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/quality"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// QualityController is the part of the background quality worker the server
// drives: it reports state for the UI and applies the Settings switch.
// *quality.Worker implements it; tests use a fake.
type QualityController interface {
	Enabled() bool
	SetEnabled(on bool)
	Status() quality.Status
	// Wake asks the worker to try again (after a failed download).
	Wake()
}

// qualityEnabled reports whether ratings should be shown at all.
func (s *Server) qualityEnabled() bool {
	return s.cfg.Quality != nil && s.cfg.Quality.Enabled()
}

// memberQuality returns the 0–10 rating to show for a cluster member, or nil.
// A rating is shown only for photos, only while the feature is on, and only
// when it was produced by the scorer this binary uses — during a re-score
// after a model or recipe change, old-scale numbers are hidden rather than
// mixed with new ones.
func memberQuality(m store.ClusterMemberView, show bool) *float64 {
	if !show || m.Kind != "photo" || !m.QualityRaw.Valid || m.QualityModel.String != local.QualityScorerID {
		return nil
	}
	v := local.QualityDisplay(m.QualityRaw.Float64)
	return &v
}

// handleQualityGet serves GET /api/quality: a snapshot of the quality
// worker (model download progress, scoring progress, failure text). The UI
// polls it while something is in flight. It is a snapshot rather than SSE
// events on purpose: the progress stream replays its history to every new
// page and its consumers treat any event as "scan running".
func (s *Server) handleQualityGet(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Quality == nil {
		writeJSON(w, quality.Status{State: quality.StateDisabled})
		return
	}
	writeJSON(w, s.cfg.Quality.Status())
}

// handleQualityRetry serves POST /api/quality/retry: the Settings page's
// "Retry" after a failed model download. It only wakes the worker; when the
// feature is off or nothing failed the wake-up is harmless.
func (s *Server) handleQualityRetry(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Quality == nil {
		http.Error(w, "quality rating unavailable", http.StatusNotFound)
		return
	}
	slog.Info("quality retry requested")
	s.cfg.Quality.Wake()
	writeJSON(w, s.cfg.Quality.Status())
}
