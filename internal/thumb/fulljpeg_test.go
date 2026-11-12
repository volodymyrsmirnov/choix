package thumb

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFullJPEGRel_CarriesTheContentHash(t *testing.T) {
	for _, c := range []struct {
		id   int64
		hash string
		want string
	}{
		{1, "0123456789abcdef", ".choix/thumbs/01/1-full-0123456789abcdef.jpg"},
		{258, "fedcba9876543210", ".choix/thumbs/02/258-full-fedcba9876543210.jpg"},
	} {
		if got := FullJPEGRel(c.id, c.hash); got != c.want {
			t.Errorf("FullJPEGRel(%d, %q) = %q, want %q", c.id, c.hash, got, c.want)
		}
	}
	// Any other hash is reduced to a safe, deterministic name element that
	// no stored (lowercase hex) hash can spell.
	odd := []string{"", "h-Day1/IMG.HEIC", "../../x", "ABCDEF0123456789", strings.Repeat("a", 65)}
	seen := map[string]string{}
	for _, h := range odd {
		got := FullJPEGRel(5, h)
		if got != FullJPEGRel(5, h) {
			t.Errorf("FullJPEGRel(5, %q) is not deterministic", h)
		}
		dir, name := path.Split(got)
		token := strings.TrimSuffix(strings.TrimPrefix(name, "5-full-"), ".jpg")
		if dir != ".choix/thumbs/05/" || !strings.HasPrefix(name, "5-full-") || !strings.HasSuffix(name, ".jpg") ||
			token == "" || strings.ContainsAny(token, "/.-") {
			t.Errorf("FullJPEGRel(5, %q) = %q: not a single safe element in the bucket", h, got)
		}
		if isStoredHash(token) {
			t.Errorf("FullJPEGRel(5, %q) = %q: its token could be a stored hash", h, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("FullJPEGRel(5, %q) = FullJPEGRel(5, %q) = %q", h, prev, got)
		}
		seen[got] = h
	}
	if FullJPEGRel(5, hashLike) == FullJPEGRel(6, hashLike) {
		t.Error("two ids share a full-size JPEG name")
	}
}

const hashLike = "00000000000000aa"

// WriteFullJPEG writes the new file and removes every other full-size JPEG
// of the same id — an older hash's and the old-style <id>-full.jpg — while
// other ids sharing the bucket, the other tiers and keyframes stay.
func TestWriteFullJPEG_RemovesSupersededOfSameID(t *testing.T) {
	root := t.TempDir()
	bucket := filepath.Join(root, ".choix", "thumbs", "01")
	keep := map[string]string{
		"1-thumb.jpg":                   "tier",
		"1-preview.jpg":                 "tier",
		"1-keyframes/0.jpg":             "frame",
		"257-full.jpg":                  "other id, old style",
		"257-full-1111111111111111.jpg": "other id",
		"1-full.jpg.0123abcd.tmp":       "a temporary, SweepTemps's business",
	}
	gone := []string{"1-full.jpg", "1-full-1111111111111111.jpg", "1-full-s0123456789abcdef.jpg"}
	for name, data := range keep {
		writeTestFile(t, filepath.Join(bucket, filepath.FromSlash(name)), data)
	}
	for _, name := range gone {
		writeTestFile(t, filepath.Join(bucket, name), "superseded")
	}

	rel, err := WriteFullJPEG(root, 1, "2222222222222222", []byte("new"))
	if err != nil {
		t.Fatalf("WriteFullJPEG: %v", err)
	}
	if rel != FullJPEGRel(1, "2222222222222222") {
		t.Fatalf("rel = %q, want %q", rel, FullJPEGRel(1, "2222222222222222"))
	}
	if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err != nil || string(got) != "new" {
		t.Fatalf("new file = %q, %v", got, err)
	}
	for _, name := range gone {
		if _, err := os.Lstat(filepath.Join(bucket, name)); !os.IsNotExist(err) {
			t.Errorf("superseded %s is still there (%v)", name, err)
		}
	}
	for name, data := range keep {
		if got, err := os.ReadFile(filepath.Join(bucket, filepath.FromSlash(name))); err != nil || string(got) != data {
			t.Errorf("%s = %q, %v; want it untouched", name, got, err)
		}
	}

	// Writing the same (id, hash) again replaces the file in place.
	if _, err := WriteFullJPEG(root, 1, "2222222222222222", []byte("again")); err != nil {
		t.Fatalf("WriteFullJPEG again: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); string(got) != "again" {
		t.Errorf("rewritten file = %q", got)
	}
}

// A superseded name that is a symlink is not followed and not removed; the
// write itself still succeeds.
func TestWriteFullJPEG_LeavesSymlinkedSuperseded(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.jpg")
	writeTestFile(t, target, "outside")
	bucket := filepath.Join(root, ".choix", "thumbs", "03")
	if err := os.MkdirAll(bucket, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(bucket, "3-full.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFullJPEG(root, 3, hashLike, []byte("new")); err != nil {
		t.Fatalf("WriteFullJPEG: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "outside" {
		t.Errorf("the symlink's target = %q; want it untouched", got)
	}
	entries, _ := os.ReadDir(bucket)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Contains(names, "3-full.jpg") || !slices.Contains(names, "3-full-"+hashLike+".jpg") {
		t.Errorf("bucket = %v; want the symlink left and the new file written", names)
	}
}

func writeTestFile(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
