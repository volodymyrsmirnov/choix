package platformtest_test

import (
	"bytes"
	"encoding/binary"
	"image/jpeg"
	"os"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

func TestMakeJPEG_Decodes(t *testing.T) {
	data := platformtest.MakeJPEG(t, 40, 30)
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 30 {
		t.Fatalf("size = %dx%d; want 40x30", b.Dx(), b.Dy())
	}
}

func TestMakeRAF_HeaderOffsets(t *testing.T) {
	preview := platformtest.MakeJPEG(t, 32, 24)
	path := platformtest.MakeRAF(t, preview)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read RAF: %v", err)
	}
	if got := string(data[:16]); got != "FUJIFILMCCD-RAW " {
		t.Fatalf("magic = %q", got)
	}
	off := binary.BigEndian.Uint32(data[84:88])
	n := binary.BigEndian.Uint32(data[88:92])
	if off != 148 {
		t.Fatalf("JPEG offset = %d; want 148", off)
	}
	if int(n) != len(preview) {
		t.Fatalf("JPEG length = %d; want %d", n, len(preview))
	}
	if !bytes.Equal(data[off:off+n], preview) {
		t.Fatal("JPEG at the recorded offset differs from the input")
	}
}

// tiffIFD is one parsed IFD: tag → raw 4-byte value field (or offset), plus
// the type and count needed to read it.
type tiffIFD struct {
	entries map[uint16]tiffEntry
	next    uint32
}

type tiffEntry struct {
	typ   uint16
	count uint32
	value []byte // the entry's 4-byte value/offset field
}

func readIFD(t *testing.T, data []byte, off uint32) tiffIFD {
	t.Helper()
	le := binary.LittleEndian
	if int(off)+2 > len(data) {
		t.Fatalf("IFD offset %d past end (%d bytes)", off, len(data))
	}
	n := uint32(le.Uint16(data[off:]))
	ifd := tiffIFD{entries: map[uint16]tiffEntry{}}
	for i := range n {
		e := data[off+2+12*i : off+2+12*i+12]
		ifd.entries[le.Uint16(e)] = tiffEntry{typ: le.Uint16(e[2:]), count: le.Uint32(e[4:]), value: e[8:12]}
	}
	ifd.next = le.Uint32(data[off+2+12*n:])
	return ifd
}

// uint reads a SHORT or LONG entry with count 1.
func (d tiffIFD) uint(t *testing.T, tag uint16) uint32 {
	t.Helper()
	e, ok := d.entries[tag]
	if !ok {
		t.Fatalf("tag 0x%04X missing", tag)
	}
	if e.count != 1 {
		t.Fatalf("tag 0x%04X count = %d; want 1", tag, e.count)
	}
	switch e.typ {
	case 3:
		return uint32(binary.LittleEndian.Uint16(e.value))
	case 4:
		return binary.LittleEndian.Uint32(e.value)
	}
	t.Fatalf("tag 0x%04X type = %d; want SHORT or LONG", tag, e.typ)
	return 0
}

// strip returns the bytes of an IFD's single strip.
func (d tiffIFD) strip(t *testing.T, data []byte) []byte {
	t.Helper()
	off, n := d.uint(t, 0x0111), d.uint(t, 0x0117)
	if int(off)+int(n) > len(data) {
		t.Fatalf("strip %d+%d past end (%d bytes)", off, n, len(data))
	}
	return data[off : off+n]
}

