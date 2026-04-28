package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects the config-file path at process start so tests
// that forget to call stubConfigDir cannot ever reach the real
// ~/.choix/config.toml. The per-test stub still wins (Cleanup restores
// configFilePathFunc), but if it is missing the fallback is still a
// temp file, not the user's home.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "choix-config-test-")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	path := filepath.Join(filepath.Clean(dir), "config.toml")
	configFilePathFunc = func() (string, error) { return path, nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
