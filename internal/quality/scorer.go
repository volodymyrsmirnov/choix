package quality

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"math"
	"os"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

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
// (no readable preview, or a preview whose short side is under
// MinShortSide), which stamps a NULL score with ScorerID so it is not
// retried; or superseded (the file changed on disk meanwhile, so the result
// was dropped). It returns an error, leaving the photo pending, when ctx is
// cancelled, when the database write fails, or when the preview could not be
// read while FailureGuard reports the folder unreachable (wrapping
// pipeline.ErrFolderUnavailable).
func (q *FileScorer) Score(ctx context.Context, item store.QualityItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	img, err := q.loadPreview(item.FileID)
	if err != nil {
		if q.FailureGuard != nil && !q.FailureGuard() {
			return fmt.Errorf("%w: quality: file %d: %w", pipeline.ErrFolderUnavailable, item.FileID, err)
		}
		return q.unratable(item, err.Error())
	}
	b := img.Bounds()
	if min(b.Dx(), b.Dy()) < MinShortSide {
		return q.unratable(item, fmt.Sprintf("preview too small to rate (%dx%d)", b.Dx(), b.Dy()))
	}
	f, ok := Measure(img)
	if !ok {
		return q.unratable(item, fmt.Sprintf("preview not measurable (%dx%d)", b.Dx(), b.Dy()))
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

// unratable stamps the photo as never ratable by ScorerID.
func (q *FileScorer) unratable(item store.QualityItem, reason string) error {
	slog.Warn("quality: photo cannot be rated", "file_id", item.FileID, "reason", reason)
	_, err := q.store.Files().SetQuality(item.FileID, item.ContentHash, sql.NullFloat64{}, ScorerID)
	return err
}

// loadPreview decodes the file's preview JPEG.
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
	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("preview unreadable: %w", err)
	}
	return img, nil
}
