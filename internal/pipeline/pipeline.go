package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"
)

// Scanner abstracts the filesystem walker (internal/scanner).
type Scanner interface {
	Walk(ctx context.Context) error
}

// Grouper abstracts the cluster builder (internal/group). RebuildAll
// recomputes clusters for every (device_key, time_bucket) whose membership
// has changed since the last call. See "Cluster recomputation" in the spec.
type Grouper interface {
	RebuildAll(ctx context.Context) error
}

// ProgressGrouper is a Grouper that reports the cluster stage's progress
// like the per-file stages do: done of total (in files placed in clusters),
// on the goroutine that called it. The pipeline uses it when the Grouper
// implements it; otherwise the stage reports only starting and done.
type ProgressGrouper interface {
	Grouper
	RebuildAllProgress(ctx context.Context, progress func(done, total int)) error
}

// IDLister exposes the subset of the store we need: returning ids whose
// scan_status equals the given value, in batches, plus a cheap count of
// the queue size for stable progress denominators.
type IDLister interface {
	ListByStatus(ctx context.Context, status string, limit int) ([]int64, error)
	CountByStatus(ctx context.Context, status string) (int, error)
}

// DefaultBatchSize is how many ids each stage consumes per ListByStatus
// call. Chosen to balance per-call SQLite cost against memory churn.
const DefaultBatchSize = 100

// Pipeline orchestrates discover → metadata → thumb → analyze → cluster.
type Pipeline struct {
	Store         IDLister
	Scanner       Scanner
	MetaExtractor MetaProcessor
	ThumbBuilder  ThumbProcessor
	Analyzer      AnalyzeProcessor
	Grouper       Grouper
	Reporter      *Reporter

	// Concurrency keys: "metadata" | "thumb" | "analyze". Missing key falls
	// back to runtime.NumCPU(). The session runs analyze on a few workers:
	// each feature print is a native call, already bounded by
	// platform.NativeSlots.
	Concurrency map[string]int

	// BatchSize overrides DefaultBatchSize when > 0.
	BatchSize int
}

// stageStatusFrom returns the scan_status that a stage *consumes*. Files
// in this status are eligible for the stage's input queue.
func stageStatusFrom(name string) string {
	switch name {
	case "metadata":
		return "discovered"
	case "thumb":
		return "metadata"
	case "analyze":
		return "thumbed"
	}
	return ""
}

// pendingStatuses are the scan_status values of files that still have a
// per-file stage ahead of them.
var pendingStatuses = []string{"discovered", "metadata", "thumbed"}

// Pending returns how many files still have per-file work ahead of them:
// those in 'discovered', 'metadata' or 'thumbed'. Analyzed, failed and
// missing files are not pending.
func (p *Pipeline) Pending(ctx context.Context) (int, error) {
	total := 0
	for _, status := range pendingStatuses {
		n, err := p.Store.CountByStatus(ctx, status)
		if err != nil {
			return 0, fmt.Errorf("pending: count by status %q: %w", status, err)
		}
		total += n
	}
	return total, nil
}

func (p *Pipeline) workersFor(stage string) int {
	if n, ok := p.Concurrency[stage]; ok && n > 0 {
		return n
	}
	return runtime.NumCPU()
}

func (p *Pipeline) batchSize() int {
	if p.BatchSize > 0 {
		return p.BatchSize
	}
	return DefaultBatchSize
}

