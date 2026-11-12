package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"sort"

	"golang.org/x/image/draw"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// QualityScorerID identifies the model plus the crop recipe behind every
// stored quality score. Bump the version suffix whenever either changes:
// rows stamped with another id become pending again and their old scores
// stop being shown, so scores from different recipes are never compared.
const QualityScorerID = "topiq-nr-koniq-384/crop5-top3/v1"

const (
	// qualitySide is the model's fixed square input.
	qualitySide = 384
	// qualityMaxLong caps the long side of the image the crops are taken
	// from. Previews are not uniform — sips caps the long side at 1600,
	// the ffmpeg path fixes the width at 1600 — so this makes a portrait
	// RAW and its portrait JPEG get sampled at the same scale.
	qualityMaxLong = 1600
	// qualityTopN crops (the sharpest ones) are averaged into the score.
	qualityTopN = 3
)

// Display scale anchors: raw scores at or below qualityRawFloor show as 0,
// at or above qualityRawCeil as 10. Chosen from measurements on real
// photos (heavy blur ≈ 0.15, the best frames ≈ 0.80) so good frames do not
// saturate. Recalibrating means changing these two numbers; stored raw
// scores stay valid.
const (
	qualityRawFloor = 0.10
	qualityRawCeil  = 0.85
)

// QualityDisplay maps a raw model score to the 0–10 rating shown in the UI,
// rounded to one decimal (0 = worst). This is the only place the mapping
// lives.
func QualityDisplay(raw float64) float64 {
	v := (raw - qualityRawFloor) / (qualityRawCeil - qualityRawFloor) * 10
	v = math.Max(0, math.Min(10, v))
	return math.Round(v*10) / 10
}

// qualityCropCenters are the fractional centres of the five crops: the
// middle of the frame and the middle of each quadrant.
var qualityCropCenters = [5][2]float64{{0.5, 0.5}, {0.25, 0.25}, {0.75, 0.25}, {0.25, 0.75}, {0.75, 0.75}}

