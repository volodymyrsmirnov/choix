package platformtest

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// encodeJPEG makes a w×h gradient JPEG at quality 85.
func encodeJPEG(w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("encode jpeg: invalid size %dx%d", w, h)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: uint8((x + y) % 256), A: 255}) //nolint:gosec // every value is in 0..255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// MakeJPEG returns a synthetic w×h JPEG.
func MakeJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	data, err := encodeJPEG(w, h)
	if err != nil {
		t.Fatalf("MakeJPEG: %v", err)
	}
	return data
}

// writeFixture writes data to a new temporary directory and returns the
// file's path.
func writeFixture(t testing.TB, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// RAF layout used by MakeRAF.
const (
	rafMagic         = "FUJIFILMCCD-RAW "
	rafJPEGOffsetPos = 84
	rafJPEGLengthPos = 88
	rafJPEGOffset    = 148
)

// MakeRAF writes a synthetic RAF whose embedded JPEG is jpegData and returns
// its path: the 16-byte magic "FUJIFILMCCD-RAW ", the big-endian JPEG offset
// at byte 84 and length at byte 88, and the JPEG at offset 148.
func MakeRAF(t testing.TB, jpegData []byte) string {
	t.Helper()
	n, err := u32(len(jpegData))
	if err != nil {
		t.Fatalf("MakeRAF: %v", err)
	}
	data := make([]byte, rafJPEGOffset+len(jpegData))
	copy(data, rafMagic)
	binary.BigEndian.PutUint32(data[rafJPEGOffsetPos:], rafJPEGOffset)
	binary.BigEndian.PutUint32(data[rafJPEGLengthPos:], n)
	copy(data[rafJPEGOffset:], jpegData)
	return writeFixture(t, "TEST0001.RAF", data)
}

// DNGPreview is one JPEG preview of a synthetic DNG.
type DNGPreview struct {
	JPEG          []byte
	Width, Height int
	// DamageBody keeps the JPEG's SOI/SOF/SOS header and cuts the scan
	// data to 16 bytes.
	DamageBody bool
	// Chained makes the preview reachable only through IFD0's next-IFD
	// offset instead of through SubIFDs.
	Chained bool
}

// DNGOptions describes a synthetic DNG.
type DNGOptions struct {
	// Orientation is IFD0's Orientation tag; 0 omits the tag.
	Orientation int
	// Previews in order; the SubIFD ones follow the raw image, the chained
	// ones form IFD0's next-IFD chain.
	Previews []DNGPreview
	// RawStrip is the raw image's single strip.
	RawStrip []byte
}

// TIFF tags and field types used by MakeDNG.
const (
	tagNewSubFileType  = 0x00FE
	tagImageWidth      = 0x0100
	tagImageLength     = 0x0101
	tagCompression     = 0x0103
	tagPhotometric     = 0x0106
	tagMake            = 0x010F
	tagModel           = 0x0110
	tagStripOffsets    = 0x0111
	tagOrientation     = 0x0112
	tagStripByteCounts = 0x0117
	tagSubIFDs         = 0x014A
	tagDNGVersion      = 0xC612

	typeByte  = 1
	typeASCII = 2
	typeShort = 3
	typeLong  = 4

	compressionJPEG  = 7
	photometricYCbCr = 6
	photometricCFA   = 32803
)

// MakeDNG writes a synthetic little-endian DNG and returns its path. IFD0
// carries DNGVersion, Make "TESTCAM", Model "D1", Orientation,
// NewSubFileType 1 and SubIFDs; the first SubIFD is the raw image
// (NewSubFileType 0, Compression 7, PhotometricInterpretation 32803, strip
// RawStrip), the following ones the non-chained previews (NewSubFileType 1,
// Compression 7, PhotometricInterpretation 6, ImageWidth, ImageLength, one
// strip). Chained previews follow IFD0 through next-IFD offsets.
func MakeDNG(t testing.TB, o DNGOptions) string {
	t.Helper()
	data, err := buildDNG(o)
	if err != nil {
		t.Fatalf("MakeDNG: %v", err)
	}
	return writeFixture(t, "TEST0001.DNG", data)
}

func buildDNG(o DNGOptions) ([]byte, error) {
	w := &tiffWriter{buf: []byte{'I', 'I', 42, 0, 0, 0, 0, 0}}

	rawOff, rawLen, err := w.blob(o.RawStrip)
	if err != nil {
		return nil, err
	}
	type strip struct {
		p        DNGPreview
		off, len uint32
	}
	strips := make([]strip, 0, len(o.Previews))
	for i, p := range o.Previews {
		body := p.JPEG
		if p.DamageBody {
			if body, err = damageJPEG(body); err != nil {
				return nil, fmt.Errorf("preview %d: %w", i, err)
			}
		}
		off, n, err := w.blob(body)
		if err != nil {
			return nil, err
		}
		strips = append(strips, strip{p: p, off: off, len: n})
	}
	makeOff, _, err := w.blob([]byte("TESTCAM\x00"))
	if err != nil {
		return nil, err
	}

	previewIFD := func(s strip, next uint32) (uint32, error) {
		width, err := u32(s.p.Width)
		if err != nil {
			return 0, err
		}
		height, err := u32(s.p.Height)
		if err != nil {
			return 0, err
		}
		return w.ifd([]ifdEntry{
			longEntry(tagNewSubFileType, 1),
			longEntry(tagImageWidth, width),
			longEntry(tagImageLength, height),
			shortEntry(tagCompression, compressionJPEG),
			shortEntry(tagPhotometric, photometricYCbCr),
			longEntry(tagStripOffsets, s.off),
			longEntry(tagStripByteCounts, s.len),
		}, next)
	}

	rawIFD, err := w.ifd([]ifdEntry{
		longEntry(tagNewSubFileType, 0),
		shortEntry(tagCompression, compressionJPEG),
		shortEntry(tagPhotometric, photometricCFA),
		longEntry(tagStripOffsets, rawOff),
		longEntry(tagStripByteCounts, rawLen),
	}, 0)
	if err != nil {
		return nil, err
	}
	subIFDs := []uint32{rawIFD}
	var chained []strip
	for _, s := range strips {
		if s.p.Chained {
			chained = append(chained, s)
			continue
		}
		off, err := previewIFD(s, 0)
		if err != nil {
			return nil, err
		}
		subIFDs = append(subIFDs, off)
	}
	// Write the chain back to front so each IFD knows its successor.
	var firstChained uint32
	for _, s := range slices.Backward(chained) {
		if firstChained, err = previewIFD(s, firstChained); err != nil {
			return nil, err
		}
	}

	nSub, err := u32(len(subIFDs))
	if err != nil {
		return nil, err
	}
	subEntry := ifdEntry{tag: tagSubIFDs, typ: typeLong, count: nSub}
	if len(subIFDs) == 1 {
		binary.LittleEndian.PutUint32(subEntry.value[:], subIFDs[0])
	} else {
		arr := make([]byte, 0, 4*len(subIFDs))
		for _, off := range subIFDs {
			arr = binary.LittleEndian.AppendUint32(arr, off)
		}
		arrOff, _, err := w.blob(arr)
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint32(subEntry.value[:], arrOff)
	}

	entries := []ifdEntry{
		longEntry(tagNewSubFileType, 1),
		{tag: tagMake, typ: typeASCII, count: 8, value: le32(makeOff)},
		{tag: tagModel, typ: typeASCII, count: 3, value: [4]byte{'D', '1', 0, 0}},
		subEntry,
		{tag: tagDNGVersion, typ: typeByte, count: 4, value: [4]byte{1, 4, 0, 0}},
	}
	if o.Orientation != 0 {
		if o.Orientation < 0 || o.Orientation > math.MaxUint16 {
			return nil, fmt.Errorf("orientation %d out of range", o.Orientation)
		}
		entries = append(entries, shortEntry(tagOrientation, uint16(o.Orientation)))
	}
	ifd0, err := w.ifd(entries, firstChained)
	if err != nil {
		return nil, err
	}
	binary.LittleEndian.PutUint32(w.buf[4:], ifd0)
	return w.buf, nil
}

// damageJPEG keeps a JPEG's header up to the end of its SOS segment and only
// the first 16 bytes of the scan data.
func damageJPEG(data []byte) ([]byte, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errors.New("damage jpeg: no SOI marker")
	}
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return nil, fmt.Errorf("damage jpeg: no marker at offset %d", pos)
		}
		marker := data[pos+1]
		end := pos + 2 + int(binary.BigEndian.Uint16(data[pos+2:]))
		if end > len(data) {
			return nil, fmt.Errorf("damage jpeg: segment at offset %d runs past the end", pos)
		}
		if marker == 0xDA {
			if end+16 > len(data) {
				return nil, errors.New("damage jpeg: scan data shorter than 16 bytes")
			}
			return slices.Clone(data[:end+16]), nil
		}
		pos = end
	}
	return nil, errors.New("damage jpeg: no SOS marker")
}

