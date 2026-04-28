package group

import (
	"database/sql"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// makeFile constructs a store.File with the fields EstimateSkew actually
// looks at — every other field is left zero.
func makeFile(id int64, dev string, capturedAt int64) store.File {
	return store.File{
		ID:         id,
		DeviceKey:  sql.NullString{String: dev, Valid: dev != ""},
		CapturedAt: sql.NullInt64{Int64: capturedAt, Valid: true},
	}
}

func TestEstimateSkewSingleDeviceReturnsEmpty(t *testing.T) {
	files := []store.File{
		makeFile(1, "iPhone#A", 100),
		makeFile(2, "iPhone#A", 200),
	}
	embeds := map[int64][]float32{
		1: normalize([]float32{1, 0, 0, 0}),
		2: normalize([]float32{1, 0, 0, 0}),
	}
	out := EstimateSkew(files, embeds)
	if len(out) != 0 {
		t.Errorf("single device, want empty map; got %v", out)
	}
}

func TestEstimateSkewNoEmbeddingsReturnsEmpty(t *testing.T) {
	files := []store.File{
		makeFile(1, "iPhone#A", 100),
		makeFile(2, "X-T5#B", 100),
	}
	out := EstimateSkew(files, map[int64][]float32{})
	if len(out) != 0 {
		t.Errorf("no embeddings, want empty map; got %v", out)
	}
}

func TestEstimateSkewRecoversKnownOffset(t *testing.T) {
	// iPhone (6 shots) is clearly the reference. X-T5 has 5 matching
	// shots at the same scenes but its clock is +90s.
	const offset = int64(90)
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	v3 := normalize([]float32{0, 0, 1, 0})
	v4 := normalize([]float32{0, 0, 0, 1})
	v5 := normalize([]float32{0.5, 0.5, 0.5, 0.5})
	v6 := normalize([]float32{0.5, -0.5, 0.5, -0.5})

	files := []store.File{
		makeFile(10, "iPhone#A", 100),
		makeFile(11, "iPhone#A", 200),
		makeFile(12, "iPhone#A", 300),
		makeFile(13, "iPhone#A", 400),
		makeFile(14, "iPhone#A", 500),
		makeFile(15, "iPhone#A", 600),
		makeFile(20, "X-T5#B", 100+offset),
		makeFile(21, "X-T5#B", 200+offset),
		makeFile(22, "X-T5#B", 300+offset),
		makeFile(23, "X-T5#B", 400+offset),
		makeFile(24, "X-T5#B", 500+offset),
	}
	embeds := map[int64][]float32{
		10: v1, 11: v2, 12: v3, 13: v4, 14: v5, 15: v6,
		20: v1, 21: v2, 22: v3, 23: v4, 24: v5,
	}
	out := EstimateSkew(files, embeds)
	if got := out["iPhone#A"]; got != 0 {
		t.Errorf("skew[iPhone] = %d, want 0 (reference, more shots)", got)
	}
	if got := out["X-T5#B"]; got != offset {
		t.Errorf("skew[X-T5] = %d, want %d", got, offset)
	}
}

func TestEstimateSkewBelowMinAnchorsLeavesZero(t *testing.T) {
	v := normalize([]float32{1, 0, 0, 0})
	files := []store.File{
		// Reference: 3 shots.
		makeFile(10, "iPhone#A", 100),
		makeFile(11, "iPhone#A", 200),
		makeFile(12, "iPhone#A", 300),
		// X-T5: only 2 anchorable shots (below minAnchors=3).
		makeFile(20, "X-T5#B", 100+50),
		makeFile(21, "X-T5#B", 200+50),
	}
	embeds := map[int64][]float32{
		10: v, 11: v, 12: v,
		20: v, 21: v,
	}
	out := EstimateSkew(files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("with %d anchors (< minAnchors), expected offset 0; got %d", 2, out["X-T5#B"])
	}
}

func TestEstimateSkewIgnoresPairsBelowSimilarity(t *testing.T) {
	// 5 ref shots, 5 candidate shots — all with orthogonal embeddings, so
	// no pair clears 0.95. Result: offset stays 0.
	files := []store.File{
		makeFile(10, "iPhone#A", 100),
		makeFile(11, "iPhone#A", 200),
		makeFile(12, "iPhone#A", 300),
		makeFile(13, "iPhone#A", 400),
		makeFile(14, "iPhone#A", 500),
		makeFile(20, "X-T5#B", 150),
		makeFile(21, "X-T5#B", 250),
		makeFile(22, "X-T5#B", 350),
		makeFile(23, "X-T5#B", 450),
		makeFile(24, "X-T5#B", 550),
	}
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	embeds := map[int64][]float32{
		10: v1, 11: v1, 12: v1, 13: v1, 14: v1,
		20: v2, 21: v2, 22: v2, 23: v2, 24: v2,
	}
	out := EstimateSkew(files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("no high-similarity pairs, expected offset 0; got %d", out["X-T5#B"])
	}
}

func TestEstimateSkewIgnoresPairsOutsideMaxSkew(t *testing.T) {
	v := normalize([]float32{1, 0, 0, 0})
	// X-T5 shots are >maxSkewSec away from the iPhone shots.
	files := []store.File{
		makeFile(10, "iPhone#A", 1000),
		makeFile(11, "iPhone#A", 1100),
		makeFile(12, "iPhone#A", 1200),
		makeFile(13, "iPhone#A", 1300),
		// Other device: shifted by 2h (> 1h max).
		makeFile(20, "X-T5#B", 1000+7200),
		makeFile(21, "X-T5#B", 1100+7200),
		makeFile(22, "X-T5#B", 1200+7200),
		makeFile(23, "X-T5#B", 1300+7200),
	}
	embeds := map[int64][]float32{
		10: v, 11: v, 12: v, 13: v,
		20: v, 21: v, 22: v, 23: v,
	}
	out := EstimateSkew(files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("pairs outside maxSkewSec, expected offset 0; got %d", out["X-T5#B"])
	}
}

func TestEstimateSkewReferenceIsLargestDevice(t *testing.T) {
	v1 := normalize([]float32{1, 0, 0, 0})
	v2 := normalize([]float32{0, 1, 0, 0})
	v3 := normalize([]float32{0, 0, 1, 0})
	v4 := normalize([]float32{0, 0, 0, 1})
	v5 := normalize([]float32{0.5, 0.5, 0.5, 0.5})
	// X-T5 has 5 shots, iPhone has 3 — reference is X-T5. Distinct scenes
	// per shot, so anchor pairs are 1:1 and median is exact.
	files := []store.File{
		makeFile(20, "X-T5#B", 1000),
		makeFile(21, "X-T5#B", 1100),
		makeFile(22, "X-T5#B", 1200),
		makeFile(23, "X-T5#B", 1300),
		makeFile(24, "X-T5#B", 1400),
		makeFile(10, "iPhone#A", 1000+30),
		makeFile(11, "iPhone#A", 1100+30),
		makeFile(12, "iPhone#A", 1200+30),
	}
	embeds := map[int64][]float32{
		20: v1, 21: v2, 22: v3, 23: v4, 24: v5,
		10: v1, 11: v2, 12: v3,
	}
	out := EstimateSkew(files, embeds)
	if out["X-T5#B"] != 0 {
		t.Errorf("X-T5 should be reference (offset 0); got %d", out["X-T5#B"])
	}
	if got := out["iPhone#A"]; got != 30 {
		t.Errorf("iPhone offset = %d, want 30", got)
	}
}
