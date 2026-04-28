package pipeline

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
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
	// back to runtime.NumCPU(). Analyze typically uses 1 to keep CoreML
	// inference single-threaded.
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
	p.Reporter.Update(Progress{Stage: "discover", Phase: "starting"})
	if err := p.Scanner.Walk(ctx); err != nil {
		p.Reporter.Update(Progress{Stage: "discover", Phase: "failed"})
		return fmt.Errorf("discover: %w", err)
	}
	p.Reporter.Update(Progress{Stage: "discover", Phase: "done"})

	return p.runStagesAndCluster(ctx)
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
	if err := p.Grouper.RebuildAll(ctx); err != nil {
		p.Reporter.Update(Progress{Stage: "cluster", Phase: "failed"})
		return fmt.Errorf("cluster: %w", err)
	}
	p.Reporter.Update(Progress{Stage: "cluster", Phase: "done"})
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
		totalOK   int64
		totalFail int64
	)
	p.Reporter.Update(Progress{Stage: st.Name(), Total: totalQueue, Phase: "starting"})

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
	}

	for {
		if err := ctx.Err(); err != nil {
			p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "failed"})
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
			// Cancellation aborts the stage and bubbles up. Per-item
			// errors are tracked in the count but don't stop the pool;
			// runErr will only be non-nil for ctx cancellation here
			// because per-item failures are isolated by the workers.
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "failed"})
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
			return fmt.Errorf("pipeline %s: stage made no progress on %d items", st.Name(), len(ids))
		}
	}
	p.Reporter.Update(Progress{Stage: st.Name(), Done: int(totalOK), Total: totalQueue, Failed: int(totalFail), Phase: "done"})
	return nil
}
