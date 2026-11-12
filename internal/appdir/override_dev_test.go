//go:build !production || server

package appdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoot_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	want := dir + "/new"
	t.Setenv("CHOIX_DATA_DIR", want)

	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != want {
		t.Fatalf("Root = %q, want %q", got, want)
	}
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Root did not create the directory: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", want)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("Root mode = %o, want 700", perm)
	}

	cfg, err := Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg != filepath.Join(want, "config.toml") {
		t.Errorf("Config = %q, want under %q", cfg, want)
	}

	db, err := LibraryDB()
	if err != nil {
		t.Fatalf("LibraryDB: %v", err)
	}
	if db != filepath.Join(want, "library.db") {
		t.Errorf("LibraryDB = %q, want under %q", db, want)
	}

	logs, err := Logs()
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if logs != filepath.Join(want, "logs") {
		t.Errorf("Logs = %q, want %q", logs, filepath.Join(want, "logs"))
	}
	if fi, err := os.Stat(logs); err != nil || !fi.IsDir() {
		t.Errorf("Logs directory not created: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(want, "models")); !os.IsNotExist(err) {
		t.Errorf("a models directory exists under the data root: %v; want none", err)
	}
}

// LegacyModels names the old models directory and creates nothing, not even
// the data root.
func TestLegacyModels_CreatesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new")
	t.Setenv("CHOIX_DATA_DIR", root)
	if got, want := LegacyModels(), filepath.Join(root, "models"); got != want {
		t.Errorf("LegacyModels = %q, want %q", got, want)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("LegacyModels created the data root (or it is unreadable): %v", err)
	}
}

func TestPreviews_RejectsBadID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHOIX_DATA_DIR", root)

	for _, id := range []string{"../x", "", "ABC", "0123456789ABCDEF", "0123456789abcde", "0123456789abcdef0", "0123456789abcdef\n"} {
		if p, err := Previews(id); err == nil {
			t.Errorf("Previews(%q) = %q, want error", id, p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "x")); !os.IsNotExist(err) {
		t.Errorf("a rejected id created something outside previews: %v", err)
	}

	const id = "0123456789abcdef"
	p, err := Previews(id)
	if err != nil {
		t.Fatalf("Previews(%q): %v", id, err)
	}
	if want := filepath.Join(root, "previews", id); p != want {
		t.Errorf("Previews = %q, want %q", p, want)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Previews did not create the directory: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", p)
	}
}

// PreviewsPath names the same directory as Previews, validates the id the
// same way, and creates nothing — not even the data root.
func TestPreviewsPath_CreatesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new")
	t.Setenv("CHOIX_DATA_DIR", root)

	for _, id := range []string{"../x", "", "ABC", "0123456789ABCDEF", "0123456789abcde", "0123456789abcdef0", "0123456789abcdef\n"} {
		if p, err := PreviewsPath(id); err == nil {
			t.Errorf("PreviewsPath(%q) = %q, want error", id, p)
		}
	}

	const id = "0123456789abcdef"
	p, err := PreviewsPath(id)
	if err != nil {
		t.Fatalf("PreviewsPath(%q): %v", id, err)
	}
	if want := filepath.Join(root, "previews", id); p != want {
		t.Errorf("PreviewsPath = %q, want %q", p, want)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("PreviewsPath created the data root (or it is unreadable): %v", err)
	}

	// Once Previews has made it, both name the same directory.
	made, err := Previews(id)
	if err != nil {
		t.Fatalf("Previews(%q): %v", id, err)
	}
	if made != p {
		t.Errorf("Previews = %q, PreviewsPath = %q; want the same", made, p)
	}
}
