package thumb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

func TestWriteCache_LayoutAndReplace(t *testing.T) {
	root := t.TempDir()
	rel, err := WriteCache(root, 258, TierPreview, []byte("one"))
	if err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	if rel != ".choix/thumbs/02/258-preview.jpg" {
		t.Fatalf("rel = %q; want .choix/thumbs/02/258-preview.jpg", rel)
	}
	if filepath.Join(root, filepath.FromSlash(rel)) != CachePath(root, 258, TierPreview) {
		t.Fatalf("rel %q disagrees with CachePath", rel)
	}
	if _, err := WriteCache(root, 258, TierPreview, []byte("two")); err != nil {
		t.Fatalf("WriteCache again: %v", err)
	}
	if got, _ := os.ReadFile(CachePath(root, 258, TierPreview)); string(got) != "two" {
		t.Fatalf("content = %q; want the newer version", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(CachePath(root, 258, TierPreview)))
	if len(entries) != 1 {
		t.Fatalf("bucket holds %d entries; want 1 (no temporary left)", len(entries))
	}
}

func TestWriteKeyframe_Layout(t *testing.T) {
	root := t.TempDir()
	rel, err := WriteKeyframe(root, 7, 3, []byte("frame"))
	if err != nil {
		t.Fatalf("WriteKeyframe: %v", err)
	}
	if rel != ".choix/thumbs/07/7-keyframes/3.jpg" {
		t.Fatalf("rel = %q; want .choix/thumbs/07/7-keyframes/3.jpg", rel)
	}
	if got, err := os.ReadFile(filepath.Join(KeyframesDir(root, 7), "3.jpg")); err != nil || string(got) != "frame" {
		t.Fatalf("keyframe = %q, %v", got, err)
	}
}

func TestWriteCache_SymlinkedBucketRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".choix", "thumbs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".choix", "thumbs", "01")); err != nil {
		t.Fatal(err)
	}
	_, err := WriteCache(root, 1, TierThumb, []byte("x"))
	if !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("WriteCache = %v; want safefs.ErrSymlink", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("something was written outside the folder: %v", entries)
	}
}

func TestWriteCache_PlantedTmpSymlink(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "IMG_0001.jpg")
	want := []byte("original bytes")
	if err := os.WriteFile(original, want, 0o600); err != nil {
		t.Fatal(err)
	}
	final := CachePath(root, 1, TierThumb)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		t.Fatal(err)
	}
	// The v1 temporary name, and the final name itself, both point at the
	// original.
	for _, link := range []string{final + ".tmp", final} {
		if err := os.Symlink(original, link); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := WriteCache(root, 1, TierThumb, []byte("thumbnail")); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	if got, _ := os.ReadFile(original); !bytes.Equal(got, want) {
		t.Fatalf("original = %q; want it untouched", got)
	}
	fi, err := os.Lstat(final)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("final = %v, %v; want a regular file", fi, err)
	}
	if got, _ := os.ReadFile(final); string(got) != "thumbnail" {
		t.Fatalf("final = %q; want the thumbnail", got)
	}
}

func TestSweepTemps(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "IMG_0001.jpg")
	if err := os.WriteFile(original, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	thumbs := filepath.Join(root, ".choix", "thumbs")
	outside := t.TempDir()
	files := map[string]string{
		"x.tmp":                             "temp",
		"02/2-thumb.jpg":                    "keep",
		"02/2-thumb.jpg.0123abcd.tmp":       "temp",
		"02/2-thumb.jpg.tmp":                "temp",
		"02/2-keyframes/1.jpg":              "keep",
		"02/2-keyframes/1.jpg.feedf00d.tmp": "temp",
	}
	for rel, content := range files {
		p := filepath.Join(thumbs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A planted *.tmp symlink to an original, and a symlinked bucket leading
	// outside the folder, are left alone.
	if err := os.Symlink(original, filepath.Join(thumbs, "02", "evil.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "victim.tmp"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(thumbs, "03")); err != nil {
		t.Fatal(err)
	}

	n, err := SweepTemps(root)
	if err != nil {
		t.Fatalf("SweepTemps: %v", err)
	}
	if n != 4 {
		t.Fatalf("removed %d; want 4", n)
	}
	var left []string
	_ = filepath.WalkDir(thumbs, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(thumbs, p)
			left = append(left, filepath.ToSlash(rel))
		}
		return err
	})
	slices.Sort(left)
	want := []string{"02/2-keyframes/1.jpg", "02/2-thumb.jpg", "02/evil.tmp", "03"}
	if !slices.Equal(left, want) {
		t.Fatalf("left = %v; want %v", left, want)
	}
	if got, _ := os.ReadFile(original); string(got) != "original" {
		t.Fatal("the original changed")
	}
	if _, err := os.Stat(filepath.Join(outside, "victim.tmp")); err != nil {
		t.Fatalf("a file outside the folder was removed: %v", err)
	}
	for _, l := range left {
		if strings.HasSuffix(l, ".tmp") && l != "02/evil.tmp" {
			t.Fatalf("temporary %s left", l)
		}
	}
}

func TestSweepTemps_NoCacheIsZero(t *testing.T) {
	n, err := SweepTemps(t.TempDir())
	if err != nil || n != 0 {
		t.Fatalf("SweepTemps = %d, %v; want 0, nil", n, err)
	}
}

func TestSweepTemps_SymlinkedThumbsRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".choix", "thumbs")); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepTemps(root); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("SweepTemps = %v; want safefs.ErrSymlink", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "victim.tmp")); err != nil {
		t.Fatalf("a file outside the folder was removed: %v", err)
	}
}
