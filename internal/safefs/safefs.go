// Package safefs is the only way Choix writes inside a user's folder. It
// confines every write to a directory that was opened component by component
// from the folder root, where no component may be a symbolic link, and creates
// files exclusively under a random temporary name that is then renamed within
// that same directory.
//
// The threat model (R16) is accidental or stale symlinks and Choix's own bugs,
// not a concurrent local attacker with write access to the folder: checks are
// made when a directory is opened and when a file is published, and nothing
// re-checks a directory that another process swaps afterwards.
//
// Every name handed to a Dir method (base, final, name, tmpName) is a single
// path element: not empty, not "." or "..", and without a "/".
package safefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
)

// ErrSymlink reports a path component, or a file, that is a symbolic link
// where safefs requires a real directory or a regular file.
var ErrSymlink = errors.New("safefs: path component is a symbolic link")

// ErrNotDir reports a path component that exists but is not a directory.
var ErrNotDir = errors.New("safefs: path component is not a directory")

// ErrExists reports that Publish refused to overwrite an existing entry.
var ErrExists = errors.New("safefs: the target already exists")

// ErrNotRegular reports an entry that exists but is not a regular file (a
// directory, FIFO, socket or device) where safefs requires one.
var ErrNotRegular = errors.New("safefs: not a regular file")

// dirPerm is the mode OpenDir gives the directories it creates.
const dirPerm = 0o750

// tempAttempts bounds how many random temporary names CreateTemp tries.
const tempAttempts = 100

// The form of CreateTemp's names: base + "." + tempHexDigits random
// lower-case hex digits + tempSuffix (TempBase recognises it).
const (
	tempHexDigits = 8
	tempSuffix    = ".tmp"
)

// Dir is a directory opened inside a root by OpenDir. Its methods operate
// relative to the opened directory, never by path, and are safe for
// concurrent use. Close it when done.
type Dir struct {
	root *os.Root // the directory itself, for Lstat, OpenFile and readdir
	file *os.File // the same directory as a descriptor, for *at calls and sync
	rel  string   // the slash-separated path OpenDir was given, for errors
	dev  int64    // st_dev of the directory, for once-per-volume warnings
}

// OpenDir opens the directory rel inside root. rel is slash-separated, local
// (filepath.IsLocal) and has no empty, "." or ".." component (it is refused,
// not cleaned); it is walked one component at a time: each
// component must Lstat as a directory (a symlink ⇒ ErrSymlink, anything else
// ⇒ ErrNotDir), is then opened, and must be os.SameFile with what Lstat saw.
// With create, missing components are made with mode 0750; without it a
// missing component is an error matching fs.ErrNotExist. root itself is
// trusted and opened as given.
func OpenDir(root, rel string, create bool) (*Dir, error) {
	return openDir(root, rel, create, false)
}

// OpenDirDurable is OpenDir(root, rel, true) for directories whose existence
// must survive a power loss (exports): after creating a component it syncs
// that component's parent directory (root itself for the first component),
// so the new directory entry is on stable storage before anything is
// published inside it. Components that already existed are not synced. The
// sync follows Sync's rule: a "not supported" answer is tolerated and logged
// once per volume, any other error fails the call.
//
// On such a failure every directory this call created is removed again,
// deepest first, and only while it is still the empty directory this call
// made (rmdir via unlinkat(AT_REMOVEDIR), never recursive), so a retry
// creates it and syncs its parent again instead of finding it and skipping
// the sync. If a removal fails (say something was put inside meanwhile), the
// sync error is returned joined with the removal error and that directory
// stays: the residual case in which a later call can find an existing
// directory whose entry was never confirmed durable. Directories that existed
// before the call are never touched. Plain OpenDir, used for caches, never
// syncs.
func OpenDirDurable(root, rel string) (*Dir, error) {
	return openDir(root, rel, true, true)
}

// createdDir is a directory openDir created: name inside parent, with the
// identity it had when created.
type createdDir struct {
	parent *os.Root
	name   string
	fi     fs.FileInfo
}

