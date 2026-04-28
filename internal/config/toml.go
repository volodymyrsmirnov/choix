package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// fileSchema is the on-disk TOML shape. Every field is a pointer so callers
// can distinguish "key absent" from "key set to zero value" — the latter is
// meaningful for bools.
type fileSchema struct {
	BucketSizeSec          *int     `toml:"bucket_size_sec"`
	VisualClusterThreshold *float64 `toml:"visual_cluster_threshold"`
	PicksDir               *string  `toml:"picks_dir"`
	AdvanceOnAction        *bool    `toml:"advance_on_action"`
	HideRejectedPhotos     *bool    `toml:"hide_rejected_photos"`
	CrossDeviceMerging     *bool    `toml:"cross_device_merging"`
}

// LoadFile reads a config TOML file at path and returns the raw schema with
// per-field presence preserved. Missing file returns (nil, nil) so callers
// can treat absence as "no overrides".
func LoadFile(path string) (*fileSchema, error) {
	var raw fileSchema
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &raw, nil
}

// ApplyTOML overlays every field src actually set onto dst. Fields whose
// pointer is nil leave dst untouched — this is what lets a caller ship a
// partial config.toml without zeroing unrelated fields.
func ApplyTOML(dst *Config, src *fileSchema) {
	if src == nil {
		return
	}
	if src.BucketSizeSec != nil {
		dst.BucketSizeSec = *src.BucketSizeSec
	}
	if src.VisualClusterThreshold != nil {
		dst.VisualClusterThreshold = *src.VisualClusterThreshold
	}
	if src.PicksDir != nil {
		dst.PicksDir = *src.PicksDir
	}
	if src.AdvanceOnAction != nil {
		dst.AdvanceOnAction = *src.AdvanceOnAction
	}
	if src.HideRejectedPhotos != nil {
		dst.HideRejectedPhotos = *src.HideRejectedPhotos
	}
	if src.CrossDeviceMerging != nil {
		dst.CrossDeviceMerging = *src.CrossDeviceMerging
	}
}
