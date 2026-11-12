package meta

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

// MovieHeader holds the creation and modification times of a QuickTime or
// MP4 movie header (moov → mvhd). A time is zero when the atom's value is 0.
type MovieHeader struct {
	Created, Modified time.Time
}

// errAtomNotFound reports that findAtom found no atom of the type.
var errAtomNotFound = errors.New("not found")

// macEpochOffset is the number of seconds from 1904-01-01 (the QuickTime
// epoch) to 1970-01-01.
const macEpochOffset = 2082844800

// maxAtoms is the most atoms findAtom examines at one level. Real movies
// have a handful at the top and a few dozen inside moov; a file with more
// is refused, so a hostile run of tiny atoms cannot keep a worker busy.
const maxAtoms = 1024

// ReadMovieHeader opens path read-only and reads moov → mvhd, version 0
// (32-bit times) or 1 (64-bit times), as seconds since 1904-01-01 UTC.
// Every atom's declared size is checked against its parent's end, the file
// size at the top, before it is used; only the atoms' headers and the first
// 20 bytes of mvhd are read, and at most maxAtoms atoms per level.
func ReadMovieHeader(path string) (MovieHeader, error) {
	return readMovieHeader(context.Background(), path)
}

// readMovieHeader is ReadMovieHeader that stops between atoms once ctx is
// done, returning an error that wraps ctx.Err().
func readMovieHeader(ctx context.Context, path string) (MovieHeader, error) {
	// os.Open is O_RDONLY: originals are never opened for writing.
	f, err := os.Open(path) //nolint:gosec // an original chosen by the caller; it is only read
	if err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: %w", err)
	}

	moovStart, moovEnd, err := findAtom(ctx, f, 0, st.Size(), "moov")
	if err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: moov: %w", err)
	}
	start, end, err := findAtom(ctx, f, moovStart, moovEnd, "mvhd")
	if err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: mvhd: %w", err)
	}

	var p [20]byte // version, flags, then the two times
	n := min(end-start, int64(len(p)))
	if n < 4 {
		return MovieHeader{}, errors.New("movie header: mvhd: truncated")
	}
	if _, err := f.ReadAt(p[:n], start); err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: mvhd: %w", err)
	}
	var created, modified uint64
	switch p[0] {
	case 0:
		if n < 12 {
			return MovieHeader{}, errors.New("movie header: mvhd: truncated")
		}
		created = uint64(binary.BigEndian.Uint32(p[4:8]))
		modified = uint64(binary.BigEndian.Uint32(p[8:12]))
	case 1:
		if n < 20 {
			return MovieHeader{}, errors.New("movie header: mvhd: truncated")
		}
		created = binary.BigEndian.Uint64(p[4:12])
		modified = binary.BigEndian.Uint64(p[12:20])
	default:
		return MovieHeader{}, fmt.Errorf("movie header: mvhd: unknown version %d", p[0])
	}

	var mh MovieHeader
	if mh.Created, err = macTime(created); err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: creation time: %w", err)
	}
	if mh.Modified, err = macTime(modified); err != nil {
		return MovieHeader{}, fmt.Errorf("movie header: modification time: %w", err)
	}
	return mh, nil
}

// findAtom walks the atoms in [start, end) and returns the payload range of
// the first one of type typ. A size of 1 means a 64-bit size follows the
// type; 0 means the atom runs to end. ctx is checked before each atom, and
// at most maxAtoms atoms are examined.
func findAtom(ctx context.Context, r io.ReaderAt, start, end int64, typ string) (payloadStart, payloadEnd int64, err error) {
	var hdr [16]byte
	for off, n := start, 0; end-off >= 8; n++ {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if n == maxAtoms {
			return 0, 0, fmt.Errorf("more than %d atoms before %s", maxAtoms, typ)
		}
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return 0, 0, fmt.Errorf("read atom header at %d: %w", off, err)
		}
		left := uint64(end - off) //nolint:gosec // the loop condition keeps end-off >= 8
		size := uint64(binary.BigEndian.Uint32(hdr[:4]))
		headerLen := uint64(8)
		switch size {
		case 0:
			size = left
		case 1:
			if end-off < 16 {
				return 0, 0, fmt.Errorf("atom at %d: truncated 64-bit size", off)
			}
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return 0, 0, fmt.Errorf("read atom size at %d: %w", off, err)
			}
			size = binary.BigEndian.Uint64(hdr[8:16])
			headerLen = 16
		}
		if size < headerLen {
			return 0, 0, fmt.Errorf("atom at %d: size %d is smaller than its header", off, size)
		}
		if size > left {
			return 0, 0, fmt.Errorf("atom at %d: size %d runs past the end (%d bytes left)", off, size, left)
		}
		if string(hdr[4:8]) == typ {
			return off + int64(headerLen), off + int64(size), nil //nolint:gosec // size <= left, so it fits
		}
		off += int64(size) //nolint:gosec // size <= left, so it fits
	}
	return 0, 0, errAtomNotFound
}

// macTime converts seconds since 1904-01-01 UTC to a time; 0 is the zero
// time.
func macTime(secs uint64) (time.Time, error) {
	if secs == 0 {
		return time.Time{}, nil
	}
	if secs > math.MaxInt64 {
		return time.Time{}, fmt.Errorf("%d seconds is out of range", secs)
	}
	return time.Unix(int64(secs)-macEpochOffset, 0).UTC(), nil
}
