package group

import (
	"context"
	"sort"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Cross-device clock-skew detection thresholds. Hardcoded — the values are
// load-bearing for "did these two cameras shoot the same scene", and the
// user toggle gates the whole feature anyway.
const (
	// anchorSimilarity is the feature-print similarity at or above which a
	// (refFile, otherFile) pair is accepted as evidence of "same scene from
	// two cameras". Set above the default cluster similarity (0.90): on a
	// labelled folder every pair at or above 0.91 was a true pair, so only
	// near-identical frames anchor the estimate. Untested on real
	// two-camera data.
	anchorSimilarity = 0.93

	// maxSkewSec bounds the search window: only pairs whose raw EXIF
	// timestamps differ by at most this much are considered. Cameras
	// rarely drift more than minutes; ±1h covers timezone misconfig too.
	maxSkewSec = 3600

	// minAnchors is the smallest number of qualifying pairs we'll trust
	// for a median estimate. Below this we leave the device's offset 0
	// (i.e. take its EXIF time at face value).
	minAnchors = 3
)

// skewEntry is one analyzed file's contribution to skew detection.
type skewEntry struct {
	ID         int64
	CapturedAt int64
}

// EstimateSkew computes per-device clock offsets relative to a reference
// device. Subtract offset[device_key] from each file's captured_at to align
// every device onto a single canonical timeline.
//
// Reference device: the one with the most printed files; ties broken
// alphabetically. The reference's offset is always 0. Each non-reference
// device D's offset is the median of (d.captured_at - r.captured_at) over
// pairs where similarity(print_d, print_r) >= anchorSimilarity and the raw time
// difference is within ±maxSkewSec. Devices with fewer than minAnchors
// qualifying pairs get offset 0 (untouched).
//
// Returns an empty map if there are zero or one devices with usable
// printed files — there's nothing to align. The pair search checks ctx once
// per reference file and every pairCheckStride comparisons, and returns
// ctx's error when it is cancelled.
func EstimateSkew(ctx context.Context, files []store.File, embeds map[int64][]float32) (map[string]int64, error) {
	byDevice := map[string][]skewEntry{}
	for _, f := range files {
		if !f.DeviceKey.Valid || f.DeviceKey.String == "" {
			continue
		}
		if !f.CapturedAt.Valid {
			continue
		}
		if _, ok := embeds[f.ID]; !ok {
			continue
		}
		dev := f.DeviceKey.String
		byDevice[dev] = append(byDevice[dev], skewEntry{ID: f.ID, CapturedAt: f.CapturedAt.Int64})
	}
	if len(byDevice) < 2 {
		return map[string]int64{}, nil
	}

	for _, list := range byDevice {
		sort.Slice(list, func(i, j int) bool { return list[i].CapturedAt < list[j].CapturedAt })
	}

	devs := make([]string, 0, len(byDevice))
	for d := range byDevice {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	ref := devs[0]
	for _, d := range devs {
		if len(byDevice[d]) > len(byDevice[ref]) {
			ref = d
		}
	}

	result := map[string]int64{ref: 0}
	for _, d := range devs {
		if d == ref {
			continue
		}
		deltas, err := pairDeltas(ctx, byDevice[ref], byDevice[d], embeds)
		if err != nil {
			return nil, err
		}
		if deltas.n < minAnchors {
			result[d] = 0
			continue
		}
		result[d] = deltas.median()
	}
	return result, nil
}

// pairCheckStride is how many pair comparisons pairDeltas makes between two
// checks of its context: a few milliseconds of dot products.
const pairCheckStride = 1024

// pairDeltas returns the histogram of (other.CapturedAt - ref.CapturedAt)
// over every pair whose similarity is >= anchorSimilarity and whose raw time
// difference is within ±maxSkewSec. Both inputs must be sorted ascending
// by CapturedAt; the implementation slides a forward-only window over
// `other` as it walks `ref`, checking ctx once per ref entry and every
// pairCheckStride comparisons. It allocates nothing per pair.
func pairDeltas(ctx context.Context, ref, other []skewEntry, embeds map[int64][]float32) (*deltaHistogram, error) {
	deltas := &deltaHistogram{}
	lo, compared := 0, 0
	for _, r := range ref {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for lo < len(other) && other[lo].CapturedAt < r.CapturedAt-maxSkewSec {
			lo++
		}
		er, ok := embeds[r.ID]
		if !ok {
			continue
		}
		for j := lo; j < len(other); j++ {
			o := other[j]
			if o.CapturedAt > r.CapturedAt+maxSkewSec {
				break
			}
			eo, ok := embeds[o.ID]
			if !ok {
				continue
			}
			if compared++; compared%pairCheckStride == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if similarity(er, eo) < anchorSimilarity {
				continue
			}
			deltas.add(o.CapturedAt - r.CapturedAt)
		}
	}
	return deltas, nil
}

// deltaHistogram counts whole-second deltas within ±maxSkewSec: bounded
// memory, however many pairs qualify, and the same median a sort gives.
type deltaHistogram struct {
	counts [2*maxSkewSec + 1]int
	n      int
}

// add counts d, which must be within ±maxSkewSec.
func (h *deltaHistogram) add(d int64) {
	h.counts[d+maxSkewSec]++
	h.n++
}

// median returns the median of the counted deltas: the middle one, or the
// mean of the two middle ones truncated toward zero. It needs n > 0.
func (h *deltaHistogram) median() int64 {
	if h.n%2 == 1 {
		return h.nth(h.n / 2)
	}
	return (h.nth(h.n/2-1) + h.nth(h.n/2)) / 2
}

// nth returns the k-th smallest counted delta (from 0).
func (h *deltaHistogram) nth(k int) int64 {
	for i, c := range h.counts {
		if k < c {
			return int64(i) - maxSkewSec
		}
		k -= c
	}
	return maxSkewSec
}
