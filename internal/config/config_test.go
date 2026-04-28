package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestDefaults(t *testing.T) {
	c := Defaults()

	if c.BucketSizeSec != 600 {
		t.Errorf("BucketSizeSec = %d, want 600", c.BucketSizeSec)
	}
	if c.VisualClusterThreshold != 0.92 {
		t.Errorf("VisualClusterThreshold = %v, want 0.92", c.VisualClusterThreshold)
	}
	if c.PicksDir != "picks" {
		t.Errorf("PicksDir = %q, want \"picks\"", c.PicksDir)
	}
	if c.ScanRoot != "" {
		t.Errorf("ScanRoot = %q, want empty", c.ScanRoot)
	}
}

func TestConfigZeroValueDistinctFromDefaults(t *testing.T) {
	var zero Config
	if zero.BucketSizeSec == Defaults().BucketSizeSec {
		t.Error("zero Config equals Defaults; tests below assume they differ")
	}
}

func TestLoadFileMissingReturnsNilNil(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil for missing file", cfg)
	}
}

func TestLoadFileParsesValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
bucket_size_sec = 300
visual_cluster_threshold = 0.85
picks_dir = "selected"
scan_root = "/tmp/photos"
advance_on_action = true
hide_rejected_photos = true
cross_device_merging = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	raw, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if raw == nil {
		t.Fatal("raw == nil")
	}
	cfg := Defaults()
	ApplyTOML(&cfg, raw)
	if cfg.BucketSizeSec != 300 {
		t.Errorf("BucketSizeSec = %d, want 300", cfg.BucketSizeSec)
	}
	if cfg.VisualClusterThreshold != 0.85 {
		t.Errorf("VisualClusterThreshold = %v, want 0.85", cfg.VisualClusterThreshold)
	}
	if cfg.PicksDir != "selected" {
		t.Errorf("PicksDir = %q", cfg.PicksDir)
	}
	if cfg.ScanRoot != "/tmp/photos" {
		t.Errorf("ScanRoot = %q", cfg.ScanRoot)
	}
	if !cfg.AdvanceOnAction {
		t.Error("AdvanceOnAction false, want true")
	}
	if !cfg.HideRejectedPhotos {
		t.Error("HideRejectedPhotos false, want true")
	}
	if !cfg.CrossDeviceMerging {
		t.Error("CrossDeviceMerging false, want true")
	}
}

func TestLoadFilePartialLeavesUnsetFieldsAtDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `bucket_size_sec = 120` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	raw, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	cfg := Defaults()
	ApplyTOML(&cfg, raw)
	if cfg.BucketSizeSec != 120 {
		t.Errorf("BucketSizeSec = %d, want 120", cfg.BucketSizeSec)
	}
	if cfg.VisualClusterThreshold != 0.92 {
		t.Errorf("VisualClusterThreshold = %v, want default 0.92", cfg.VisualClusterThreshold)
	}
	if cfg.AdvanceOnAction || cfg.HideRejectedPhotos || cfg.CrossDeviceMerging {
		t.Errorf("bools changed by partial load: %+v", cfg)
	}
}

func TestLoadFileExplicitFalseSticks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
advance_on_action = false
hide_rejected_photos = false
cross_device_merging = false
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	cfg := Config{
		AdvanceOnAction:    true,
		HideRejectedPhotos: true,
		CrossDeviceMerging: true,
	}
	raw, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	ApplyTOML(&cfg, raw)
	if cfg.AdvanceOnAction || cfg.HideRejectedPhotos || cfg.CrossDeviceMerging {
		t.Errorf("explicit false did not overlay: %+v", cfg)
	}
}

func TestLoadFileMalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("not = valid = toml"), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Error("expected parse error, got nil")
	}
}

func TestApplyEnvOverridesDefaults(t *testing.T) {
	t.Setenv("CHOIX_BUCKET_SIZE", "120")
	t.Setenv("CHOIX_PICKS_DIR", "selected")

	c := Defaults()
	if err := ApplyEnv(&c); err != nil {
		t.Fatalf("ApplyEnv: %v", err)
	}
	if c.BucketSizeSec != 120 {
		t.Errorf("BucketSizeSec = %d, want 120", c.BucketSizeSec)
	}
	if c.PicksDir != "selected" {
		t.Errorf("PicksDir = %q", c.PicksDir)
	}
}

