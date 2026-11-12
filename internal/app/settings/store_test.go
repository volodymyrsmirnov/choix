package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/config"
)

// dataDir points CHOIX_DATA_DIR at a fresh temporary directory and returns
// it; config.toml lives directly inside.
func dataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CHOIX_DATA_DIR", dir)
	return dir
}

func newStore(t *testing.T) *Store {
	t.Helper()
	dataDir(t)
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func ptr[T any](v T) *T { return &v }

func TestNewStore_LoadsConfigTOML(t *testing.T) {
	dir := dataDir(t)
	body := "bucket_size_sec = 300\npicks_dir = \"selected\"\nquality_rating = false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	want := config.Defaults()
	want.BucketSizeSec = 300
	want.PicksDir = "selected"
	want.QualityRating = false
	if got := s.Get(); got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestNewStore_UnreadableTOMLFails(t *testing.T) {
	dir := dataDir(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("bucket_size_sec = ["), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if _, err := NewStore(); err == nil {
		t.Error("NewStore with a malformed config.toml succeeded, want an error")
	}
}

func TestApply_ReturnsOldAndNew(t *testing.T) {
	s := newStore(t)
	old, cur, err := s.Apply(Patch{BucketSize: ptr(900)})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if old != config.Defaults() {
		t.Errorf("old = %+v, want defaults", old)
	}
	if cur.BucketSizeSec != 900 || s.Get() != cur {
		t.Errorf("cur = %+v, Get = %+v, want bucket 900 in both", cur, s.Get())
	}
}

func TestApply_InvalidChangesNothing(t *testing.T) {
	dir := dataDir(t)
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	called := false
	s.OnChange(func(_, _ config.Config) { called = true })

	// A valid field next to an invalid one is not applied either.
	_, _, err = s.Apply(Patch{AdvanceOnAction: ptr(true), BucketSize: ptr(10)})
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != apperr.CodeInvalid {
		t.Fatalf("Apply = %v, want an invalid *apperr.Error", err)
	}
	if s.Get() != config.Defaults() {
		t.Errorf("Get = %+v after a refused patch, want defaults", s.Get())
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config.toml stat = %v after a refused patch, want not exist", err)
	}
	if called {
		t.Error("OnChange called for a refused patch")
	}
}

func TestApply_SaveFailureChangesNothing(t *testing.T) {
	dir := dataDir(t)
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// A directory where config.toml should be makes the rename fail.
	if err := os.Mkdir(filepath.Join(dir, "config.toml"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml", "x"), nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := s.Apply(Patch{BucketSize: ptr(900)}); err == nil {
		t.Fatal("Apply succeeded although config.toml could not be written")
	}
	if s.Get() != config.Defaults() {
		t.Errorf("Get = %+v after a failed save, want defaults", s.Get())
	}
}

func TestOnChange_ReceivesOldAndNew(t *testing.T) {
	s := newStore(t)
	type change struct{ old, cur config.Config }
	var got []change
	s.OnChange(func(old, cur config.Config) { got = append(got, change{old, cur}) })
	var second int
	s.OnChange(func(_, _ config.Config) { second++ })

	if _, _, err := s.Apply(Patch{Similarity: ptr(0.8), QualityRating: ptr(false)}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := config.Defaults()
	want.VisualSimilarity = 0.8
	want.QualityRating = false
	if len(got) != 1 || got[0].old != config.Defaults() || got[0].cur != want {
		t.Fatalf("OnChange calls = %+v, want one (defaults → %+v)", got, want)
	}
	if second != 1 {
		t.Errorf("second subscriber called %d times, want 1", second)
	}

	// A patch that changes nothing notifies no one.
	if _, _, err := s.Apply(Patch{Similarity: ptr(0.8)}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("OnChange called %d times after a no-op patch, want 1", len(got))
	}
}

func TestOnChange_CallbackMayReadTheStore(t *testing.T) {
	s := newStore(t)
	var seen config.Config
	s.OnChange(func(_, _ config.Config) { seen = s.Get() })
	_, cur, err := s.Apply(Patch{PicksDir: ptr("out")})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if seen != cur {
		t.Errorf("Get inside OnChange = %+v, want %+v", seen, cur)
	}
}

func TestApply_ConcurrentPatchesAreNotLost(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, _, err := s.Apply(Patch{AdvanceOnAction: ptr(true)}); err != nil {
			t.Errorf("Apply: %v", err)
		}
	})
	wg.Go(func() {
		if _, _, err := s.Apply(Patch{HideRejectedPhotos: ptr(true)}); err != nil {
			t.Errorf("Apply: %v", err)
		}
	})
	wg.Wait()
	got := s.Get()
	if !got.AdvanceOnAction || !got.HideRejectedPhotos {
		t.Errorf("Get = %+v, want both concurrent patches applied", got)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if *reloaded != got {
		t.Errorf("config.toml = %+v, want %+v", *reloaded, got)
	}
}

func TestSave_PersistsToTOML(t *testing.T) {
	dir := dataDir(t)
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewService(s, Hooks{}, newGate())
	if _, err := svc.Save(Patch{PicksDir: ptr("exports/luminar"), CrossDeviceMerging: ptr(true), VisualGrouping: ptr(false)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	for _, line := range []string{`picks_dir = "exports/luminar"`, "cross_device_merging = true", "visual_grouping = false"} {
		if !strings.Contains(string(raw), line) {
			t.Errorf("config.toml lacks %q:\n%s", line, raw)
		}
	}

	// A new store (the next launch) reads the saved values back.
	again, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if again.Get() != s.Get() {
		t.Errorf("reloaded = %+v, want %+v", again.Get(), s.Get())
	}
}
