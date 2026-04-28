package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain sets up a process-wide isolation against the real
// ~/Library/Application Support/choix/config.toml. Any test in this
// package that ends up calling config.Save (directly, or transitively
// via /api/settings) would otherwise overwrite the developer's actual
// settings — that's how the picks_dir="exported" pollution happened
// during the cross-device-merging implementation. Setting HOME and
// XDG_CONFIG_HOME at process start is broad enough to catch tests that
// don't go through newTestServer.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "choix-server-test-")
	if err != nil {
		panic("test setup: mkdir temp: " + err.Error())
	}
	if err := os.Setenv("HOME", filepath.Clean(dir)); err != nil {
		panic("test setup: setenv HOME: " + err.Error())
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Clean(dir)); err != nil {
		panic("test setup: setenv XDG_CONFIG_HOME: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
