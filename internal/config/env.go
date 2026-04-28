package config

import (
	"fmt"
	"os"
	"strconv"
)

// ApplyEnv overlays settings from the process environment onto cfg. Unset (or
// empty) variables leave the corresponding fields untouched. Returns an error
// if a numeric env var is present but cannot be parsed.
//
// Recognized variables:
//
//	CHOIX_BUCKET_SIZE  -> BucketSizeSec (integer seconds)
//	CHOIX_PICKS_DIR    -> PicksDir
func ApplyEnv(cfg *Config) error {
	if v := os.Getenv("CHOIX_BUCKET_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CHOIX_BUCKET_SIZE=%q: %w", v, err)
		}
		cfg.BucketSizeSec = n
	}
	if v := os.Getenv("CHOIX_PICKS_DIR"); v != "" {
		cfg.PicksDir = v
	}
	return nil
}
