package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects the user-config dir at process start so tests that
// forget to call stubConfigDir cannot ever reach the real
// ~/Library/Application Support/choix/config.toml. The per-test stub
// still wins (Cleanup restores configDirFunc), but if it is missing the
// fallback is still a temp dir, not the user's home.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "choix-config-test-")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	configDirFunc = func() (string, error) { return filepath.Clean(dir), nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
