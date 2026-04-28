// Package config holds choix's runtime configuration. Values resolve in the
// precedence order CLI flags > environment variables > config.toml > built-in
// defaults. This file is pure-data: no I/O, no globals, no logging.
package config

// Config is the fully-resolved settings struct used by the rest of the binary.
type Config struct {
	// Grouping
	BucketSizeSec          int     // time bucket width in seconds
	VisualClusterThreshold float64 // CLIP cosine threshold for visual clusters
	CrossDeviceMerging     bool    // merge clusters across devices using clock-skew detection

	// Pick export
	PicksDir string // relative to scan root

	// Behaviour
	AdvanceOnAction    bool // cycle to next photo after Pick/Reject in Focus
	HideRejectedPhotos bool // hide rejected photos from the Library

	// Scan target
	ScanRoot string // absolute filesystem path; empty means "current working dir"
}

// Defaults returns the built-in default configuration. Callers layer TOML, env,
// and CLI flag overrides on top of this.
func Defaults() Config {
	return Config{
		BucketSizeSec:          600,
		VisualClusterThreshold: 0.92,
		PicksDir:               "picks",
	}
}
