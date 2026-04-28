package scanner

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cespare/xxhash/v2"
)

// hashWindow is the size (in bytes) of each end of the file mixed into the hash.
const hashWindow = 64 * 1024

// ContentHash returns a hex-encoded xxhash64 over the first and last hashWindow
// bytes of the file at path. For files smaller than 2*hashWindow, it hashes the
// entire content (no overlap to worry about). The resulting string is suitable
// for storing in files.content_hash.
func ContentHash(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from filepath.WalkDir over a user-supplied trusted root
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	size := st.Size()

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
