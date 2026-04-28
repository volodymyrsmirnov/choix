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
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Analyzer computes the CLIP embedding for a single file and writes it
// into ai_signals. Other per-file scores (sharpness, exposure, NIMA,
// face landmarks) were removed when the AI "best of cluster" picker was
// retired; CLIP is the only signal still in production use, where it
// drives within-bucket visual grouping.
type Analyzer struct {
	store  *store.Store
	models *ModelStore

	mu       sync.Mutex
	clipSess *OrtSession
}

// NewAnalyzer returns an Analyzer backed by the given store and model store.
func NewAnalyzer(s *store.Store, models *ModelStore) *Analyzer {
	return &Analyzer{store: s, models: models}
}

// Close releases the cached CLIP session.
func (a *Analyzer) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clipSess != nil {
		err := a.clipSess.Close()
		a.clipSess = nil
		return err
	}
	return nil
}

// Analyze loads the tier-1 thumbnail at thumbPath, runs CLIP if the model
// is installed, writes the ai_signals row, and bumps scan_status to
// "analyzed". When the CLIP model is absent the row is still written
// (with a NULL embedding) so the cluster step has uniform input.
//
// CLIP inference (session acquire + CLIPEmbed) runs under a.mu to prevent
// concurrent calls from racing on the shared OrtSession. The mutex is
// released before the Upsert/UpdateStatus calls, which do not touch the
// session. Pipeline analyze-concurrency is already 1 by convention
// (see CLAUDE.md), but holding the lock here makes the invariant explicit
// and safe if concurrency is ever raised.
func (a *Analyzer) Analyze(ctx context.Context, fileID int64, thumbPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	img, err := loadImage(thumbPath)
	if err != nil {
		if ferr := a.store.Files().UpdateStatus(fileID, "failed", fmt.Sprintf("load thumb: %s", err)); ferr != nil {
			return fmt.Errorf("load thumb: %w (also: record failure: %v)", err, ferr)
		}
		return fmt.Errorf("load thumb: %w", err)
	}

	row := store.AISignals{FileID: fileID}

	if a.models.Has(ModelCLIP) {
		// Hold the mutex across both the session acquire and CLIPEmbed so
		// no two goroutines call into the same OrtSession concurrently.
		a.mu.Lock()
		sess, sessErr := a.lockedClipSession()
		if sessErr == nil {
			if emb, embErr := CLIPEmbed(sess, img); embErr == nil {
				row.ClipEmbedding = float32sToBytes(emb)
			}
		}
		a.mu.Unlock()
	}

	row.ComputedAt = sql.NullInt64{Int64: time.Now().Unix(), Valid: true}

	if err := a.store.AISignals().Upsert(row); err != nil {
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

// lockedClipSession returns (or lazily creates) the shared CLIP session.
// Callers MUST hold a.mu before calling this method.
func (a *Analyzer) lockedClipSession() (*OrtSession, error) {
	if a.clipSess != nil {
		return a.clipSess, nil
	}
	s, err := NewSession(a.models.Path(ModelCLIP))
	if err != nil {
		return nil, err
	}
	a.clipSess = s
	return s, nil
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