func openDir(root, rel string, create, durable bool) (dir *Dir, err error) {
	parts, err := splitRel(rel)
	if err != nil {
		return nil, err
	}
	cur, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("safefs: open root: %w", err)
	}
	// Every handle opened on the way stays open until the end, so a failed
	// durable open can remove what it created through the parents' handles.
	opened := []*os.Root{cur}
	defer func() {
		last := len(opened) - 1
		if err != nil {
			last = len(opened)
		}
		for _, r := range opened[:last] {
			_ = r.Close()
		}
	}()
	h := currentHooks()
	var created []createdDir
	for i, name := range parts {
		next, made, err := openComponent(cur, name, create)
		if err != nil {
			return nil, fmt.Errorf("safefs: open %q: %w", path.Join(parts[:i+1]...), err)
		}
		opened = append(opened, next)
		if made {
			if fi, serr := cur.Lstat(name); serr == nil {
				created = append(created, createdDir{parent: cur, name: name, fi: fi})
			}
		}
		if made && durable {
			if serr := syncParent(h, cur); serr != nil {
				serr = fmt.Errorf("sync parent after creating it: %w", serr)
				serr = errors.Join(serr, removeCreated(created))
				return nil, fmt.Errorf("safefs: open %q: %w", path.Join(parts[:i+1]...), serr)
			}
		}
		cur = next
	}
	f, err := cur.Open(".")
	if err != nil {
		return nil, fmt.Errorf("safefs: open %q: %w", rel, err) // the deferred loop closes cur
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("safefs: stat %q: %w", rel, err)
	}
	var dev int64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		dev = int64(st.Dev)
	}
	return &Dir{root: cur, file: f, rel: rel, dev: dev}, nil
}

// splitRel validates rel and returns its components exactly as given. rel is
// never cleaned: collapsing "x/.." would skip the checks on x (a symlink or a
// file), so any empty, "." or ".." component refuses the whole path before
// anything is looked at or created.
func splitRel(rel string) ([]string, error) {
	if rel == "" || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("safefs: %q is not a local relative path", rel)
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, fmt.Errorf("safefs: %q has an empty, \".\" or \"..\" component", rel)
		}
	}
	return parts, nil
}

// openComponent opens the directory name inside parent, refusing symlinks and
// non-directories, and creating it first when asked. created reports that
// this call made the directory.
func openComponent(parent *os.Root, name string, create bool) (next *os.Root, created bool, err error) {
	fi, err := parent.Lstat(name)
	if create && errors.Is(err, fs.ErrNotExist) {
		switch merr := parent.Mkdir(name, dirPerm); {
		case merr == nil:
			created = true
		case !errors.Is(merr, fs.ErrExist):
			return nil, false, merr
		}
		fi, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, false, err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, false, ErrSymlink
	case !fi.IsDir():
		return nil, false, ErrNotDir
	}
	next, err = parent.OpenRoot(name)
	if err != nil {
		return nil, false, err
	}
	opened, err := next.Stat(".")
	if err != nil {
		_ = next.Close()
		return nil, false, err
	}
	if !os.SameFile(fi, opened) {
		_ = next.Close()
		return nil, false, errors.New("safefs: directory changed while it was being opened")
	}
	return next, created, nil
}

// removeCreated removes the directories a failed OpenDirDurable created,
// deepest first: each only while it is still the directory that was created
// (same identity, by Lstat) and empty, with unlinkat(AT_REMOVEDIR), which
// never removes anything but an empty directory. It returns the removal
// failures joined.
func removeCreated(created []createdDir) error {
	var errs []error
	for i := len(created) - 1; i >= 0; i-- {
		c := created[i]
		cur, err := c.parent.Lstat(c.name)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("safefs: remove created %s: %w", c.name, err))
			}
			continue
		}
		if !os.SameFile(cur, c.fi) {
			errs = append(errs, fmt.Errorf("safefs: remove created %s: it was replaced", c.name))
			continue
		}
		pf, err := c.parent.Open(".")
		if err != nil {
			errs = append(errs, fmt.Errorf("safefs: remove created %s: %w", c.name, err))
			continue
		}
		err = withFd(pf, func(dirfd int) error {
			return ignoringEINTR(func() error { return unix.Unlinkat(dirfd, c.name, unix.AT_REMOVEDIR) })
		})
		_ = pf.Close()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("safefs: remove created %s: %w", c.name, err))
		}
	}
	return errors.Join(errs...)
}

// syncParent flushes parent, in which a directory was just created.
func syncParent(h Hooks, parent *os.Root) error {
	f, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var dev int64
	if fi, err := f.Stat(); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			dev = int64(st.Dev)
		}
	}
	h.trace("mkdir-sync")
	sync := h.SyncMkdir
	if sync == nil {
		sync = fullSync
	}
	return tolerate(sync(f), dev)
}

