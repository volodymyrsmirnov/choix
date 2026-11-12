package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
)

// Reader produces a file's metadata record: a JSON array holding one object
// keyed like exiftool's `-j -G -n` output. Parse reads it.
type Reader interface {
	Read(ctx context.Context, absPath string) ([]byte, error)
}

// NativeReader reads metadata through ImageIO (stills) and AVFoundation
// (videos) and maps it to exiftool's keys, so the record it writes and the
// one v1 wrote through exiftool parse to the same values — device_key
// above all.
type NativeReader struct {
	Stills platform.Stills
	Video  platform.Video
}

var _ Reader = (*NativeReader)(nil)

// videoExts are the extensions read through AVFoundation.
var videoExts = map[string]bool{".mov": true, ".mp4": true, ".m4v": true}

// Read returns the native record of the file at absPath:
//
//   - a video (.mov, .mp4, .m4v): Video.Properties and the movie header;
//   - a RAF: the properties of its embedded JPEG, which carries the full
//     EXIF and the pixel size v1 reported; the RAF itself when it has none;
//   - a DNG: the container's properties (its IFD0 and Exif IFD hold make,
//     model, serial, dates and GPS), with the width and height of the
//     embedded preview that decodes — the one the thumbnail stage uses —
//     when there is one;
//   - any other still: its properties.
func (n *NativeReader) Read(ctx context.Context, absPath string) ([]byte, error) {
	st, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	fi := FileInfo{Name: filepath.Base(absPath), ModTime: st.ModTime()}

	var rec map[string]any
	if videoExts[strings.ToLower(filepath.Ext(absPath))] {
		rec, err = n.readVideo(ctx, absPath, fi)
	} else {
		rec, err = n.readStill(ctx, absPath, fi)
	}
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal([]map[string]any{rec})
	if err != nil {
		return nil, fmt.Errorf("encode record: %w", err)
	}
	return out, nil
}

func (n *NativeReader) readVideo(ctx context.Context, absPath string, fi FileInfo) (map[string]any, error) {
	props, err := n.Video.Properties(ctx, absPath)
	if err != nil {
		return nil, fmt.Errorf("video properties: %w", err)
	}
	// A movie AVFoundation reads but whose header this parser cannot find
	// keeps its other metadata; its capture time falls back to the file's,
	// as exiftool's does when it finds no QuickTime dates. A cancellation is
	// not a missing header: it is returned so the row stays pending.
	mh, err := readMovieHeader(ctx, absPath)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("movie header: %w", ctxErr)
	}
	if err != nil {
		slog.Warn("metadata: movie header unreadable; using the file time", "path", absPath, "err", err)
		mh = MovieHeader{}
	}
	return FromAVAsset(props, mh, fi), nil
}

func (n *NativeReader) readStill(ctx context.Context, absPath string, fi FileInfo) (map[string]any, error) {
	switch rawpreview.Kind(absPath) {
	case "raf":
		in := platform.ImageInput{Path: absPath}
		p, err := rawpreview.Embedded(absPath, nil)
		switch {
		case err == nil:
			in = platform.ImageInput{Data: p.JPEG}
		case !errors.Is(err, rawpreview.ErrNoPreview):
			return nil, fmt.Errorf("embedded JPEG: %w", err)
		}
		props, err := n.Stills.Properties(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("image properties: %w", err)
		}
		return FromImageIO(props, fi), nil

	case "dng":
		props, err := n.Stills.Properties(ctx, platform.ImageInput{Path: absPath})
		if err != nil {
			return nil, fmt.Errorf("image properties: %w", err)
		}
		rec := FromImageIO(props, fi)
		// Spec §7 (C34): the reported size is the embedded JPEG's.
		p, err := rawpreview.Embedded(absPath, rawpreview.Usable(ctx, n.Stills))
		switch {
		case err == nil:
			rec["EXIF:ExifImageWidth"] = p.Width
			rec["EXIF:ExifImageHeight"] = p.Height
		case errors.Is(err, rawpreview.ErrNoPreview):
			// No usable preview: the container's size stays.
		default:
			return nil, fmt.Errorf("embedded JPEG: %w", err)
		}
		return rec, nil

	default:
		props, err := n.Stills.Properties(ctx, platform.ImageInput{Path: absPath})
		if err != nil {
			return nil, fmt.Errorf("image properties: %w", err)
		}
		return FromImageIO(props, fi), nil
	}
}
