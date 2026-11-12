package thumb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

// thumbsRel is the cache directory inside a folder, slash-separated.
const thumbsRel = ".choix/thumbs"

// bucketRel returns the slash-separated directory, relative to the folder
// root, that holds fileID's cache files: .choix/thumbs/<file_id % 256 as %02x>.
func bucketRel(fileID int64) string {
	return path.Join(thumbsRel, fmt.Sprintf("%02x", fileID%256))
}

// WriteCache writes jpeg as fileID's tier cache file,
// .choix/thumbs/<bucket>/<id>-<tier>.jpg, through safefs: every directory on
// the way is a real directory inside root (a symlink is refused), and the
// file is written under a random temporary name and renamed over any older
// version. It is not synced. It returns the file's slash-separated path
// relative to root.
func WriteCache(root string, fileID int64, tier string, jpeg []byte) (rel string, err error) {
	dir := bucketRel(fileID)
	name := fmt.Sprintf("%d-%s.jpg", fileID, tier)
	if err := writeCacheFile(root, dir, name, jpeg); err != nil {
		return "", err
	}
	return path.Join(dir, name), nil
}

// WriteKeyframe writes keyframe n of video fileID to
// .choix/thumbs/<bucket>/<id>-keyframes/<n>.jpg like WriteCache and returns
// its slash-separated path relative to root.
func WriteKeyframe(root string, fileID int64, n int, jpeg []byte) (rel string, err error) {
	dir := path.Join(bucketRel(fileID), fmt.Sprintf("%d-keyframes", fileID))
	name := fmt.Sprintf("%d.jpg", n)
	if err := writeCacheFile(root, dir, name, jpeg); err != nil {
		return "", err
	}
	return path.Join(dir, name), nil
}

// writeCacheFile opens dir inside root through safefs, creating it, and
// replaces name in it with data.
func writeCacheFile(root, dir, name string, data []byte) error {
	d, err := safefs.OpenDir(root, dir, true)
	if err != nil {
		return fmt.Errorf("open cache directory: %w", err)
	}
	werr := d.WriteCache(name, data)
	cerr := d.Close()
	if werr != nil {
		return fmt.Errorf("write cache %s: %w", name, werr)
	}
	if cerr != nil {
		return fmt.Errorf("close cache directory: %w", cerr)
	}
	return nil
}

// sweepDepth is how deep below .choix/thumbs SweepTemps looks:
// <bucket>/<id>-keyframes.
const sweepDepth = 2

// SweepTemps removes the temporary files (*.tmp) an interrupted write left
// under .choix/thumbs, through safefs: only regular files are removed, and
// neither a symlinked directory nor a symlinked *.tmp is followed. A folder
// without a cache is 0, nil. It returns how many files it removed.
func SweepTemps(root string) (int, error) {
	n, err := sweepDir(root, thumbsRel, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return n, nil
	}
	return n, err
}

// sweepDir removes the temporaries in rel and, down to sweepDepth, in its
// real subdirectories.
func sweepDir(root, rel string, depth int) (int, error) {
	d, err := safefs.OpenDir(root, rel, false)
	if err != nil {
		return 0, fmt.Errorf("sweep %s: %w", rel, err)
	}
	removed, err := d.RemoveTemps()
	errs := []error{err}
	if cerr := d.Close(); cerr != nil {
		errs = append(errs, fmt.Errorf("sweep %s: close: %w", rel, cerr))
	}
	if depth < sweepDepth {
		// Listing is a read; each subdirectory is reopened through safefs,
		// which refuses it if it has become a symlink meanwhile.
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			errs = append(errs, fmt.Errorf("sweep %s: list: %w", rel, err))
		}
		for _, e := range entries {
			if !e.IsDir() { // a symlink is not a directory entry here
				continue
			}
			n, err := sweepDir(root, path.Join(rel, e.Name()), depth+1)
			removed += n
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return removed, errors.Join(errs...)
}
