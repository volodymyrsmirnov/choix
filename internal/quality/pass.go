package quality

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Scorer rates photos. See local.QualityScorer for the contract of Score:
// nil means the photo was dealt with (rated, unratable or superseded); an
// error leaves it pending.
type Scorer interface {
	CountPending() (int, error)
	ListPending(afterID int64, limit int) ([]store.QualityItem, error)
	Score(ctx context.Context, item store.QualityItem) error
}

// maxConsecutiveFailures ends a scoring pass after this many photos in a
// row failed for reasons other than the photo itself.
const maxConsecutiveFailures = 3

// listBatch is how many pending photos a pass lists at a time.
const listBatch = 50

// Pass is one sweep over the photos that have no rating yet, in id order.
// Scorer is required; the other fields are optional.
type Pass struct {
	Scorer Scorer
	// IsUnavailable reports whether a Score error means scoring cannot
	// work at all in this process (as opposed to one failed photo). Such an
	// error ends the pass at once. nil → never.
	IsUnavailable func(error) bool
	// OnProgress is called after every photo with the photos dealt with so
	// far and the number pending when the pass started.
	OnProgress func(done, total int)
}

// Run rates every pending photo and returns how many were dealt with. ctx
// is checked before every photo; on cancellation Run returns ctx's error.
// A photo whose Score fails stays pending for a later pass and the pass
// goes on, unless the error is unavailable (IsUnavailable) or it is the
// third such failure in a row: then Run stops and returns that error.
func (p *Pass) Run(ctx context.Context) (rated int, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	total, err := p.Scorer.CountPending()
	if err != nil {
		return 0, fmt.Errorf("quality: count pending photos: %w", err)
	}
	if total == 0 {
		return 0, nil
	}
	slog.Info("quality rating pass starting", "photos", total)
	start := time.Now()

	var cursor int64
	failed, streak := 0, 0
	for {
		items, err := p.Scorer.ListPending(cursor, listBatch)
		if err != nil {
			return rated, fmt.Errorf("quality: list pending photos: %w", err)
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				slog.Info("quality rating pass cancelled", "rated", rated)
				return rated, err
			}
			err := p.Scorer.Score(ctx, item)
			cursor = item.FileID
			switch {
			case err == nil:
				rated, streak = rated+1, 0
			case ctx.Err() != nil:
				slog.Info("quality rating pass cancelled", "rated", rated)
				return rated, ctx.Err()
			case p.IsUnavailable != nil && p.IsUnavailable(err):
				slog.Warn("quality rating cannot run", "err", err)
				return rated, fmt.Errorf("quality: rate file %d: %w", item.FileID, err)
			default:
				// Not the photo's fault (inference or database error): it
				// stays pending for a later pass.
				failed, streak = failed+1, streak+1
				slog.Warn("quality: rating failed", "file_id", item.FileID, "err", err)
				if streak >= maxConsecutiveFailures {
					slog.Warn("quality rating pass stopped after repeated failures", "rated", rated, "failed", failed)
					return rated, fmt.Errorf("quality: rate file %d: %w", item.FileID, err)
				}
			}
			if p.OnProgress != nil {
				p.OnProgress(min(rated+failed, total), total)
			}
		}
	}
	slog.Info("quality rating pass done", "rated", rated, "failed", failed, "elapsed_ms", time.Since(start).Milliseconds())
	return rated, nil
}
