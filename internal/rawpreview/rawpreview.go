// Package rawpreview finds the JPEG preview embedded in a RAW still
// (Fujifilm RAF, DNG) so thumbnails never need a RAW decode when the camera
// already stored a usable picture. It is pure Go, opens files read-only and
// never writes.
package rawpreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// ErrNoPreview reports a file without an embedded JPEG that is usable: none
// is present, the container is damaged, or every candidate was rejected.
var ErrNoPreview = errors.New("rawpreview: no embedded JPEG")

// MaxPreviewBytes is the largest embedded JPEG that is read.
const MaxPreviewBytes = 64 << 20

// Preview is an embedded JPEG. Orientation is 0 for RAF (the JPEG carries
// its own) and IFD0's Orientation tag for DNG (1 when absent). Width and
// Height are read from the JPEG's own header.
type Preview struct {
	JPEG                       []byte
	Orientation, Width, Height int
}

// usableThumbWidth is the width of the throwaway thumbnail Usable decodes.
const usableThumbWidth = 64

// Kind returns "raf" or "dng" for a path with that extension, compared
// case-insensitively, and "" for anything else.
func Kind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".raf":
		return "raf"
	case ".dng":
		return "dng"
	}
	return ""
}

// candidate is a byte range of the file that may hold a JPEG preview.
type candidate struct {
	off, n int64
	width  int64
}

// Embedded opens path read-only and offers its embedded JPEG candidates to
// try in descending width, returning the first for which try returns nil.
// A candidate is offered only when its bytes lie inside the file, are 4 to
// MaxPreviewBytes long, start with FF D8 and have a JPEG header that parses.
//
// A context error from try stops the search and is returned; any other
// error moves on to the next candidate. A nil try accepts the first
// candidate whose header parses. When no candidate is accepted the error is
// ErrNoPreview; I/O errors other than a short read are returned as they are.
func Embedded(path string, try func(Preview) error) (Preview, error) {
	kind := Kind(path)
	if kind == "" {
		return Preview{}, fmt.Errorf("%w: not a RAF or DNG file", ErrNoPreview)
	}
	// os.Open is O_RDONLY: originals are never opened for writing.
	f, err := os.Open(path) //nolint:gosec // path is an original chosen by the caller; it is only read
	if err != nil {
		return Preview{}, fmt.Errorf("rawpreview: open %s: %w", kind, err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Preview{}, fmt.Errorf("rawpreview: stat %s: %w", kind, err)
	}
	r := &reader{r: f, size: st.Size()}

	var (
		cands       []candidate
		orientation int
	)
	switch kind {
	case "raf":
		cands, err = rafCandidates(r)
	case "dng":
		cands, orientation, err = dngCandidates(r)
	}
	if err == nil {
		var p Preview
		if p, err = offer(r, cands, orientation, try); err == nil {
			return p, nil
		}
	}
	if isMalformed(err) {
		return Preview{}, fmt.Errorf("%w: %s: %v", ErrNoPreview, kind, err)
	}
	return Preview{}, fmt.Errorf("rawpreview: %s: %w", kind, err)
}

// Usable returns the standard try for Embedded: a real decode of the
// preview into a small thumbnail through stills. A header that parses
// proves nothing about the scan data behind it. Once ctx is done every
// failure is reported as ctx's error, so the search stops.
func Usable(ctx context.Context, stills platform.Stills) func(Preview) error {
	return func(p Preview) error {
		in := platform.ImageInput{Data: p.JPEG, Orientation: p.Orientation}
		if _, err := stills.Thumbnail(ctx, in, usableThumbWidth, platform.FitWidth); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("decode preview: %w", ctxErr)
			}
			return fmt.Errorf("decode preview: %w", err)
		}
		return nil
	}
}

// offer reads each candidate in order, validates it and hands it to try.
func offer(r *reader, cands []candidate, orientation int, try func(Preview) error) (Preview, error) {
	reason := "no candidate found"
	for _, c := range cands {
		if c.n < 4 || c.n > MaxPreviewBytes || c.off < 0 || c.off > r.size-c.n {
			reason = fmt.Sprintf("preview of %d bytes at offset %d is outside the file", c.n, c.off)
			continue
		}
		data, err := r.read(c.off, c.n)
		if err != nil {
			if isMalformed(err) {
				reason = err.Error()
				continue
			}
			return Preview{}, err
		}
		if data[0] != 0xFF || data[1] != 0xD8 {
			reason = fmt.Sprintf("data at offset %d is not a JPEG", c.off)
			continue
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			reason = fmt.Sprintf("JPEG header at offset %d does not parse: %v", c.off, err)
			continue
		}
		p := Preview{JPEG: data, Orientation: orientation, Width: cfg.Width, Height: cfg.Height}
		if try == nil {
			return p, nil
		}
		err = try(p)
		if err == nil {
			return p, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Preview{}, err
		}
		reason = fmt.Sprintf("%dx%d preview rejected: %v", p.Width, p.Height, err)
	}
	return Preview{}, malformed(reason)
}

// malformed is a problem with a file's structure or content, as opposed to
// an I/O error: the file has no usable preview. Embedded reports it as
// ErrNoPreview.
type malformed string

func (m malformed) Error() string { return string(m) }

func malformedf(format string, args ...any) error { return malformed(fmt.Sprintf(format, args...)) }

// isMalformed reports whether err is, or wraps, a malformed.
func isMalformed(err error) bool {
	var m malformed
	return errors.As(err, &m)
}

// errPastEnd reports a read of bytes that the file does not have.
const errPastEnd = malformed("data past the end of the file")

// reader reads byte ranges of a file of known size, refusing any range that
// does not lie inside it before allocating anything.
type reader struct {
	r    io.ReaderAt
	size int64
}

// read returns the n bytes at off. A range outside the file, or one cut
// short because the file shrank, is errPastEnd. Callers bound n.
func (r *reader) read(off, n int64) ([]byte, error) {
	if off < 0 || n < 0 || off > r.size || n > r.size-off {
		return nil, errPastEnd
	}
	buf := make([]byte, n)
	if _, err := r.r.ReadAt(buf, off); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errPastEnd
		}
		return nil, fmt.Errorf("read %d bytes at offset %d: %w", n, off, err)
	}
	return buf, nil
}