// qualityCropRects returns the five qualitySide squares for a w×h image,
// clamped to its bounds. w and h must both be at least qualitySide.
func qualityCropRects(w, h int) []image.Rectangle {
	out := make([]image.Rectangle, 0, len(qualityCropCenters))
	for _, c := range qualityCropCenters {
		x := clampInt(int(math.Round(c[0]*float64(w)))-qualitySide/2, 0, w-qualitySide)
		y := clampInt(int(math.Round(c[1]*float64(h)))-qualitySide/2, 0, h-qualitySide)
		out = append(out, image.Rect(x, y, x+qualitySide, y+qualitySide))
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// qualityTargetSize returns the size the preview (pw×ph) is reduced to
// before cropping. The long side ends at most qualityMaxLong and at most the
// original's long side (origW/origH, 0 when unknown) — the latter undoes a
// preview that was scaled up from a small original, which would otherwise
// read as blurry. It never returns a size larger than the preview.
func qualityTargetSize(pw, ph int, origW, origH int64) (int, int) {
	long := max(pw, ph)
	limit := min(long, qualityMaxLong)
	if origLong := max(origW, origH); origLong > 0 && origLong < int64(limit) {
		limit = int(origLong)
	}
	if limit >= long {
		return pw, ph
	}
	scale := float64(limit) / float64(long)
	return max(1, int(math.Round(float64(pw)*scale))), max(1, int(math.Round(float64(ph)*scale)))
}

// topMean averages the n largest values.
func topMean(scores []float64, n int) float64 {
	s := append([]float64(nil), scores...)
	sort.Sort(sort.Reverse(sort.Float64Slice(s)))
	if n > len(s) {
		n = len(s)
	}
	var sum float64
	for _, v := range s[:n] {
		sum += v
	}
	return sum / float64(n)
}

// cropTensor converts one crop of img into the model's input layout:
// float32 NCHW, RGB scaled to 0..1 (the graph normalises internally).
func cropTensor(img image.Image, r image.Rectangle) []float32 {
	rgba := image.NewRGBA(image.Rect(0, 0, qualitySide, qualitySide))
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min.Add(r.Min), draw.Src)
	plane := qualitySide * qualitySide
	out := make([]float32, 3*plane)
	for i := 0; i < plane; i++ {
		out[i] = float32(rgba.Pix[i*4]) / 255
		out[i+plane] = float32(rgba.Pix[i*4+1]) / 255
		out[i+2*plane] = float32(rgba.Pix[i*4+2]) / 255
	}
	return out
}

// ErrQualityUnavailable means the scorer cannot run at all in this process
// (the model or the ONNX runtime failed to load, or the sessions are
// closed). ONNX runtime initialisation is attempted once per process, so the
// condition lasts until choix is restarted.
var ErrQualityUnavailable = errors.New("quality scorer unavailable")

// QualityScorer rates photos for technical quality (focus, motion blur,
// noise) with the TOPIQ-NR model and stores the raw score on the file row.
// The rating is advisory: nothing else reads it but the UI.
type QualityScorer struct {
	store *store.Store
	// inf runs one 384×384 crop through the model — normally the app's
	// shared *Sessions, which owns (and closes) the ONNX session.
	inf CropInferer
	// previewPath resolves a file id to its tier-2 preview JPEG.
	previewPath func(fileID int64) (string, error)

	// FailureGuard, when set, is consulted before a photo whose preview
	// could not be found, read or decoded is stamped as never ratable. When
	// it returns false (the folder is unreachable) nothing is stamped — the
	// photo stays pending — and Score returns an error wrapping
	// pipeline.ErrFolderUnavailable. nil stamps it as before.
	FailureGuard func() bool
}

// NewQualityScorer builds a scorer that runs crops through inf.
// previewPath must return the absolute path of the tier-2 preview for a
// file id.
func NewQualityScorer(s *store.Store, inf CropInferer, previewPath func(fileID int64) (string, error)) *QualityScorer {
	return &QualityScorer{store: s, inf: inf, previewPath: previewPath}
}

// CountPending returns how many photos still need a rating.
func (q *QualityScorer) CountPending() (int, error) {
	return q.store.Files().CountQualityPending(QualityScorerID)
}

// ListPending returns up to limit pending photos with id > afterID.
func (q *QualityScorer) ListPending(afterID int64, limit int) ([]store.QualityItem, error) {
	return q.store.Files().ListQualityPending(QualityScorerID, afterID, limit)
}

// Score rates one photo and records the result.
//
// It returns nil when the photo has been dealt with: rated; found unratable
// (no usable preview, or too small — stamped so it is not retried); or
// superseded (the file changed on disk meanwhile, so the result was
// dropped). It returns an error — leaving the photo pending — when ctx is
// cancelled, when the scorer cannot run (ErrQualityUnavailable), when
// inference or the database write failed, or when the preview could not be
// read while FailureGuard reports the folder unreachable
// (pipeline.ErrFolderUnavailable).
func (q *QualityScorer) Score(ctx context.Context, item store.QualityItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	img, reason, readFailed := q.loadScoringImage(item)
	if img == nil {
		if readFailed {
			if err := unavailable(q.FailureGuard, fmt.Errorf("quality: file %d: %s", item.FileID, reason)); err != nil {
				return err
			}
		}
		slog.Warn("quality: photo cannot be rated", "file_id", item.FileID, "reason", reason)
		_, err := q.store.Files().SetQuality(item.FileID, item.ContentHash, sql.NullFloat64{}, QualityScorerID)
		return err
	}

	b := img.Bounds()
	rects := qualityCropRects(b.Dx(), b.Dy())
	scores := make([]float64, 0, len(rects))
	for _, r := range rects {
		// Between crops is the finest grain at which cancellation can be
		// honoured: a single inference cannot be interrupted.
		if err := ctx.Err(); err != nil {
			return err
		}
		v, err := q.inf.QualityCrop(cropTensor(img, r))
		if err != nil {
			return err
		}
		scores = append(scores, v)
	}
	raw := topMean(scores, qualityTopN)
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return fmt.Errorf("quality inference for file %d: non-finite score", item.FileID)
	}
	_, err := q.store.Files().SetQuality(item.FileID, item.ContentHash, sql.NullFloat64{Float64: raw, Valid: true}, QualityScorerID)
	return err
}

// loadScoringImage returns the image the crops are taken from, or nil and
// the reason the photo cannot be rated; readFailed is set when that reason
// is that the preview could not be found, read or decoded.
func (q *QualityScorer) loadScoringImage(item store.QualityItem) (img image.Image, reason string, readFailed bool) {
	path, err := q.previewPath(item.FileID)
	if err != nil {
		return nil, "preview missing: " + err.Error(), true
	}
	img, err = loadImage(path)
	if err != nil {
		return nil, "preview unreadable: " + err.Error(), true
	}
	b := img.Bounds()
	tw, th := qualityTargetSize(b.Dx(), b.Dy(), item.Width.Int64, item.Height.Int64)
	if min(tw, th) < qualitySide {
		// Scaling up would only invent blur; leave tiny images and
		// extreme panoramas unrated.
		return nil, fmt.Sprintf("too small to rate (%dx%d)", tw, th), false
	}
	if tw != b.Dx() || th != b.Dy() {
		dst := image.NewRGBA(image.Rect(0, 0, tw, th))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	return img, "", false
}
