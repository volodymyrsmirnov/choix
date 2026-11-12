// Package thumb builds and caches three tiers of media thumbnails for choix:
// tier-1 ("thumb", 256) for the Library grid, tier-2 ("preview", 1600) for
// Focus mode, and "full" (4096), a JPEG made on demand for originals a
// browser cannot show. Images come from the platform layer (ImageIO,
// AVFoundation) as bytes; every cache file is written through safefs.
package thumb

import (
	"fmt"
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
