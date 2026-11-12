package group

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// MergedDeviceKey is the sentinel device_key used for clusters whose
// members come from more than one device. Library and any other label
// formatter must special-case this — the literal string is also exposed
// to other packages so they don't have to copy it.
const MergedDeviceKey = "*merged*"

// MaxClusterWindow is the most vectors one Cluster call is given. A time
// bucket with more files that have a vector is clustered in consecutive
// windows of this many, in the bucket's time order, so memory stays bounded
// by one similarity matrix of this size (spec §5.4). Groups never span two
// windows.
const MaxClusterWindow = 4000

// Grouper rebuilds clusters from analyzed files in a single (device, time-bucket).
type Grouper struct {
	s           *store.Store
	bucketSec   int64
	threshold   float64
	crossDevice bool
	// noEmbeddings is the inverse of SetUseEmbeddings, so the zero value
	// means "use them" (the default).
	noEmbeddings atomic.Bool
}

// NewGrouper constructs a Grouper. bucketSec is the time-bucket width in seconds
// (default 600 per spec). threshold is the cosine similarity for visual clustering
// (default 0.92 per spec). crossDevice enables the cross-device merging mode:
// devices are aligned via clock-skew detection before bucketing, and bursts that
// span devices collapse into one cluster.
func NewGrouper(s *store.Store, bucketSec int64, threshold float64, crossDevice bool) *Grouper {
	return &Grouper{s: s, bucketSec: bucketSec, threshold: threshold, crossDevice: crossDevice}
}

// SetUseEmbeddings switches visual sub-clustering. Off, every file is
// treated as having no embedding, so clusters are the time buckets alone
// (and cross-device mode, which needs embeddings, falls back to per-device
// grouping). On is the default. It applies from the next rebuild.
func (g *Grouper) SetUseEmbeddings(on bool) { g.noEmbeddings.Store(!on) }

func (g *Grouper) useEmbeddings() bool { return !g.noEmbeddings.Load() }

