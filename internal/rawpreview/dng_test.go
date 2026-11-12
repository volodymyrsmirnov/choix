package rawpreview_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
)

// TIFF tags, field types and values used by the hand-built files.
const (
	tagNewSubFileType  = 0x00FE
	tagImageWidth      = 0x0100
	tagImageLength     = 0x0101
	tagCompression     = 0x0103
	tagPhotometric     = 0x0106
	tagStripOffsets    = 0x0111
	tagOrientation     = 0x0112
	tagStripByteCounts = 0x0117
	tagSubIFDs         = 0x014A
	tagJPEGIF          = 0x0201
	tagJPEGIFLength    = 0x0202
	tagDNGVersion      = 0xC612

	typeByte  = 1
	typeShort = 3
	typeLong  = 4

	compressionNone  = 1
	compressionJPEG  = 7
	photometricRGB   = 2
	photometricYCbCr = 6
	photometricCFA   = 32803
	photometricLin   = 34892
)

// tiffEntry is one IFD entry; val is the inline value (a SHORT with count 1
// is stored left-justified) or the offset of the data.
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	val      uint32
}

func short(tag, v uint16) tiffEntry {
	return tiffEntry{tag: tag, typ: typeShort, count: 1, val: uint32(v)}
}

func long(tag uint16, v uint32) tiffEntry {
	return tiffEntry{tag: tag, typ: typeLong, count: 1, val: v}
}

// tiffBuilder hand-builds a TIFF in either byte order, so tests can make
// structures MakeDNG never produces: big-endian files, loops, hostile
// counts and offsets.
// byteOrder reads and appends in one byte order.
type byteOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

type tiffBuilder struct {
	order byteOrder
	buf   []byte
}

func newTIFF(order byteOrder) *tiffBuilder {
	b := &tiffBuilder{order: order}
	if order == binary.BigEndian {
		b.buf = []byte{'M', 'M', 0, 42, 0, 0, 0, 0}
	} else {
		b.buf = []byte{'I', 'I', 42, 0, 0, 0, 0, 0}
	}
	return b
}

func (b *tiffBuilder) align() {
	if len(b.buf)%2 == 1 {
		b.buf = append(b.buf, 0)
	}
}

// blob appends p and returns its offset.
func (b *tiffBuilder) blob(p []byte) uint32 {
	b.align()
	off := uint32(len(b.buf))
	b.buf = append(b.buf, p...)
	return off
}

// longs appends an array of LONGs and returns its offset.
func (b *tiffBuilder) longs(vs ...uint32) uint32 {
	arr := make([]byte, 4*len(vs))
	for i, v := range vs {
		b.order.PutUint32(arr[4*i:], v)
	}
	return b.blob(arr)
}

// ifd appends an IFD with the entries in the given order and returns its
// offset.
func (b *tiffBuilder) ifd(next uint32, entries ...tiffEntry) uint32 {
	b.align()
	off := uint32(len(b.buf))
	b.buf = b.order.AppendUint16(b.buf, uint16(len(entries)))
	for _, e := range entries {
		b.buf = b.order.AppendUint16(b.buf, e.tag)
		b.buf = b.order.AppendUint16(b.buf, e.typ)
		b.buf = b.order.AppendUint32(b.buf, e.count)
		var v [4]byte
		if e.typ == typeShort && e.count == 1 {
			b.order.PutUint16(v[:], uint16(e.val))
		} else {
			b.order.PutUint32(v[:], e.val)
		}
		b.buf = append(b.buf, v[:]...)
	}
	b.buf = b.order.AppendUint32(b.buf, next)
	return off
}

// put32 overwrites the 32-bit value at off.
func (b *tiffBuilder) put32(off, v uint32) { b.order.PutUint32(b.buf[off:], v) }

// setIFD0 points the header at off.
func (b *tiffBuilder) setIFD0(off uint32) { b.put32(4, off) }