// CreateTemp creates a new file in d, open for writing, with
// O_WRONLY|O_CREATE|O_EXCL and mode 0600, named base + "." + 8 random hex
// digits + ".tmp". Because the create is exclusive it never follows or reuses
// an existing entry, a planted symlink included. The caller writes to f and
// then hands it to Replace or Publish.
func (d *Dir) CreateTemp(base string) (f *os.File, tmpName string, err error) {
	if err := checkName(base); err != nil {
		return nil, "", err
	}
	for range tempAttempts {
		name := base + "." + randomHex() + tempSuffix
		f, err := d.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", fmt.Errorf("safefs: create temporary file in %s: %w", d.rel, err)
		}
	}
	return nil, "", fmt.Errorf("safefs: create temporary file for %s in %s: every name tried exists", base, d.rel)
}

// Replace is for caches: it closes f and renames tmpName over final inside d,
// so an older version of the same cache file is replaced atomically. On any
// failure tmpName is removed.
func (d *Dir) Replace(f *os.File, tmpName, final string) error {
	if err := checkName(tmpName); err != nil {
		_ = f.Close()
		return err
	}
	if err := checkName(final); err != nil {
		d.discard(f, tmpName)
		return err
	}
	if err := f.Close(); err != nil {
		d.removeTemp(tmpName)
		return fmt.Errorf("safefs: close %s: %w", tmpName, err)
	}
	if err := d.renamePlain(tmpName, final); err != nil {
		d.removeTemp(tmpName)
		return err
	}
	return nil
}

// Publish is for exports and never overwrites: it closes f and renames
// tmpName to final inside d with renameatx_np(RENAME_EXCL). An existing final
// of any kind, a symlink included, gives ErrExists and tmpName is removed.
//
// With durable, f is synced before the rename and d after it; a volume that
// answers "not supported" to a sync is tolerated (see Sync). Any other sync
// error fails Publish: tmpName is removed and, when the directory sync is what
// failed, the just-published final is removed too, so a failed Publish leaves
// nothing behind.
//
// On a volume that cannot rename exclusively (renameatx_np answers ENOTSUP,
// as exFAT and SMB do), Publish falls back for that call to: Lstat(final)
// must report "does not exist" (anything else ⇒ ErrExists), then a plain
// rename inside d. The window between that check and the rename is inside the
// threat model. The fallback is logged once per volume.
func (d *Dir) Publish(f *os.File, tmpName, final string, durable bool) error {
	if err := checkName(tmpName); err != nil {
		_ = f.Close()
		return err
	}
	if err := checkName(final); err != nil {
		d.discard(f, tmpName)
		return err
	}
	h := currentHooks()
	if durable {
		if err := d.syncFile(h, f); err != nil {
			d.discard(f, tmpName)
			return fmt.Errorf("safefs: sync %s in %s: %w", tmpName, d.rel, err)
		}
	}
	published, err := f.Stat()
	if err != nil {
		d.discard(f, tmpName)
		return fmt.Errorf("safefs: stat %s in %s: %w", tmpName, d.rel, err)
	}
	if err := f.Close(); err != nil {
		d.removeTemp(tmpName)
		return fmt.Errorf("safefs: close %s in %s: %w", tmpName, d.rel, err)
	}
	if h.BeforeRename != nil {
		h.BeforeRename()
	}
	if err := d.renameExclusive(h, tmpName, final); err != nil {
		d.removeTemp(tmpName)
		return err
	}
	if durable {
		if err := d.syncDir(h); err != nil {
			d.unpublish(final, published)
			return fmt.Errorf("safefs: sync directory %s: %w", d.rel, err)
		}
	}
	return nil
}

// WriteCache writes data to final in d: CreateTemp, write, Replace.
func (d *Dir) WriteCache(final string, data []byte) error {
	if err := checkName(final); err != nil {
		return err
	}
	f, tmp, err := d.CreateTemp(final)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		d.discard(f, tmp)
		return fmt.Errorf("safefs: write %s in %s: %w", tmp, d.rel, err)
	}
	return d.Replace(f, tmp, final)
}

