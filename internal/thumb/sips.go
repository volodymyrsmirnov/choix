package thumb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// sipsAvailable is cached on first lookup. sips is bundled with macOS so on
// our target platform it's always present, but we still check at startup so
// the absence is reported clearly rather than leaking out as a generic exec
// error.
var sipsPath string

func init() {
	if p, err := exec.LookPath("sips"); err == nil {
		sipsPath = p
	}
}

// SipsConvert is the exported wrapper around sipsConvert. macOS-only;
// returns ("sips not available") when sips isn't on PATH (Linux/Windows
// dev builds, primarily — production v1 is macOS-only).
func SipsConvert(ctx context.Context, srcAbs, dstAbs string, maxDim int) (int, int, error) {
	return sipsConvert(ctx, srcAbs, dstAbs, maxDim)
}

// sipsConvert downscales any image format that macOS understands (HEIC, JPEG,
// PNG, TIFF, GIF, BMP, plus most RAW formats via ImageIO) to a JPEG capped
// at maxDim pixels on its longest side. The output is written atomically.
// Returns the resulting pixel dimensions.
//
// We reach for sips because ffmpeg's HEIC pipeline (libheif → HEVC video
// stream) generates a "complex filtergraph" and refuses to combine it with
// a simple `-vf scale=...` filter. sips uses Apple's ImageIO directly and
// produces a clean JPEG in one shot.
func sipsConvert(ctx context.Context, srcAbs, dstAbs string, maxDim int) (int, int, error) {
	if sipsPath == "" {
		return 0, 0, fmt.Errorf("sips not available")
	}
	tmp := dstAbs + ".tmp"
	cmd := exec.CommandContext(ctx, sipsPath, //nolint:gosec // sipsPath resolved at startup, srcAbs/dstAbs are internal cache paths
		"-s", "format", "jpeg",
		"-s", "formatOptions", "85",
		"-Z", fmt.Sprintf("%d", maxDim),
		"--out", tmp,
		srcAbs,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, fmt.Errorf("sips: %w (%s)", err, out)
	}
	if err := os.Rename(tmp, dstAbs); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, fmt.Errorf("rename tmp: %w", err)
	}
	return readJPEGSize(dstAbs)
}