// write stores the TIFF as a .DNG in a new temporary directory.
func (b *tiffBuilder) write(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "HAND0001.DNG")
	if err := os.WriteFile(path, b.buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// previewEntries are the tags of a valid JPEG preview stored as one strip.
func previewEntries(width uint16, off uint32, n int) []tiffEntry {
	return []tiffEntry{
		long(tagNewSubFileType, 1),
		short(tagImageWidth, width),
		short(tagCompression, compressionJPEG),
		short(tagPhotometric, photometricYCbCr),
		long(tagStripOffsets, off),
		long(tagStripByteCounts, uint32(n)),
	}
}

// decodeFully is a try that decodes the whole JPEG with image/jpeg.
func decodeFully(p rawpreview.Preview) error {
	_, err := jpeg.Decode(bytes.NewReader(p.JPEG))
	return err
}

func TestDNG_PicksWidestPreview(t *testing.T) {
	small := sharedJPEG(t, 256, 171)
	large := sharedJPEG(t, 1600, 1067)
	orders := map[string][]platformtest.DNGPreview{
		// The IFD heights are deliberately wrong: Height must come from
		// the JPEG itself.
		"small first": {
			{JPEG: small, Width: 256, Height: 1},
			{JPEG: large, Width: 1600, Height: 1},
		},
		"large first": {
			{JPEG: large, Width: 1600, Height: 1},
			{JPEG: small, Width: 256, Height: 1},
		},
	}
	for name, previews := range orders {
		t.Run(name, func(t *testing.T) {
			path := platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: []byte{1, 2, 3, 4}, Previews: previews})
			p, err := rawpreview.Embedded(path, nil)
			if err != nil {
				t.Fatalf("Embedded: %v", err)
			}
			if !bytes.Equal(p.JPEG, large) {
				t.Fatal("did not return the 1600-wide preview")
			}
			if p.Width != 1600 || p.Height != 1067 {
				t.Fatalf("size = %dx%d; want 1600x1067", p.Width, p.Height)
			}
		})
	}
}

func TestDNG_SkipsWidestPreviewWithGarbageHeader(t *testing.T) {
	small := sharedJPEG(t, 256, 171)
	garbage := append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte{0x13, 0x37}, 4096)...)
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte{1, 2, 3, 4},
		Previews: []platformtest.DNGPreview{
			{JPEG: garbage, Width: 1600, Height: 1067},
			{JPEG: small, Width: 256, Height: 171},
		},
	})
	r := &recorder{}
	p, err := rawpreview.Embedded(path, r.try)
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if !bytes.Equal(p.JPEG, small) || p.Width != 256 {
		t.Fatalf("returned a %d-wide preview; want the 256 one", p.Width)
	}
	if len(r.widths) != 1 || r.widths[0] != 256 {
		t.Fatalf("offered widths = %v; want only [256]", r.widths)
	}

	p, err = rawpreview.Embedded(path, nil)
	if err != nil {
		t.Fatalf("Embedded(nil try): %v", err)
	}
	if !bytes.Equal(p.JPEG, small) {
		t.Fatalf("nil try returned a %d-wide preview; want the 256 one", p.Width)
	}
}

func TestDNG_HeaderValidBodyDamaged(t *testing.T) {
	small := sharedJPEG(t, 256, 171)
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte{1, 2, 3, 4},
		Previews: []platformtest.DNGPreview{
			{JPEG: small, Width: 256, Height: 171},
			{JPEG: sharedJPEG(t, 1600, 1067), Width: 1600, Height: 1067, DamageBody: true},
		},
	})

	t.Run("nil try accepts the parsing header", func(t *testing.T) {
		p, err := rawpreview.Embedded(path, nil)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if p.Width != 1600 || p.Height != 1067 {
			t.Fatalf("size = %dx%d; want 1600x1067", p.Width, p.Height)
		}
	})

	t.Run("decoding try falls back to the next preview", func(t *testing.T) {
		p, err := rawpreview.Embedded(path, decodeFully)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if p.Width != 256 || !bytes.Equal(p.JPEG, small) {
			t.Fatalf("Width = %d; want the 256 preview", p.Width)
		}
	})

	t.Run("Usable falls back to the next preview", func(t *testing.T) {
		p, err := rawpreview.Embedded(path, rawpreview.Usable(t.Context(), &platformtest.Stills{}))
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if p.Width != 256 || !bytes.Equal(p.JPEG, small) {
			t.Fatalf("Width = %d; want the 256 preview", p.Width)
		}
	})
}