// u32 converts a length or size to a TIFF/RAF 32-bit field.
func u32(n int) (uint32, error) {
	if n < 0 || n > math.MaxUint32 {
		return 0, fmt.Errorf("value %d does not fit in 32 bits", n)
	}
	return uint32(n), nil
}

// ifdEntry is one 12-byte TIFF IFD entry; value holds the inline value or
// the offset of the data.
type ifdEntry struct {
	tag, typ uint16
	count    uint32
	value    [4]byte
}

func le32(v uint32) [4]byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b
}

func longEntry(tag uint16, v uint32) ifdEntry {
	return ifdEntry{tag: tag, typ: typeLong, count: 1, value: le32(v)}
}

func shortEntry(tag, v uint16) ifdEntry {
	e := ifdEntry{tag: tag, typ: typeShort, count: 1}
	binary.LittleEndian.PutUint16(e.value[:], v)
	return e
}

// tiffWriter appends word-aligned blobs and IFDs to a little-endian TIFF.
type tiffWriter struct{ buf []byte }

func (w *tiffWriter) align() {
	if len(w.buf)%2 == 1 {
		w.buf = append(w.buf, 0)
	}
}

// blob appends p and returns its offset and length.
func (w *tiffWriter) blob(p []byte) (off, n uint32, err error) {
	w.align()
	if off, err = u32(len(w.buf)); err != nil {
		return 0, 0, err
	}
	if n, err = u32(len(p)); err != nil {
		return 0, 0, err
	}
	w.buf = append(w.buf, p...)
	return off, n, nil
}

// ifd appends an IFD with entries sorted by tag and returns its offset.
func (w *tiffWriter) ifd(entries []ifdEntry, next uint32) (uint32, error) {
	w.align()
	off, err := u32(len(w.buf))
	if err != nil {
		return 0, err
	}
	count := len(entries)
	if count > math.MaxUint16 {
		return 0, fmt.Errorf("%d IFD entries", count)
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b ifdEntry) int { return int(a.tag) - int(b.tag) })
	le := binary.LittleEndian
	w.buf = le.AppendUint16(w.buf, uint16(count))
	for _, e := range sorted {
		w.buf = le.AppendUint16(w.buf, e.tag)
		w.buf = le.AppendUint16(w.buf, e.typ)
		w.buf = le.AppendUint32(w.buf, e.count)
		w.buf = append(w.buf, e.value[:]...)
	}
	w.buf = le.AppendUint32(w.buf, next)
	return off, nil
}