// Run executes the full pipeline: discover → per-file stages → cluster.
// Returns the first error encountered (including ctx cancellation). On
// cancellation, completed work is durable in the store; the next Run or
// Resume picks up where this one stopped.
func (p *Pipeline) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	slog.Info("pipeline run start")
	start := time.Now()
	p.Reporter.Update(Progress{Stage: "discover", Phase: "starting"})
	slog.Info("pipeline stage start", "stage", "discover")
	walkStart := time.Now()
	if err := p.Scanner.Walk(ctx); err != nil {
		p.Reporter.Update(Progress{Stage: "discover", Phase: "failed"})
		slog.Warn("pipeline stage failed", "stage", "discover", "err", err)
		return fmt.Errorf("discover: %w", err)
	}
	p.Reporter.Update(Progress{Stage: "discover", Phase: "done"})
	slog.Info("pipeline stage done", "stage", "discover", "elapsed_ms", time.Since(walkStart).Milliseconds())

	if err := p.runStagesAndCluster(ctx); err != nil {
		slog.Warn("pipeline run failed", "elapsed_ms", time.Since(start).Milliseconds(), "err", err)
		return err
	}
	slog.Info("pipeline run done", "elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// Resume is equivalent to Run but skips the discover step. It assumes the
// files table already reflects the on-disk corpus and resumes per-stage
// processing from each file's current scan_status. See Task 8.5.
func (p *Pipeline) Resume(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.runStagesAndCluster(ctx)
}

func (p *Pipeline) runStagesAndCluster(ctx context.Context) error {
	stages := RegisterStages(p)
	for _, st := range stages {
		if err := p.runStage(ctx, st); err != nil {
			return err
		}
	}
	p.Reporter.Update(Progress{Stage: "cluster", Phase: "starting"})
	slog.Info("pipeline stage start", "stage", "cluster")
	clusterStart := time.Now()
	// The latest count, carried into the done or failed update like the
	// per-file stages do. The grouper reports on this goroutine.
	var last Progress
	rebuild := p.Grouper.RebuildAll
	if pg, ok := p.Grouper.(ProgressGrouper); ok {
		rebuild = func(ctx context.Context) error {
			return pg.RebuildAllProgress(ctx, func(done, total int) {
				last = Progress{Stage: "cluster", Done: done, Total: total, Phase: "running"}
				p.Reporter.Update(last)
			})
		}
	}
	if err := rebuild(ctx); err != nil {
		p.Reporter.Update(Progress{Stage: "cluster", Done: last.Done, Total: last.Total, Phase: "failed"})
		slog.Warn("pipeline stage failed", "stage", "cluster", "err", err)
		return fmt.Errorf("cluster: %w", err)
	}
	p.Reporter.Update(Progress{Stage: "cluster", Done: last.Done, Total: last.Total, Phase: "done"})
	slog.Info("pipeline stage done", "stage", "cluster", "elapsed_ms", time.Since(clusterStart).Milliseconds())
	return nil
}

// runStage drains every file currently in the stage's input status, in
// batches, until the queue is empty or ctx is cancelled. Each batch is
// processed by RunPool with the stage's configured concurrency. Progress
// is emitted per file (not per batch) so the UI shows live throughput
// instead of jumping in 100-file leaps.
func (p *Pipeline) runStage(ctx context.Context, st Stage) error {
	from := stageStatusFrom(st.Name())
	if from == "" {
		return fmt.Errorf("pipeline: unknown stage %q", st.Name())
	}
	workers := p.workersFor(st.Name())
	batch := p.batchSize()

	// Snapshot the full queue size up front so the denominator stays
	// stable across batches. Without this, "Total" would only count
	// what's been processed in the current batch.
	totalQueue, err := p.Store.CountByStatus(ctx, from)
	if err != nil {
		return fmt.Errorf("%s: count by status %q: %w", st.Name(), from, err)
	}

	var (
		totalOK    int64
		totalFail  int64
		lastLogged int64
		stageStart = time.Now()
		logEvery   = stageLogInterval(totalQueue)
	)
	p.Reporter.Update(Progress{Stage: st.Name(), Total: totalQueue, Phase: "starting"})
	slog.Info("pipeline stage start", "stage", st.Name(), "queue", totalQueue, "workers", workers)

	progress := func(okDelta, failDelta int) {
		ok := atomic.AddInt64(&totalOK, int64(okDelta))
		fail := atomic.AddInt64(&totalFail, int64(failDelta))
		p.Reporter.Update(Progress{
			Stage:  st.Name(),
			Done:   int(ok),
			Total:  totalQueue,
			Failed: int(fail),
			Phase:  "running",
		})
		// Periodic INFO progress so the console shows movement on long
		// stages — every logEvery completions, racy but cheap; the lossy
		// CAS means we may skip a tick under heavy contention, which is
		// fine for human-readable progress.
		done := ok + fail
		prev := atomic.LoadInt64(&lastLogged)
		if done-prev >= int64(logEvery) && atomic.CompareAndSwapInt64(&lastLogged, prev, done) {
			slog.Info("pipeline stage progress",
				"stage", st.Name(),
				"done", ok,
				"failed", fail,
				"total", totalQueue,
			)
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "failed"})
			slog.Warn("pipeline stage cancelled", "stage", st.Name(), "done", totalOK, "failed", totalFail, "total", totalQueue)
			return err
		}

		// Count how many files are still in the input status before we
		// dequeue. If, after processing a non-empty batch, the count is
		// unchanged, every item in the batch failed without advancing its
		// status — infinite retry would follow. Break with an error so the
		// operator sees the stuck stage instead of spinning forever.
		countBefore, err := p.Store.CountByStatus(ctx, from)
		if err != nil {
			return fmt.Errorf("%s: count by status %q: %w", st.Name(), from, err)
		}

		ids, err := p.Store.ListByStatus(ctx, from, batch)
		if err != nil {
			return fmt.Errorf("%s: list by status %q: %w", st.Name(), from, err)
		}
		if len(ids) == 0 {
			break
		}
		_, _, runErr := RunPoolWithProgress(ctx, workers, ids, st.Process, progress)
		if runErr != nil {
			// Cancellation, or the folder becoming unavailable, aborts
			// the stage and bubbles up. Per-item errors are tracked in
			// the count but don't stop the pool.
			if isCancellation(runErr) {
				p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "failed"})
				slog.Warn("pipeline stage cancelled", "stage", st.Name(), "done", totalOK, "failed", totalFail, "total", totalQueue)
				return runErr
			}
			// Per-item error: log it via the failed count and keep going.
		}

		// No-progress guard: if the queue size at the input status did not
		// shrink after processing a non-empty batch, the stage is stuck
		// (every item failed without changing its status). Abort to avoid
		// infinite reprocessing.
		countAfter, err := p.Store.CountByStatus(ctx, from)
		if err != nil {
			return fmt.Errorf("%s: count by status %q (post-batch): %w", st.Name(), from, err)
		}
		if countAfter >= countBefore {
			p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "failed"})
			slog.Warn("pipeline stage stuck", "stage", st.Name(), "batch", len(ids), "done", totalOK, "failed", totalFail, "total", totalQueue)
			return fmt.Errorf("pipeline %s: stage made no progress on %d items", st.Name(), len(ids))
		}
	}
	p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "done"})
	slog.Info("pipeline stage done",
		"stage", st.Name(),
		"done", totalOK,
		"failed", totalFail,
		"total", totalQueue,
		"elapsed_ms", time.Since(stageStart).Milliseconds(),
	)
	return nil
}

// stageLogInterval picks how often the per-stage progress callback emits an
// INFO log line — small queues log on every file, big queues log every ~5%.
// The result is always at least 1.
func stageLogInterval(total int) int {
	if total <= 25 {
		return 1
	}
	step := total / 20
	if step < 5 {
		step = 5
	}
	if step > 100 {
		step = 100
	}
	return step
}
