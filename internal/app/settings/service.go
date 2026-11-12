package settings

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/config"
)

// Body is the settings as the frontend sees them (the design's
// SettingsBody).
type Body struct {
	BucketSize         int     `json:"bucket_size"`
	Similarity         float64 `json:"similarity"`
	PicksDir           string  `json:"picks_dir"`
	AdvanceOnAction    bool    `json:"advance_on_action"`
	HideRejectedPhotos bool    `json:"hide_rejected_photos"`
	CrossDeviceMerging bool    `json:"cross_device_merging"`
	QualityRating      bool    `json:"quality_rating"`
	VisualGrouping     bool    `json:"visual_grouping"`
}

func bodyOf(c config.Config) Body {
	return Body{
		BucketSize:         c.BucketSizeSec,
		Similarity:         c.VisualSimilarity,
		PicksDir:           c.PicksDir,
		AdvanceOnAction:    c.AdvanceOnAction,
		HideRejectedPhotos: c.HideRejectedPhotos,
		CrossDeviceMerging: c.CrossDeviceMerging,
		QualityRating:      c.QualityRating,
		VisualGrouping:     c.VisualGrouping,
	}
}

// SaveResult is the outcome of Save: Saved once the settings are in
// config.toml, Reclustered when the open folder was re-clustered because a
// clustering setting changed.
type SaveResult struct {
	Saved       bool `json:"saved"`
	Reclustered bool `json:"reclustered"`
}

// Hooks are what Save calls besides the Store.
type Hooks struct {
	// Recluster re-clusters the open folder with the current settings and
	// reports false when no folder is open. After an error it is called
	// again by every Save until a call succeeds. nil: nothing is
	// re-clustered.
	Recluster func() (bool, error)
}

// Service is the settings binding. Every exported method is a frontend
// binding; nothing else may be exported on it.
type Service struct {
	store *Store
	hooks Hooks
	gate  *opgate.Gate

	// reclusterMu serialises calls of Hooks.Recluster and the updates of
	// owed that follow them.
	reclusterMu sync.Mutex
	// owed is set while a re-cluster is due but has not succeeded: a failed
	// call leaves it set, and every later Save calls the hook again until
	// one succeeds. In memory only: a folder that is not open is brought up
	// to date by the session when it is opened (its stored ClusterParams
	// differ from the settings), which is not the service's job.
	owed atomic.Bool
}

// NewService returns the settings binding over s. Every call enters g.
func NewService(s *Store, h Hooks, g *opgate.Gate) *Service {
	return &Service{store: s, hooks: h, gate: g}
}

// Get returns the current settings.
func (s *Service) Get() (Body, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return Body{}, err
	}
	defer leave()
	return bodyOf(s.store.Get()), nil
}

// Save applies p and saves it to config.toml. A refused field is an invalid
// error naming the field and a reason code. When the clustering settings
// changed (session.ClusterParams), or an earlier re-cluster failed, the open
// folder is re-clustered. A re-cluster failure never undoes the save: Save
// returns the hook's error (mapped by apperr) with the settings already
// saved, and the re-cluster stays owed, so the next Save — even of the same
// values — tries again.
func (s *Service) Save(p Patch) (SaveResult, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return SaveResult{}, err
	}
	defer leave()

	old, cur, err := s.store.Apply(p)
	if err != nil {
		return SaveResult{}, apperr.From(err)
	}
	res := SaveResult{Saved: true}
	changed := session.ClusterParams(old) != session.ClusterParams(cur)
	if s.hooks.Recluster == nil || (!changed && !s.owed.Load()) {
		return res, nil
	}

	s.reclusterMu.Lock()
	defer s.reclusterMu.Unlock()
	if !changed && !s.owed.Load() {
		return res, nil // a concurrent Save has just paid it
	}
	s.owed.Store(true)
	ok, err := s.hooks.Recluster()
	if err != nil {
		slog.Warn("settings: re-cluster after a settings change failed", "err", err)
		return res, apperr.From(err)
	}
	// Done, or no folder is open (false): either way nothing is owed.
	s.owed.Store(false)
	res.Reclustered = ok
	return res, nil
}
