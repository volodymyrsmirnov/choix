package server

import (
	"errors"
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Legacy KV keys that previously held the three settings now living in
// config.toml. Kept here only so the one-shot migration can read and
// delete them; nothing else in the codebase should reference these.
const (
	legacyKVPicksDir           = "picks_dir"
	legacyKVAdvanceOnAction    = "advance_on_action"
	legacyKVHideRejectedPhotos = "hide_rejected_photos"
)

// migrateLegacyKVSettings carries any pre-existing per-folder KV settings
// into the global config.toml on first launch after the upgrade. Behavior:
//
//   - For each legacy key: if the corresponding TOML field is still at its
//     default AND the KV row exists, overlay the KV value onto the live
//     config and persist with config.Save.
//   - After successful overlay (or if there was nothing to migrate), delete
//     the KV rows so subsequent launches don't re-trigger the migration.
//
// The "TOML still at default" guard makes the migration first-folder-wins
// across multiple scan roots: once a user has saved any value through the
// new Settings UI, this stops touching it. The function is idempotent —
// after a successful run, the KV rows are gone and the TOML defaults check
// short-circuits anyway.
func migrateLegacyKVSettings(st *store.Store) error {
	if st == nil {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	defaults := config.Defaults()

	dirty := false
	if v, err := st.KV().Get(legacyKVPicksDir); err == nil && v != "" {
		if cfg.PicksDir == defaults.PicksDir {
			cfg.PicksDir = v
			dirty = true
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("read legacy picks_dir: %w", err)
	}

	if v, err := st.KV().Get(legacyKVAdvanceOnAction); err == nil {
		if !cfg.AdvanceOnAction {
			cfg.AdvanceOnAction = v == "1" || v == "true"
			if cfg.AdvanceOnAction {
				dirty = true
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("read legacy advance_on_action: %w", err)
	}

	if v, err := st.KV().Get(legacyKVHideRejectedPhotos); err == nil {
		if !cfg.HideRejectedPhotos {
			cfg.HideRejectedPhotos = v == "1" || v == "true"
			if cfg.HideRejectedPhotos {
				dirty = true
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("read legacy hide_rejected_photos: %w", err)
	}

	if dirty {
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("save migrated config: %w", err)
		}
	}

	// Always delete the legacy rows on success — even if nothing was
	// migrated, leaving them around would falsely suggest the values are
	// still load-bearing.
	for _, k := range []string{legacyKVPicksDir, legacyKVAdvanceOnAction, legacyKVHideRejectedPhotos} {
		if err := st.KV().Delete(k); err != nil {
			return fmt.Errorf("delete legacy %s: %w", k, err)
		}
	}
	return nil
}