func TestDNG_PreviewInChainedIFD(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 512, 341)
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte{1, 2, 3, 4},
		Previews: []platformtest.DNGPreview{{JPEG: jpg, Width: 512, Height: 341, Chained: true}},
	})
	p, err := rawpreview.Embedded(path, nil)
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if !bytes.Equal(p.JPEG, jpg) {
		t.Fatal("did not return the chained preview")
	}
}

func TestDNG_NeverPicksRawStrip(t *testing.T) {
	// The raw strip is a real JPEG padded to 5 MB, so only the IFD tags
	// can keep it from being offered.
	raw := platformtest.MakeJPEG(t, 64, 48)
	raw = append(raw, make([]byte, 5<<20-len(raw))...)
	preview := sharedJPEG(t, 1600, 1067)
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: raw,
		Previews: []platformtest.DNGPreview{{JPEG: preview, Width: 1600, Height: 1067}},
	})
	p, err := rawpreview.Embedded(path, nil)
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if !bytes.Equal(p.JPEG, preview) {
		t.Fatalf("returned %d bytes; want the %d-byte preview", len(p.JPEG), len(preview))
	}
	r := &recorder{errs: []error{errors.New("reject"), errors.New("reject")}}
	if _, err := rawpreview.Embedded(path, r.try); !errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("err = %v; want ErrNoPreview", err)
	}
	if len(r.widths) != 1 {
		t.Fatalf("offered widths = %v; want only the preview", r.widths)
	}

	// Raw data marked as a reduced-resolution image is still refused by
	// its PhotometricInterpretation.
	for _, photometric := range []uint16{photometricCFA, photometricLin} {
		b := newTIFF(binary.LittleEndian)
		rawOff := b.blob(raw)
		rawIFD := b.ifd(0, long(tagNewSubFileType, 1), long(tagImageWidth, 6000),
			short(tagCompression, compressionJPEG), short(tagPhotometric, photometric),
			long(tagStripOffsets, rawOff), long(tagStripByteCounts, uint32(len(raw))))
		b.setIFD0(b.ifd(0, long(tagSubIFDs, rawIFD)))
		r := &recorder{}
		if _, err := rawpreview.Embedded(b.write(t), r.try); !errors.Is(err, rawpreview.ErrNoPreview) {
			t.Fatalf("photometric %d: err = %v; want ErrNoPreview", photometric, err)
		}
		if len(r.widths) != 0 {
			t.Fatalf("photometric %d: offered widths = %v; want none", photometric, r.widths)
		}
	}
}

func TestDNG_OrientationFromIFD0(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 64, 48)
	for _, tt := range []struct{ tag, want int }{{8, 8}, {6, 6}, {0, 1}} {
		path := platformtest.MakeDNG(t, platformtest.DNGOptions{
			Orientation: tt.tag,
			RawStrip:    []byte{1, 2, 3, 4},
			Previews:    []platformtest.DNGPreview{{JPEG: jpg, Width: 64, Height: 48}},
		})
		p, err := rawpreview.Embedded(path, nil)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if p.Orientation != tt.want {
			t.Fatalf("tag %d: Orientation = %d; want %d", tt.tag, p.Orientation, tt.want)
		}
	}
}

func TestDNG_NoPreview(t *testing.T) {
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: bytes.Repeat([]byte{7}, 4096)})
	assertNoPreview(t, path)
}

