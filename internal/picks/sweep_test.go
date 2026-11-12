package picks

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

// sweepTestWrite writes data at root/rel, creating its directories.
func sweepTestWrite(t *testing.T, root, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	return p
}

// sweepTestLeftover makes what an export of base into dir (relative to root)
// leaves when it is cut short after its copy began: a temporary created by
// safefs.CreateTemp, as publishCopy creates it, with part of the bytes.
func sweepTestLeftover(t *testing.T, root, dir, base string) string {
	t.Helper()
	d, err := safefs.OpenDirDurable(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	f, name, err := d.CreateTemp(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("partial"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, filepath.FromSlash(dir), name)
}

func sweepTestPresent(t *testing.T, p string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(p) //nolint:gosec // test fixture
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, %v; want %q kept", p, got, err, want)
	}
}

func sweepTestAbsent(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still exists (%v); want it removed", p, err)
	}
}

// TestSweepTemps_RemovesOnlyProvablyOwnTemporaries: a temporary is removed
// only when its base is a name Choix itself would export into that
// directory — a known file's base name (or a collision-suffixed variant)
// in the matching subdirectory, or a recorded export path. Files that
// merely have the temporary form are the user's and are kept, as is
// everything outside the picks directory.
func TestSweepTemps_RemovesOnlyProvablyOwnTemporaries(t *testing.T) {
	e := newEnv(t, "picks")
	e.addFile(t, "Day1/IMG_0001.JPG", "PIXELS")
	e.addFile(t, "IMG_0002", "PIXELS") // no extension, at the root
	renamed := e.addFile(t, "Day2/IMG_0003.JPG", "PIXELS")
	e.setRow(t, renamed, "picked", "picks/Elsewhere/IMG 3 (copy).JPG")

	removed := []string{
		sweepTestLeftover(t, e.root, "picks/Day1", "IMG_0001.JPG"),
		sweepTestLeftover(t, e.root, "picks/Day1", "IMG_0001_2.JPG"), // a collision suffix
		sweepTestLeftover(t, e.root, "picks", "IMG_0002_17"),
		sweepTestLeftover(t, e.root, "picks/Elsewhere", "IMG 3 (copy).JPG"), // a recorded export path
	}
	kept := []string{
		sweepTestWrite(t, e.root, "picks/notes.0123abcd.tmp", []byte("mine")),
		sweepTestWrite(t, e.root, "picks/Day1/IMG_9999.JPG.0123abcd.tmp", []byte("mine")), // unknown file
		sweepTestWrite(t, e.root, "picks/IMG_0001.JPG.0123abcd.tmp", []byte("mine")),      // not where it would be exported
		sweepTestWrite(t, e.root, "picks/Day1/IMG_0001_01.JPG.0123abcd.tmp", []byte("mine")),
		sweepTestWrite(t, e.root, "picks/Day1/IMG_0001_1.JPG.0123abcd.tmp", []byte("mine")),
		sweepTestWrite(t, e.root, "picks/Day1/notes.tmp", []byte("mine")),
		sweepTestWrite(t, e.root, "Day1/IMG_0001.JPG.0123abcd.tmp", []byte("mine")), // outside the picks directory
	}

	n, err := SweepTemps(e.st, e.root, "picks")
	if err != nil || n != len(removed) {
		t.Fatalf("SweepTemps = %d, %v; want %d, nil", n, err, len(removed))
	}
	for _, p := range removed {
		sweepTestAbsent(t, p)
	}
	for _, p := range kept {
		sweepTestPresent(t, p, []byte("mine"))
	}
	if got := readFile(t, e.root, "Day1/IMG_0001.JPG"); got != "PIXELS" {
		t.Fatalf("original = %q; want it untouched", got)
	}
}

// TestSweepTemps_AfterInterruptedPick: an export cut short after its copy
// began leaves a temporary that the next sweep removes, and the pick then
// still works.
func TestSweepTemps_AfterInterruptedPick(t *testing.T) {
	e := newEnv(t, "exports/out")
	id := e.addFile(t, "Day1/IMG_0001.JPG", "PIXELS")
	leftover := sweepTestLeftover(t, e.root, "exports/out/Day1", "IMG_0001.JPG")

	if n, err := SweepTemps(e.st, e.root, "exports/out"); err != nil || n != 1 {
		t.Fatalf("SweepTemps = %d, %v; want 1, nil", n, err)
	}
	sweepTestAbsent(t, leftover)
	if err := e.pick(id); err != nil {
		t.Fatalf("pick after the sweep: %v", err)
	}
	if got := regularFilesUnder(t, e.root, "exports"); len(got) != 1 {
		t.Fatalf("exports/ holds %q; want the one copy", got)
	}
}

// TestSweepTemps_MissingDir: a folder without a picks directory has nothing
// to sweep, and none is created.
func TestSweepTemps_MissingDir(t *testing.T) {
	e := newEnv(t, "picks")
	if n, err := SweepTemps(e.st, e.root, "picks"); err != nil || n != 0 {
		t.Fatalf("SweepTemps = %d, %v; want 0, nil", n, err)
	}
	sweepTestAbsent(t, filepath.Join(e.root, "picks"))
}

// TestSweepTemps_NoSymlinkFollowed: a picks directory that is a symlink is
// refused (safefs.ErrSymlink), and a symlinked subdirectory or temporary
// inside it is neither followed nor removed — what they point at is kept,
// even when its name is one Choix would own.
func TestSweepTemps_NoSymlinkFollowed(t *testing.T) {
	outside := t.TempDir()
	victim := sweepTestWrite(t, outside, "Day1/IMG_0001.JPG.0123abcd.tmp", []byte("elsewhere"))
	victimTop := sweepTestWrite(t, outside, "IMG_0001.JPG.0123abcd.tmp", []byte("elsewhere"))

	e := newEnv(t, "picks")
	e.addFile(t, "Day1/IMG_0001.JPG", "PIXELS")
	e.addFile(t, "IMG_0001.JPG", "PIXELS")
	if err := os.Symlink(outside, filepath.Join(e.root, "picks")); err != nil {
		t.Fatal(err)
	}
	if n, err := SweepTemps(e.st, e.root, "picks"); !errors.Is(err, safefs.ErrSymlink) || n != 0 {
		t.Fatalf("SweepTemps through a symlinked picks directory = %d, %v; want 0, ErrSymlink", n, err)
	}
	sweepTestPresent(t, victim, []byte("elsewhere"))
	sweepTestPresent(t, victimTop, []byte("elsewhere"))

	e2 := newEnv(t, "picks")
	e2.addFile(t, "Day1/IMG_0001.JPG", "PIXELS")
	e2.addFile(t, "IMG_0001.JPG", "PIXELS")
	if err := os.MkdirAll(filepath.Join(e2.root, "picks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "Day1"), filepath.Join(e2.root, "picks", "Day1")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e2.root, "picks", "IMG_0001.JPG.89abcdef.tmp")
	if err := os.Symlink(victimTop, link); err != nil {
		t.Fatal(err)
	}
	if n, err := SweepTemps(e2.st, e2.root, "picks"); err != nil || n != 0 {
		t.Fatalf("SweepTemps with symlinks inside = %d, %v; want 0, nil", n, err)
	}
	sweepTestPresent(t, victim, []byte("elsewhere"))
	sweepTestPresent(t, victimTop, []byte("elsewhere"))
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("the symlinked temporary was removed: %v", err)
	}
}
