package group

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// MergedDeviceKey is the sentinel device_key used for clusters whose
// members come from more than one device. Library and any other label
// formatter must special-case this — the literal string is also exposed
// to other packages so they don't have to copy it.
const MergedDeviceKey = "*merged*"

// Grouper rebuilds clusters from analyzed files in a single (device, time-bucket).
type Grouper struct {
	s           *store.Store
	bucketSec   int64
	threshold   float64
	crossDevice bool
}

// NewGrouper constructs a Grouper. bucketSec is the time-bucket width in seconds
// (default 600 per spec). threshold is the cosine similarity for visual clustering
// (default 0.92 per spec). crossDevice enables the cross-device merging mode:
// devices are aligned via clock-skew detection before bucketing, and bursts that
// span devices collapse into one cluster.
func NewGrouper(s *store.Store, bucketSec int64, threshold float64, crossDevice bool) *Grouper {
	return &Grouper{s: s, bucketSec: bucketSec, threshold: threshold, crossDevice: crossDevice}
}

// RebuildBucket recomputes clusters for the given (device_key, time_bucket).
// The delete of old clusters and the insert of new ones happen inside a single
// transaction (via Store.WriteBucket) so the bucket is never left partially
// rebuilt if an insert fails mid-way.
func (g *Grouper) RebuildBucket(ctx context.Context, deviceKey string, timeBucket sql.NullInt64) error {
	files, err := g.s.Files().ListAnalyzedInBucket(deviceKey, timeBucket, g.bucketSec)
	if err != nil {
		return fmt.Errorf("list bucket files: %w", err)
	}

	if len(files) == 0 {
		// No analyzed files in bucket — delete any stale clusters and return.
		return g.s.WriteBucket(ctx, deviceKey, timeBucket, nil)
	}

	// Gather embeddings. Files without embeddings (e.g., when the CLIP model
	// is not installed) are merged into the bucket-level fallback cluster:
	// time proximity already does the grouping work, so without a visual
	// signal saying these are different scenes we trust the bucket.
	embeds := make(map[int64][]float32, len(files))
	noEmb := make([]int64, 0)
	for _, f := range files {
		sig, err := g.s.AISignals().GetByFileID(f.ID)
		if err != nil || sig.Embedding == nil {
			noEmb = append(noEmb, f.ID)
			continue
		}
		embeds[f.ID] = sig.Embedding
	}

	groups := Cluster(embeds, g.threshold)
	if len(noEmb) > 0 {
		sort.Slice(noEmb, func(i, j int) bool { return noEmb[i] < noEmb[j] })
		groups = append(groups, noEmb)
		sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	}

	defs := make([]store.ClusterDef, 0, len(groups))
	for _, members := range groups {
		defs = append(defs, store.ClusterDef{
			DeviceKey:  deviceKey,
			TimeBucket: timeBucket,
			Members:    members,
		})
	}

	return g.s.WriteBucket(ctx, deviceKey, timeBucket, defs)
}

// runFile is the shared in-memory file shape used by both clustering paths.
type runFile struct {
	ID         int64
	DeviceKey  string
	CapturedAt sql.NullInt64
}

