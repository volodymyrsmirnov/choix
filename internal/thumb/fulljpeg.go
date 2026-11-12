package thumb

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

// maxStoredHash bounds the length of a content hash used as it is in a
// file name (the scanner's xxhash64 is 16 hex digits).
const maxStoredHash = 64

// FullJPEGRel returns the slash-separated path, relative to the folder root,
// of the full-size JPEG of fileID made from the original whose content hash
// is contentHash:
//
//	.choix/thumbs/<file_id % 256 as %02x>/<file_id>-full-<token>.jpg
//
// The token is the hash itself when it has the form the scanner stores (1
// to 64 lowercase hex digits), and otherwise "s" followed by 16 hex digits
// of its SHA-256 — a single safe name element that no stored hash spells.
// Because the name carries the hash, a full-size JPEG is never served for
// other bytes than it was made from: not after the original changed, and
// not to another file that was given the same (reused) id.
func FullJPEGRel(fileID int64, contentHash string) string {
	return path.Join(bucketRel(fileID), fullJPEGName(fileID, contentHash))
}

// WriteFullJPEG writes jpeg as FullJPEGRel(fileID, contentHash) like
// WriteCache (through safefs), then removes from the same bucket every other
// full-size JPEG of fileID: those of an earlier content hash and the
// old-style <file_id>-full.jpg, which named no hash and is never served.
// Only regular files are removed (a symlink is left, not followed). A
// failed removal is logged, not returned: the new file is in place, and a
// leftover is never served since full-size JPEGs are found by hash only. It
// returns the new file's slash-separated path relative to root.
func WriteFullJPEG(root string, fileID int64, contentHash string, jpeg []byte) (rel string, err error) {
	dir := bucketRel(fileID)
	name := fullJPEGName(fileID, contentHash)
	d, err := safefs.OpenDir(root, dir, true)
	if err != nil {
		return "", fmt.Errorf("open cache directory: %w", err)
	}
	defer func() {
		if cerr := d.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close cache directory: %w", cerr)
		}
	}()
	if err := d.WriteCache(name, jpeg); err != nil {
		return "", fmt.Errorf("write cache %s: %w", name, err)
	}
	if err := removeSupersededFull(d, root, dir, fileID, name); err != nil {
		slog.Warn("thumb: remove superseded full-size JPEG", "file", fileID, "err", err)
	}
	return path.Join(dir, name), nil
}

// removeSupersededFull removes, through d (the bucket dir), fileID's
// full-size JPEGs other than keep.
func removeSupersededFull(d *safefs.Dir, root, dir string, fileID int64, keep string) error {
	// Listing is a read; each removal goes through d, which unlinks only a
	// regular file and never follows a symlink.
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return fmt.Errorf("list %s: %w", dir, err)
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if name == keep || !isFullJPEGOf(fileID, name) {
			continue
		}
		if err := d.Remove(name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fullJPEGName is the base name of fileID's full-size JPEG for contentHash.
func fullJPEGName(fileID int64, contentHash string) string {
	return fmt.Sprintf("%d-%s-%s.jpg", fileID, TierFullJPEG, hashToken(contentHash))
}

// isFullJPEGOf reports whether name is a full-size JPEG of fileID, of any
// hash or of the old style without one.
func isFullJPEGOf(fileID int64, name string) bool {
	prefix := strconv.FormatInt(fileID, 10) + "-" + TierFullJPEG
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	return rest == ".jpg" || strings.HasPrefix(rest, "-") && strings.HasSuffix(rest, ".jpg")
}

// hashToken is contentHash as a file-name element; see FullJPEGRel.
func hashToken(contentHash string) string {
	if isStoredHash(contentHash) {
		return contentHash
	}
	sum := sha256.Sum256([]byte(contentHash))
	return "s" + hex.EncodeToString(sum[:8])
}

// isStoredHash reports whether s has the form of a stored content hash: 1
// to maxStoredHash lowercase hex digits.
func isStoredHash(s string) bool {
	if s == "" || len(s) > maxStoredHash {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
