package thumb

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"

	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/meta"
)

// BuildPhotoTier1 writes a 256-wide JPEG thumbnail for srcAbs to dstAbs.
// Strategy on macOS:
//  1. sips: handles HEIC/JPEG/PNG/TIFF natively via ImageIO; reliable.
//  2. exiftool embedded PreviewImage piped through ffmpeg (fast for RAFs).
//  3. Full ffmpeg decode of the original (last-resort fallback).
//
// Returns the final pixel dimensions.
func BuildPhotoTier1(ctx context.Context, exiftool *meta.ExifTool, ffmpeg *deps.Runner, srcAbs, dstAbs string) (int, int, error) {
	if err := ensureDir(dstAbs); err != nil {
		return 0, 0, err
	}

	if w, h, err := sipsConvert(ctx, srcAbs, dstAbs, WidthThumb); err == nil {
		return w, h, nil
	}

	preview, err := exiftool.ExtractPreviewImage(ctx, srcAbs)
	if err == nil && len(preview) > 0 {
		if w, h, derr := downscaleJPEG(ctx, ffmpeg, preview, dstAbs, WidthThumb); derr == nil {
			return w, h, nil
		}
	}

	w, h, derr := downscaleFromFile(ctx, ffmpeg, srcAbs, dstAbs, WidthThumb)
	if derr != nil {
		return 0, 0, fmt.Errorf("photo tier1 fallback decode: %w", derr)
	}
	return w, h, nil
}

// downscaleJPEG pipes raw JPEG bytes into ffmpeg, scales to width=targetW,
// keeping aspect ratio, and writes the result to dstAbs atomically.
// Reads the resulting file to recover pixel dimensions.
func downscaleJPEG(ctx context.Context, ffmpeg *deps.Runner, src []byte, dstAbs string, targetW int) (int, int, error) {
	tmp := dstAbs + ".tmp"
	args := []string{
		"-y", "-loglevel", "error",
		"-i", "pipe:0",
		"-vf", fmt.Sprintf("scale=%d:-1", targetW),
		"-frames:v", "1",
		"-f", "image2",
		tmp,
	}
	if err := ffmpeg.RunWithStdin(ctx, bytes.NewReader(src), args...); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, fmt.Errorf("ffmpeg downscale (stdin): %w", err)
	}
	if err := os.Rename(tmp, dstAbs); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, fmt.Errorf("rename tmp: %w", err)
	}
	return readJPEGSize(dstAbs)
}

// downscaleFromFile invokes ffmpeg directly on a source file when no embedded
// preview is available. Writes atomically via a .tmp file.
func downscaleFromFile(ctx context.Context, ffmpeg *deps.Runner, srcAbs, dstAbs string, targetW int) (int, int, error) {
	tmp := dstAbs + ".tmp"
	args := []string{
		"-y", "-loglevel", "error",
		"-i", srcAbs,
		"-vf", fmt.Sprintf("scale=%d:-1", targetW),
		"-frames:v", "1",
		"-f", "image2",
		tmp,
	}
	if _, err := ffmpeg.Run(ctx, args...); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, fmt.Errorf("ffmpeg downscale (file): %w", err)
	}
	if err := os.Rename(tmp, dstAbs); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, fmt.Errorf("rename tmp: %w", err)
	}
	return readJPEGSize(dstAbs)
}

// BuildPhotoTier2 writes a 1600-wide JPEG preview for srcAbs to dstAbs.
// Strategy on macOS mirrors tier-1: sips first (handles HEIC reliably),
// then exiftool's -JpgFromRaw / -PreviewImage piped through ffmpeg,
// then a full ffmpeg decode. Returns the final pixel dimensions.
func BuildPhotoTier2(ctx context.Context, exiftool *meta.ExifTool, ffmpeg *deps.Runner, srcAbs, dstAbs string) (int, int, error) {
	if err := ensureDir(dstAbs); err != nil {
		return 0, 0, err
	}

	if w, h, err := sipsConvert(ctx, srcAbs, dstAbs, WidthPreview); err == nil {
		return w, h, nil
	}

	if jpg, err := exiftool.ExtractJpgFromRaw(ctx, srcAbs); err == nil && len(jpg) > 0 {
		if w, h, derr := downscaleJPEG(ctx, ffmpeg, jpg, dstAbs, WidthPreview); derr == nil {
			return w, h, nil
		}
	}

	if preview, err := exiftool.ExtractPreviewImage(ctx, srcAbs); err == nil && len(preview) > 0 {
		if w, h, derr := downscaleJPEG(ctx, ffmpeg, preview, dstAbs, WidthPreview); derr == nil {
			return w, h, nil
		}
	}

	w, h, derr := downscaleFromFile(ctx, ffmpeg, srcAbs, dstAbs, WidthPreview)
	if derr != nil {
		return 0, 0, fmt.Errorf("photo tier2 fallback decode: %w", derr)
	}
	return w, h, nil
}

// BuildPhotoFullJPEG writes a high-resolution (4096w max) JPEG transcode
// of srcAbs to dstAbs for the pixel-peep view. Used at request time when
// the source format is not browser-renderable (HEIC, RAF). Mirrors the
// tier-2 strategy: sips first, then exiftool-extracted previews, then a
// full ffmpeg decode.
func BuildPhotoFullJPEG(ctx context.Context, exiftool *meta.ExifTool, ffmpeg *deps.Runner, srcAbs, dstAbs string) (int, int, error) {
	if err := ensureDir(dstAbs); err != nil {
		return 0, 0, err
	}

	if w, h, err := sipsConvert(ctx, srcAbs, dstAbs, WidthFullJPEG); err == nil {
		return w, h, nil
	}

	if exiftool != nil {
		if jpg, err := exiftool.ExtractJpgFromRaw(ctx, srcAbs); err == nil && len(jpg) > 0 {
			if w, h, derr := downscaleJPEG(ctx, ffmpeg, jpg, dstAbs, WidthFullJPEG); derr == nil {
				return w, h, nil
			}
		}
		if preview, err := exiftool.ExtractPreviewImage(ctx, srcAbs); err == nil && len(preview) > 0 {
			if w, h, derr := downscaleJPEG(ctx, ffmpeg, preview, dstAbs, WidthFullJPEG); derr == nil {
				return w, h, nil
			}
		}
	}

	w, h, derr := downscaleFromFile(ctx, ffmpeg, srcAbs, dstAbs, WidthFullJPEG)
	if derr != nil {
		return 0, 0, fmt.Errorf("photo full-jpeg fallback decode: %w", derr)
	}
	return w, h, nil
}

// readJPEGSize decodes only the JPEG header to recover dimensions.
func readJPEGSize(path string) (int, int, error) {
	f, err := os.Open(path) //nolint:gosec // path is an internal cache path, not user input
	if err != nil {
		return 0, 0, fmt.Errorf("open thumb: %w", err)
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, fmt.Errorf("decode jpeg config: %w", err)
	}
	return cfg.Width, cfg.Height, nil
}
