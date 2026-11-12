package scanner

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/cespare/xxhash/v2"
)

// hashWindow is the size (in bytes) of each end of the file mixed into the hash.
const hashWindow = 64 * 1024

// errNotRegular reports that an entry is not a regular file (a FIFO, a
// socket, a device, a directory): it is never read.
var errNotRegular = errors.New("not a regular file")

// ContentHash returns a hex-encoded xxhash64 over the first and last hashWindow
// bytes of the file at path. For files smaller than 2*hashWindow, it hashes the
// entire content (no overlap to worry about). The resulting string is suitable
// for storing in files.content_hash.
//
// path is followed if it is a symlink. Anything that is not a regular file is
// refused without blocking (see openRegular).
func ContentHash(path string) (string, error) {
	hash, _, err := hashRegular(path, true)
	return hash, err
}

// hashRegular opens path with openRegular and returns its content hash and
// the status of the file it read (size and mtime come from the descriptor, so
// they describe the bytes that were hashed, and a symlink's target).
func hashRegular(path string, follow bool) (string, fs.FileInfo, error) {
	f, info, err := openRegular(path, follow)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	hash, err := hashFile(f, path, info.Size())
	if err != nil {
		return "", nil, err
	}
	return hash, info, nil
}

// openRegular opens path read-only and checks, on the descriptor, that it is
// a regular file. O_NONBLOCK keeps open(2) from waiting for a writer when the
// entry is (or was swapped for) a FIFO; it has no effect on reading a regular
// file. With follow false, O_NOFOLLOW refuses an entry that was replaced by a
// symlink after the directory was read.
func openRegular(path string, follow bool) (*os.File, fs.FileInfo, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if !follow {
		flags |= syscall.O_NOFOLLOW
	}
	// The *fs.PathError of OpenFile and Stat already names the operation and
	// the path.
	f, err := os.OpenFile(path, flags, 0) //nolint:gosec // path comes from filepath.WalkDir over the user's folder; opened read-only
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("open %s: %w (%s)", path, errNotRegular, info.Mode().Type())
	}
	return f, info, nil
}

// hashFile hashes f, a regular file of the given size (see ContentHash).
func hashFile(f *os.File, path string, size int64) (string, error) {
	h := xxhash.New()

	if size <= int64(2*hashWindow) {
		// Whole file fits in the window budget — hash everything.
		if _, err := io.Copy(h, f); err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}

	// First window.
	head := make([]byte, hashWindow)
	if _, err := io.ReadFull(f, head); err != nil {
		return "", fmt.Errorf("read head of %s: %w", path, err)
	}
	if _, err := h.Write(head); err != nil {
		return "", fmt.Errorf("hash head of %s: %w", path, err)
	}

	// Last window.
	tail := make([]byte, hashWindow)
	if _, err := f.Seek(size-int64(hashWindow), io.SeekStart); err != nil {
		return "", fmt.Errorf("seek tail of %s: %w", path, err)
	}
	if _, err := io.ReadFull(f, tail); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read tail of %s: %w", path, err)
	}
	if _, err := h.Write(tail); err != nil {
		return "", fmt.Errorf("hash tail of %s: %w", path, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