func TestApplyEnvUnsetLeavesValuesAlone(t *testing.T) {
	for _, k := range []string{"CHOIX_BUCKET_SIZE", "CHOIX_PICKS_DIR"} {
		t.Setenv(k, "")
	}

	c := Defaults()
	c.PicksDir = "preserved"
	if err := ApplyEnv(&c); err != nil {
		t.Fatalf("ApplyEnv: %v", err)
	}
	if c.PicksDir != "preserved" {
		t.Errorf("PicksDir clobbered: %q", c.PicksDir)
	}
	if c.BucketSizeSec != 600 {
		t.Errorf("BucketSizeSec = %d, want 600 (unchanged)", c.BucketSizeSec)
	}
}

func TestApplyEnvBadIntFails(t *testing.T) {
	t.Setenv("CHOIX_BUCKET_SIZE", "not-a-number")
	c := Defaults()
	if err := ApplyEnv(&c); err == nil {
		t.Error("expected error for bad CHOIX_BUCKET_SIZE, got nil")
	}
}

func TestFlagsApplySetValues(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	Flags(cmd)

	if err := cmd.ParseFlags([]string{
		"--bucket-size=42",
		"--picks-dir=outpicks",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	c := Defaults()
	if err := ApplyFlags(&c, cmd); err != nil {
		t.Fatalf("ApplyFlags: %v", err)
	}
	if c.BucketSizeSec != 42 {
		t.Errorf("BucketSizeSec = %d, want 42", c.BucketSizeSec)
	}
	if c.PicksDir != "outpicks" {
		t.Errorf("PicksDir = %q", c.PicksDir)
	}
}

func TestFlagsUnsetLeavesValuesAlone(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	Flags(cmd)
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	c := Defaults()
	c.PicksDir = "from-toml"
	if err := ApplyFlags(&c, cmd); err != nil {
		t.Fatalf("ApplyFlags: %v", err)
	}
	if c.PicksDir != "from-toml" {
		t.Errorf("PicksDir clobbered: %q", c.PicksDir)
	}
	if c.BucketSizeSec != 600 {
		t.Errorf("BucketSizeSec = %d", c.BucketSizeSec)
	}
}

func TestPrecedenceCLIBeatsEnvBeatsTOMLBeatsDefaults(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	tomlBody := `
bucket_size_sec = 100
picks_dir = "from-toml"
`
	if err := os.WriteFile(tomlPath, []byte(tomlBody), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	t.Setenv("CHOIX_BUCKET_SIZE", "200")
	// CHOIX_PICKS_DIR is intentionally NOT set so PicksDir falls through
	// from TOML.
	t.Setenv("CHOIX_PICKS_DIR", "")

	cmd := &cobra.Command{Use: "test"}
	Flags(cmd)
	if err := cmd.ParseFlags([]string{"--bucket-size=300"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg := Defaults()
	raw, err := LoadFile(tomlPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	ApplyTOML(&cfg, raw)
	if err := ApplyEnv(&cfg); err != nil {
		t.Fatalf("ApplyEnv: %v", err)
	}
	if err := ApplyFlags(&cfg, cmd); err != nil {
		t.Fatalf("ApplyFlags: %v", err)
	}

	// CLI wins for the fields it set.
	if cfg.BucketSizeSec != 300 {
		t.Errorf("BucketSizeSec = %d, want 300 (CLI wins)", cfg.BucketSizeSec)
	}

	// TOML wins over defaults where neither env nor CLI was set.
	if cfg.PicksDir != "from-toml" {
		t.Errorf("PicksDir = %q, want from-toml", cfg.PicksDir)
	}

	// Untouched defaults remain.
	if cfg.VisualClusterThreshold != 0.92 {
		t.Errorf("VisualClusterThreshold = %v, want 0.92 (default)", cfg.VisualClusterThreshold)
	}
}
