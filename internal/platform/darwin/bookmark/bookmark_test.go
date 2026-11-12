//go:build darwin

package bookmark

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// forceSandboxed makes the package see the given sandbox answer for the rest
// of the test.
func forceSandboxed(t *testing.T, v bool) {
	t.Helper()
	orig := sandboxed
	sandboxed = func() bool { return v }
	t.Cleanup(func() { sandboxed = orig })
}

// newDir returns a fresh directory under t.TempDir and its real path.
func newDir(t *testing.T, name string) (dir, real string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, evalSymlinks(t, dir)
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func mustCreate(t *testing.T, path string) []byte {
	t.Helper()
	data, err := New().Create(path)
	if err != nil {
		t.Fatalf("Create(%s): %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("Create(%s) returned no bytes", path)
	}
	return data
}

func TestBookmark_RoundTrip(t *testing.T) {
	dir, real := newDir(t, "trip")
	data := mustCreate(t, dir)

	got, stale, err := New().Resolve(data)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != real {
		t.Errorf("Resolve path = %q, want %q", got, real)
	}
	if stale {
		t.Error("Resolve stale = true for a fresh bookmark")
	}
}

func TestBookmark_FollowsRename(t *testing.T) {
	dir, _ := newDir(t, "before")
	data := mustCreate(t, dir)

	renamed := filepath.Join(filepath.Dir(dir), "after")
	if err := os.Rename(dir, renamed); err != nil {
		t.Fatal(err)
	}
	got, _, err := New().Resolve(data)
	if err != nil {
		t.Fatalf("Resolve after rename: %v", err)
	}
	if want := evalSymlinks(t, renamed); got != want {
		t.Errorf("Resolve path = %q, want %q", got, want)
	}
}

func TestBookmark_DeletedIsErrGone(t *testing.T) {
	dir, _ := newDir(t, "gone")
	data := mustCreate(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	_, _, err := New().Resolve(data)
	if !errors.Is(err, platform.ErrGone) {
		t.Fatalf("Resolve of a deleted folder: err = %v, want ErrGone", err)
	}
	if errors.Is(err, platform.ErrUnusable) {
		t.Errorf("err = %v is also ErrUnusable", err)
	}
	if !strings.Contains(err.Error(), "NSCocoaErrorDomain") {
		t.Errorf("err = %v, want the system error wrapped", err)
	}

	if _, stop, err := New().StartAccess(data); !errors.Is(err, platform.ErrGone) || stop != nil {
		t.Errorf("StartAccess of a deleted folder: stop != nil is %v, err = %v, want ErrGone", stop != nil, err)
	}
}

func TestBookmark_GarbageIsErrUnusable(t *testing.T) {
	for _, data := range [][]byte{[]byte("x"), nil, {}} {
		_, _, err := New().Resolve(data)
		if !errors.Is(err, platform.ErrUnusable) {
			t.Errorf("Resolve(%q): err = %v, want ErrUnusable", data, err)
		}
		if errors.Is(err, platform.ErrGone) {
			t.Errorf("Resolve(%q): err = %v is also ErrGone", data, err)
		}
	}
	if _, stop, err := New().StartAccess([]byte("x")); !errors.Is(err, platform.ErrUnusable) || stop != nil {
		t.Errorf("StartAccess(garbage): stop != nil is %v, err = %v, want ErrUnusable", stop != nil, err)
	}
	if fresh, err := New().Refresh([]byte("x")); !errors.Is(err, platform.ErrUnusable) || fresh != nil {
		t.Errorf("Refresh(garbage) = %d bytes, %v, want nil, ErrUnusable", len(fresh), err)
	}
}

func TestBookmark_StartAccessStopTwice(t *testing.T) {
	forceSandboxed(t, false)
	dir, real := newDir(t, "lease")
	data := mustCreate(t, dir)

	path, stop, err := New().StartAccess(data)
	if err != nil {
		t.Fatalf("StartAccess: %v", err)
	}
	if stop == nil {
		t.Fatal("StartAccess returned a nil stop")
	}
	if path != real {
		t.Errorf("StartAccess path = %q, want %q", path, real)
	}
	stop()
	stop()
}

func TestBookmark_RefreshAfterRename(t *testing.T) {
	dir, _ := newDir(t, "old-name")
	data := mustCreate(t, dir)
	orig := bytes.Clone(data)

	renamed := filepath.Join(filepath.Dir(dir), "new-name")
	if err := os.Rename(dir, renamed); err != nil {
		t.Fatal(err)
	}
	fresh, err := New().Refresh(data)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(fresh) == 0 || bytes.Equal(fresh, data) {
		t.Fatal("Refresh returned no new bytes")
	}
	if !bytes.Equal(data, orig) {
		t.Error("Refresh changed the caller's old bytes")
	}

	got, stale, err := New().Resolve(fresh)
	if err != nil {
		t.Fatalf("Resolve(refreshed): %v", err)
	}
	if want := evalSymlinks(t, renamed); got != want {
		t.Errorf("refreshed bookmark resolves to %q, want %q", got, want)
	}
	if stale {
		t.Error("refreshed bookmark is stale")
	}
}

func TestBookmark_RefreshOfDeletedKeepsError(t *testing.T) {
	dir, _ := newDir(t, "deleted")
	data := mustCreate(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	fresh, err := New().Refresh(data)
	if !errors.Is(err, platform.ErrGone) {
		t.Fatalf("Refresh of a deleted folder: err = %v, want ErrGone", err)
	}
	if fresh != nil {
		t.Errorf("Refresh returned %d bytes with an error", len(fresh))
	}
}

func TestBookmark_CreateOnFileFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(file, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := New().Create(file)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Create(file) = %d bytes, %v, want an error containing %q", len(data), err, "not a directory")
	}
	if data != nil {
		t.Errorf("Create(file) returned %d bytes with an error", len(data))
	}
}

// The tests below cover the requirements on every entry point that the
// brief's cases do not reach on their own.

func TestBookmark_CreateMissingIsErrUnusable(t *testing.T) {
	data, err := New().Create(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, platform.ErrUnusable) || data != nil {
		t.Fatalf("Create(missing) = %d bytes, %v, want nil, ErrUnusable", len(data), err)
	}
	for _, path := range []string{"", "a\x00b"} {
		if _, err := New().Create(path); !errors.Is(err, platform.ErrUnusable) {
			t.Errorf("Create(%q): err = %v, want ErrUnusable", path, err)
		}
	}
}

func TestBookmark_SandboxedRoundTrip(t *testing.T) {
	forceSandboxed(t, true)
	dir, real := newDir(t, "scoped")
	data := mustCreate(t, dir)
	path, stop, err := New().StartAccess(data)
	if err != nil {
		t.Fatalf("StartAccess: %v", err)
	}
	stop()
	if path != real {
		t.Errorf("StartAccess path = %q, want %q", path, real)
	}
}

func TestRefusedStart(t *testing.T) {
	forceSandboxed(t, true)
	if err := refusedStart("/x"); !errors.Is(err, platform.ErrUnusable) {
		t.Errorf("sandboxed refusal: err = %v, want ErrUnusable", err)
	}
	forceSandboxed(t, false)
	if err := refusedStart("/x"); err != nil {
		t.Errorf("unsandboxed refusal: err = %v, want a lease", err)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		domain string
		code   int
		want   error
	}{
		{"NSCocoaErrorDomain", 4, platform.ErrGone},
		{"NSCocoaErrorDomain", 256, platform.ErrUnusable},
		{"NSCocoaErrorDomain", 259, platform.ErrUnusable},
		{"NSCocoaErrorDomain", 260, platform.ErrUnusable},
		{"NSPOSIXErrorDomain", 4, platform.ErrUnusable},
		{"NSOSStatusErrorDomain", -35, platform.ErrUnusable},
	}
	for _, tt := range tests {
		if got := classify(tt.domain, tt.code); got != tt.want {
			t.Errorf("classify(%s, %d) = %v, want %v", tt.domain, tt.code, got, tt.want)
		}
	}
}

func TestEntryPoints_CatchObjCException(t *testing.T) {
	err := raiseForTest("ChoixTestException")
	if !errors.Is(err, platform.ErrUnusable) || !strings.Contains(err.Error(), "ChoixTestException") {
		t.Fatalf("err = %v, want ErrUnusable naming ChoixTestException", err)
	}
}
