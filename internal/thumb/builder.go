package thumb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Builder produces and caches thumbnails for files tracked in the store,
// through the platform layer: native calls return bytes, and every cache
// file is written through safefs.
type Builder struct {
	ScanRoot string
	Store    *store.Store
	Stills   platform.Stills
	Video    platform.Video

	// FailureGuard, when set, is consulted right before a failure would be
	// recorded. When it returns false (the folder is unreachable) nothing is
	// written — the row keeps its status — and the error wraps
	// pipeline.ErrFolderUnavailable. nil records every failure.
	FailureGuard func() bool
}

// tierLimits are the cached tiers the pipeline builds, with their numbers.
var tierLimits = []struct {
	tier  string
	limit int
}{
	{TierThumb, WidthThumb},
	{TierPreview, WidthPreview},
}

// built is what one file's native calls produced, before anything is written.
type built struct {
	tiers     map[string]platform.Image
	keyframes []platform.Image
}

// Build makes the file's thumbnails, writes the cache files and the thumbs
// rows, and sets scan_status to 'thumbed'. On a failure it sets
// scan_status='failed' with the error text and returns the error, unless
// FailureGuard reports the folder unreachable (see FailureGuard). A
// cancelled context returns ctx.Err() and leaves the row's status unchanged:
// the context is checked before every write and database call, so nothing is
// written once it is done (see write for what an earlier step may leave).
func (b *Builder) Build(ctx context.Context, fileID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	row, err := b.Store.Files().GetByID(fileID)
	if err != nil {
		return fmt.Errorf("load file %d: %w", fileID, err)
	}

	buildErr := b.build(ctx, row)
	if buildErr != nil {
		// Cancellation is not the file's failure: leave its status alone so
		// the next run resumes it.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(buildErr, context.Canceled) || errors.Is(buildErr, context.DeadlineExceeded) {
			return buildErr
		}
		// Nor is a failure while the folder is unreachable.
		if b.FailureGuard != nil && !b.FailureGuard() {
			return fmt.Errorf("%w: %w", pipeline.ErrFolderUnavailable, buildErr)
		}
		_ = b.Store.Files().SetScanStatus(fileID, "failed", buildErr.Error())
		return buildErr
	}
	if err := ctx.Err(); err != nil {
		return err // never advance the status of a cancelled build
	}
	if err := b.Store.Files().SetScanStatus(fileID, "thumbed", ""); err != nil {
		return fmt.Errorf("set status thumbed: %w", err)
	}
	return nil
}

// build makes every image of the file first and writes them only once all
// native calls have succeeded and the context is still live.
func (b *Builder) build(ctx context.Context, row store.File) error {
	srcAbs := filepath.Join(b.ScanRoot, filepath.FromSlash(row.Path))
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	var (
		out built
		err error
	)
	switch row.Kind {
	case "photo":
		out, err = b.photo(ctx, srcAbs)
	case "video":
		out, err = b.video(ctx, srcAbs)
	default:
		return fmt.Errorf("unsupported kind %q for file %d", row.Kind, row.ID)
	}
	if err != nil {
		return err
	}
	return b.write(ctx, row.ID, out)
}

// photo makes both tiers of a still with Photo.
func (b *Builder) photo(ctx context.Context, srcAbs string) (built, error) {
	out := built{tiers: make(map[string]platform.Image, len(tierLimits))}
	for _, t := range tierLimits {
		img, err := Photo(ctx, b.Stills, srcAbs, t.limit)
		if err != nil {
			return built{}, fmt.Errorf("%s: %w", t.tier, err)
		}
		out.tiers[t.tier] = img
	}
	return out, nil
}

// video takes the keyframes at KeyframeFractions and makes both tiers from
// the first one, each at most its number of pixels wide.
func (b *Builder) video(ctx context.Context, srcAbs string) (built, error) {
	if b.Video == nil || b.Stills == nil {
		return built{}, errors.New("video thumbnail: no video or image decoder")
	}
	frames, err := b.Video.Frames(ctx, srcAbs, KeyframeFractions)
	if err != nil {
		return built{}, fmt.Errorf("keyframes: %w", err)
	}
	if len(frames) == 0 {
		return built{}, errors.New("keyframes: none produced")
	}
	out := built{tiers: make(map[string]platform.Image, len(tierLimits)), keyframes: frames}
	for _, t := range tierLimits {
		img, err := b.Stills.Thumbnail(ctx, platform.ImageInput{Data: frames[0].JPEG}, t.limit, platform.FitWidth)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return built{}, ctxErr
			}
			return built{}, fmt.Errorf("%s: %w", t.tier, err)
		}
		out.tiers[t.tier] = img
	}
	return out, nil
}

// persistHook, when set by a test, runs after each persistence step of
// write, named "keyframe <n>", "<tier> cache" or "<tier> row".
var persistHook func(step string)

func afterPersist(step string) {
	if persistHook != nil {
		persistHook(step)
	}
}

// write stores the keyframes, then each tier's file and row, checking ctx
// before every step. Neither the cache writes nor the store calls take a
// context, so a step already started (one file write, or one short SQLite
// statement) completes; the next one is not started. A cache file written
// before the cancellation may stay: it is a cache that the next build
// replaces, and without its row and the 'thumbed' status nothing uses it.
func (b *Builder) write(ctx context.Context, fileID int64, out built) error {
	for i, f := range out.keyframes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := WriteKeyframe(b.ScanRoot, fileID, i+1, f.JPEG); err != nil {
			return fmt.Errorf("keyframe %d: %w", i+1, err)
		}
		afterPersist(fmt.Sprintf("keyframe %d", i+1))
	}
	for _, t := range tierLimits {
		img := out.tiers[t.tier]
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := WriteCache(b.ScanRoot, fileID, t.tier, img.JPEG)
		if err != nil {
			return fmt.Errorf("%s: %w", t.tier, err)
		}
		afterPersist(t.tier + " cache")
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.Store.Thumbs().Upsert(store.Thumb{
			FileID:  fileID,
			Tier:    t.tier,
			RelPath: rel,
			Width:   int64(img.Width),
			Height:  int64(img.Height),
		}); err != nil {
			return fmt.Errorf("%s: save row: %w", t.tier, err)
		}
		afterPersist(t.tier + " row")
	}
	return nil
}

// BuildByStatus drains up to limit files whose scan_status equals status,
// calling Build on each. Per-file errors are absorbed (each failed file is
// already marked 'failed' by Build) so one bad row does not stop the batch.
// The returned count is the number of files attempted.
func (b *Builder) BuildByStatus(ctx context.Context, status string, limit int) (int, error) {
	ids, err := b.Store.Files().IDsByStatus(status, limit)
	if err != nil {
		return 0, fmt.Errorf("query files by status %q: %w", status, err)
	}
	processed := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		_ = b.Build(ctx, id) // per-file error already persisted as 'failed'
		processed++
	}
	return processed, nil
}
