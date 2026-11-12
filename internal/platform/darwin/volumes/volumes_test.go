//go:build darwin

package volumes

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// otherUUID is a well-formed volume UUID that no mounted volume has.
const otherUUID = "00000000-0000-4000-8000-0000000C4013"

func mustInfo(t *testing.T, path string) platform.VolumeInfo {
	t.Helper()
	info, err := New().Info(path)
	if err != nil {
		t.Fatalf("Info(%s): %v", path, err)
	}
	return info
}

func TestVolumes_InfoOfTempDir(t *testing.T) {
	info := mustInfo(t, t.TempDir())
	if info.UUID == "" {
		t.Errorf("UUID is empty, want the temp volume's UUID")
	}
	if info.ReadOnly {
		t.Errorf("ReadOnly = true, want false")
	}
	if info.Name == "" {
		t.Errorf("Name is empty")
	}
}

func TestVolumes_MountedBootVolume(t *testing.T) {
	info := mustInfo(t, "/")
	if info.UUID == "" {
		t.Fatal("boot volume has no UUID")
	}
	if !New().Mounted(info.UUID) {
		t.Errorf("Mounted(%s) = false, want true for the boot volume", info.UUID)
	}
}

func TestVolumes_UnknownAndEmptyUUIDNotMounted(t *testing.T) {
	v := New()
	if v.Mounted(otherUUID) {
		t.Errorf("Mounted(%s) = true, want false", otherUUID)
	}
	if v.Mounted("") {
		t.Errorf(`Mounted("") = true, want false`)
	}
}

func TestVolumes_InTrash_UserTrash(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	trash := filepath.Join(home, ".Trash")
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(trash, "choix-test-"+hex.EncodeToString(suffix[:]))
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Skipf("~/.Trash is not writable: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove %s: %v", dir, err)
		}
	})

	in, err := New().InTrash(dir)
	if err != nil {
		t.Fatalf("InTrash(%s): %v", dir, err)
	}
	if !in {
		t.Errorf("InTrash(%s) = false, want true", dir)
	}
}

func TestVolumes_NotInTrash_LookalikeName(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{".Trash/x", ".Trashes/501/x"} {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		in, err := New().InTrash(dir)
		if err != nil {
			t.Fatalf("InTrash(%s): %v", rel, err)
		}
		if in {
			t.Errorf("InTrash(%s) = true, want false", rel)
		}
	}
}

func TestVolumes_InTrash_MissingPathIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	in, err := New().InTrash(path)
	if err == nil {
		t.Fatalf("InTrash(missing) = %v, nil; want an error", in)
	}
	if in {
		t.Errorf("InTrash(missing) = true alongside error %v", err)
	}
}

func TestVolumes_AbsentOn(t *testing.T) {
	v := New()
	root := t.TempDir()
	uuid := mustInfo(t, root).UUID
	if uuid == "" {
		t.Fatal("temp volume has no UUID")
	}

	gone := filepath.Join(root, "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if !v.AbsentOn(gone, uuid) {
		t.Errorf("AbsentOn(removed dir, temp volume) = false, want true")
	}
	if v.AbsentOn(gone, otherUUID) {
		t.Errorf("AbsentOn(removed dir, other volume) = true, want false")
	}
	if v.AbsentOn(gone, "") {
		t.Errorf(`AbsentOn(removed dir, "") = true, want false`)
	}

	existing := filepath.Join(root, "here")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if v.AbsentOn(existing, uuid) {
		t.Errorf("AbsentOn(existing dir) = true, want false")
	}

	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o700); err != nil {
			t.Errorf("restore mode of %s: %v", locked, err)
		}
	})
	if v.AbsentOn(filepath.Join(locked, "x"), uuid) {
		t.Errorf("AbsentOn(path under a 0000 directory) = true, want false")
	}

	// A path that is not already clean is refused as given: cleaning it would
	// turn lstat's ENOTDIR or EACCES into the ENOENT of another path.
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		file + "/../missing",
		locked + "/../missing",
		root + "/./missing",
		root + "//missing",
		root + "/missing/",
	} {
		if v.AbsentOn(p, uuid) {
			t.Errorf("AbsentOn(%s) = true, want false for a path that is not clean", p)
		}
	}
	if v.AbsentOn("missing", uuid) {
		t.Errorf("AbsentOn(relative path) = true, want false")
	}

	// A stored path under a mount point whose drive is not mounted: the
	// probe lands on whatever volume holds /Volumes.
	stale := "/Volumes/choix-no-such-volume/x"
	bootUUID := mustInfo(t, "/").UUID
	volumesUUID := mustInfo(t, "/Volumes").UUID
	if got, want := v.AbsentOn(stale, bootUUID), bootUUID == volumesUUID; got != want {
		t.Errorf("AbsentOn(%s, boot volume) = %v, want %v (/Volumes on the boot volume: %v)", stale, got, want, want)
	}
	if !v.AbsentOn(stale, volumesUUID) {
		t.Errorf("AbsentOn(%s, volume of /Volumes) = false, want true", stale)
	}
	if v.AbsentOn(stale, otherUUID) {
		t.Errorf("AbsentOn(%s, other volume) = true, want false", stale)
	}
}

func TestVolumes_InfoMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	if info, err := New().Info(path); err == nil {
		t.Fatalf("Info(missing) = %+v, nil; want an error", info)
	}
}

func TestVolumes_ExceptionBecomesError(t *testing.T) {
	err := raiseForTest("ChoixTestException")
	if err == nil || !strings.Contains(err.Error(), "ChoixTestException") {
		t.Fatalf("err = %v, want one naming ChoixTestException", err)
	}
}
