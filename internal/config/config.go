// Package config holds choix's runtime configuration. There is one source:
// the machine-wide config.toml in the app's data directory (appdir.Config),
// layered over the built-in defaults. The environment and per-folder files
// are not read. This file is pure-data: no I/O, no globals, no logging.
package config

// Config is the fully-resolved settings struct used by the rest of the binary.
type Config struct {
	// Grouping
	BucketSizeSec          int     // time bucket width in seconds
	VisualClusterThreshold float64 // CLIP cosine threshold for visual clusters
	CrossDeviceMerging     bool    // merge clusters across devices using clock-skew detection

	// VisualGrouping enables CLIP-based visual sub-clustering: the CLIP
	// model is downloaded and embeddings are computed. Off means time-only
	// clusters, no download and no embeddings.
	VisualGrouping bool

	// Pick export
	PicksDir string // relative to scan root

	// Behaviour
	AdvanceOnAction    bool // cycle to next photo after Pick/Reject in Focus
	HideRejectedPhotos bool // hide rejected photos from the Library

	// QualityRating enables the advisory technical-quality rating: the
	// model is downloaded automatically and photos are scored in the
	// background. Off means no download, no scoring and no badges.
	QualityRating bool
}

// Defaults returns the built-in default configuration. Load overlays
// config.toml on top of this.
func Defaults() Config {
	return Config{
		BucketSizeSec:          600,
		VisualClusterThreshold: 0.92,
		VisualGrouping:         true,
		PicksDir:               "picks",
		QualityRating:          true,
	}
}