func TestDNG_BigEndian(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 320, 213)
	b := newTIFF(binary.BigEndian)
	jpgOff := b.blob(jpg)
	preview := b.ifd(0,
		long(tagNewSubFileType, 1),
		short(tagImageWidth, 320),
		short(tagImageLength, 213),
		short(tagCompression, 6),
		short(tagPhotometric, photometricYCbCr),
		long(tagJPEGIF, jpgOff),
		long(tagJPEGIFLength, uint32(len(jpg))),
	)
	b.setIFD0(b.ifd(0,
		long(tagNewSubFileType, 1),
		short(tagOrientation, 6),
		long(tagSubIFDs, preview),
		tiffEntry{tag: tagDNGVersion, typ: typeByte, count: 4, val: 0x01040000},
	))
	p, err := rawpreview.Embedded(b.write(t), nil)
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if !bytes.Equal(p.JPEG, jpg) {
		t.Fatal("did not return the preview")
	}
	if p.Orientation != 6 || p.Width != 320 || p.Height != 213 {
		t.Fatalf("got orientation %d, %dx%d; want 6, 320x213", p.Orientation, p.Width, p.Height)
	}
}

// embedWithin runs Embedded and fails the test when it does not return
// within d.
func embedWithin(t *testing.T, d time.Duration, path string) (rawpreview.Preview, error) {
	t.Helper()
	type result struct {
		p   rawpreview.Preview
		err error
	}
	done := make(chan result, 1)
	go func() {
		p, err := rawpreview.Embedded(path, nil)
		done <- result{p, err}
	}()
	select {
	case r := <-done:
		return r.p, r.err
	case <-time.After(d):
		t.Fatalf("Embedded did not return within %v", d)
		return rawpreview.Preview{}, nil
	}
}

func TestDNG_IFDLoop(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 128, 96)

	t.Run("SubIFD points at IFD0", func(t *testing.T) {
		b := newTIFF(binary.LittleEndian)
		jpgOff := b.blob(jpg)
		arr := b.longs(0, 0)
		ifd0 := b.ifd(0, long(tagNewSubFileType, 1), tiffEntry{tag: tagSubIFDs, typ: typeLong, count: 2, val: arr})
		preview := b.ifd(ifd0, previewEntries(128, jpgOff, len(jpg))...)
		b.put32(arr, preview)
		b.put32(arr+4, ifd0)
		b.put32(ifd0+2+12*2, ifd0) // IFD0's next-IFD offset points at itself
		b.setIFD0(ifd0)
		p, err := embedWithin(t, 5*time.Second, b.write(t))
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if !bytes.Equal(p.JPEG, jpg) {
			t.Fatal("did not return the preview")
		}
	})

	t.Run("next-IFD chain cycles", func(t *testing.T) {
		b := newTIFF(binary.LittleEndian)
		jpgOff := b.blob(jpg)
		ifd1 := b.ifd(0, previewEntries(128, jpgOff, len(jpg))...)
		ifd0 := b.ifd(ifd1, long(tagNewSubFileType, 0))
		b.put32(ifd1+2+12*6, ifd0)
		b.setIFD0(ifd0)
		p, err := embedWithin(t, 5*time.Second, b.write(t))
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if !bytes.Equal(p.JPEG, jpg) {
			t.Fatal("did not return the preview")
		}
	})
}

