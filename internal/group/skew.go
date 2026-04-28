package group

import (
	"sort"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Cross-device clock-skew detection thresholds. Hardcoded — the values are
// load-bearing for "did these two cameras shoot the same scene", and the
// user toggle gates the whole feature anyway.
const (
	// anchorSimilarity is the cosine threshold for accepting a (refFile,
	// otherFile) pair as evidence of "same scene from two cameras". Set
	// well above the default cluster threshold (0.92) so we only rely on
	// near-identical frames when estimating skew.
	anchorSimilarity = 0.95

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
// Reference device: the one with the most embedded files; ties broken
// alphabetically. The reference's offset is always 0. Each non-reference
// device D's offset is the median of (d.captured_at - r.captured_at) over
// pairs where cosine(emb_d, emb_r) >= anchorSimilarity and the raw time
// difference is within ±maxSkewSec. Devices with fewer than minAnchors
// qualifying pairs get offset 0 (untouched).
//
// Returns an empty map if there are zero or one devices with usable
// embedded files — there's nothing to align.
func EstimateSkew(files []store.File, embeds map[int64][]float32) map[string]int64 {
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
		return map[string]int64{}
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
		deltas := pairDeltas(byDevice[ref], byDevice[d], embeds)
		if len(deltas) < minAnchors {
			result[d] = 0
			continue
		}
		result[d] = medianInt64(deltas)
	}
	return result
}

// pairDeltas returns (other.CapturedAt - ref.CapturedAt) for every pair
// whose cosine similarity is >= anchorSimilarity and whose raw time
// difference is within ±maxSkewSec. Both inputs must be sorted ascending
// by CapturedAt; the implementation slides a forward-only window over
// `other` as it walks `ref`.
func pairDeltas(ref, other []skewEntry, embeds map[int64][]float32) []int64 {
	var deltas []int64
	lo := 0
	for _, r := range ref {
		for lo < len(other) && other[lo].CapturedAt < r.CapturedAt-maxSkewSec {
			lo++
		}
		for j := lo; j < len(other); j++ {
			o := other[j]
			if o.CapturedAt > r.CapturedAt+maxSkewSec {
				break
			}
			er, ok1 := embeds[r.ID]
			eo, ok2 := embeds[o.ID]
			if !ok1 || !ok2 {
				continue
			}
			if cosine(er, eo) < anchorSimilarity {
				continue
			}
			deltas = append(deltas, o.CapturedAt-r.CapturedAt)
		}
	}
	return deltas
}

// medianInt64 returns the median of xs (xs is mutated by sorting). Caller
// must guarantee len(xs) > 0.
func medianInt64(xs []int64) int64 {
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}
