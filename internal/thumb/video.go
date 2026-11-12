package thumb

import (
	"fmt"
	"path/filepath"
)

// KeyframeFractions are the points of a video's duration whose frames are
// kept as keyframes. 0.95 rather than 1 keeps the last one off the end of
// short clips.
var KeyframeFractions = []float64{0, 0.25, 0.5, 0.75, 0.95}

// KeyframesDir returns the per-file folder where video keyframes are cached
// for the given file id. It mirrors CachePath's bucket sharding.
func KeyframesDir(root string, fileID int64) string {
	bucket := fmt.Sprintf("%02x", fileID%256)
	return filepath.Join(root, ".choix", "thumbs", bucket, fmt.Sprintf("%d-keyframes", fileID))
}
