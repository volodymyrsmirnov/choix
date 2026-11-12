package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubConfigDir replaces configFilePathFunc for the duration of a test,
// ensuring Save/Load round-trip against a temporary directory rather
// than the user's actual data directory.
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
		BucketSizeSec:      900,
		VisualSimilarity:   0.88,
		PicksDir:           "exports",
		AdvanceOnAction:    true,
		HideRejectedPhotos: true,
		CrossDeviceMerging: true,
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
		BucketSizeSec:      600,
		VisualSimilarity:   0.92,
		PicksDir:           "picks",
		AdvanceOnAction:    true,
		HideRejectedPhotos: true,
		CrossDeviceMerging: true,
	}); err != nil {
		t.Fatalf("Save (true): %v", err)
	}

	// Now overwrite with all bools false and verify they don't snap back.
	if err := Save(&Config{
		BucketSizeSec:    600,
		VisualSimilarity: 0.92,
		PicksDir:         "picks",
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
	if err := Save(&Config{BucketSizeSec: 1, VisualSimilarity: 0.5, PicksDir: "x"}); err != nil {
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

// QualityRating defaults to on, so "off" is the value that must survive a
// save/load round trip — and a config.toml written before the key existed
// must still read as on.
func TestQualityRatingDefaultsOnAndOffPersists(t *testing.T) {
	stubConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.QualityRating {
		t.Fatal("QualityRating must default to true when the file is missing")
	}

	cfg.QualityRating = false
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.QualityRating {
		t.Fatal("QualityRating=false did not persist")
	}
}

func TestQualityRatingAbsentKeyReadsAsOn(t *testing.T) {
	dir := stubConfigDir(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("bucket_size_sec = 300\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.QualityRating || cfg.BucketSizeSec != 300 {
		t.Fatalf("got %+v, want QualityRating on with bucket 300", *cfg)
	}
}

func TestDefaults_VisualGroupingOn(t *testing.T) {
	if !Defaults().VisualGrouping {
		t.Fatal("Defaults().VisualGrouping = false, want true")
	}
	stubConfigDir(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.VisualGrouping {
		t.Fatal("VisualGrouping must default to true when the file is missing")
	}
}

func TestSaveLoad_RoundTripsVisualGrouping(t *testing.T) {
	dir := stubConfigDir(t)

	cfg := Defaults()
	cfg.VisualGrouping = false
	if err := Save(&cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "visual_grouping = false") {
		t.Errorf("config.toml lacks visual_grouping = false:\n%s", raw)
	}
	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.VisualGrouping {
		t.Fatal("VisualGrouping=false did not persist")
	}

	out.VisualGrouping = true
	if err := Save(out); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !back.VisualGrouping {
		t.Fatal("VisualGrouping=true did not persist")
	}
}

func TestLoad_IgnoresEnvironment(t *testing.T) {
	stubConfigDir(t)
	t.Setenv("CHOIX_BUCKET_SIZE", "60")
	t.Setenv("CHOIX_PICKS_DIR", "from-env")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BucketSizeSec != 600 {
		t.Errorf("BucketSizeSec = %d, want 600 (environment must be ignored)", cfg.BucketSizeSec)
	}
	if cfg.PicksDir != "picks" {
		t.Errorf("PicksDir = %q, want picks (environment must be ignored)", cfg.PicksDir)
	}
}

// TestLoad_IgnoresRetiredThresholdKey: a config.toml written by an earlier
// build holds visual_cluster_threshold, on another similarity scale. It is
// ignored on read (the similarity is the default) and not written back.
func TestLoad_IgnoresRetiredThresholdKey(t *testing.T) {
	dir := stubConfigDir(t)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("visual_cluster_threshold = 0.7\nbucket_size_sec = 300\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VisualSimilarity != 0.90 || cfg.BucketSizeSec != 300 {
		t.Fatalf("similarity %v, bucket %d; want 0.90, 300", cfg.VisualSimilarity, cfg.BucketSizeSec)
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "visual_similarity = 0.9") {
		t.Errorf("saved config has no visual_similarity:\n%s", data)
	}
	if strings.Contains(string(data), "visual_cluster_threshold") {
		t.Errorf("saved config still writes visual_cluster_threshold:\n%s", data)
	}
}