// OpenRead opens the regular file name in d read-only with O_NOFOLLOW. An
// absent name gives an error matching fs.ErrNotExist; a symlink gives
// ErrSymlink; a directory, FIFO, socket or device gives ErrNotRegular naming
// the entry. It never blocks on such an entry: the type is checked by Lstat
// before opening, the open itself is O_NONBLOCK (a FIFO swapped in meanwhile
// does not wait for a writer), and the opened descriptor must fstat as a
// regular file; the returned file is in blocking mode. (os.Root's own
// OpenFile resolves symlinks inside the root even under O_NOFOLLOW, so this
// calls openat directly.)
func (d *Dir) OpenRead(name string) (*os.File, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	fi, err := d.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := regular(fi, name, d.rel); err != nil {
		return nil, err
	}
	var fd int
	err = withFd(d.file, func(dirfd int) error {
		return ignoringEINTR(func() error {
			var err error
			fd, err = unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
			return err
		})
	})
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: %s in %s", ErrSymlink, name, d.rel)
		}
		return nil, &os.PathError{Op: "open", Path: filepath.Join(d.root.Name(), name), Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "fstat", Path: filepath.Join(d.root.Name(), name), Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%w: %s in %s", ErrNotRegular, name, d.rel)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "fcntl", Path: filepath.Join(d.root.Name(), name), Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(d.root.Name(), name)), nil
}

// SyncFile flushes f, a file of d (typically from OpenRead), to stable
// storage like Publish's durable file sync: F_FULLFSYNC with the fsync
// fallback, a "not supported" answer tolerated and logged once per volume,
// any other error returned. It lets a caller that reuses an existing file
// make it as durable as one it published.
func (d *Dir) SyncFile(f *os.File) error {
	if err := d.syncFile(currentHooks(), f); err != nil {
		return fmt.Errorf("safefs: sync %s in %s: %w", filepath.Base(f.Name()), d.rel, err)
	}
	return nil
}

// VerifyRegular checks that each of names that exists in d is a regular file
// (by Lstat): a symlink gives ErrSymlink, anything else an error naming it.
// Absent names pass.
func (d *Dir) VerifyRegular(names ...string) error {
	for _, name := range names {
		if err := checkName(name); err != nil {
			return err
		}
		fi, err := d.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("safefs: stat %s in %s: %w", name, d.rel, err)
		}
		if err := regular(fi, name, d.rel); err != nil {
			return err
		}
	}
	return nil
}

// Remove unlinks name from d if it is a regular file (by Lstat). A missing
// name is not an error; a symlink gives ErrSymlink and a directory or other
// entry an error, and neither is touched.
func (d *Dir) Remove(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	fi, err := d.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("safefs: stat %s in %s: %w", name, d.rel, err)
	}
	if err := regular(fi, name, d.rel); err != nil {
		return err
	}
	if err := d.unlink(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("safefs: remove %s in %s: %w", name, d.rel, err)
	}
	return nil
}

// RemoveTemps unlinks every regular file in d whose name ends in ".tmp" and
// returns how many it removed. Symlinks and directories are left alone. It
// is for Choix's own cache directories; in a directory the user also uses
// (the picks directory), RemoveOwnTemps removes only what is provably
// Choix's.
func (d *Dir) RemoveTemps() (int, error) {
	return d.removeTemps(func(name string) bool { return strings.HasSuffix(name, tempSuffix) }, nil)
}

// RemoveOwnTemps unlinks every regular file in d whose name has the form
// CreateTemp gives its temporary files (TempBase) and whose base owned
// accepts — what an interrupted write of that base left behind — and returns
// how many it removed. The form alone proves nothing (a user may name a file
// "notes.0123abcd.tmp"): owned decides, and is asked only about regular
// files. Anything else — a file of that form whose base owned refuses, a
// plain *.tmp, symlinks, directories — is left alone.
func (d *Dir) RemoveOwnTemps(owned func(base string) bool) (int, error) {
	match := func(name string) bool {
		_, ok := TempBase(name)
		return ok
	}
	return d.removeTemps(match, func(name string) bool {
		base, _ := TempBase(name)
		return owned(base)
	})
}

