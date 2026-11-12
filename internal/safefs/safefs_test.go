package safefs_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

// --- helpers -----------------------------------------------------------------

// tempRoot returns a temporary root with symlinks resolved, so expectations
// about paths are not disturbed by macOS's /var -> /private/var link.
func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func openDir(t *testing.T, root, rel string) *safefs.Dir {
	t.Helper()
	d, err := safefs.OpenDir(root, rel, true)
	if err != nil {
		t.Fatalf("OpenDir(%q): %v", rel, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	if got := readFile(t, path); !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s: want absent, Lstat err = %v", path, err)
	}
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("temporary files left behind: %v", m)
	}
}

// publish creates a temp file holding data and publishes it as final.
func publish(t *testing.T, d *safefs.Dir, final string, data []byte, durable bool) error {
	t.Helper()
	f, tmp, err := d.CreateTemp(final)
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	return d.Publish(f, tmp, final, durable)
}

// captureLogs routes slog's default logger into a buffer for the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) count(s string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), s)
}

func setHooks(t *testing.T, h safefs.Hooks) {
	t.Helper()
	restore := safefs.SetHooksForTest(h)
	t.Cleanup(restore)
}

// --- OpenDir -----------------------------------------------------------------

func TestOpenDir_CreatesNested(t *testing.T) {
	root := tempRoot(t)
	d, err := safefs.OpenDir(root, "a/b/c", true)
	if err != nil {
		t.Fatalf("OpenDir: %v", err)
	}
	defer func() { _ = d.Close() }()

	for _, p := range []string{"a", "a/b", "a/b/c"} {
		fi, err := os.Lstat(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !fi.IsDir() {
			t.Fatalf("%s is not a directory", p)
		}
		if perm := fi.Mode().Perm(); perm&^0o750 != 0 {
			t.Fatalf("%s mode = %v, want at most 0750", p, perm)
		}
	}

	// The handle really refers to a/b/c.
	if err := d.WriteCache("probe", []byte("x")); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	assertBytes(t, filepath.Join(root, "a/b/c/probe"), []byte("x"))

	// Opening an existing tree without create works; a missing one does not.
	d2, err := safefs.OpenDir(root, "a/b", false)
	if err != nil {
		t.Fatalf("OpenDir existing: %v", err)
	}
	_ = d2.Close()
	if _, err := safefs.OpenDir(root, "a/missing", false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenDir missing without create: err = %v, want ErrNotExist", err)
	}
	assertAbsent(t, filepath.Join(root, "a/missing"))
}

func TestOpenDir_SymlinkComponentRefused(t *testing.T) {
	t.Run("top level", func(t *testing.T) {
		root := tempRoot(t)
		if err := os.Mkdir(filepath.Join(root, "originals"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("originals", filepath.Join(root, "picks")); err != nil {
			t.Fatal(err)
		}
		if _, err := safefs.OpenDir(root, "picks", true); !errors.Is(err, safefs.ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
	})
	t.Run("middle component", func(t *testing.T) {
		root := tempRoot(t)
		if err := os.MkdirAll(filepath.Join(root, "a", "real", "c"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("real", filepath.Join(root, "a", "link")); err != nil {
			t.Fatal(err)
		}
		if _, err := safefs.OpenDir(root, "a/link/c", true); !errors.Is(err, safefs.ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
	})
	t.Run("pointing outside the root", func(t *testing.T) {
		outside := tempRoot(t)
		root := tempRoot(t)
		if err := os.Symlink(outside, filepath.Join(root, "picks")); err != nil {
			t.Fatal(err)
		}
		if _, err := safefs.OpenDir(root, "picks/sub", true); !errors.Is(err, safefs.ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
		assertAbsent(t, filepath.Join(outside, "sub"))
	})
}

func TestOpenDir_FileComponentRefused(t *testing.T) {
	root := tempRoot(t)
	writeFile(t, filepath.Join(root, "file"), []byte("x"))
	for _, rel := range []string{"file", "file/sub"} {
		if _, err := safefs.OpenDir(root, rel, true); !errors.Is(err, safefs.ErrNotDir) {
			t.Fatalf("OpenDir(%q): err = %v, want ErrNotDir", rel, err)
		}
	}
}

func TestOpenDir_RejectsNonLocal(t *testing.T) {
	root := tempRoot(t)
	for _, rel := range []string{"..", "/abs", ".", "a/../..", ""} {
		d, err := safefs.OpenDir(root, rel, true)
		if err == nil {
			_ = d.Close()
			t.Fatalf("OpenDir(%q) succeeded, want an error", rel)
		}
	}
	assertAbsent(t, filepath.Join(root, "a"))
}

// A ".." after a symlink or a file must not let the walk skip that component.
// rel is never cleaned: a path with a "..", "." or empty component is refused
// outright, before any component is looked at or created.
func TestOpenDir_DotDotDoesNotBypassComponentChecks(t *testing.T) {
	root := tempRoot(t)
	if err := os.Mkdir(filepath.Join(root, "real"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "file"), []byte("x"))

	for _, rel := range []string{"link/../cache", "file/../cache"} {
		for _, create := range []bool{true, false} {
			d, err := safefs.OpenDir(root, rel, create)
			if err == nil {
				_ = d.Close()
				t.Fatalf("OpenDir(%q, create=%v) succeeded, want an error", rel, create)
			}
			assertAbsent(t, filepath.Join(root, "cache"))
			assertAbsent(t, filepath.Join(root, "real", "cache"))
		}
	}
	// Spelled without "..", the same components still give the sentinels.
	if _, err := safefs.OpenDir(root, "link/cache", true); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("link/cache: err = %v, want ErrSymlink", err)
	}
	if _, err := safefs.OpenDir(root, "file/cache", true); !errors.Is(err, safefs.ErrNotDir) {
		t.Fatalf("file/cache: err = %v, want ErrNotDir", err)
	}
	assertAbsent(t, filepath.Join(root, "real", "cache"))
}

func TestOpenDir_RejectsNonCanonicalComponents(t *testing.T) {
	root := tempRoot(t)
	for _, rel := range []string{"a//b", "a/./b", "a/../b", "a/b/..", "./a", "a/"} {
		d, err := safefs.OpenDir(root, rel, true)
		if err == nil {
			_ = d.Close()
			t.Fatalf("OpenDir(%q) succeeded, want an error", rel)
		}
		for _, p := range []string{"a", "b"} {
			assertAbsent(t, filepath.Join(root, p))
		}
	}
}

// --- CreateTemp / WriteCache / Replace ---------------------------------------

func TestCreateTemp_DoesNotFollowPlantedSymlink(t *testing.T) {
	root := tempRoot(t)
	original := []byte("original bytes that must never change")
	writeFile(t, filepath.Join(root, "o.jpg"), original)

	d := openDir(t, root, "cache")
	dir := filepath.Join(root, "cache")
	if err := os.Symlink("../o.jpg", filepath.Join(dir, "x.jpg.tmp")); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		name := "x.jpg." + hex.EncodeToString(b) + ".tmp"
		if err := os.Symlink("../o.jpg", filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	data := []byte("thumbnail")
	if err := d.WriteCache("x.jpg", data); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	assertBytes(t, filepath.Join(root, "o.jpg"), original)
	assertBytes(t, filepath.Join(dir, "x.jpg"), data)
}

func TestCreateTemp_NameAndMode(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	f, tmp, err := d.CreateTemp("x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if !strings.HasPrefix(tmp, "x.jpg.") || !strings.HasSuffix(tmp, ".tmp") {
		t.Fatalf("tmp name = %q", tmp)
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(tmp, "x.jpg."), ".tmp")
	if _, err := hex.DecodeString(mid); err != nil || len(mid) != 8 {
		t.Fatalf("tmp name %q: want 8 hex digits, got %q", tmp, mid)
	}
	fi, err := os.Lstat(filepath.Join(root, "c", tmp))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode())
	}
}

func TestReplace_ReplacesExistingFileAtomically(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	final := filepath.Join(root, "c", "x.jpg")
	writeFile(t, final, []byte("old"))

	// A reader that opened the old version keeps reading the old bytes:
	// the new version is a different file renamed over the name.
	old, err := os.Open(final)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()

	f, tmp, err := d.CreateTemp("x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := d.Replace(f, tmp, "x.jpg"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	assertBytes(t, final, []byte("new"))
	got, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("old handle reads %q, want %q", got, "old")
	}
	assertNoTemps(t, filepath.Join(root, "c"))
}

func TestReplace_FailureRemovesTemp(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	// A non-empty directory under the final name makes the rename fail.
	if err := os.MkdirAll(filepath.Join(root, "c", "x.jpg", "inner"), 0o750); err != nil {
		t.Fatal(err)
	}
	f, tmp, err := d.CreateTemp("x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Replace(f, tmp, "x.jpg"); err == nil {
		t.Fatal("Replace succeeded, want an error")
	}
	assertNoTemps(t, filepath.Join(root, "c"))
}

func TestWriteCache_RejectsPathSeparator(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	for _, name := range []string{"a/b", "../x", "..", ".", "", "/abs"} {
		if err := d.WriteCache(name, []byte("x")); err == nil {
			t.Fatalf("WriteCache(%q) succeeded, want an error", name)
		}
	}
	assertAbsent(t, filepath.Join(root, "x"))
	assertNoTemps(t, filepath.Join(root, "c"))
}

// --- Publish -----------------------------------------------------------------

func TestPublish_NeverOverwrites(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "picks")
	final := filepath.Join(root, "picks", "p.jpg")
	writeFile(t, final, []byte("existing"))

	err := publish(t, d, "p.jpg", []byte("new"), true)
	if !errors.Is(err, safefs.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	assertBytes(t, final, []byte("existing"))
	assertNoTemps(t, filepath.Join(root, "picks"))
}

func TestPublish_PublishesFreeName(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "picks")
	for _, durable := range []bool{false, true} {
		name := "p.jpg"
		if durable {
			name = "q.jpg"
		}
		if err := publish(t, d, name, []byte("data"), durable); err != nil {
			t.Fatalf("Publish(durable=%v): %v", durable, err)
		}
		assertBytes(t, filepath.Join(root, "picks", name), []byte("data"))
	}
	assertNoTemps(t, filepath.Join(root, "picks"))
}

func TestPublish_CollisionArrivingAfterCheck(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "picks")
	final := filepath.Join(root, "picks", "p.jpg")

	// The caller's existence check: the name is free.
	if err := d.VerifyRegular("p.jpg"); err != nil {
		t.Fatal(err)
	}
	setHooks(t, safefs.Hooks{BeforeRename: func() {
		writeFile(t, final, []byte("late"))
	}})
	err := publish(t, d, "p.jpg", []byte("mine"), false)
	if !errors.Is(err, safefs.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	assertBytes(t, final, []byte("late"))
	assertNoTemps(t, filepath.Join(root, "picks"))
}

func TestPublish_FinalIsSymlinkIsNotFollowed(t *testing.T) {
	root := tempRoot(t)
	original := []byte("original")
	writeFile(t, filepath.Join(root, "o.jpg"), original)
	d := openDir(t, root, "picks")
	link := filepath.Join(root, "picks", "p.jpg")
	if err := os.Symlink("../o.jpg", link); err != nil {
		t.Fatal(err)
	}

	err := publish(t, d, "p.jpg", []byte("mine"), true)
	if !errors.Is(err, safefs.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	assertBytes(t, filepath.Join(root, "o.jpg"), original)
	if target, err := os.Readlink(link); err != nil || target != "../o.jpg" {
		t.Fatalf("symlink changed: target=%q err=%v", target, err)
	}
	assertNoTemps(t, filepath.Join(root, "picks"))
}

func TestPublish_DurableCallsSyncInOrder(t *testing.T) {
	for _, tc := range []struct {
		durable bool
		want    []string
	}{
		{true, []string{"file-sync", "rename", "dir-sync"}},
		{false, []string{"rename"}},
	} {
		root := tempRoot(t)
		d := openDir(t, root, "picks")
		var events []string
		setHooks(t, safefs.Hooks{Trace: func(e string) { events = append(events, e) }})
		if err := publish(t, d, "p.jpg", []byte("x"), tc.durable); err != nil {
			t.Fatalf("Publish(durable=%v): %v", tc.durable, err)
		}
		if !slices.Equal(events, tc.want) {
			t.Fatalf("durable=%v: events = %v, want %v", tc.durable, events, tc.want)
		}
	}
}

func TestPublish_SyncNotSupportedIsTolerated(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOTSUP, syscall.EINVAL, syscall.ENOTTY} {
		t.Run(errno.Error(), func(t *testing.T) {
			logs := captureLogs(t)
			root := tempRoot(t)
			d := openDir(t, root, "picks")
			notSupported := func(*os.File) error {
				return &os.PathError{Op: "fcntl", Path: "x", Err: errno}
			}
			setHooks(t, safefs.Hooks{SyncFile: notSupported, SyncDir: notSupported})

			for _, name := range []string{"a.jpg", "b.jpg"} {
				if err := publish(t, d, name, []byte(name), true); err != nil {
					t.Fatalf("Publish(%s): %v", name, err)
				}
				assertBytes(t, filepath.Join(root, "picks", name), []byte(name))
			}
			if err := d.Sync(); err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if n := logs.count("volume cannot sync"); n != 1 {
				t.Fatalf("%d warnings, want 1", n)
			}
		})
	}
}

func TestPublish_SyncIOErrorFails(t *testing.T) {
	eio := func(*os.File) error { return &os.PathError{Op: "fsync", Path: "x", Err: syscall.EIO} }
	for _, tc := range []struct {
		name  string
		hooks safefs.Hooks
	}{
		{"file sync", safefs.Hooks{SyncFile: eio}},
		{"directory sync", safefs.Hooks{SyncDir: eio}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tempRoot(t)
			d := openDir(t, root, "picks")
			setHooks(t, tc.hooks)
			err := publish(t, d, "p.jpg", []byte("x"), true)
			if !errors.Is(err, syscall.EIO) {
				t.Fatalf("err = %v, want EIO", err)
			}
			assertAbsent(t, filepath.Join(root, "picks", "p.jpg"))
			assertNoTemps(t, filepath.Join(root, "picks"))
		})
	}
	t.Run("Dir.Sync", func(t *testing.T) {
		root := tempRoot(t)
		d := openDir(t, root, "picks")
		setHooks(t, safefs.Hooks{SyncDir: eio})
		if err := d.Sync(); !errors.Is(err, syscall.EIO) {
			t.Fatalf("Sync err = %v, want EIO", err)
		}
	})
}

func TestPublish_ExclusiveRenameNotSupportedFallsBack(t *testing.T) {
	notSupported := func(int, string, string) error { return syscall.ENOTSUP }

	t.Run("free name", func(t *testing.T) {
		logs := captureLogs(t)
		root := tempRoot(t)
		d := openDir(t, root, "picks")
		setHooks(t, safefs.Hooks{RenameExcl: notSupported})
		for _, name := range []string{"a.jpg", "b.jpg"} {
			if err := publish(t, d, name, []byte(name), true); err != nil {
				t.Fatalf("Publish(%s): %v", name, err)
			}
			assertBytes(t, filepath.Join(root, "picks", name), []byte(name))
		}
		if n := logs.count("volume cannot rename exclusively"); n != 1 {
			t.Fatalf("%d warnings, want 1", n)
		}
		assertNoTemps(t, filepath.Join(root, "picks"))
	})
	t.Run("existing final", func(t *testing.T) {
		root := tempRoot(t)
		d := openDir(t, root, "picks")
		final := filepath.Join(root, "picks", "p.jpg")
		writeFile(t, final, []byte("existing"))
		setHooks(t, safefs.Hooks{RenameExcl: notSupported})
		if err := publish(t, d, "p.jpg", []byte("new"), true); !errors.Is(err, safefs.ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
		assertBytes(t, final, []byte("existing"))
		assertNoTemps(t, filepath.Join(root, "picks"))
	})
	t.Run("final is a symlink", func(t *testing.T) {
		root := tempRoot(t)
		original := []byte("original")
		writeFile(t, filepath.Join(root, "o.jpg"), original)
		d := openDir(t, root, "picks")
		link := filepath.Join(root, "picks", "p.jpg")
		if err := os.Symlink("../o.jpg", link); err != nil {
			t.Fatal(err)
		}
		setHooks(t, safefs.Hooks{RenameExcl: notSupported})
		if err := publish(t, d, "p.jpg", []byte("new"), true); !errors.Is(err, safefs.ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
		assertBytes(t, filepath.Join(root, "o.jpg"), original)
		if target, err := os.Readlink(link); err != nil || target != "../o.jpg" {
			t.Fatalf("symlink changed: target=%q err=%v", target, err)
		}
		assertNoTemps(t, filepath.Join(root, "picks"))
	})
	t.Run("EEXIST from the exclusive rename", func(t *testing.T) {
		root := tempRoot(t)
		d := openDir(t, root, "picks")
		setHooks(t, safefs.Hooks{RenameExcl: func(int, string, string) error { return syscall.EEXIST }})
		if err := publish(t, d, "p.jpg", []byte("new"), false); !errors.Is(err, safefs.ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
		assertAbsent(t, filepath.Join(root, "picks", "p.jpg"))
		assertNoTemps(t, filepath.Join(root, "picks"))
	})
}

// --- OpenRead / VerifyRegular / Remove / RemoveTemps -------------------------

func TestOpenRead(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	dir := filepath.Join(root, "c")
	writeFile(t, filepath.Join(dir, "f"), []byte("content"))
	writeFile(t, filepath.Join(root, "o.jpg"), []byte("original"))
	if err := os.Symlink("../o.jpg", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	f, err := d.OpenRead("f")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	got, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || string(got) != "content" {
		t.Fatalf("read %q, %v", got, err)
	}
	if _, err := d.OpenRead("absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("absent: err = %v, want ErrNotExist", err)
	}
	if f, err := d.OpenRead("link"); err == nil {
		_ = f.Close()
		t.Fatal("OpenRead followed a symlink")
	} else if !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("link: err = %v, want ErrSymlink", err)
	}
	if _, err := d.OpenRead("a/f"); err == nil {
		t.Fatal("OpenRead accepted a path with a separator")
	}
}

func TestVerifyRegular(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	dir := filepath.Join(root, "c")
	writeFile(t, filepath.Join(dir, "f"), []byte("x"))
	if err := os.Symlink("f", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := d.VerifyRegular("f", "absent"); err != nil {
		t.Fatalf("regular + absent: %v", err)
	}
	if err := d.VerifyRegular(); err != nil {
		t.Fatalf("no names: %v", err)
	}
	if err := d.VerifyRegular("f", "link"); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("symlink: err = %v, want ErrSymlink", err)
	}
	err := d.VerifyRegular("sub")
	if err == nil {
		t.Fatal("directory accepted")
	}
	if !strings.Contains(err.Error(), "sub") {
		t.Fatalf("error %q does not name the entry", err)
	}
	if err := d.VerifyRegular("a/b"); err == nil {
		t.Fatal("path with separator accepted")
	}
}

func TestRemove_RefusesDirectoryAndSymlink(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	dir := filepath.Join(root, "c")
	original := []byte("original")
	writeFile(t, filepath.Join(root, "o.jpg"), original)
	writeFile(t, filepath.Join(dir, "f"), []byte("x"))
	if err := os.Symlink("../o.jpg", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := d.Remove("f"); err != nil {
		t.Fatalf("Remove regular: %v", err)
	}
	assertAbsent(t, filepath.Join(dir, "f"))
	if err := d.Remove("absent"); err != nil {
		t.Fatalf("Remove absent: %v", err)
	}
	if err := d.Remove("sub"); err == nil {
		t.Fatal("Remove accepted a directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); err != nil {
		t.Fatalf("directory removed: %v", err)
	}
	if err := d.Remove("link"); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("Remove symlink: err = %v, want ErrSymlink", err)
	}
	// The symlink's target is never touched.
	assertBytes(t, filepath.Join(root, "o.jpg"), original)
	if err := d.Remove("a/b"); err == nil {
		t.Fatal("Remove accepted a path with a separator")
	}
}

func TestRemoveTemps(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	dir := filepath.Join(root, "c")
	original := []byte("original")
	writeFile(t, filepath.Join(root, "o.jpg"), original)
	writeFile(t, filepath.Join(dir, "a.jpg.0123abcd.tmp"), []byte("x"))
	writeFile(t, filepath.Join(dir, "b.tmp"), []byte("x"))
	writeFile(t, filepath.Join(dir, "keep.jpg"), []byte("x"))
	if err := os.Symlink("../o.jpg", filepath.Join(dir, "link.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.tmp"), 0o750); err != nil {
		t.Fatal(err)
	}

	n, err := d.RemoveTemps()
	if err != nil {
		t.Fatalf("RemoveTemps: %v", err)
	}
	if n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	assertAbsent(t, filepath.Join(dir, "a.jpg.0123abcd.tmp"))
	assertAbsent(t, filepath.Join(dir, "b.tmp"))
	assertBytes(t, filepath.Join(dir, "keep.jpg"), []byte("x"))
	if _, err := os.Lstat(filepath.Join(dir, "link.tmp")); err != nil {
		t.Fatalf("symlink removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dir.tmp")); err != nil {
		t.Fatalf("directory removed: %v", err)
	}
	assertBytes(t, filepath.Join(root, "o.jpg"), original)

	// A second call sees a fresh listing.
	writeFile(t, filepath.Join(dir, "c.tmp"), []byte("x"))
	if n, err := d.RemoveTemps(); err != nil || n != 1 {
		t.Fatalf("second RemoveTemps = %d, %v; want 1, nil", n, err)
	}
}

func TestTempBase(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	f, made, err := d.CreateTemp("IMG_0001.JPG")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if base, ok := safefs.TempBase(made); !ok || base != "IMG_0001.JPG" {
		t.Fatalf("TempBase(%q) = %q, %v; want the base CreateTemp was given", made, base, ok)
	}
	tests := []struct {
		name, base string
		ok         bool
	}{
		{"a.jpg.0123abcd.tmp", "a.jpg", true},
		{"IMG 0001 (2).HEIC.ffffffff.tmp", "IMG 0001 (2).HEIC", true},
		{"notes.0123abcd.tmp", "notes", true},       // the form only: ownership is the caller's
		{".0123abcd.tmp", "", false},                // no base
		{"a.jpg.0123ABCD.tmp", "", false},           // CreateTemp writes lower-case hex
		{"a.jpg.0123abc.tmp", "", false},            // seven digits
		{"a.jpg.0123abcde.tmp", "", false},          // nine digits
		{"a.jpg0123abcd.tmp", "", false},            // no dot before the digits
		{"a.jpg.0123abcg.tmp", "", false},           // not hex
		{"notes.tmp", "", false},                    // no digits
		{"a.jpg.0123abcd.tmp.jpg", "", false},       // not the suffix
		{"a.jpg.0123abcd.TMP", "", false},           // not the suffix
		{"backup.0123abcd.tmp.0123abcd", "", false}, // not the suffix
	}
	for _, tt := range tests {
		if base, ok := safefs.TempBase(tt.name); base != tt.base || ok != tt.ok {
			t.Errorf("TempBase(%q) = %q, %v; want %q, %v", tt.name, base, ok, tt.base, tt.ok)
		}
	}
}

// TestRemoveOwnTemps: only regular files with CreateTemp's form whose base
// the caller owns are removed; a user's file of the same form for a base the
// caller does not own, a plain *.tmp, a symlink and a directory are left
// alone, and owned is never asked about a symlink or a directory.
func TestRemoveOwnTemps(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	dir := filepath.Join(root, "c")
	original := []byte("original")
	writeFile(t, filepath.Join(root, "o.jpg"), original)
	writeFile(t, filepath.Join(dir, "a.jpg.0123abcd.tmp"), []byte("x"))
	writeFile(t, filepath.Join(dir, "notes.0123abcd.tmp"), []byte("mine"))
	writeFile(t, filepath.Join(dir, "notes.tmp"), []byte("mine too"))
	writeFile(t, filepath.Join(dir, "keep.jpg"), []byte("x"))
	if err := os.Symlink("../o.jpg", filepath.Join(dir, "a.jpg.89abcdef.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a.jpg.fedcba98.tmp"), 0o750); err != nil {
		t.Fatal(err)
	}

	var asked []string
	n, err := d.RemoveOwnTemps(func(base string) bool {
		asked = append(asked, base)
		return base == "a.jpg"
	})
	if err != nil || n != 1 {
		t.Fatalf("RemoveOwnTemps = %d, %v; want 1, nil", n, err)
	}
	slices.Sort(asked)
	if !slices.Equal(asked, []string{"a.jpg", "notes"}) {
		t.Fatalf("owned asked about %q; want only the regular files of the temporary form", asked)
	}
	assertAbsent(t, filepath.Join(dir, "a.jpg.0123abcd.tmp"))
	assertBytes(t, filepath.Join(dir, "notes.0123abcd.tmp"), []byte("mine"))
	assertBytes(t, filepath.Join(dir, "notes.tmp"), []byte("mine too"))
	assertBytes(t, filepath.Join(dir, "keep.jpg"), []byte("x"))
	if fi, err := os.Lstat(filepath.Join(dir, "a.jpg.89abcdef.tmp")); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("symlink touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.jpg.fedcba98.tmp")); err != nil {
		t.Fatalf("directory removed: %v", err)
	}
	assertBytes(t, filepath.Join(root, "o.jpg"), original)
}

func TestDirSync_Real(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	if err := d.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// --- OpenDirDurable ----------------------------------------------------------

func TestOpenDirDurable_SyncsParentOfEachCreatedComponent(t *testing.T) {
	root := tempRoot(t)
	if err := os.Mkdir(filepath.Join(root, "a"), 0o750); err != nil {
		t.Fatal(err)
	}
	var events []string
	var parents []os.FileInfo
	setHooks(t, safefs.Hooks{
		Trace: func(e string) { events = append(events, e) },
		SyncMkdir: func(f *os.File) error {
			fi, err := f.Stat()
			if err != nil {
				return err
			}
			parents = append(parents, fi)
			return f.Sync()
		},
	})

	d, err := safefs.OpenDirDurable(root, "a/b/c")
	if err != nil {
		t.Fatalf("OpenDirDurable: %v", err)
	}
	_ = d.Close()
	if !slices.Equal(events, []string{"mkdir-sync", "mkdir-sync"}) {
		t.Fatalf("events = %v, want two mkdir-sync (b and c were created; a existed)", events)
	}
	for i, want := range []string{"a", "a/b"} {
		fi, err := os.Stat(filepath.Join(root, want))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(parents[i], fi) {
			t.Fatalf("sync %d was not of %s", i, want)
		}
	}

	// An existing tree is opened without any sync.
	events = nil
	d, err = safefs.OpenDirDurable(root, "a/b/c")
	if err != nil {
		t.Fatalf("OpenDirDurable existing: %v", err)
	}
	_ = d.Close()
	if len(events) != 0 {
		t.Fatalf("events for an existing tree = %v, want none", events)
	}
}

func TestOpenDirDurable_SyncErrors(t *testing.T) {
	t.Run("EIO fails", func(t *testing.T) {
		root := tempRoot(t)
		setHooks(t, safefs.Hooks{SyncMkdir: func(*os.File) error {
			return &os.PathError{Op: "fcntl", Path: "x", Err: syscall.EIO}
		}})
		if _, err := safefs.OpenDirDurable(root, "picks/day"); !errors.Is(err, syscall.EIO) {
			t.Fatalf("err = %v, want EIO", err)
		}
	})
	t.Run("not supported is tolerated", func(t *testing.T) {
		logs := captureLogs(t)
		root := tempRoot(t)
		setHooks(t, safefs.Hooks{SyncMkdir: func(*os.File) error {
			return &os.PathError{Op: "fcntl", Path: "x", Err: syscall.ENOTSUP}
		}})
		d, err := safefs.OpenDirDurable(root, "picks/day")
		if err != nil {
			t.Fatalf("OpenDirDurable: %v", err)
		}
		_ = d.Close()
		if n := logs.count("volume cannot sync"); n != 1 {
			t.Fatalf("%d warnings, want 1", n)
		}
	})
	t.Run("real", func(t *testing.T) {
		root := tempRoot(t)
		d, err := safefs.OpenDirDurable(root, "x/y")
		if err != nil {
			t.Fatalf("OpenDirDurable: %v", err)
		}
		_ = d.Close()
	})
	t.Run("symlink component refused", func(t *testing.T) {
		root := tempRoot(t)
		if err := os.Symlink(".", filepath.Join(root, "picks")); err != nil {
			t.Fatal(err)
		}
		if _, err := safefs.OpenDirDurable(root, "picks/day"); !errors.Is(err, safefs.ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
		assertAbsent(t, filepath.Join(root, "day"))
	})
}

// A failed parent sync removes, deepest first, every directory this call
// created, so a retry creates and syncs them again; directories that existed
// before are left alone.
func TestOpenDirDurable_SyncFailureRemovesCreatedDirs(t *testing.T) {
	root := tempRoot(t)
	if err := os.Mkdir(filepath.Join(root, "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	var calls int
	eio := func(*os.File) error {
		calls++
		return &os.PathError{Op: "fcntl", Path: "x", Err: syscall.EIO}
	}

	for _, rel := range []string{"x/y/z", "x/y/z"} { // the retry fails the same way
		calls = 0
		setHooks(t, safefs.Hooks{SyncMkdir: eio})
		if _, err := safefs.OpenDirDurable(root, rel); !errors.Is(err, syscall.EIO) {
			t.Fatalf("err = %v, want EIO", err)
		}
		if calls != 1 {
			t.Fatalf("SyncMkdir calls = %d, want 1 (y was created, then its parent sync failed)", calls)
		}
		assertAbsent(t, filepath.Join(root, "x", "y"))
		if fi, err := os.Stat(filepath.Join(root, "x")); err != nil || !fi.IsDir() {
			t.Fatalf("pre-existing x touched: %v", err)
		}
	}

	// A failure deeper down removes the deeper directory and the ones above
	// it that this call created.
	calls = 0
	setHooks(t, safefs.Hooks{SyncMkdir: func(f *os.File) error {
		calls++
		if calls == 2 {
			return &os.PathError{Op: "fcntl", Path: "x", Err: syscall.EIO}
		}
		return f.Sync()
	}})
	if _, err := safefs.OpenDirDurable(root, "x/y/z"); !errors.Is(err, syscall.EIO) {
		t.Fatalf("err = %v, want EIO", err)
	}
	assertAbsent(t, filepath.Join(root, "x", "y"))

	// Once syncs work again, the tree is created and every new component's
	// parent is synced.
	var events []string
	setHooks(t, safefs.Hooks{Trace: func(e string) { events = append(events, e) }})
	d, err := safefs.OpenDirDurable(root, "x/y/z")
	if err != nil {
		t.Fatalf("OpenDirDurable after recovery: %v", err)
	}
	_ = d.Close()
	if !slices.Equal(events, []string{"mkdir-sync", "mkdir-sync"}) {
		t.Fatalf("events = %v, want two mkdir-sync", events)
	}
}

// A created directory that is no longer empty is never removed recursively:
// the sync error is returned joined with the removal error, and it stays.
func TestOpenDirDurable_SyncFailureKeepsNonEmptyDir(t *testing.T) {
	root := tempRoot(t)
	var calls int
	setHooks(t, safefs.Hooks{SyncMkdir: func(f *os.File) error {
		calls++
		if calls == 2 { // parent = a: something lands in a meanwhile
			writeFile(t, filepath.Join(root, "a", "stray"), []byte("x"))
			return &os.PathError{Op: "fcntl", Path: "x", Err: syscall.EIO}
		}
		return f.Sync()
	}})
	_, err := safefs.OpenDirDurable(root, "a/b")
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("err = %v, want EIO", err)
	}
	if !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("err = %v, want the removal failure joined", err)
	}
	assertAbsent(t, filepath.Join(root, "a", "b"))
	assertBytes(t, filepath.Join(root, "a", "stray"), []byte("x"))
}

// --- OpenRead never blocks; SyncFile -----------------------------------------

func TestOpenRead_NonRegularIsRefusedWithoutBlocking(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	dir := filepath.Join(root, "c")
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo.jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fifo.jpg", "sub"} {
		done := make(chan error, 1)
		go func() {
			f, err := d.OpenRead(name)
			if err == nil {
				_ = f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, safefs.ErrNotRegular) {
				t.Fatalf("%s: err = %v, want ErrNotRegular", name, err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("%s: error %q does not name the entry", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("OpenRead(%s) blocked", name)
		}
	}
}

func TestDirSyncFile(t *testing.T) {
	root := tempRoot(t)
	d := openDir(t, root, "c")
	writeFile(t, filepath.Join(root, "c", "f"), []byte("x"))
	open := func() *os.File {
		f, err := d.OpenRead("f")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}

	t.Run("real, read-only descriptor", func(t *testing.T) {
		var events []string
		setHooks(t, safefs.Hooks{Trace: func(e string) { events = append(events, e) }})
		if err := d.SyncFile(open()); err != nil {
			t.Fatalf("SyncFile: %v", err)
		}
		if !slices.Equal(events, []string{"file-sync"}) {
			t.Fatalf("events = %v, want [file-sync]", events)
		}
	})
	t.Run("EIO fails", func(t *testing.T) {
		setHooks(t, safefs.Hooks{SyncFile: func(*os.File) error { return syscall.EIO }})
		if err := d.SyncFile(open()); !errors.Is(err, syscall.EIO) {
			t.Fatalf("err = %v, want EIO", err)
		}
	})
	t.Run("not supported is tolerated", func(t *testing.T) {
		setHooks(t, safefs.Hooks{SyncFile: func(*os.File) error { return syscall.ENOTSUP }})
		if err := d.SyncFile(open()); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}