// RebuildAll wipes every existing cluster and rebuilds the table.
//
// Per-device mode (the default): within a single device's timeline, two
// consecutive shots stay in the same bucket as long as the gap between
// them is at most g.bucketSec. Two shots taken five minutes apart
// therefore always land together regardless of where they fall on the
// absolute clock — fixing the boundary-alignment artifact where a shot at
// 14:34:59 and one at 14:35:01 ended up in different clusters.
//
// Cross-device mode (when g.crossDevice && embeddings exist): EstimateSkew
// computes a per-device offset, the gap-walk runs over the merged
// canonical timeline, and clusters whose members span devices are tagged
// with MergedDeviceKey. Files without embeddings still cluster only with
// their own device — without a visual signal, we have no evidence two
// cameras shot the same scene.
//
// Bucket key (clusters.time_bucket) is the (canonical) captured_at of the
// first file in the bucket; this is what the UI displays as the cluster's
// start time. Files with NULL captured_at form their own per-device
// singleton bucket with NULL time_bucket.
//
// All embeddings are loaded in one query and the cluster/cluster_members
// rebuild lands in a single transaction, so a recluster of a 2 000-photo
// library is one fsync rather than thousands.
//
// Picks live in a separate table and are unaffected by this rebuild.
func (g *Grouper) RebuildAll(ctx context.Context) error {
	start := time.Now()
	slog.Info("cluster rebuild start",
		"bucket_sec", g.bucketSec,
		"threshold", g.threshold,
		"cross_device", g.crossDevice,
	)
	files, err := g.s.Files().ListAllAnalyzed()
	if err != nil {
		slog.Warn("cluster rebuild failed", "phase", "list_files", "err", err)
		return fmt.Errorf("list analyzed files: %w", err)
	}
	if len(files) == 0 {
		slog.Info("cluster rebuild done", "files", 0, "embeddings", 0, "mode", "empty", "elapsed_ms", time.Since(start).Milliseconds())
		return g.s.WriteClusters(ctx, nil)
	}

	embeds, err := g.s.AISignals().AllEmbeddings()
	if err != nil {
		slog.Warn("cluster rebuild failed", "phase", "load_embeddings", "err", err)
		return fmt.Errorf("load embeddings: %w", err)
	}

	// Cross-device mode requires at least one embedding to anchor the
	// timelines. Without any, fall back to per-device behavior — merging
	// adjacent devices on time alone would mix unrelated scenes.
	mode := "per_device"
	if g.crossDevice && len(embeds) > 0 {
		mode = "cross_device"
		err = g.rebuildAllCrossDevice(ctx, files, embeds)
	} else {
		err = g.rebuildAllPerDevice(ctx, files, embeds)
	}
	if err != nil {
		slog.Warn("cluster rebuild failed", "phase", "rebuild", "mode", mode, "err", err)
		return err
	}
	slog.Info("cluster rebuild done",
		"files", len(files),
		"embeddings", len(embeds),
		"mode", mode,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// rebuildAllPerDevice is the original, device-partitioned path.
func (g *Grouper) rebuildAllPerDevice(ctx context.Context, files []store.File, embeds map[int64][]float32) error {
	byDevice := map[string][]runFile{}
	deviceOrder := []string{}
	for _, f := range files {
		dev := "Unknown"
		if f.DeviceKey.Valid && f.DeviceKey.String != "" {
			dev = f.DeviceKey.String
		}
		if _, ok := byDevice[dev]; !ok {
			deviceOrder = append(deviceOrder, dev)
		}
		byDevice[dev] = append(byDevice[dev], runFile{ID: f.ID, DeviceKey: dev, CapturedAt: f.CapturedAt})
	}

	var defs []store.ClusterDef
	emit := func(dev string, start sql.NullInt64, ids []int64) {
		for _, members := range clusterIDs(ids, embeds, g.threshold) {
			defs = append(defs, store.ClusterDef{
				DeviceKey:  dev,
				TimeBucket: start,
				Members:    members,
			})
		}
	}

	for _, dev := range deviceOrder {
		if err := ctx.Err(); err != nil {
			return err
		}
		seq := byDevice[dev]
		sortRunFiles(seq)

		var (
			currIDs   []int64
			currStart sql.NullInt64
			prev      runFile
			havePrev  bool
		)
		flush := func() {
			if len(currIDs) == 0 {
				return
			}
			emit(dev, currStart, currIDs)
			currIDs = nil
		}

		for _, f := range seq {
			if !f.CapturedAt.Valid {
				flush()
				emit(dev, sql.NullInt64{}, []int64{f.ID})
				havePrev = false
				continue
			}
			if !havePrev {
				currStart = f.CapturedAt
				currIDs = []int64{f.ID}
				prev = f
				havePrev = true
				continue
			}
			gap := f.CapturedAt.Int64 - prev.CapturedAt.Int64
			if gap > g.bucketSec {
				flush()
				currStart = f.CapturedAt
				currIDs = []int64{f.ID}
			} else {
				currIDs = append(currIDs, f.ID)
			}
			prev = f
		}
		flush()
	}

	return g.s.WriteClusters(ctx, defs)
}

// rebuildAllCrossDevice runs the gap-walk over a single canonical
// timeline built by subtracting each device's estimated clock skew. The
// per-cluster device_key is computed from the membership: single-device
// clusters keep that device's key; multi-device clusters are tagged with
// MergedDeviceKey. Files without embeddings stay anchored to their own
// device's timeline so we never claim two cameras shot the same scene
// when we have no visual evidence.
func (g *Grouper) rebuildAllCrossDevice(ctx context.Context, files []store.File, embeds map[int64][]float32) error {
	skew := EstimateSkew(files, embeds)

	deviceFor := make(map[int64]string, len(files))
	timed := make([]runFile, 0, len(files))
	noTimeByDevice := map[string][]int64{}
	noEmbByDevice := map[string][]runFile{}
	deviceOrder := []string{}
	seenDev := map[string]bool{}

	for _, f := range files {
		dev := "Unknown"
		if f.DeviceKey.Valid && f.DeviceKey.String != "" {
			dev = f.DeviceKey.String
		}
		deviceFor[f.ID] = dev
		if !seenDev[dev] {
			seenDev[dev] = true
			deviceOrder = append(deviceOrder, dev)
		}
		if !f.CapturedAt.Valid {
			noTimeByDevice[dev] = append(noTimeByDevice[dev], f.ID)
			continue
		}
		// Files without embeddings opt out of cross-device merging — the
		// gap-walk happens per device for these (handled below).
		if _, ok := embeds[f.ID]; !ok {
			noEmbByDevice[dev] = append(noEmbByDevice[dev], runFile{
				ID: f.ID, DeviceKey: dev, CapturedAt: f.CapturedAt,
			})
			continue
		}
		canonical := f.CapturedAt.Int64 - skew[dev]
		timed = append(timed, runFile{
			ID:         f.ID,
			DeviceKey:  dev,
			CapturedAt: sql.NullInt64{Int64: canonical, Valid: true},
		})
	}

	sort.SliceStable(timed, func(i, j int) bool {
		ai, aj := timed[i].CapturedAt.Int64, timed[j].CapturedAt.Int64
		if ai != aj {
			return ai < aj
		}
		return timed[i].ID < timed[j].ID
	})

	var defs []store.ClusterDef
	emit := func(start sql.NullInt64, members []runFile) {
		ids := make([]int64, len(members))
		idDevice := make(map[int64]string, len(members))
		for i, m := range members {
			ids[i] = m.ID
			idDevice[m.ID] = m.DeviceKey
		}
		for _, group := range clusterIDs(ids, embeds, g.threshold) {
			devs := map[string]struct{}{}
			for _, id := range group {
				devs[idDevice[id]] = struct{}{}
			}
			devKey := MergedDeviceKey
			if len(devs) == 1 {
				for d := range devs {
					devKey = d
				}
			}
			defs = append(defs, store.ClusterDef{
				DeviceKey:  devKey,
				TimeBucket: start,
				Members:    group,
			})
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	var (
		currMembers []runFile
		currStart   sql.NullInt64
		prev        runFile
		havePrev    bool
	)
	flushTimed := func() {
		if len(currMembers) == 0 {
			return
		}
		emit(currStart, currMembers)
		currMembers = nil
	}
	for _, f := range timed {
		if !havePrev {
			currStart = f.CapturedAt
			currMembers = []runFile{f}
			prev = f
			havePrev = true
			continue
		}
		gap := f.CapturedAt.Int64 - prev.CapturedAt.Int64
		if gap > g.bucketSec {
			flushTimed()
			currStart = f.CapturedAt
			currMembers = []runFile{f}
		} else {
			currMembers = append(currMembers, f)
		}
		prev = f
	}
	flushTimed()

	// Per-device fallback: files without embeddings still need clusters,
	// but we don't merge them across devices. Run the standard gap-walk
	// per device for these.
	for _, dev := range deviceOrder {
		seq := noEmbByDevice[dev]
		if len(seq) == 0 {
			continue
		}
		sortRunFiles(seq)
		var (
			ids   []int64
			start sql.NullInt64
			pf    runFile
			hp    bool
		)
		flush := func() {
			if len(ids) == 0 {
				return
			}
			defs = append(defs, store.ClusterDef{
				DeviceKey:  dev,
				TimeBucket: start,
				Members:    ids,
			})
			ids = nil
		}
		for _, f := range seq {
			if !hp {
				start = f.CapturedAt
				ids = []int64{f.ID}
				pf = f
				hp = true
				continue
			}
			gap := f.CapturedAt.Int64 - pf.CapturedAt.Int64
			if gap > g.bucketSec {
				flush()
				start = f.CapturedAt
				ids = []int64{f.ID}
			} else {
				ids = append(ids, f.ID)
			}
			pf = f
		}
		flush()
	}

	// NULL captured_at: per-device singleton clusters, exactly like the
	// per-device path.
	for _, dev := range deviceOrder {
		ids := noTimeByDevice[dev]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			defs = append(defs, store.ClusterDef{
				DeviceKey:  dev,
				TimeBucket: sql.NullInt64{},
				Members:    []int64{id},
			})
		}
	}

	return g.s.WriteClusters(ctx, defs)
}

// clusterIDs runs the visual sub-clusterer on `ids` and appends a single
// fallback group containing any ids without an embedding. Returned groups
// have ascending member ids; outer slice ordered by smallest member id.
func clusterIDs(ids []int64, embeds map[int64][]float32, threshold float64) [][]int64 {
	if len(ids) == 0 {
		return nil
	}
	bucketEmbeds := make(map[int64][]float32, len(ids))
	noEmb := make([]int64, 0)
	for _, id := range ids {
		if e, ok := embeds[id]; ok {
			bucketEmbeds[id] = e
		} else {
			noEmb = append(noEmb, id)
		}
	}
	groups := Cluster(bucketEmbeds, threshold)
	if len(noEmb) > 0 {
		sort.Slice(noEmb, func(i, j int) bool { return noEmb[i] < noEmb[j] })
		groups = append(groups, noEmb)
		sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	}
	return groups
}

// sortRunFiles sorts in place: valid captured_at ascending first, NULLs
// last; ties by id.
func sortRunFiles(seq []runFile) {
	sort.SliceStable(seq, func(i, j int) bool {
		ai, aj := seq[i].CapturedAt, seq[j].CapturedAt
		if ai.Valid != aj.Valid {
			return ai.Valid
		}
		if ai.Valid && ai.Int64 != aj.Int64 {
			return ai.Int64 < aj.Int64
		}
		return seq[i].ID < seq[j].ID
	})
}
