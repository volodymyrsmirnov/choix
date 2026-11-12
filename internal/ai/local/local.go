package local

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/jpeg" // decoder
	_ "image/png"  // decoder
	"math"
	"os"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Analyzer computes the CLIP embedding for a single file and writes it
// into ai_signals. Other per-file scores (sharpness, exposure, NIMA,
// face landmarks) were removed when the AI "best of cluster" picker was
// retired; CLIP is the only signal still in production use, where it
// drives within-bucket visual grouping.
type Analyzer struct {
	store    *store.Store
	embedder Embedder

	// FailureGuard, when set, is consulted right before a failure would be
	// recorded. When it returns false (the folder is unreachable) nothing is
	// written — the row keeps its status — and the error wraps
	// pipeline.ErrFolderUnavailable. nil records every failure.
	FailureGuard func() bool
}

// unavailable returns cause wrapped in pipeline.ErrFolderUnavailable when
// guard reports the folder unreachable, and nil otherwise (including when
// guard is nil).
func unavailable(guard func() bool, cause error) error {
	if guard != nil && !guard() {
		return fmt.Errorf("%w: %w", pipeline.ErrFolderUnavailable, cause)
	}
	return nil
}

// NewAnalyzer returns an Analyzer backed by the given store. e computes the
// CLIP embedding — normally the app's shared *Sessions, which owns (and
// closes) the ONNX session; with e == nil no embedding is computed. Pass a
// nil interface, not a typed nil pointer.
func NewAnalyzer(s *store.Store, e Embedder) *Analyzer {
	return &Analyzer{store: s, embedder: e}
}

// Analyze loads the tier-1 thumbnail at thumbPath, computes its CLIP
// embedding when an Embedder is set, writes the ai_signals row, and bumps
// scan_status to "analyzed". When there is no embedding — no Embedder, the
// model is not installed (ErrModelMissing), or any other Embedder error —
// the row is still written with a NULL embedding and the file is still
// analyzed, so the cluster step has uniform input.
func (a *Analyzer) Analyze(ctx context.Context, fileID int64, thumbPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	img, err := loadImage(thumbPath)
	if err != nil {
		if uerr := unavailable(a.FailureGuard, fmt.Errorf("load thumb: %w", err)); uerr != nil {
			return uerr
		}
		if ferr := a.store.Files().UpdateStatus(fileID, "failed", fmt.Sprintf("load thumb: %s", err)); ferr != nil {
			return fmt.Errorf("load thumb: %w (also: record failure: %v)", err, ferr)
		}
		return fmt.Errorf("load thumb: %w", err)
	}

	row := store.AISignals{FileID: fileID}

	if a.embedder != nil {
		// Any failure leaves the embedding NULL; the grouper then relies on
		// time proximity alone for this file.
		if emb, embErr := a.embedder.Embed(img); embErr == nil {
			row.ClipEmbedding = float32sToBytes(emb)
		}
	}

	row.ComputedAt = sql.NullInt64{Int64: time.Now().Unix(), Valid: true}

	if err := a.store.AISignals().Upsert(row); err != nil {
		if uerr := unavailable(a.FailureGuard, fmt.Errorf("upsert ai_signals: %w", err)); uerr != nil {
			return uerr
		}
		if ferr := a.store.Files().UpdateStatus(fileID, "failed", fmt.Sprintf("upsert ai_signals: %s", err)); ferr != nil {
			return fmt.Errorf("upsert ai_signals: %w (also: record failure: %v)", err, ferr)
		}
		return fmt.Errorf("upsert ai_signals: %w", err)
	}
	if err := a.store.Files().UpdateStatus(fileID, "analyzed", ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal thumbnail cache path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	return img, err
}

// float32sToBytes encodes f32 values little-endian into bytes for storage in
// the ai_signals.clip_embedding BLOB column.
func float32sToBytes(in []float32) []byte {
	out := make([]byte, len(in)*4)
	for i, v := range in {
		b := math.Float32bits(v)
		out[i*4] = byte(b & 0xff)           //nolint:gosec // G115: intentional low-byte extraction
		out[i*4+1] = byte((b >> 8) & 0xff)  //nolint:gosec // G115: intentional byte extraction
		out[i*4+2] = byte((b >> 16) & 0xff) //nolint:gosec // G115: intentional byte extraction
		out[i*4+3] = byte((b >> 24) & 0xff) //nolint:gosec // G115: intentional byte extraction
	}
	return out
}
