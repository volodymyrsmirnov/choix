package quality

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"math"
	"os"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// MaxPreviewSide is the largest side, in pixels, a preview's header may
// claim. A preview tier is at most 1600 px; one claiming more than this is a
// damaged cache file, treated as unreadable and never decoded, so it cannot
// exhaust memory.
const MaxPreviewSide = 8192

// decodePreview decodes a preview JPEG; a test replaces it.
var decodePreview = jpeg.Decode

// FileScorer rates the photos of one folder's store with Measure. The
// rating is advisory: nothing else reads it but the UI.
type FileScorer struct {
	store *store.Store
	// previewPath resolves a file id to its preview-tier JPEG.
	previewPath func(fileID int64) (string, error)

	// FailureGuard, when set, reports whether the folder is reachable. It is
	// consulted before a photo whose preview could not be found, read or
	// decoded is stamped as never ratable: when it returns false nothing is
	// stamped (the photo stays pending) and Score returns an error wrapping
	// pipeline.ErrFolderUnavailable. nil: the photo is stamped.
	FailureGuard func() bool
}

var _ Scorer = (*FileScorer)(nil)

// NewFileScorer builds a scorer over s. previewPath must return the
// absolute path of a file's preview-tier JPEG.
func NewFileScorer(s *store.Store, previewPath func(fileID int64) (string, error)) *FileScorer {
	return &FileScorer{store: s, previewPath: previewPath}
}

// CountPending returns how many photos still need a rating by ScorerID.
func (q *FileScorer) CountPending() (int, error) {
	return q.store.Files().CountQualityPending(ScorerID)
}

// ListPending returns up to limit pending photos with id > afterID.
func (q *FileScorer) ListPending(afterID int64, limit int) ([]store.QualityItem, error) {
	return q.store.Files().ListQualityPending(ScorerID, afterID, limit)
}

// Score rates one photo and records the result.
//
// It returns nil when the photo has been dealt with: rated; found unratable
// (no readable preview, a preview whose header claims more than
// MaxPreviewSide pixels on a side, or one whose short side is under
// MinShortSide), which stamps a NULL score with ScorerID so it is not
// retried; or superseded (the file changed on disk meanwhile, so the result
// was dropped). It returns an error, leaving the photo pending, when ctx is
// cancelled (checked right before any write), when the database write fails,
// or when the preview could not be read while FailureGuard reports the
// folder unreachable (wrapping pipeline.ErrFolderUnavailable).
func (q *FileScorer) Score(ctx context.Context, item store.QualityItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	img, err := q.loadPreview(item.FileID)
	if err != nil {
		if q.FailureGuard != nil && !q.FailureGuard() {
			return fmt.Errorf("%w: quality: file %d: %w", pipeline.ErrFolderUnavailable, item.FileID, err)
		}
		return q.unratable(ctx, item, err.Error())
	}
	b := img.Bounds()
	if min(b.Dx(), b.Dy()) < MinShortSide {
		return q.unratable(ctx, item, fmt.Sprintf("preview too small to rate (%dx%d)", b.Dx(), b.Dy()))
	}
	f, ok := Measure(img)
	if !ok {
		return q.unratable(ctx, item, fmt.Sprintf("preview not measurable (%dx%d)", b.Dx(), b.Dy()))
	}
	raw := Raw(f)
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return fmt.Errorf("quality: file %d: non-finite score", item.FileID)
	}
	// Measuring takes a few tens of milliseconds; a pass cancelled meanwhile
	// writes nothing.
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = q.store.Files().SetQuality(item.FileID, item.ContentHash, sql.NullFloat64{Float64: raw, Valid: true}, ScorerID)
	return err
}

// unratable stamps the photo as never ratable by ScorerID, unless ctx has
// ended: a cancelled pass stamps nothing, whatever it found.
func (q *FileScorer) unratable(ctx context.Context, item store.QualityItem, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	slog.Warn("quality: photo cannot be rated", "file_id", item.FileID, "reason", reason)
	_, err := q.store.Files().SetQuality(item.FileID, item.ContentHash, sql.NullFloat64{}, ScorerID)
	return err
}

// loadPreview decodes the file's preview JPEG. Its header is read first: a
// preview claiming more than MaxPreviewSide pixels on a side is refused
// without being decoded.
func (q *FileScorer) loadPreview(fileID int64) (image.Image, error) {
	path, err := q.previewPath(fileID)
	if err != nil {
		return nil, fmt.Errorf("preview missing: %w", err)
	}
	f, err := os.Open(path) //nolint:gosec // G304: path is the folder's own preview cache
	if err != nil {
		return nil, fmt.Errorf("preview unreadable: %w", err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("preview unreadable: %w", err)
	}
	if cfg.Width > MaxPreviewSide || cfg.Height > MaxPreviewSide {
		return nil, fmt.Errorf("preview unreadable: its header claims %dx%d pixels, over %d on a side", cfg.Width, cfg.Height, MaxPreviewSide)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("preview unreadable: %w", err)
	}
	img, err := decodePreview(f)
	if err != nil {
		return nil, fmt.Errorf("preview unreadable: %w", err)
	}
	return img, nil
}
