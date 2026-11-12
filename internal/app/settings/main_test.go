package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points CHOIX_DATA_DIR at a temporary directory before any test
// runs, so a test that forgets its own t.Setenv still never reaches the
// real data directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "choix-settings-test-")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	if err := os.Setenv("CHOIX_DATA_DIR", filepath.Clean(dir)); err != nil {
		panic("test setup: setenv CHOIX_DATA_DIR: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
