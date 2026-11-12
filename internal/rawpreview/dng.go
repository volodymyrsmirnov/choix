package rawpreview

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"slices"
)

// Limits on the TIFF structure walked in a DNG.
const (
	maxIFDs       = 64
	maxIFDEntries = 4096
)

// TIFF tags read from a DNG.
const (
	tagNewSubFileType  = 0x00FE
	tagImageWidth      = 0x0100
	tagCompression     = 0x0103
	tagPhotometric     = 0x0106
	tagStripOffsets    = 0x0111
	tagOrientation     = 0x0112
	tagStripByteCounts = 0x0117
	tagSubIFDs         = 0x014A
	tagJPEGIF          = 0x0201
	tagJPEGIFLength    = 0x0202
)

// TIFF field types read from a DNG.
const (
	typeShort = 3
	typeLong  = 4
	typeIFD   = 13
)

// tiffEntry is one IFD entry: its type, count and raw 4-byte value field.
type tiffEntry struct {
	typ   uint16
	count uint32
	value [4]byte
}

// tiffIFD is one parsed IFD.
type tiffIFD struct {
	entries map[uint16]tiffEntry
	next    int64
}

// tiffFile reads IFDs of a TIFF in one byte order.
type tiffFile struct {
	r  *reader
	bo binary.ByteOrder
}

// dngCandidates walks a DNG's IFDs — IFD0, every IFD chained from it
// through the next-IFD offset, and each IFD listed in the SubIFDs tag of any
// of those; at most maxIFDs, never parsing the same offset twice — and returns the
// JPEG preview candidates in descending width (ties by byte count) and
// IFD0's orientation.
func dngCandidates(r *reader) ([]candidate, int, error) {
	h, err := r.read(0, 8)
	if err != nil {
		return nil, 0, fmt.Errorf("header: %w", err)
	}
	t := tiffFile{r: r}
	switch string(h[:4]) {
	case "II*\x00":
		t.bo = binary.LittleEndian
	case "MM\x00*":
		t.bo = binary.BigEndian
	default:
		return nil, 0, malformed("not a TIFF header")
	}

	type item struct {
		off  int64
		main bool // IFD0 or chained from it: its next-IFD and SubIFDs are followed
	}
	ifd0 := int64(t.bo.Uint32(h[4:]))
	queue := []item{{off: ifd0, main: true}}
	// parsed holds every IFD read so far (nil when it could not be read):
	// each offset is parsed once and at most maxIFDs are. expanded holds the
	// main-chain IFDs whose next-IFD and SubIFDs were queued, so an IFD
	// reached first as a SubIFD and later on the main chain is still
	// expanded, and a cycle in the chain ends.
	parsed := make(map[int64]*tiffIFD, maxIFDs)
	expanded := make(map[int64]bool, maxIFDs)
	orientation := 1
	var cands []candidate
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.off == 0 || (it.main && expanded[it.off]) {
			continue
		}
		d, seen := parsed[it.off]
		if !seen {
			if len(parsed) >= maxIFDs {
				continue
			}
			ifd, err := t.readIFD(it.off)
			switch {
			case err == nil:
				d = &ifd
			case !isMalformed(err) || it.off == ifd0:
				return nil, 0, err // an I/O error, or nothing is reachable without IFD0
			}
			// A damaged IFD (d == nil) hides only what it describes.
			parsed[it.off] = d
			if d != nil {
				if it.off == ifd0 {
					orientation = d.orientation(t.bo)
				}
				cands = append(cands, d.candidates(t.bo)...)
			}
		}
		if !it.main || d == nil {
			continue
		}
		expanded[it.off] = true
		subs, err := t.subIFDs(*d)
		if err != nil {
			return nil, 0, err
		}
		for _, s := range subs {
			queue = append(queue, item{off: s})
		}
		queue = append(queue, item{off: d.next, main: true})
	}
	slices.SortStableFunc(cands, func(a, b candidate) int {
		if c := cmp.Compare(b.width, a.width); c != 0 {
			return c
		}
		return cmp.Compare(b.n, a.n)
	})
	return cands, orientation, nil
}

