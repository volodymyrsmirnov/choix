package quality

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// scorerEnv is a FileScorer over a real store, with previews in a
// temporary directory.
type scorerEnv struct {
	store    *store.Store
	scorer   *FileScorer
	previews map[int64]string
	dir      string
}

func newScorerEnv(t *testing.T) *scorerEnv {
	t.Helper()
	e := &scorerEnv{store: store.NewTestStore(t), previews: map[int64]string{}, dir: t.TempDir()}
	e.scorer = NewFileScorer(e.store, func(id int64) (string, error) {
		p, ok := e.previews[id]
		if !ok {
			return "", errors.New("no preview row")
		}
		return p, nil
	})
	return e
}

// addPhoto inserts an analyzed photo whose preview file holds data (nil:
// no preview) and returns its pending item.
func (e *scorerEnv) addPhoto(t *testing.T, name string, data []byte) store.QualityItem {
	t.Helper()
	id, err := e.store.Files().Insert(store.File{Path: name, Size: 1, Mtime: 1, ContentHash: "hash-" + name, Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		p := filepath.Join(e.dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		e.previews[id] = p
	}
	return store.QualityItem{FileID: id, ContentHash: "hash-" + name}
}

// stored returns the file's quality_raw and quality_model.
func (e *scorerEnv) stored(t *testing.T, id int64) (sql.NullFloat64, sql.NullString) {
	t.Helper()
	var raw sql.NullFloat64
	var model sql.NullString
	if err := e.store.DB().QueryRowContext(context.Background(),
		`SELECT quality_raw, quality_model FROM files WHERE id = ?`, id).Scan(&raw, &model); err != nil {
		t.Fatal(err)
	}
	return raw, model
}

func (e *scorerEnv) pending(t *testing.T) int {
	t.Helper()
	n, err := e.scorer.CountPending()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// previewJPEG encodes a w×h photo-like test image.
func previewJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := refImage(w, h, func(x, y int) (uint8, uint8, uint8) {
		v := refA(x, y, 2)
		return uint8(v), clamp255(v + 30), clamp255(v / 2)
	})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestScorer_RatesAPreview(t *testing.T) {
	e := newScorerEnv(t)
	data := previewJPEG(t, 1200, 800)
	item := e.addPhoto(t, "a.jpg", data)

	if err := e.scorer.Score(context.Background(), item); err != nil {
		t.Fatalf("Score: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := Measure(img)
	if !ok {
		t.Fatal("the preview is not measurable")
	}
	want := Raw(f)
	raw, model := e.stored(t, item.FileID)
	if !raw.Valid || math.Abs(raw.Float64-want) > 1e-9 {
		t.Fatalf("quality_raw = %+v; want %v", raw, want)
	}
	if model.String != ScorerID {
		t.Fatalf("quality_model = %q; want %q", model.String, ScorerID)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d after rating; want 0", n)
	}
}

func TestScorer_SmallPreviewIsUnrated(t *testing.T) {
	e := newScorerEnv(t)
	item := e.addPhoto(t, "small.jpg", previewJPEG(t, 500, 300))

	if err := e.scorer.Score(context.Background(), item); err != nil {
		t.Fatalf("Score: %v; an unratable photo is not an error", err)
	}
	raw, model := e.stored(t, item.FileID)
	if raw.Valid || model.String != ScorerID {
		t.Fatalf("stored raw=%+v model=%q; want a NULL score stamped %q", raw, model.String, ScorerID)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d; an unratable photo is not retried", n)
	}
}

func TestScorer_UnreadablePreview(t *testing.T) {
	for _, tc := range []struct {
		name        string
		data        []byte // nil: no preview at all
		reachable   bool
		wantPending bool
	}{
		{"zero bytes, folder reachable", []byte{}, true, false},
		{"not a JPEG, folder reachable", []byte("not a jpeg"), true, false},
		{"no preview, folder reachable", nil, true, false},
		{"zero bytes, folder gone", []byte{}, false, true},
		{"no preview, folder gone", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newScorerEnv(t)
			e.scorer.FailureGuard = func() bool { return tc.reachable }
			item := e.addPhoto(t, "a.jpg", tc.data)

			err := e.scorer.Score(context.Background(), item)
			if got := errors.Is(err, pipeline.ErrFolderUnavailable); got != tc.wantPending {
				t.Fatalf("Score = %v; ErrFolderUnavailable %v, want %v", err, got, tc.wantPending)
			}
			if !tc.wantPending && err != nil {
				t.Fatalf("Score = %v; an unratable photo is not an error", err)
			}
			raw, model := e.stored(t, item.FileID)
			if tc.wantPending {
				if raw.Valid || model.Valid {
					t.Fatalf("stored raw=%+v model=%+v; nothing may be stamped while the folder is gone", raw, model)
				}
			} else if raw.Valid || model.String != ScorerID {
				t.Fatalf("stored raw=%+v model=%q; want a NULL score stamped %q", raw, model.String, ScorerID)
			}
			if n := e.pending(t); (n == 1) != tc.wantPending {
				t.Fatalf("pending = %d; want pending %v", n, tc.wantPending)
			}
		})
	}
}

func TestScorer_PendingByID(t *testing.T) {
	e := newScorerEnv(t)
	item := e.addPhoto(t, "a.jpg", previewJPEG(t, 800, 600))
	if _, err := e.store.Files().SetQuality(item.FileID, item.ContentHash, sql.NullFloat64{Float64: 1, Valid: true}, "other-scorer/v0"); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d; want the row stamped by another scorer pending", n)
	}
	items, err := e.scorer.ListPending(0, 10)
	if err != nil || len(items) != 1 || items[0].FileID != item.FileID {
		t.Fatalf("ListPending = %+v, %v; want the one photo", items, err)
	}
	if err := e.scorer.Score(context.Background(), items[0]); err != nil {
		t.Fatalf("Score: %v", err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d after rating; want 0", n)
	}
}

func TestScorer_Cancelled(t *testing.T) {
	e := newScorerEnv(t)
	item := e.addPhoto(t, "a.jpg", previewJPEG(t, 800, 600))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := e.scorer.Score(ctx, item); !errors.Is(err, context.Canceled) {
		t.Fatalf("Score = %v; want context.Canceled", err)
	}
	raw, model := e.stored(t, item.FileID)
	if raw.Valid || model.Valid {
		t.Fatalf("stored raw=%+v model=%+v; want the row untouched", raw, model)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d; want 1", n)
	}
}
