package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeFiles creates each name in dir with a few bytes.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// dirNames lists dir's entries.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRemoveLegacyModels(t *testing.T) {
	models := []string{"clip_vit_b32.onnx", "topiq_nr_koniq_384.onnx", "topiq_nr_koniq_384.onnx.lock", "clip_vit_b32.onnx.part"}

	t.Run("other files are kept", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "models")
		writeFiles(t, dir, append(slices.Clone(models), "notes.txt")...)
		removed, err := removeLegacyModels(dir)
		if err != nil {
			t.Fatalf("removeLegacyModels: %v", err)
		}
		slices.Sort(removed)
		want := slices.Sorted(slices.Values(models))
		if !slices.Equal(removed, want) {
			t.Errorf("removed = %v; want %v", removed, want)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{"notes.txt"}) {
			t.Errorf("directory holds %v; want only notes.txt", got)
		}
	})

	t.Run("only model files: the directory goes too", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "models")
		writeFiles(t, dir, models...)
		if _, err := removeLegacyModels(dir); err != nil {
			t.Fatalf("removeLegacyModels: %v", err)
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("directory still there (%v); want it removed", err)
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		removed, err := removeLegacyModels(filepath.Join(t.TempDir(), "models"))
		if err != nil || len(removed) != 0 {
			t.Errorf("removeLegacyModels = %v, %v; want nothing and no error", removed, err)
		}
		if removed, err := removeLegacyModels(""); err != nil || len(removed) != 0 {
			t.Errorf("removeLegacyModels(\"\") = %v, %v; want nothing and no error", removed, err)
		}
	})

	t.Run("a symlinked model is removed as a link", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "elsewhere.bin")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(base, "models")
		writeFiles(t, dir, "notes.txt")
		if err := os.Symlink(target, filepath.Join(dir, "clip_vit_b32.onnx")); err != nil {
			t.Fatal(err)
		}
		if _, err := removeLegacyModels(dir); err != nil {
			t.Fatalf("removeLegacyModels: %v", err)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{"notes.txt"}) {
			t.Errorf("directory holds %v; want only notes.txt", got)
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
			t.Errorf("link target = %q, %v; want it untouched", b, err)
		}
	})

	t.Run("a symlinked directory is left alone", func(t *testing.T) {
		base := t.TempDir()
		realDir := filepath.Join(base, "realDir")
		writeFiles(t, realDir, models...)
		dir := filepath.Join(base, "models")
		if err := os.Symlink(realDir, dir); err != nil {
			t.Fatal(err)
		}
		removed, err := removeLegacyModels(dir)
		if err != nil || len(removed) != 0 {
			t.Errorf("removeLegacyModels = %v, %v; want nothing and no error", removed, err)
		}
		if fi, err := os.Lstat(dir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link is gone or changed: %v", err)
		}
		if got := dirNames(t, realDir); len(got) != len(models) {
			t.Errorf("link target holds %v; want every file kept", got)
		}
	})

	t.Run("a directory with a model's name is left alone", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "models")
		writeFiles(t, filepath.Join(dir, "clip_vit_b32.onnx.d"), "inside")
		if _, err := removeLegacyModels(dir); err != nil {
			t.Fatalf("removeLegacyModels: %v", err)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{"clip_vit_b32.onnx.d"}) {
			t.Errorf("directory holds %v; want the subdirectory kept", got)
		}
	})
}

// Start removes the models an earlier build downloaded into the data
// directory.
func TestRuntime_StartRemovesLegacyModels(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.data, "models")
	writeFiles(t, dir, "clip_vit_b32.onnx", "topiq_nr_koniq_384.onnx")
	r := h.start(t)
	waitFor(t, "the old models directory to be removed", func() bool {
		_, err := os.Lstat(dir)
		return os.IsNotExist(err)
	})
	r.Shutdown()
}
