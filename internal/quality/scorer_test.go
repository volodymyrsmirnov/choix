package quality

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
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

// withClaimedSize returns the JPEG data with its SOF0 header patched to
// claim w×h pixels; the scan data stays that of the small image.
func withClaimedSize(t *testing.T, data []byte, w, h int) []byte {
	t.Helper()
	out := bytes.Clone(data)
	for i := 2; i+9 < len(out); {
		if out[i] != 0xFF {
			t.Fatalf("no marker at %d", i)
		}
		marker := out[i+1]
		n := int(out[i+2])<<8 | int(out[i+3])
		if marker == 0xC0 {
			// FF C0, length (2), precision (1), height (2), width (2).
			out[i+5], out[i+6] = byte(h>>8), byte(h) //nolint:gosec // h ≤ 65535
			out[i+7], out[i+8] = byte(w>>8), byte(w) //nolint:gosec // w ≤ 65535
			return out
		}
		i += 2 + n
	}
	t.Fatal("no SOF0 marker")
	return nil
}

// A preview whose header claims more than MaxPreviewSide pixels on a side is
// treated as unreadable and never decoded: a damaged cache file cannot
// exhaust memory.
func TestScorer_HugePreviewIsNotDecoded(t *testing.T) {
	small := previewJPEG(t, 64, 48)
	decode := decodePreview
	decodePreview = func(r io.Reader) (image.Image, error) {
		t.Error("a preview claiming a huge size was decoded")
		return decode(r)
	}
	t.Cleanup(func() { decodePreview = decode })

	for _, size := range [][2]int{{MaxPreviewSide + 1, 100}, {100, MaxPreviewSide + 1}, {65535, 65535}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			data := withClaimedSize(t, small, size[0], size[1])
			if cfg, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != size[0] || cfg.Height != size[1] {
				t.Fatalf("patched header reads %dx%d, %v; want %dx%d", cfg.Width, cfg.Height, err, size[0], size[1])
			}
			for _, reachable := range []bool{true, false} {
				e := newScorerEnv(t)
				e.scorer.FailureGuard = func() bool { return reachable }
				item := e.addPhoto(t, "huge.jpg", data)
				err := e.scorer.Score(context.Background(), item)
				raw, model := e.stored(t, item.FileID)
				if reachable {
					if err != nil || raw.Valid || model.String != ScorerID {
						t.Fatalf("reachable: Score = %v, stored raw=%+v model=%q; want a NULL score stamped %q", err, raw, model.String, ScorerID)
					}
				} else if !errors.Is(err, pipeline.ErrFolderUnavailable) || model.Valid {
					t.Fatalf("folder gone: Score = %v, model=%+v; want ErrFolderUnavailable and nothing stamped", err, model)
				}
			}
		})
	}
}

// Nothing is stamped once the pass is cancelled, whichever outcome was about
// to be recorded: the context is cancelled while the preview is looked up.
func TestScorer_CancelledDuringPreviewLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte // nil: the lookup fails
	}{
		{"unreadable", nil},
		{"not a JPEG", []byte("not a jpeg")},
		{"too small", previewJPEG(t, 500, 300)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newScorerEnv(t)
			item := e.addPhoto(t, "a.jpg", tc.data)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lookup := e.scorer.previewPath
			e.scorer.previewPath = func(id int64) (string, error) {
				cancel()
				return lookup(id)
			}

			if err := e.scorer.Score(ctx, item); !errors.Is(err, context.Canceled) {
				t.Fatalf("Score = %v; want context.Canceled", err)
			}
			if raw, model := e.stored(t, item.FileID); raw.Valid || model.Valid {
				t.Fatalf("stored raw=%+v model=%+v; want nothing stamped after the cancellation", raw, model)
			}
			if n := e.pending(t); n != 1 {
				t.Fatalf("pending = %d; want 1", n)
			}
		})
	}
}