// TestDNG_SubIFDAlsoOnMainChain: IFD0 lists B both in SubIFDs and as its
// next IFD, so B is reached first as a SubIFD. B must still be expanded as
// part of the main chain: its next IFD (C) and its SubIFDs (D) hold
// previews.
func TestDNG_SubIFDAlsoOnMainChain(t *testing.T) {
	jpgC := platformtest.MakeJPEG(t, 300, 200)
	jpgD := platformtest.MakeJPEG(t, 200, 150)
	b := newTIFF(binary.LittleEndian)
	offC := b.blob(jpgC)
	offD := b.blob(jpgD)
	ifdC := b.ifd(0, previewEntries(300, offC, len(jpgC))...)
	ifdD := b.ifd(0, previewEntries(200, offD, len(jpgD))...)
	ifdB := b.ifd(ifdC, long(tagNewSubFileType, 0), long(tagSubIFDs, ifdD))
	b.setIFD0(b.ifd(ifdB, long(tagNewSubFileType, 0), long(tagSubIFDs, ifdB)))
	path := b.write(t)

	p, err := rawpreview.Embedded(path, nil)
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if !bytes.Equal(p.JPEG, jpgC) {
		t.Fatalf("returned a %d-wide preview; want C (300)", p.Width)
	}
	reject := errors.New("reject")
	r := &recorder{errs: []error{reject, reject}}
	if _, err := rawpreview.Embedded(path, r.try); !errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("err = %v; want ErrNoPreview", err)
	}
	if len(r.widths) != 2 || r.widths[0] != 300 || r.widths[1] != 200 {
		t.Fatalf("offered widths = %v; want [300 200]", r.widths)
	}
}

// TestDNG_JPEGInterchangeFormatFallsBackToStrip: an IFD with both a
// JPEGInterchangeFormat range and a single strip offers both, so a broken
// or undecodable JPEGInterchangeFormat does not hide a good strip.
func TestDNG_JPEGInterchangeFormatFallsBackToStrip(t *testing.T) {
	strip := platformtest.MakeJPEG(t, 32, 24)
	big := platformtest.MakeJPEG(t, 256, 171)
	// Both ranges are larger than the strip, so with equal ImageWidth the
	// JPEGInterchangeFormat range is offered first (ties by byte count).
	truncated := big[:len(big)*2/3]
	garbage := append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte{0x42}, len(big))...)

	build := func(t *testing.T, jif []byte, jifOff func(off uint32) uint32, jifLen int) string {
		t.Helper()
		b := newTIFF(binary.LittleEndian)
		stripOff := b.blob(strip)
		off := b.blob(jif)
		b.setIFD0(b.ifd(0,
			long(tagNewSubFileType, 1), short(tagImageWidth, 256),
			short(tagCompression, compressionJPEG), short(tagPhotometric, photometricYCbCr),
			long(tagStripOffsets, stripOff), long(tagStripByteCounts, uint32(len(strip))),
			long(tagJPEGIF, jifOff(off)), long(tagJPEGIFLength, uint32(jifLen))))
		return b.write(t)
	}
	same := func(off uint32) uint32 { return off }

	t.Run("invalid header", func(t *testing.T) {
		path := build(t, garbage, same, len(garbage))
		r := &recorder{}
		p, err := rawpreview.Embedded(path, r.try)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if !bytes.Equal(p.JPEG, strip) {
			t.Fatalf("returned a %d-wide preview; want the strip", p.Width)
		}
		if len(r.widths) != 1 || r.widths[0] != 32 {
			t.Fatalf("offered widths = %v; want only [32]", r.widths)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		path := build(t, big, func(uint32) uint32 { return 0xFFFFFF00 }, len(big))
		p, err := rawpreview.Embedded(path, nil)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if !bytes.Equal(p.JPEG, strip) {
			t.Fatalf("returned a %d-wide preview; want the strip", p.Width)
		}
	})

	t.Run("rejected by try", func(t *testing.T) {
		path := build(t, truncated, same, len(truncated))
		p, err := rawpreview.Embedded(path, nil)
		if err != nil {
			t.Fatalf("Embedded(nil try): %v", err)
		}
		if p.Width != 256 {
			t.Fatalf("nil try: Width = %d; want the 256 JPEGInterchangeFormat header", p.Width)
		}
		p, err = rawpreview.Embedded(path, decodeFully)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if !bytes.Equal(p.JPEG, strip) || p.Width != 32 {
			t.Fatalf("Width = %d; want the 32-wide strip", p.Width)
		}
	})

	t.Run("same range offered once", func(t *testing.T) {
		b := newTIFF(binary.LittleEndian)
		off := b.blob(strip)
		b.setIFD0(b.ifd(0,
			long(tagNewSubFileType, 1), short(tagImageWidth, 32),
			short(tagCompression, compressionJPEG), short(tagPhotometric, photometricYCbCr),
			long(tagStripOffsets, off), long(tagStripByteCounts, uint32(len(strip))),
			long(tagJPEGIF, off), long(tagJPEGIFLength, uint32(len(strip)))))
		r := &recorder{errs: []error{errors.New("reject"), errors.New("reject")}}
		if _, err := rawpreview.Embedded(b.write(t), r.try); !errors.Is(err, rawpreview.ErrNoPreview) {
			t.Fatalf("err = %v; want ErrNoPreview", err)
		}
		if len(r.widths) != 1 {
			t.Fatalf("offered widths = %v; want one candidate", r.widths)
		}
	})
}

