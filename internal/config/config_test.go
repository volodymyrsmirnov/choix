package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Defaults()

	if c.BucketSizeSec != 600 {
		t.Errorf("BucketSizeSec = %d, want 600", c.BucketSizeSec)
	}
	if c.VisualSimilarity != 0.90 {
		t.Errorf("VisualSimilarity = %v, want 0.90", c.VisualSimilarity)
	}
	if c.PicksDir != "picks" {
		t.Errorf("PicksDir = %q, want \"picks\"", c.PicksDir)
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
visual_similarity = 0.85
picks_dir = "selected"
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
	if cfg.VisualSimilarity != 0.85 {
		t.Errorf("VisualSimilarity = %v, want 0.85", cfg.VisualSimilarity)
	}
	if cfg.PicksDir != "selected" {
		t.Errorf("PicksDir = %q", cfg.PicksDir)
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
	if cfg.VisualSimilarity != 0.90 {
		t.Errorf("VisualSimilarity = %v, want default 0.90", cfg.VisualSimilarity)
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
