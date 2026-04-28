// Package thumb builds and caches three tiers of media thumbnails for choix:
// tier-1 ("thumb", 256w) for the Library grid, tier-2 ("preview", 1600w)
// for Focus mode, and "full" which streams the original on demand.
package thumb

import (
	"fmt"
	"os"
	"path/filepath"
)

// Tier names used in the thumbs table and on-disk filenames.
const (
	TierThumb    = "thumb"
	TierPreview  = "preview"
	TierFullJPEG = "full"
)

// Pixel widths for the cached tiers. TierFullJPEG is used for the
// pixel-peep view: a high-resolution JPEG transcode of formats browsers
// can't render natively (HEIC, RAF). 4096 is a reasonable cap for a
// 5K-class display while staying under typical JPEG decode budgets.
const (
	WidthThumb    = 256
	WidthPreview  = 1600
	WidthFullJPEG = 4096
)

// CachePath returns the absolute on-disk path where the cached JPEG for the
// given file id and tier lives. Files are sharded across 256 hex buckets by
// (file_id % 256) to avoid putting every thumbnail in one directory.
//
// Layout: <root>/.choix/thumbs/<bucket>/<file_id>-<tier>.jpg
func CachePath(root string, fileID int64, tier string) string {
	bucket := fmt.Sprintf("%02x", fileID%256)
	name := fmt.Sprintf("%d-%s.jpg", fileID, tier)
	return filepath.Join(root, ".choix", "thumbs", bucket, name)
}

// ensureDir creates the parent directory of path with mode 0o750, including
// any intermediate directories. It is a no-op if the directory already exists.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}