// readIFD parses the IFD at off. An IFD that does not lie inside the file
// or has more than maxIFDEntries entries is malformed.
func (t tiffFile) readIFD(off int64) (tiffIFD, error) {
	head, err := t.r.read(off, 2)
	if err != nil {
		return tiffIFD{}, fmt.Errorf("IFD at offset %d: %w", off, err)
	}
	n := int64(t.bo.Uint16(head))
	if n > maxIFDEntries {
		return tiffIFD{}, malformedf("IFD at offset %d has %d entries", off, n)
	}
	body, err := t.r.read(off+2, 12*n+4)
	if err != nil {
		return tiffIFD{}, fmt.Errorf("IFD at offset %d: %w", off, err)
	}
	d := tiffIFD{entries: make(map[uint16]tiffEntry, n), next: int64(t.bo.Uint32(body[12*n:]))}
	for i := range n {
		e := body[12*i : 12*i+12]
		var v [4]byte
		copy(v[:], e[8:12])
		d.entries[t.bo.Uint16(e)] = tiffEntry{typ: t.bo.Uint16(e[2:]), count: t.bo.Uint32(e[4:]), value: v}
	}
	return d, nil
}

// subIFDs returns the offsets in d's SubIFDs tag, at most maxIFDs of them.
// A tag of the wrong type or whose array does not lie inside the file is
// ignored.
func (t tiffFile) subIFDs(d tiffIFD) ([]int64, error) {
	e, ok := d.entries[tagSubIFDs]
	if !ok || (e.typ != typeLong && e.typ != typeIFD) || e.count == 0 {
		return nil, nil
	}
	if e.count == 1 {
		return []int64{int64(t.bo.Uint32(e.value[:]))}, nil
	}
	arrOff := int64(t.bo.Uint32(e.value[:]))
	if arrOff > t.r.size || 4*int64(e.count) > t.r.size-arrOff {
		return nil, nil
	}
	arr, err := t.r.read(arrOff, 4*min(int64(e.count), maxIFDs))
	if err != nil {
		if isMalformed(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("SubIFDs: %w", err)
	}
	offs := make([]int64, 0, len(arr)/4)
	for i := 0; i < len(arr); i += 4 {
		offs = append(offs, int64(t.bo.Uint32(arr[i:])))
	}
	return offs, nil
}

// scalar returns a SHORT or LONG entry with count 1.
func (d tiffIFD) scalar(bo binary.ByteOrder, tag uint16) (int64, bool) {
	e, ok := d.entries[tag]
	if !ok || e.count != 1 {
		return 0, false
	}
	switch e.typ {
	case typeShort:
		return int64(bo.Uint16(e.value[:])), true
	case typeLong:
		return int64(bo.Uint32(e.value[:])), true
	}
	return 0, false
}

// orientation returns d's Orientation tag, 1 when absent or not 1–8.
func (d tiffIFD) orientation(bo binary.ByteOrder) int {
	if v, ok := d.scalar(bo, tagOrientation); ok && v >= 1 && v <= 8 {
		return int(v)
	}
	return 1
}

// candidates returns d's JPEG preview ranges when d describes a preview: a
// reduced-resolution image (NewSubFileType bit 0), JPEG-compressed (7, or
// old-style 6), YCbCr or RGB — never CFA or LinearRaw raw data. Its
// JPEGInterchangeFormat range and its single strip are both returned when
// both are present and differ, so a damaged one does not hide the other.
func (d tiffIFD) candidates(bo binary.ByteOrder) []candidate {
	if v, ok := d.scalar(bo, tagNewSubFileType); !ok || v&1 == 0 {
		return nil
	}
	if v, ok := d.scalar(bo, tagCompression); !ok || (v != 7 && v != 6) {
		return nil
	}
	if v, ok := d.scalar(bo, tagPhotometric); !ok || (v != 6 && v != 2) {
		return nil
	}
	width, _ := d.scalar(bo, tagImageWidth)
	var out []candidate
	for _, tags := range [][2]uint16{{tagJPEGIF, tagJPEGIFLength}, {tagStripOffsets, tagStripByteCounts}} {
		off, okOff := d.scalar(bo, tags[0])
		n, okN := d.scalar(bo, tags[1])
		if !okOff || !okN {
			continue
		}
		c := candidate{off: off, n: n, width: width}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}