func TestDNG_OversizePreviewRefused(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 128, 96)
	declared := 65 << 20
	b := newTIFF(binary.LittleEndian)
	jpgOff := b.blob(jpg)
	preview := b.ifd(0, previewEntries(128, jpgOff, declared)...)
	b.setIFD0(b.ifd(0, long(tagNewSubFileType, 1), long(tagSubIFDs, preview)))
	path := b.write(t)
	// Make the file long enough that the declared range is inside it, so
	// only the MaxPreviewBytes limit refuses it (sparse on APFS).
	if err := os.Truncate(path, int64(jpgOff)+int64(declared)+1024); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r := &recorder{}
	_, err := rawpreview.Embedded(path, r.try)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("err = %v; want ErrNoPreview", err)
	}
	if len(r.widths) != 0 {
		t.Fatalf("offered widths = %v; want none", r.widths)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 4<<20 {
		t.Fatalf("allocated %d bytes; want well under the declared %d", got, declared)
	}
}

// TestDNG_HostileStructure feeds structures a damaged or hostile file may
// contain; each must end in ErrNoPreview without a panic or a large
// allocation.
func TestDNG_HostileStructure(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 64, 48)
	tests := map[string]func(b *tiffBuilder){
		"IFD0 offset past EOF": func(b *tiffBuilder) { b.setIFD0(0xFFFFFFF0) },
		"IFD0 offset zero":     func(b *tiffBuilder) {},
		"IFD0 runs past EOF": func(b *tiffBuilder) {
			b.setIFD0(uint32(len(b.buf)))
			b.buf = b.order.AppendUint16(b.buf, 3)
		},
		"too many entries": func(b *tiffBuilder) {
			off := b.blob(jpg)
			entries := previewEntries(64, off, len(jpg))
			for range 4097 - len(entries) {
				entries = append(entries, short(0xFFFF, 0))
			}
			b.setIFD0(b.ifd(0, entries...))
		},
		"huge SubIFDs count": func(b *tiffBuilder) {
			b.setIFD0(b.ifd(0, tiffEntry{tag: tagSubIFDs, typ: typeLong, count: 0xFFFFFFFF, val: 8}))
		},
		"SubIFD offset past EOF": func(b *tiffBuilder) {
			b.setIFD0(b.ifd(0, long(tagSubIFDs, 0xFFFFFFF0)))
		},
		"strip past EOF": func(b *tiffBuilder) {
			b.setIFD0(b.ifd(0, previewEntries(64, 0xFFFFFF00, 0x1000)...))
		},
		"strip length below 4": func(b *tiffBuilder) {
			off := b.blob(jpg)
			b.setIFD0(b.ifd(0, previewEntries(64, off, 3)...))
		},
		"two strips": func(b *tiffBuilder) {
			off := b.blob(jpg)
			arr := b.longs(off, off)
			counts := b.longs(uint32(len(jpg)), uint32(len(jpg)))
			b.setIFD0(b.ifd(0,
				long(tagNewSubFileType, 1), short(tagImageWidth, 64),
				short(tagCompression, compressionJPEG), short(tagPhotometric, photometricYCbCr),
				tiffEntry{tag: tagStripOffsets, typ: typeLong, count: 2, val: arr},
				tiffEntry{tag: tagStripByteCounts, typ: typeLong, count: 2, val: counts}))
		},
		"NewSubFileType absent": func(b *tiffBuilder) {
			off := b.blob(jpg)
			b.setIFD0(b.ifd(0, previewEntries(64, off, len(jpg))[1:]...))
		},
		"uncompressed": func(b *tiffBuilder) {
			off := b.blob(jpg)
			e := previewEntries(64, off, len(jpg))
			e[2] = short(tagCompression, compressionNone)
			b.setIFD0(b.ifd(0, e...))
		},
		"photometric absent": func(b *tiffBuilder) {
			off := b.blob(jpg)
			e := previewEntries(64, off, len(jpg))
			b.setIFD0(b.ifd(0, append(e[:3:3], e[4:]...)...))
		},
		"strip offset of wrong type": func(b *tiffBuilder) {
			off := b.blob(jpg)
			e := previewEntries(64, off, len(jpg))
			e[4].typ = typeByte
			b.setIFD0(b.ifd(0, e...))
		},
		"bad magic": func(b *tiffBuilder) {
			off := b.blob(jpg)
			b.setIFD0(b.ifd(0, previewEntries(64, off, len(jpg))...))
			b.buf[2] = 43
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			b := newTIFF(binary.LittleEndian)
			build(b)
			assertNoPreview(t, b.write(t))
		})
	}

	t.Run("short file", func(t *testing.T) {
		for _, data := range [][]byte{nil, []byte("II"), []byte("II*\x00\x08\x00")} {
			path := filepath.Join(t.TempDir(), "SHORT.DNG")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			assertNoPreview(t, path)
		}
	})

	t.Run("RGB photometric is accepted", func(t *testing.T) {
		b := newTIFF(binary.LittleEndian)
		off := b.blob(jpg)
		e := previewEntries(64, off, len(jpg))
		e[3] = short(tagPhotometric, photometricRGB)
		b.setIFD0(b.ifd(0, e...))
		p, err := rawpreview.Embedded(b.write(t), nil)
		if err != nil {
			t.Fatalf("Embedded: %v", err)
		}
		if !bytes.Equal(p.JPEG, jpg) {
			t.Fatal("did not return the preview")
		}
	})
}

