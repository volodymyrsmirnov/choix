package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// configFilePathFunc is the seam tests override to redirect Load/Save to
// a tmp file. Production points it at appdir.Config (~/.choix/config.toml).
var configFilePathFunc = appdir.Config

func configFilePath() (string, error) {
	return configFilePathFunc()
}

// Load reads the global config file and returns the fully-resolved Config.
// Missing file returns Defaults with no error.
func Load() (*Config, error) {
	path, err := configFilePath()
	if err != nil {
		return nil, err
	}
	cfg := Defaults()
	src, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	ApplyTOML(&cfg, src)
	return &cfg, nil
}

// Save writes cfg to the global config file atomically (tmp + rename). All
// six user-facing settings are emitted explicitly so a `false` bool persists
// through round-trips.
func Save(cfg *Config) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	schema := fileSchema{
		BucketSizeSec:          &cfg.BucketSizeSec,
		VisualClusterThreshold: &cfg.VisualClusterThreshold,
		PicksDir:               &cfg.PicksDir,
		AdvanceOnAction:        &cfg.AdvanceOnAction,
		HideRejectedPhotos:     &cfg.HideRejectedPhotos,
		CrossDeviceMerging:     &cfg.CrossDeviceMerging,
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(schema); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}