func TestMakeDNG_Layout(t *testing.T) {
	raw := []byte("synthetic raw sensor data, not a real image")
	small := platformtest.MakeJPEG(t, 32, 24)
	large := platformtest.MakeJPEG(t, 64, 48)
	chained := platformtest.MakeJPEG(t, 16, 12)
	damaged := platformtest.MakeJPEG(t, 48, 36)
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		Orientation: 6,
		Previews: []platformtest.DNGPreview{
			{JPEG: small, Width: 32, Height: 24},
			{JPEG: large, Width: 64, Height: 48},
			{JPEG: chained, Width: 16, Height: 12, Chained: true},
			{JPEG: damaged, Width: 48, Height: 36, DamageBody: true},
		},
		RawStrip: raw,
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read DNG: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("II*\x00")) {
		t.Fatalf("header = %q; want II*\\x00", data[:4])
	}
	le := binary.LittleEndian
	ifd0 := readIFD(t, data, le.Uint32(data[4:]))

	if got := ifd0.uint(t, 0x0112); got != 6 {
		t.Fatalf("Orientation = %d; want 6", got)
	}
	if got := ifd0.uint(t, 0x00FE); got != 1 {
		t.Fatalf("IFD0 NewSubFileType = %d; want 1", got)
	}
	if e, ok := ifd0.entries[0xC612]; !ok || e.count != 4 || !bytes.Equal(e.value, []byte{1, 4, 0, 0}) {
		t.Fatalf("DNGVersion entry = %+v, present %v", e, ok)
	}
	if e := ifd0.entries[0x010F]; e.count != 8 || string(data[le.Uint32(e.value):le.Uint32(e.value)+7]) != "TESTCAM" {
		t.Fatalf("Make entry = %+v", e)
	}
	if e := ifd0.entries[0x0110]; e.count != 3 || string(e.value[:2]) != "D1" {
		t.Fatalf("Model entry = %+v", e)
	}

	sub, ok := ifd0.entries[0x014A]
	if !ok {
		t.Fatal("SubIFDs missing")
	}
	if sub.count != 4 { // raw + three SubIFD previews
		t.Fatalf("SubIFDs count = %d; want 4", sub.count)
	}
	arr := le.Uint32(sub.value)
	var subs []tiffIFD
	for i := range sub.count {
		subs = append(subs, readIFD(t, data, le.Uint32(data[arr+4*i:])))
	}

	rawIFD := subs[0]
	if rawIFD.uint(t, 0x00FE) != 0 || rawIFD.uint(t, 0x0103) != 7 || rawIFD.uint(t, 0x0106) != 32803 {
		t.Fatal("raw SubIFD: wrong NewSubFileType, Compression or PhotometricInterpretation")
	}
	if !bytes.Equal(rawIFD.strip(t, data), raw) {
		t.Fatal("raw strip differs from RawStrip")
	}

	checkPreview := func(name string, d tiffIFD, w, h uint32) []byte {
		t.Helper()
		if d.uint(t, 0x00FE) != 1 || d.uint(t, 0x0103) != 7 || d.uint(t, 0x0106) != 6 {
			t.Fatalf("%s: wrong NewSubFileType, Compression or PhotometricInterpretation", name)
		}
		if d.uint(t, 0x0100) != w || d.uint(t, 0x0101) != h {
			t.Fatalf("%s: size = %dx%d; want %dx%d", name, d.uint(t, 0x0100), d.uint(t, 0x0101), w, h)
		}
		return d.strip(t, data)
	}
	if got := checkPreview("small", subs[1], 32, 24); !bytes.Equal(got, small) {
		t.Fatal("small preview differs")
	}
	if got := checkPreview("large", subs[2], 64, 48); !bytes.Equal(got, large) {
		t.Fatal("large preview differs")
	}

	// The damaged preview keeps its header and is cut 16 bytes into the scan.
	got := checkPreview("damaged", subs[3], 48, 36)
	sos := bytes.Index(damaged, []byte{0xFF, 0xDA})
	if sos < 0 {
		t.Fatal("no SOS marker in the source JPEG")
	}
	headerEnd := sos + 2 + int(binary.BigEndian.Uint16(damaged[sos+2:]))
	if len(got) != headerEnd+16 || !bytes.Equal(got, damaged[:headerEnd+16]) {
		t.Fatalf("damaged preview = %d bytes; want the first %d bytes of the source", len(got), headerEnd+16)
	}
	if _, err := jpeg.Decode(bytes.NewReader(got)); err == nil {
		t.Fatal("damaged preview decodes; want an error")
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(got)); err != nil {
		t.Fatalf("damaged preview header: %v", err)
	}

	// The chained preview is reachable only through IFD0's next-IFD offset.
	if ifd0.next == 0 {
		t.Fatal("IFD0 next-IFD offset = 0; want the chained preview")
	}
	next := readIFD(t, data, ifd0.next)
	if got := checkPreview("chained", next, 16, 12); !bytes.Equal(got, chained) {
		t.Fatal("chained preview differs")
	}
	if next.next != 0 {
		t.Fatalf("chained preview next-IFD offset = %d; want 0", next.next)
	}
}

func TestMakeDNG_NoPreviewsNoChain(t *testing.T) {
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: []byte{1, 2, 3, 4}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read DNG: %v", err)
	}
	ifd0 := readIFD(t, data, binary.LittleEndian.Uint32(data[4:]))
	if _, ok := ifd0.entries[0x0112]; ok {
		t.Fatal("Orientation 0: want no Orientation tag")
	}
	if ifd0.next != 0 {
		t.Fatalf("next-IFD offset = %d; want 0", ifd0.next)
	}
	sub := ifd0.entries[0x014A]
	if sub.count != 1 {
		t.Fatalf("SubIFDs count = %d; want 1", sub.count)
	}
	rawIFD := readIFD(t, data, binary.LittleEndian.Uint32(sub.value))
	if !bytes.Equal(rawIFD.strip(t, data), []byte{1, 2, 3, 4}) {
		t.Fatal("raw strip differs")
	}
}
