package thumb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
)

// Photo returns a JPEG of the still at srcAbs for the tier whose number is
// limit, sized by v1's rule for the source's kind:
//
//   - A RAW still (RAF, DNG) is made from its embedded JPEG, which is at
//     most limit pixels wide. Every embedded candidate is tried, widest
//     first; DNG previews get IFD0's orientation.
//   - Only when no embedded JPEG decodes is the RAW itself decoded, as the
//     last resort, with one "raw decode fallback" warning; its long side is
//     at most limit.
//   - Any other still (JPEG, HEIC, TIFF, PNG) has its long side at most
//     limit.
//
// The source is only read. Once ctx is done the result is ctx.Err().
func Photo(ctx context.Context, stills platform.Stills, srcAbs string, limit int) (platform.Image, error) {
	if stills == nil {
		return platform.Image{}, errors.New("thumbnail: no image decoder")
	}
	if rawpreview.Kind(srcAbs) == "" {
		return thumbnailByPath(ctx, stills, srcAbs, limit)
	}

	var img platform.Image
	_, err := rawpreview.Embedded(srcAbs, func(p rawpreview.Preview) error {
		in := platform.ImageInput{Data: p.JPEG, Orientation: p.Orientation}
		out, err := stills.Thumbnail(ctx, in, limit, platform.FitWidth)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr // stops the search
			}
			return err
		}
		img = out
		return nil
	})
	switch {
	case err == nil:
		return img, nil
	case ctx.Err() != nil:
		return platform.Image{}, ctx.Err()
	case !errors.Is(err, rawpreview.ErrNoPreview):
		return platform.Image{}, fmt.Errorf("embedded preview: %w", err)
	}
	slog.Warn("raw decode fallback", "file", srcAbs, "limit", limit, "reason", err)
	return thumbnailByPath(ctx, stills, srcAbs, limit)
}

// thumbnailByPath decodes srcAbs itself, its long side at most limit.
func thumbnailByPath(ctx context.Context, stills platform.Stills, srcAbs string, limit int) (platform.Image, error) {
	img, err := stills.Thumbnail(ctx, platform.ImageInput{Path: srcAbs}, limit, platform.FitLongSide)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return platform.Image{}, ctxErr
		}
		return platform.Image{}, fmt.Errorf("decode: %w", err)
	}
	return img, nil
}
