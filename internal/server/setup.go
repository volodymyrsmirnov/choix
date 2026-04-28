package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/firstrun"
)

// pathResolver implements firstrun.Resolver against the filesystem PATH
// the user invoked choix from. We deliberately only handle PATH lookups
// here — the wizard's tool-install branch is unreachable for choix
// because main aborts startup if exiftool/ffmpeg aren't already
// installed via brew (see CLAUDE.md). Detect is still allowed to call
// us so the wizard can show the right "missing tool" copy.
type pathResolver struct{}

func (pathResolver) Resolve(_ context.Context, name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return p, nil
}

// installerStateDetector packages a *local.Installer (which satisfies
// both firstrun.ModelStore via Has and firstrun.ModelInstaller via
// Install) plus a Resolver into a single firstrun.StateDetector.
type installerStateDetector struct {
	r         firstrun.Resolver
	installer *local.Installer
}

func (d *installerStateDetector) Detect(ctx context.Context) (firstrun.SetupState, error) {
	return firstrun.Detect(ctx, d.r, d.installer, config.Config{})
}

// handleSetupState serves GET /api/setup/state. Returns the same shape
// the wizard's HTML template uses, but as JSON so the SPA (or curl) can
// poll without parsing markup.
func (s *Server) handleSetupState(w http.ResponseWriter, r *http.Request) {
	if s.detector == nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.detector.Detect(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	models := make([]string, 0, len(st.RequiresModels))
	for _, k := range st.RequiresModels {
		models = append(models, k.String())
	}
	writeJSON(w, map[string]any{
		"ready":             st.IsReady(),
		"requires_exiftool": st.RequiresExifTool,
		"requires_ffmpeg":   st.RequiresFFmpeg,
		"requires_models":   models,
	})
}

// handleSetupFinalize serves POST /api/setup/finalize. The wizard hits
// this after a fresh model install so files that already passed
// metadata + thumb (and therefore never get revisited by a normal
// Resume) get their CLIP embeddings filled in. We bump every analyzed
// row whose ai_signals.clip_embedding IS NULL back to scan_status='thumb'
// so the analyze stage picks them up, then start the pipeline.
//
// Returns 200 once the pipeline has been kicked off (it runs in the
// background; clients watch /api/progress for progress).
func (s *Server) handleSetupFinalize(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	if _, err := s.cfg.Store.Files().BumpAnalyzedMissingEmbedding(); err != nil {
		http.Error(w, "bump stale analyzed rows: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.pipe != nil && !s.pipe.Running() {
		// The pipeline must outlive this HTTP request — using r.Context()
		// here cancels the work the moment the response flushes. Use the
		// background context the CLI passed in at startup; fall back to
		// context.Background() for tests / minimal embeddings.
		bg := s.cfg.BackgroundContext
		if bg == nil {
			bg = context.Background()
		}
		events, err := s.pipe.Start(bg)
		if err != nil && !errors.Is(err, ErrPipelineAlreadyRunning) {
			http.Error(w, "start pipeline: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err == nil {
			s.AttachProgress(events)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"finalized": true})
}
