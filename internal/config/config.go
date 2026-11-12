// Package config holds choix's runtime configuration. There is one source:
// the machine-wide config.toml in the app's data directory (appdir.Config),
// layered over the built-in defaults. The environment and per-folder files
// are not read. This file is pure-data: no I/O, no globals, no logging.
package config

// Config is the fully-resolved settings struct used by the rest of the binary.
type Config struct {
	// Grouping
	BucketSizeSec int // time bucket width in seconds
	// VisualSimilarity is how alike two shots of one time bucket must be to
	// share a cluster: complete-link on the dot product of their feature
	// prints, strictly greater than this value.
	VisualSimilarity   float64
	CrossDeviceMerging bool // merge clusters across devices using clock-skew detection

	// VisualGrouping enables visual sub-clustering: Vision feature prints
	// are computed and time buckets are split by them. Off means time-only
	// clusters and no prints.
	VisualGrouping bool

	// Pick export
	PicksDir string // relative to scan root

	// Behaviour
	AdvanceOnAction    bool // cycle to next photo after Pick/Reject in Focus
	HideRejectedPhotos bool // hide rejected photos from the Library

	// QualityRating enables the advisory technical-quality rating: photos
	// are scored in the background. Off means no scoring and no badges.
	QualityRating bool
}

// Defaults returns the built-in default configuration. Load overlays
// config.toml on top of this.
func Defaults() Config {
	return Config{
		BucketSizeSec:    600,
		VisualSimilarity: 0.90,
		VisualGrouping:   true,
		PicksDir:         "picks",
		QualityRating:    true,
	}
}
