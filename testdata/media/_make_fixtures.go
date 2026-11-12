//go:build ignore

// _make_fixtures.go writes the synthetic JPEG fixtures in testdata/media.
// Run from the repository root:
//
//	go run testdata/media/_make_fixtures.go
//
// It writes:
//
//   - orient6.jpg: 64×32 stored pixels, left half red and right half blue,
//     with EXIF Orientation 6 (display it turned 90° clockwise: 32×64, top
//     half red).
//   - nan-exif.jpg: a 64×32 JPEG whose EXIF ExposureTime rational is 1/0;
//     its FNumber (28/10) and ISOSpeedRatings (200) are ordinary.
//
// With -heic-source PATH it also writes, to PATH, the 800×600 gradient JPEG
// that platformtest.MakeJPEG(t, 800, 600) produces, from which sample.heic
// is made (see README.md).
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
)

// TIFF tags and field types.
const (
	tagMake            = 0x010F
	tagOrientation     = 0x0112
	tagExifIFD         = 0x8769
	tagExposureTime    = 0x829A
	tagFNumber         = 0x829D
	tagISOSpeedRatings = 0x8827

	typeASCII    = 2
	typeShort    = 3
	typeLong     = 4
	typeRational = 5
)

func main() {
	dir := flag.String("dir", "testdata/media", "output directory")
	heicSource := flag.String("heic-source", "", "also write the 800×600 gradient JPEG for sample.heic here")
	flag.Parse()

	write(filepath.Join(*dir, "orient6.jpg"), withEXIF(halves(64, 32), orientationEXIF(6)))
	write(filepath.Join(*dir, "nan-exif.jpg"), withEXIF(halves(64, 32), nanEXIF()))
	if *heicSource != "" {
		write(*heicSource, gradient(800, 600))
	}
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // fixtures are public
		log.Fatal(err)
	}
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

// halves is a w×h JPEG whose left half is red and right half blue.
func halves(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 255, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return encode(img)
}

// gradient is platformtest's encodeJPEG: the same pixels, quality 85.
func gradient(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: uint8((x + y) % 256), A: 255})
		}
	}
	return encode(img)
}

// withEXIF inserts an APP1 Exif segment holding tiff right after the SOI.
func withEXIF(jpg, tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, 0xFF, 0xE1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

type entry struct {
	tag, typ uint16
	count    uint32
	value    []byte // inline when ≤ 4 bytes, otherwise stored after the IFD
}

// tiffBuilder lays out little-endian IFDs one after another; each IFD's
// out-of-line values follow it.
type tiffBuilder struct{ buf []byte }

func newTIFF() *tiffBuilder {
	return &tiffBuilder{buf: []byte{'I', 'I', 42, 0, 8, 0, 0, 0}}
}

// ifd appends an IFD with entries (sorted by tag) and returns its offset.
func (b *tiffBuilder) ifd(entries []entry) uint32 {
	off := uint32(len(b.buf))
	valueOff := off + 2 + 12*uint32(len(entries)) + 4
	le := binary.LittleEndian
	b.buf = le.AppendUint16(b.buf, uint16(len(entries)))
	var values []byte
	for _, e := range entries {
		b.buf = le.AppendUint16(b.buf, e.tag)
		b.buf = le.AppendUint16(b.buf, e.typ)
		b.buf = le.AppendUint32(b.buf, e.count)
		if len(e.value) <= 4 {
			v := make([]byte, 4)
			copy(v, e.value)
			b.buf = append(b.buf, v...)
			continue
		}
		b.buf = le.AppendUint32(b.buf, valueOff+uint32(len(values)))
		values = append(values, e.value...)
		if len(values)%2 == 1 {
			values = append(values, 0)
		}
	}
	b.buf = le.AppendUint32(b.buf, 0) // no next IFD
	b.buf = append(b.buf, values...)
	return off
}

func short(tag, v uint16) entry {
	return entry{tag: tag, typ: typeShort, count: 1, value: binary.LittleEndian.AppendUint16(nil, v)}
}

func long(tag uint16, v uint32) entry {
	return entry{tag: tag, typ: typeLong, count: 1, value: binary.LittleEndian.AppendUint32(nil, v)}
}

func rational(tag uint16, num, den uint32) entry {
	v := binary.LittleEndian.AppendUint32(nil, num)
	return entry{tag: tag, typ: typeRational, count: 1, value: binary.LittleEndian.AppendUint32(v, den)}
}

func ascii(tag uint16, s string) entry {
	v := append([]byte(s), 0)
	return entry{tag: tag, typ: typeASCII, count: uint32(len(v)), value: v}
}

func orientationEXIF(o uint16) []byte {
	b := newTIFF()
	b.ifd([]entry{short(tagOrientation, o)})
	return b.buf
}

// nanEXIF has IFD0 (Make, ExifIFD pointer) followed by the Exif IFD.
func nanEXIF() []byte {
	b := newTIFF()
	exifEntries := []entry{
		rational(tagExposureTime, 1, 0),
		rational(tagFNumber, 28, 10),
		short(tagISOSpeedRatings, 200),
	}
	// IFD0 is 2 entries plus "TESTCAM\0" (8 bytes) out of line.
	ifd0Size := uint32(2 + 12*2 + 4 + 8)
	b.ifd([]entry{ascii(tagMake, "TESTCAM"), long(tagExifIFD, 8+ifd0Size)})
	if got := b.ifd(exifEntries); got != 8+ifd0Size {
		log.Fatalf("exif IFD at %d, want %d", got, 8+ifd0Size)
	}
	return b.buf
}
