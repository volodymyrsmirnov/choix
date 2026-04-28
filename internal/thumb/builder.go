package thumb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Builder produces and caches thumbnails for files tracked in the store.
type Builder struct {
	ScanRoot string
	ExifTool *meta.ExifTool
	Ffmpeg   *deps.Runner
	Store    *store.Store
}

// Build dispatches on the file's kind, writes the thumbnail caches and the
// corresponding thumbs rows, and bumps scan_status to 'thumbed'. On error
// it sets scan_status='failed' with the error text and returns the original
// error.
func (b *Builder) Build(ctx context.Context, fileID int64) error {
	row, err := b.Store.Files().GetByID(fileID)
	if err != nil {
		return fmt.Errorf("load file %d: %w", fileID, err)
	}

	srcAbs := filepath.Join(b.ScanRoot, row.Path)
	var buildErr error
	switch row.Kind {
	case "photo":
		buildErr = b.buildPhoto(ctx, row.ID, srcAbs)
	case "video":
		buildErr = b.buildVideo(ctx, row.ID, srcAbs)
	default:
		buildErr = fmt.Errorf("unsupported kind %q for file %d", row.Kind, row.ID)
	}

	if buildErr != nil {
		_ = b.Store.Files().SetScanStatus(fileID, "failed", buildErr.Error())
		return buildErr
	}
	if err := b.Store.Files().SetScanStatus(fileID, "thumbed", ""); err != nil {
		return fmt.Errorf("set status thumbed: %w", err)
	}
	return nil
}

func (b *Builder) buildPhoto(ctx context.Context, fileID int64, srcAbs string) error {
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	thumbDst := CachePath(b.ScanRoot, fileID, TierThumb)
	tw, th, err := BuildPhotoTier1(ctx, b.ExifTool, b.Ffmpeg, srcAbs, thumbDst)
	if err != nil {
		return fmt.Errorf("tier1: %w", err)
	}
	if err := b.upsertThumb(fileID, TierThumb, thumbDst, tw, th); err != nil {
		return err
	}

	previewDst := CachePath(b.ScanRoot, fileID, TierPreview)
	pw, ph, err := BuildPhotoTier2(ctx, b.ExifTool, b.Ffmpeg, srcAbs, previewDst)
	if err != nil {
		return fmt.Errorf("tier2: %w", err)
	}
	if err := b.upsertThumb(fileID, TierPreview, previewDst, pw, ph); err != nil {
		return err
	}
	return nil
}

func (b *Builder) buildVideo(ctx context.Context, fileID int64, srcAbs string) error {
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	kfDir := KeyframesDir(b.ScanRoot, fileID)
	if err := os.MkdirAll(kfDir, 0o750); err != nil {
		return fmt.Errorf("mkdir keyframes: %w", err)
	}
	pattern := filepath.Join(kfDir, "%d.jpg")
	frames, err := BuildVideoKeyframes(ctx, b.Ffmpeg, srcAbs, pattern)
	if err != nil {
		return fmt.Errorf("keyframes: %w", err)
	}
	if len(frames) == 0 {
		return errors.New("no keyframes produced")
	}

	// Tier-1 thumb = first keyframe, written atomically via a .tmp file.
	thumbDst := CachePath(b.ScanRoot, fileID, TierThumb)
	if err := ensureDir(thumbDst); err != nil {
		return err
	}
	thumbTmp := thumbDst + ".tmp"
	if err := copyFileBytes(frames[0], thumbTmp); err != nil {
		_ = os.Remove(thumbTmp)
		return fmt.Errorf("copy keyframe to tier1: %w", err)
	}
	if err := os.Rename(thumbTmp, thumbDst); err != nil {
		_ = os.Remove(thumbTmp)
		return fmt.Errorf("rename tier1 thumb: %w", err)
	}
	w, h, err := readJPEGSize(thumbDst)
	if err != nil {
		return fmt.Errorf("size of tier1 video thumb: %w", err)
	}
	return b.upsertThumb(fileID, TierThumb, thumbDst, w, h)
}

func (b *Builder) upsertThumb(fileID int64, tier, abs string, w, h int) error {
	rel, err := filepath.Rel(b.ScanRoot, abs)
	if err != nil {
		return fmt.Errorf("rel path: %w", err)
	}
	return b.Store.Thumbs().Upsert(store.Thumb{
		FileID:  fileID,
		Tier:    tier,
		RelPath: rel,
		Width:   int64(w),
		Height:  int64(h),
	})
}

// BuildByStatus drains up to limit files whose scan_status equals status,
// calling Build on each. Per-file errors are absorbed (each failed file is
// already marked 'failed' by Build) so one bad row does not stop the batch.
// The returned count is the number of files attempted.
func (b *Builder) BuildByStatus(ctx context.Context, status string, limit int) (int, error) {
	ids, err := b.Store.Files().IDsByStatus(status, limit)
	if err != nil {
		return 0, fmt.Errorf("query files by status %q: %w", status, err)
	}
	processed := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		_ = b.Build(ctx, id) // per-file error already persisted as 'failed'
		processed++
	}
	return processed, nil
}

func copyFileBytes(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is an internal cache path, not user input
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // dst is an internal cache path, not user input
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