// RebuildBucket recomputes clusters for the given (device_key, time_bucket).
// The bucket is clustered like every other path (clusterIDs, in time order)
// and written only once its clustering succeeded: the delete of old clusters
// and the insert of new ones happen inside a single transaction (via
// Store.WriteBucket), so a cancelled or failed rebuild leaves the bucket as it
// was.
func (g *Grouper) RebuildBucket(ctx context.Context, deviceKey string, timeBucket sql.NullInt64) error {
	files, err := g.s.Files().ListAnalyzedInBucket(deviceKey, timeBucket, g.bucketSec)
	if err != nil {
		return fmt.Errorf("list bucket files: %w", err)
	}

	if len(files) == 0 {
		// No analyzed files in bucket — delete any stale clusters and return.
		return g.s.WriteBucket(ctx, deviceKey, timeBucket, nil)
	}

	// The bucket's files in time order, for the windows of clusterIDs.
	seq := make([]runFile, len(files))
	for i, f := range files {
		seq[i] = runFile{ID: f.ID, DeviceKey: deviceKey, CapturedAt: f.CapturedAt}
	}
	sortRunFiles(seq)
	ids := make([]int64, len(seq))
	for i, f := range seq {
		ids[i] = f.ID
	}

	// Gather embeddings. Files without embeddings (e.g., when the CLIP model
	// is not installed) are merged into the bucket-level fallback cluster:
	// time proximity already does the grouping work, so without a visual
	// signal saying these are different scenes we trust the bucket.
	embeds := make(map[int64][]float32, len(files))
	if g.useEmbeddings() {
		for _, id := range ids {
			sig, err := g.s.AISignals().GetByFileID(id)
			if err == nil && sig.Embedding != nil {
				embeds[id] = sig.Embedding
			}
		}
	}

	groups, err := clusterIDs(ctx, ids, embeds, g.threshold)
	if err != nil {
		return err
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
	return g.RebuildAllProgress(ctx, nil)
}

// clusterProgress counts the files a rebuild has placed in clusters.
type clusterProgress struct {
	fn          func(done, total int) // nil: nothing is reported
	done, total int
}

// add reports n more files clustered.
func (p *clusterProgress) add(n int) {
	if p.fn == nil {
		return
	}
	p.done += n
	p.fn(p.done, p.total)
}

// RebuildAllProgress is RebuildAll reporting its progress in files: progress
// (nil: none) is called with 0 of the analyzed files once they are listed,
// then after each bucket (a device's time bucket, or a bucket of the merged
// timeline) has been sub-clustered, with the files placed so far; the last
// call has done == total, before the clusters are written in one
// transaction. Files, not buckets, because the total is known before any
// bucket is cut and a bucket's cost grows with its files. It is called on
// the caller's goroutine.
func (g *Grouper) RebuildAllProgress(ctx context.Context, progress func(done, total int)) error {
	start := time.Now()
	useEmb := g.useEmbeddings()
	slog.Info("cluster rebuild start",
		"bucket_sec", g.bucketSec,
		"threshold", g.threshold,
		"cross_device", g.crossDevice,
		"use_embeddings", useEmb,
	)
	files, err := g.s.Files().ListAllAnalyzed()
	if err != nil {
		slog.Warn("cluster rebuild failed", "phase", "list_files", "err", err)
		return fmt.Errorf("list analyzed files: %w", err)
	}
	prog := &clusterProgress{fn: progress, total: len(files)}
	prog.add(0)
	if len(files) == 0 {
		slog.Info("cluster rebuild done", "files", 0, "embeddings", 0, "mode", "empty", "elapsed_ms", time.Since(start).Milliseconds())
		return g.s.WriteClusters(ctx, nil)
	}

	// With embeddings off every file counts as having none: an empty map
	// leaves only the time buckets (and short-circuits cross-device mode).
	embeds := map[int64][]float32{}
	if useEmb {
		embeds, err = g.s.AISignals().AllEmbeddings()
		if err != nil {
			slog.Warn("cluster rebuild failed", "phase", "load_embeddings", "err", err)
			return fmt.Errorf("load embeddings: %w", err)
		}
	}

	// Cross-device mode requires at least one embedding to anchor the
	// timelines. Without any, fall back to per-device behavior — merging
	// adjacent devices on time alone would mix unrelated scenes.
	mode := "per_device"
	if g.crossDevice && len(embeds) > 0 {
		mode = "cross_device"
		err = g.rebuildAllCrossDevice(ctx, files, embeds, prog)
	} else {
		err = g.rebuildAllPerDevice(ctx, files, embeds, prog)
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
func (g *Grouper) rebuildAllPerDevice(ctx context.Context, files []store.File, embeds map[int64][]float32, prog *clusterProgress) error {
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
	emit := func(dev string, start sql.NullInt64, ids []int64) error {
		groups, err := clusterIDs(ctx, ids, embeds, g.threshold)
		if err != nil {
			return err
		}
		for _, members := range groups {
			defs = append(defs, store.ClusterDef{
				DeviceKey:  dev,
				TimeBucket: start,
				Members:    members,
			})
		}
		prog.add(len(ids))
		return nil
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
		flush := func() error {
			if len(currIDs) == 0 {
				return nil
			}
			err := emit(dev, currStart, currIDs)
			currIDs = nil
			return err
		}

		for _, f := range seq {
			if !f.CapturedAt.Valid {
				if err := flush(); err != nil {
					return err
				}
				if err := emit(dev, sql.NullInt64{}, []int64{f.ID}); err != nil {
					return err
				}
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
				if err := flush(); err != nil {
					return err
				}
				currStart = f.CapturedAt
				currIDs = []int64{f.ID}
			} else {
				currIDs = append(currIDs, f.ID)
			}
			prev = f
		}
		if err := flush(); err != nil {
			return err
		}
	}

	// Every bucket is clustered: only now are the old clusters replaced.
	return g.s.WriteClusters(ctx, defs)
}

// rebuildAllCrossDevice runs the gap-walk over a single canonical
// timeline built by subtracting each device's estimated clock skew. The
// per-cluster device_key is computed from the membership: single-device
// clusters keep that device's key; multi-device clusters are tagged with
// MergedDeviceKey. Files without embeddings stay anchored to their own
// device's timeline so we never claim two cameras shot the same scene
// when we have no visual evidence.
func (g *Grouper) rebuildAllCrossDevice(ctx context.Context, files []store.File, embeds map[int64][]float32, prog *clusterProgress) error {
	skew, err := EstimateSkew(ctx, files, embeds)
	if err != nil {
		return err
	}

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
	emit := func(start sql.NullInt64, members []runFile) error {
		ids := make([]int64, len(members))
		idDevice := make(map[int64]string, len(members))
		for i, m := range members {
			ids[i] = m.ID
			idDevice[m.ID] = m.DeviceKey
		}
		groups, err := clusterIDs(ctx, ids, embeds, g.threshold)
		if err != nil {
			return err
		}
		for _, group := range groups {
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
		prog.add(len(members))
		return nil
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
	flushTimed := func() error {
		if len(currMembers) == 0 {
			return nil
		}
		err := emit(currStart, currMembers)
		currMembers = nil
		return err
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
			if err := flushTimed(); err != nil {
				return err
			}
			currStart = f.CapturedAt
			currMembers = []runFile{f}
		} else {
			currMembers = append(currMembers, f)
		}
		prev = f
	}
	if err := flushTimed(); err != nil {
		return err
	}

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
			prog.add(len(ids))
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
		if len(ids) > 0 {
			prog.add(len(ids))
		}
	}

	// Every bucket is clustered: only now are the old clusters replaced.
	return g.s.WriteClusters(ctx, defs)
}

// clusterIDs sub-clusters one time bucket. ids are the bucket's files in
// its time order. Those with a vector are cut into consecutive runs of at
// most MaxClusterWindow, each clustered on its own (Cluster); the ids
// without one form a single fallback group. Returned groups have ascending
// member ids; outer slice ordered by smallest member id. It returns ctx's
// error when it is cancelled.
func clusterIDs(ctx context.Context, ids []int64, embeds map[int64][]float32, threshold float64) ([][]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var withVec, noEmb []int64
	for _, id := range ids {
		if _, ok := embeds[id]; ok {
			withVec = append(withVec, id)
		} else {
			noEmb = append(noEmb, id)
		}
	}
	var groups [][]int64
	for start := 0; start < len(withVec); start += MaxClusterWindow {
		window := withVec[start:min(start+MaxClusterWindow, len(withVec))]
		vectors := make(map[int64][]float32, len(window))
		for _, id := range window {
			vectors[id] = embeds[id]
		}
		got, err := Cluster(ctx, vectors, threshold)
		if err != nil {
			return nil, err
		}
		groups = append(groups, got...)
	}
	if len(noEmb) > 0 {
		sort.Slice(noEmb, func(i, j int) bool { return noEmb[i] < noEmb[j] })
		groups = append(groups, noEmb)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	return groups, nil
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