// FuzzEmbeddedDNG checks that arbitrary bytes never panic, never hang and
// end either in a JPEG that parses or in ErrNoPreview.
func FuzzEmbeddedDNG(f *testing.F) {
	jpg := platformtest.MakeJPEG(f, 32, 24)
	for _, order := range []byteOrder{binary.LittleEndian, binary.BigEndian} {
		b := newTIFF(order)
		off := b.blob(jpg)
		preview := b.ifd(0, previewEntries(32, off, len(jpg))...)
		chained := b.ifd(0, previewEntries(32, off, len(jpg))...)
		b.setIFD0(b.ifd(chained, long(tagNewSubFileType, 1), short(tagOrientation, 3), long(tagSubIFDs, preview)))
		f.Add(b.buf)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "FUZZ.DNG")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := rawpreview.Embedded(path, nil)
		if err != nil {
			if !errors.Is(err, rawpreview.ErrNoPreview) {
				t.Fatalf("err = %v; want ErrNoPreview", err)
			}
			return
		}
		if len(p.JPEG) < 4 || p.JPEG[0] != 0xFF || p.JPEG[1] != 0xD8 || p.Width <= 0 || p.Height <= 0 {
			t.Fatalf("accepted a preview that is not a JPEG: %d bytes, %dx%d", len(p.JPEG), p.Width, p.Height)
		}
		if p.Orientation < 1 || p.Orientation > 8 {
			t.Fatalf("Orientation = %d; want 1..8", p.Orientation)
		}
	})
}
