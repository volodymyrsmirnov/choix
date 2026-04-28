package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// configFilePath returns the path to the global config file.
// On macOS this is ~/Library/Application Support/choix/config.toml.
// The location is injectable via the configDirFunc seam for tests.
var configDirFunc = os.UserConfigDir

func configFilePath() (string, error) {
	dir, err := configDirFunc()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return filepath.Join(dir, "choix", "config.toml"), nil
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