// TempBase returns the base CreateTemp was given when name has the form of
// its temporary files — a non-empty base, ".", tempHexDigits lower-case hex
// digits, ".tmp" — and false otherwise.
func TempBase(name string) (base string, ok bool) {
	stem, ok := strings.CutSuffix(name, tempSuffix)
	if !ok || len(stem) < tempHexDigits+2 {
		return "", false
	}
	base, digits := stem[:len(stem)-tempHexDigits-1], stem[len(stem)-tempHexDigits:]
	if stem[len(base)] != '.' {
		return "", false
	}
	for _, c := range digits {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return base, true
}

// removeTemps unlinks every regular file in d whose name match accepts and,
// when owned is not nil, owned accepts too (asked after the Lstat that found
// a regular file).
func (d *Dir) removeTemps(match, owned func(name string) bool) (int, error) {
	listing, err := d.root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("safefs: list %s: %w", d.rel, err)
	}
	names, err := listing.Readdirnames(-1)
	_ = listing.Close()
	if err != nil {
		return 0, fmt.Errorf("safefs: list %s: %w", d.rel, err)
	}
	removed := 0
	var errs []error
	for _, name := range names {
		if !match(name) {
			continue
		}
		fi, err := d.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("safefs: stat %s in %s: %w", name, d.rel, err))
			continue
		}
		if !fi.Mode().IsRegular() || (owned != nil && !owned(name)) {
			continue
		}
		if err := d.unlink(name); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("safefs: remove %s in %s: %w", name, d.rel, err))
			}
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// Sync flushes d to stable storage with F_FULLFSYNC, falling back to fsync
// when the volume answers "not supported". A volume whose sync fails with a
// "not supported" class error (ENOTSUP, EINVAL, ENOTTY) is treated as synced
// and logged once per volume; any other error is returned.
func (d *Dir) Sync() error {
	return d.syncDir(currentHooks())
}

// Close releases d's descriptors.
func (d *Dir) Close() error {
	return errors.Join(d.file.Close(), d.root.Close())
}

// --- internals ---------------------------------------------------------------

func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return fmt.Errorf("safefs: %q is not a single path element", name)
	}
	return nil
}

func regular(fi fs.FileInfo, name, rel string) error {
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%w: %s in %s", ErrSymlink, name, rel)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%w: %s in %s", ErrNotRegular, name, rel)
	}
	return nil
}

func randomHex() string {
	b := make([]byte, tempHexDigits/2)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go ≥ 1.24)
	return hex.EncodeToString(b)
}

// discard closes f and removes the temporary file tmpName.
func (d *Dir) discard(f *os.File, tmpName string) {
	_ = f.Close()
	d.removeTemp(tmpName)
}

// removeTemp best-effort unlinks tmpName from d.
func (d *Dir) removeTemp(tmpName string) {
	if err := d.unlink(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("safefs: remove temporary file", "dir", d.rel, "name", tmpName, "err", err)
	}
}

// unpublish removes final after a failed Publish, but only while it is still
// the file Publish renamed there.
func (d *Dir) unpublish(final string, published fs.FileInfo) {
	cur, err := d.root.Lstat(final)
	if err != nil || !os.SameFile(cur, published) {
		return
	}
	if err := d.unlink(final); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("safefs: remove file after failed publish", "dir", d.rel, "name", final, "err", err)
	}
}

// unlink removes a non-directory entry of d without following symlinks.
func (d *Dir) unlink(name string) error {
	return withFd(d.file, func(dirfd int) error {
		return ignoringEINTR(func() error { return unix.Unlinkat(dirfd, name, 0) })
	})
}

// renamePlain renames from over to inside d.
func (d *Dir) renamePlain(from, to string) error {
	err := withFd(d.file, func(dirfd int) error { return unix.Renameat(dirfd, from, dirfd, to) })
	if err != nil {
		return fmt.Errorf("safefs: rename %s to %s in %s: %w", from, to, d.rel, err)
	}
	return nil
}

// renameExclusive renames from to to inside d without ever overwriting to.
func (d *Dir) renameExclusive(h Hooks, from, to string) error {
	h.trace("rename")
	rename := h.RenameExcl
	if rename == nil {
		rename = renameExcl
	}
	err := withFd(d.file, func(dirfd int) error { return rename(dirfd, from, to) })
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		// Also a real "target exists" on volumes without RENAME_EXCL.
		return fmt.Errorf("%w: %s in %s", ErrExists, to, d.rel)
	case !errors.Is(err, unix.ENOTSUP):
		return fmt.Errorf("safefs: rename %s to %s in %s: %w", from, to, d.rel, err)
	}

	warnOnce("volume cannot rename exclusively", d.dev)
	if _, err := d.root.Lstat(to); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("%w: %s in %s (%w)", ErrExists, to, d.rel, err)
		}
		return fmt.Errorf("%w: %s in %s", ErrExists, to, d.rel)
	}
	return d.renamePlain(from, to)
}

func renameExcl(dirfd int, from, to string) error {
	return unix.RenameatxNp(dirfd, from, dirfd, to, unix.RENAME_EXCL)
}

func (d *Dir) syncFile(h Hooks, f *os.File) error {
	h.trace("file-sync")
	sync := h.SyncFile
	if sync == nil {
		sync = fullSync
	}
	return d.tolerate(sync(f))
}

