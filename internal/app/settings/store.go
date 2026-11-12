// Package settings holds the machine-wide settings: Store is the live copy
// of config.toml (in the app's data directory) that the rest of the app
// reads and subscribes to, and Service is the frontend binding that reads
// and saves it.
package settings

import (
	"errors"
	"fmt"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
)

// minBucketSize is the smallest time-bucket gap, in seconds, Save accepts.
const minBucketSize = 60

// Validation reason codes (apperr.Error.Reason) besides those of
// picks.ValidateDir ("empty", "absolute", "traversal", "reserved"). The
// frontend shows its own copy for each.
const (
	reasonMin   = "min"   // bucket_size below minBucketSize
	reasonRange = "range" // similarity outside (0, 1]
)

// Fields of a refused patch (apperr.Error.Field): the JSON names of Body.
const (
	fieldBucketSize = "bucket_size"
	fieldSimilarity = "similarity"
	fieldPicksDir   = "picks_dir"
)

// Patch is a partial settings change: a nil field is left as it is. The
// JSON names are those of Body.
type Patch struct {
	BucketSize         *int     `json:"bucket_size,omitempty"`
	Similarity         *float64 `json:"similarity,omitempty"`
	PicksDir           *string  `json:"picks_dir,omitempty"`
	AdvanceOnAction    *bool    `json:"advance_on_action,omitempty"`
	HideRejectedPhotos *bool    `json:"hide_rejected_photos,omitempty"`
	CrossDeviceMerging *bool    `json:"cross_device_merging,omitempty"`
	QualityRating      *bool    `json:"quality_rating,omitempty"`
	VisualGrouping     *bool    `json:"visual_grouping,omitempty"`
}

// validate checks the fields p sets. A field p leaves nil is not checked,
// so a value config.toml already holds never blocks saving another one.
func (p Patch) validate() error {
	if p.BucketSize != nil && *p.BucketSize < minBucketSize {
		return apperr.Invalid(fieldBucketSize, reasonMin,
			fmt.Sprintf("settings: bucket_size %d is below %d seconds", *p.BucketSize, minBucketSize))
	}
	// Written so that NaN is refused too.
	if p.Similarity != nil && (!(*p.Similarity > 0) || *p.Similarity > 1) {
		return apperr.Invalid(fieldSimilarity, reasonRange,
			fmt.Sprintf("settings: similarity %v is outside (0, 1]", *p.Similarity))
	}
	if p.PicksDir != nil {
		if err := picks.ValidateDir(*p.PicksDir); err != nil {
			reason := "invalid" // unreachable: ValidateDir returns only *DirError
			var de *picks.DirError
			if errors.As(err, &de) {
				reason = de.Reason
			}
			return apperr.Invalid(fieldPicksDir, reason, "settings: "+err.Error())
		}
	}
	return nil
}

// applyTo overlays the fields p sets onto cfg.
func (p Patch) applyTo(cfg *config.Config) {
	if p.BucketSize != nil {
		cfg.BucketSizeSec = *p.BucketSize
	}
	if p.Similarity != nil {
		cfg.VisualSimilarity = *p.Similarity
	}
	if p.PicksDir != nil {
		cfg.PicksDir = *p.PicksDir
	}
	if p.AdvanceOnAction != nil {
		cfg.AdvanceOnAction = *p.AdvanceOnAction
	}
	if p.HideRejectedPhotos != nil {
		cfg.HideRejectedPhotos = *p.HideRejectedPhotos
	}
	if p.CrossDeviceMerging != nil {
		cfg.CrossDeviceMerging = *p.CrossDeviceMerging
	}
	if p.QualityRating != nil {
		cfg.QualityRating = *p.QualityRating
	}
	if p.VisualGrouping != nil {
		cfg.VisualGrouping = *p.VisualGrouping
	}
}

// Store is the live, machine-wide settings. It is safe for concurrent use.
type Store struct {
	// applyMu serialises Apply, including its OnChange callbacks, so no
	// change is lost and subscribers see changes in the order they were made.
	applyMu sync.Mutex

	mu  sync.RWMutex // guards cur and subs
	cur config.Config
	// subs are the OnChange subscribers, in subscription order.
	subs []func(old, cur config.Config)
}

// NewStore loads config.toml (config.Load): built-in defaults overlaid with
// what the file sets. A missing file is the defaults; an unreadable one is
// an error.
func NewStore() (*Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("settings: load: %w", err)
	}
	return &Store{cur: *cfg}, nil
}

// Get returns the current settings.
func (s *Store) Get() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// OnChange subscribes f to every change Apply makes. f is called with the
// settings before and after, after the change is saved, one call at a
// time, in the order of the changes, while changes are serialised: f may
// call Get, but must not call Apply or Service.Save (that deadlocks), and
// should return quickly — the next change waits for it — handing anything
// slow (a download, queueing work) to another goroutine.
func (s *Store) OnChange(f func(old, cur config.Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, f)
}

// Apply validates p, saves the patched settings to config.toml and makes
// them current, then calls the OnChange subscribers when anything changed.
// A refused patch is an invalid *apperr.Error naming the field and the
// reason; on any error nothing is saved and the current settings stay.
func (s *Store) Apply(p Patch) (old, cur config.Config, err error) {
	if err := p.validate(); err != nil {
		return config.Config{}, config.Config{}, err
	}

	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	old = s.Get()
	cur = old
	p.applyTo(&cur)
	if err := config.Save(&cur); err != nil {
		return config.Config{}, config.Config{}, fmt.Errorf("settings: save: %w", err)
	}

	s.mu.Lock()
	s.cur = cur
	subs := s.subs[:len(s.subs):len(s.subs)]
	s.mu.Unlock()

	if old != cur {
		for _, f := range subs {
			f(old, cur)
		}
	}
	return old, cur, nil
}
