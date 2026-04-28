package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/config"
)

// settingsBody mirrors the JSON shape the React Settings page consumes.
type settingsBody struct {
	BucketSize         int     `json:"bucket_size"`
	Similarity         float64 `json:"similarity"`
	PicksDir           string  `json:"picks_dir"`
	AdvanceOnAction    bool    `json:"advance_on_action"`
	HideRejectedPhotos bool    `json:"hide_rejected_photos"`
	CrossDeviceMerging bool    `json:"cross_device_merging"`
}

// liveCfgOrDefaults returns the current live config, falling back to
// Defaults if nothing has been loaded yet (uncommon but possible during
// startup races).
func (s *Server) liveCfgOrDefaults() config.Config {
	if c := s.liveCfg.Load(); c != nil {
		return *c
	}
	return config.Defaults()
}

// effectivePicksDir resolves the picks export directory from the live
// machine-wide config, falling back to "picks" when unset.
func (s *Server) effectivePicksDir() string {
	cfg := s.liveCfgOrDefaults()
	if cfg.PicksDir != "" {
		return cfg.PicksDir
	}
	return "picks"
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.liveCfgOrDefaults()
	writeJSON(w, settingsBody{
		BucketSize:         cfg.BucketSizeSec,
		Similarity:         cfg.VisualClusterThreshold,
		PicksDir:           cfg.PicksDir,
		AdvanceOnAction:    cfg.AdvanceOnAction,
		HideRejectedPhotos: cfg.HideRejectedPhotos,
		CrossDeviceMerging: cfg.CrossDeviceMerging,
	})
}

type settingsForm struct {
	BucketSize         *int     `json:"bucket_size"`
	Similarity         *float64 `json:"similarity"`
	PicksDir           *string  `json:"picks_dir"`
	AdvanceOnAction    *bool    `json:"advance_on_action"`
	HideRejectedPhotos *bool    `json:"hide_rejected_photos"`
	CrossDeviceMerging *bool    `json:"cross_device_merging"`
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	var form settingsForm
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	clusteringChanged := false
	if form.BucketSize != nil {
		if *form.BucketSize < 60 {
			http.Error(w, "bucket_size must be >= 60", http.StatusBadRequest)
			return
		}
		if cfg.BucketSizeSec != *form.BucketSize {
			clusteringChanged = true
		}
		cfg.BucketSizeSec = *form.BucketSize
	}
	if form.Similarity != nil {
		if *form.Similarity <= 0 || *form.Similarity > 1 {
			http.Error(w, "similarity must be in (0,1]", http.StatusBadRequest)
			return
		}
		if cfg.VisualClusterThreshold != *form.Similarity {
			clusteringChanged = true
		}
		cfg.VisualClusterThreshold = *form.Similarity
	}
	if form.PicksDir != nil {
		if err := validatePicksDir(*form.PicksDir); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg.PicksDir = *form.PicksDir
	}
	if form.AdvanceOnAction != nil {
		cfg.AdvanceOnAction = *form.AdvanceOnAction
	}
	if form.HideRejectedPhotos != nil {
		cfg.HideRejectedPhotos = *form.HideRejectedPhotos
	}
	if form.CrossDeviceMerging != nil {
		if cfg.CrossDeviceMerging != *form.CrossDeviceMerging {
			clusteringChanged = true
		}
		cfg.CrossDeviceMerging = *form.CrossDeviceMerging
	}

	if err := config.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.ReloadConfig(); err != nil {
		_ = err
	}

	reclustered := false
	if clusteringChanged {
		if err := s.recluster(r.Context()); err != nil {
			http.Error(w, "saved settings, recluster failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		reclustered = true
	}

	writeJSON(w, map[string]any{"saved": true, "reclustered": reclustered})
}

func validatePicksDir(p string) error {
	if p == "" {
		return errors.New("picks_dir cannot be empty")
	}
	if filepath.IsAbs(p) {
		home, err := osUserHomeDir()
		if err != nil {
			return errors.New("picks_dir invalid: " + err.Error())
		}
		if !strings.HasPrefix(p, home+string(filepath.Separator)) && p != home {
			return errors.New("absolute picks_dir must be under home directory")
		}
		return nil
	}
	// Relative path: must be local (no "..", no absolute, no volume names).
	if !filepath.IsLocal(p) {
		return errors.New("picks_dir must not contain path traversal (..)")
	}
	return nil
}

// osUserHomeDir is a seam for testing. Defaults to os.UserHomeDir.
var osUserHomeDir = osUserHomeDirReal

func osUserHomeDirReal() (string, error) { return os.UserHomeDir() }
