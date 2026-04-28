package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// stubConfigDirToTemp redirects config.configFilePath to a temp dir for
// the duration of a test. Returns the temp dir.
func stubConfigDirToTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux fallback path; macOS ignores
	// On macOS os.UserConfigDir reads ~/Library/Application Support, so
	// also override HOME to keep this test isolated from the real config.
	t.Setenv("HOME", dir)
	return dir
}

func TestMigrateLegacyKVCarriesValues(t *testing.T) {
	stubConfigDirToTemp(t)

	st := store.NewTestStore(t)
	if err := st.KV().Set(legacyKVPicksDir, "exports"); err != nil {
		t.Fatalf("seed picks_dir: %v", err)
	}
	if err := st.KV().Set(legacyKVAdvanceOnAction, "1"); err != nil {
		t.Fatalf("seed advance_on_action: %v", err)
	}
	if err := st.KV().Set(legacyKVHideRejectedPhotos, "1"); err != nil {
		t.Fatalf("seed hide_rejected_photos: %v", err)
	}

	if err := migrateLegacyKVSettings(st); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PicksDir != "exports" {
		t.Errorf("PicksDir = %q, want exports", cfg.PicksDir)
	}
	if !cfg.AdvanceOnAction {
		t.Errorf("AdvanceOnAction = false, want true")
	}
	if !cfg.HideRejectedPhotos {
		t.Errorf("HideRejectedPhotos = false, want true")
	}

	for _, k := range []string{legacyKVPicksDir, legacyKVAdvanceOnAction, legacyKVHideRejectedPhotos} {
		if _, err := st.KV().Get(k); err == nil {
			t.Errorf("KV row %q still exists after migration", k)
		}
	}
}

func TestMigrateLegacyKVIdempotent(t *testing.T) {
	stubConfigDirToTemp(t)

	st := store.NewTestStore(t)
	if err := st.KV().Set(legacyKVPicksDir, "exports"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := migrateLegacyKVSettings(st); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	cfg1, _ := config.Load()
	if err := migrateLegacyKVSettings(st); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	cfg2, _ := config.Load()
	if *cfg1 != *cfg2 {
		t.Errorf("migration not idempotent:\n first %+v\nsecond %+v", *cfg1, *cfg2)
	}
}

func TestMigrateLegacyKVDoesNotOverrideExistingTOML(t *testing.T) {
	stubConfigDirToTemp(t)

	// Pre-existing user-saved value in TOML — migration must not clobber.
	if err := config.Save(&config.Config{
		BucketSizeSec:          600,
		VisualClusterThreshold: 0.92,
		PicksDir:               "user-set",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	st := store.NewTestStore(t)
	if err := st.KV().Set(legacyKVPicksDir, "stale-kv"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := migrateLegacyKVSettings(st); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg, _ := config.Load()
	if cfg.PicksDir != "user-set" {
		t.Errorf("user-saved TOML PicksDir overwritten: got %q, want user-set", cfg.PicksDir)
	}
}

// First-folder-wins: once one scan-root has migrated its KV value into
// TOML, a subsequent scan-root's migration must not overwrite it (because
// the TOML field is no longer at default). The second store's KV rows
// still get deleted so its own future migrations are a no-op.
func TestMigrateLegacyKVFirstFolderWins(t *testing.T) {
	stubConfigDirToTemp(t)

	stA := store.NewTestStore(t)
	if err := stA.KV().Set(legacyKVPicksDir, "from-A"); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if err := migrateLegacyKVSettings(stA); err != nil {
		t.Fatalf("migrate A: %v", err)
	}
	cfgAfterA, _ := config.Load()
	if cfgAfterA.PicksDir != "from-A" {
		t.Fatalf("after A: PicksDir = %q, want from-A", cfgAfterA.PicksDir)
	}

	stB := store.NewTestStore(t)
	if err := stB.KV().Set(legacyKVPicksDir, "from-B"); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	if err := migrateLegacyKVSettings(stB); err != nil {
		t.Fatalf("migrate B: %v", err)
	}
	cfgAfterB, _ := config.Load()
	if cfgAfterB.PicksDir != "from-A" {
		t.Errorf("second-folder migration overwrote first: PicksDir = %q, want from-A", cfgAfterB.PicksDir)
	}
	// stB's KV row is gone too — second migration was a clean no-op for
	// the value but did delete the dead row.
	if _, err := stB.KV().Get(legacyKVPicksDir); err == nil {
		t.Errorf("stB KV row still present after migration; want deleted")
	}
}

func TestMigrateLegacyKVNoLegacyRowsIsNoOp(t *testing.T) {
	stubConfigDirToTemp(t)
	st := store.NewTestStore(t)

	if err := migrateLegacyKVSettings(st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Defaults applied since there's no TOML and no KV.
	cfg, _ := config.Load()
	def := config.Defaults()
	if *cfg != def {
		t.Errorf("Load = %+v, want defaults %+v", *cfg, def)
	}
}

// Sanity: prove the temp dir override actually points config.toml writes
// at the test directory rather than the user's real config file.
func TestStubConfigDirRedirectsWrites(t *testing.T) {
	dir := stubConfigDirToTemp(t)
	if err := config.Save(&config.Config{
		BucketSizeSec:          900,
		VisualClusterThreshold: 0.5,
		PicksDir:               "x",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// On macOS the path is $HOME/Library/Application Support/choix/config.toml.
	// On Linux it is $XDG_CONFIG_HOME/choix/config.toml.
	candidates := []string{
		filepath.Join(dir, "Library", "Application Support", "choix", "config.toml"),
		filepath.Join(dir, "choix", "config.toml"),
	}
	found := false
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("config.toml not written under %q (tried %v)", dir, candidates)
	}
}
