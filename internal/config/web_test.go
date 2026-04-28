package config

import (
	"path/filepath"
	"testing"
)

// stubConfigDir replaces configFilePathFunc for the duration of a test,
// ensuring Save/Load round-trip against a temporary directory rather
// than the user's actual ~/.choix.
func stubConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	prev := configFilePathFunc
	configFilePathFunc = func() (string, error) { return path, nil }
	t.Cleanup(func() { configFilePathFunc = prev })
	return dir
}

func TestSaveLoadRoundTripAllFields(t *testing.T) {
	stubConfigDir(t)

	in := &Config{
		BucketSizeSec:          900,
		VisualClusterThreshold: 0.88,
		PicksDir:               "exports",
		AdvanceOnAction:        true,
		HideRejectedPhotos:     true,
		CrossDeviceMerging:     true,
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *out != *in {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", *out, *in)
	}
}

func TestSaveLoadRoundTripFalseBoolsPersist(t *testing.T) {
	stubConfigDir(t)

	// First write everything true so the file exists with the bools set.
	if err := Save(&Config{
		BucketSizeSec:          600,
		VisualClusterThreshold: 0.92,
		PicksDir:               "picks",
		AdvanceOnAction:        true,
		HideRejectedPhotos:     true,
		CrossDeviceMerging:     true,
	}); err != nil {
		t.Fatalf("Save (true): %v", err)
	}

	// Now overwrite with all bools false and verify they don't snap back.
	if err := Save(&Config{
		BucketSizeSec:          600,
		VisualClusterThreshold: 0.92,
		PicksDir:               "picks",
	}); err != nil {
		t.Fatalf("Save (false): %v", err)
	}

	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.AdvanceOnAction || out.HideRejectedPhotos || out.CrossDeviceMerging {
		t.Errorf("false bools did not stick: %+v", *out)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	stubConfigDir(t)
	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Defaults()
	if *out != def {
		t.Errorf("Load with missing file = %+v, want defaults %+v", *out, def)
	}
}

// TestSavedFilePathIsExpected guards the configFilePath shape so the
// upgrade-path migration test has a stable target to write into.
func TestSavedFilePathIsExpected(t *testing.T) {
	dir := stubConfigDir(t)
	if err := Save(&Config{BucketSizeSec: 1, VisualClusterThreshold: 0.5, PicksDir: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := filepath.Join(dir, "config.toml")
	got, err := configFilePath()
	if err != nil {
		t.Fatalf("configFilePath: %v", err)
	}
	if got != want {
		t.Errorf("configFilePath = %q, want %q", got, want)
	}
}