func (d *Dir) syncDir(h Hooks) error {
	h.trace("dir-sync")
	sync := h.SyncDir
	if sync == nil {
		sync = fullSync
	}
	return d.tolerate(sync(d.file))
}

// tolerate turns a "not supported" class sync error into success, logged once
// per volume (R20).
func (d *Dir) tolerate(err error) error {
	return tolerate(err, d.dev)
}

// tolerate is Dir.tolerate for the volume dev.
func tolerate(err error, dev int64) error {
	if err == nil {
		return nil
	}
	if notSupported(err) {
		warnOnce("volume cannot sync", dev)
		return nil
	}
	return err
}

// notSupported reports the errors with which a volume says it cannot sync.
// Spike S1(f) found no further errno on exFAT or SMB.
func notSupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTTY)
}

// fullSync flushes f with F_FULLFSYNC and, when that is not supported (SMB),
// with fsync. A "not supported" answer from fsync too is returned for the
// caller to tolerate.
func fullSync(f *os.File) error {
	err := withFd(f, func(fd int) error {
		err := ignoringEINTR(func() error {
			_, err := unix.FcntlInt(uintptr(fd), unix.F_FULLFSYNC, 0)
			return err
		})
		if err != nil && notSupported(err) {
			err = ignoringEINTR(func() error { return unix.Fsync(fd) })
		}
		return err
	})
	if err != nil {
		return &os.PathError{Op: "sync", Path: f.Name(), Err: err}
	}
	return nil
}

// withFd runs fn with f's descriptor, keeping f open for the duration.
func withFd(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var opErr error
	if err := rc.Control(func(fd uintptr) {
		opErr = fn(int(fd))
	}); err != nil {
		return err
	}
	return opErr
}

func ignoringEINTR(fn func() error) error {
	for {
		if err := fn(); !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

// --- once-per-volume warnings ------------------------------------------------

type warnKey struct {
	msg string
	dev int64
}

var warned sync.Map // warnKey → struct{}

func warnOnce(msg string, dev int64) {
	if _, seen := warned.LoadOrStore(warnKey{msg, dev}, struct{}{}); !seen {
		slog.Warn(msg, "device", dev)
	}
}

// --- test seam ---------------------------------------------------------------

// Hooks replaces safefs's system calls and observation points in tests. Zero
// fields keep the real behaviour. Install them with SetHooksForTest.
type Hooks struct {
	// Trace is called with "file-sync", "rename" and "dir-sync" just before
	// Publish performs that step, with "dir-sync" by Dir.Sync, "file-sync"
	// by Dir.SyncFile, and "mkdir-sync" by OpenDirDurable before it syncs the
	// parent of a directory it created. Publish without durable emits only
	// "rename".
	Trace func(event string)
	// SyncFile replaces the durable file sync Publish performs before the
	// rename (F_FULLFSYNC, falling back to fsync). Its error is classified
	// like the real one: "not supported" is tolerated, anything else fails.
	SyncFile func(f *os.File) error
	// SyncDir replaces the directory sync performed by Publish after the
	// rename and by Dir.Sync, with the same classification as SyncFile.
	SyncDir func(d *os.File) error
	// RenameExcl replaces renameatx_np(dirfd, from, dirfd, to, RENAME_EXCL).
	// Returning ENOTSUP exercises the fallback for volumes that cannot
	// rename exclusively; EEXIST means the target exists.
	RenameExcl func(dirfd int, from, to string) error
	// BeforeRename is called by Publish after the temporary file is closed
	// and before the rename, i.e. after any existence check the caller made.
	BeforeRename func()
	// SyncMkdir replaces the sync OpenDirDurable performs on the parent of
	// each directory it creates, with the same classification as SyncFile.
	SyncMkdir func(parent *os.File) error
}

var hooks atomic.Pointer[Hooks]

func currentHooks() Hooks {
	if h := hooks.Load(); h != nil {
		return *h
	}
	return Hooks{}
}

func (h Hooks) trace(event string) {
	if h.Trace != nil {
		h.Trace(event)
	}
}

// SetHooksForTest installs h for every Dir in the process and forgets which
// volumes have already been warned about, so a test can count warnings. The
// returned restore reinstates the previous hooks (and forgets the warnings
// again). For tests only: hooks are process-wide, so tests that publish must
// not run in parallel with a test that set them.
func SetHooksForTest(h Hooks) (restore func()) {
	prev := hooks.Swap(&h)
	warned.Clear()
	return func() {
		hooks.Store(prev)
		warned.Clear()
	}
}
