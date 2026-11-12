// Command genfolder writes a folder of synthetic, dated JPEG photos for the
// end-to-end tests (frontend/e2e):
//
//	genfolder -out <dir> -n <count> [-cameras 2] [-bursts 3]
//
// Each photo is a 640×480 JPEG of one colour of its own, carrying an EXIF
// block with Make, Model, BodySerialNumber and DateTimeOriginal. Photo i is
// shot by camera i mod cameras; the photos are split in order into bursts
// of ten minutes, two hours apart, the first starting 2026-03-04 08:00:00,
// and the shots of a burst are spread evenly over its ten minutes. Every
// value is invented: the cameras are "Acme" "Model One", "Model Two", …
// with serials SN-0001, SN-0002, ….
//
// The EXIF block is written by hand (a little-endian TIFF structure in an
// APP1 segment after the JPEG's SOI marker), so the generator needs nothing
// beyond the standard library. Files are created exclusively: an existing
// file is never overwritten, and the run fails instead. It exits 2 on bad
// flags and 1 when a file cannot be written.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Image size and the burst layout.
const (
	width       = 640
	height      = 480
	burstLength = 10 * time.Minute
	burstGap    = 2 * time.Hour
	jpegQuality = 90
)

// firstShot is when the first burst starts (EXIF times are zone-less; the
// app reads them as UTC).
var firstShot = time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)

// modelNames name the first cameras; later ones are "Model <n>".
var modelNames = []string{"One", "Two", "Three", "Four", "Five", "Six"}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run parses args, writes the folder and returns the exit code.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("genfolder", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory to write the photos into (created when missing)")
	n := fs.Int("n", 0, "number of photos")
	cameras := fs.Int("cameras", 2, "number of cameras")
	bursts := fs.Int("bursts", 3, "number of bursts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case fs.NArg() != 0:
		_, _ = fmt.Fprintf(stderr, "genfolder: unexpected arguments %q\n", fs.Args())
		return 2
	case *out == "":
		_, _ = fmt.Fprintln(stderr, "genfolder: -out is required")
		return 2
	case *n <= 0 || *cameras <= 0 || *bursts <= 0:
		_, _ = fmt.Fprintln(stderr, "genfolder: -n, -cameras and -bursts must be positive")
		return 2
	}
	if fi, err := os.Stat(*out); err == nil && !fi.IsDir() {
		_, _ = fmt.Fprintf(stderr, "genfolder: %s is not a directory\n", *out)
		return 2
	}
	if err := write(*out, plan(*n, *cameras, *bursts)); err != nil {
		_, _ = fmt.Fprintf(stderr, "genfolder: %v\n", err)
		return 1
	}
	return 0
}

// camera is one invented camera body.
type camera struct {
	make, model, serial string
}

// cameraOf returns the k-th camera (0-based).
func cameraOf(k int) camera {
	model := fmt.Sprintf("Model %d", k+1)
	if k < len(modelNames) {
		model = "Model " + modelNames[k]
	}
	return camera{make: "Acme", model: model, serial: fmt.Sprintf("SN-%04d", k+1)}
}

// shot is one photo to write.
type shot struct {
	index  int
	camera int
	at     time.Time
	colour color.RGBA
}

// plan lays out n shots over the cameras and bursts: shot i is taken by
// camera i mod cameras, the shots are split in order into bursts (as evenly
// as integer division allows), and the m shots of a burst are spread over
// its ten minutes, j·10min/m after its start.
func plan(n, cameras, bursts int) []shot {
	shots := make([]shot, n)
	for i := range shots {
		k := i * bursts / n
		// The shots of burst k are first..last-1.
		first := (k*n + bursts - 1) / bursts
		last := ((k+1)*n + bursts - 1) / bursts
		j, m := i-first, last-first
		at := firstShot.Add(time.Duration(k) * burstGap).Add(time.Duration(j) * burstLength / time.Duration(m))
		shots[i] = shot{index: i, camera: i % cameras, at: at, colour: hue(i, n)}
	}
	return shots
}

// hue returns the i-th of n colours spread around the colour wheel.
func hue(i, n int) color.RGBA {
	h := float64(i) * 6 / float64(n) // sector 0..6
	const v, s = 230.0, 0.8
	f := h - float64(int(h))
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	var r, g, b float64
	switch int(h) % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xFF}
}

// fileName is the name of shot i.
func fileName(i int) string {
	return fmt.Sprintf("photo-%04d.jpg", i+1)
}

// write creates dir when missing and writes every shot into it.
func write(dir string, shots []shot) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	for _, s := range shots {
		data, err := encode(s)
		if err != nil {
			return err
		}
		if err := writeNew(filepath.Join(dir, fileName(s.index)), data); err != nil {
			return err
		}
	}
	return nil
}

// encode returns shot s as a JPEG with its EXIF segment.
func encode(s shot) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, s.colour)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encode %s: %w", fileName(s.index), err)
	}
	enc := buf.Bytes()
	if len(enc) < 2 || enc[0] != 0xFF || enc[1] != 0xD8 {
		return nil, fmt.Errorf("encode %s: no SOI marker", fileName(s.index))
	}
	// SOI, then the APP1 (EXIF) segment, then the rest of the encoder's output.
	app1 := exifSegment(cameraOf(s.camera), s.at)
	out := make([]byte, 0, len(enc)+len(app1))
	out = append(out, enc[:2]...)
	out = append(out, app1...)
	return append(out, enc[2:]...), nil
}

// writeNew creates path exclusively and writes data to it.
func writeNew(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the -out directory the test harness names
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// TIFF field types and the tags written.
const (
	typeASCII     = 2
	typeLong      = 4
	typeUndefined = 7

	tagMake             = 0x010F
	tagModel            = 0x0110
	tagExifIFD          = 0x8769
	tagExifVersion      = 0x9000
	tagDateTimeOriginal = 0x9003
	tagBodySerialNumber = 0xA431
)

// entry is one IFD entry; value holds its bytes (a LONG's included).
type entry struct {
	tag   uint16
	typ   uint16
	count uint32
	value []byte
}

func ascii(tag uint16, s string) entry {
	v := append([]byte(s), 0)
	return entry{tag: tag, typ: typeASCII, count: uint32(len(v)), value: v} //nolint:gosec // G115: short invented strings
}

// exifSegment returns the APP1 segment (marker included) holding a
// little-endian TIFF structure: IFD0 with Make, Model and the Exif IFD
// pointer, and the Exif IFD with ExifVersion, DateTimeOriginal and
// BodySerialNumber. Entries are in ascending tag order, as TIFF requires.
func exifSegment(c camera, at time.Time) []byte {
	le := binary.LittleEndian
	exif := []entry{
		{tag: tagExifVersion, typ: typeUndefined, count: 4, value: []byte("0232")},
		ascii(tagDateTimeOriginal, at.Format("2006:01:02 15:04:05")),
		ascii(tagBodySerialNumber, c.serial),
	}
	ifd0 := []entry{
		ascii(tagMake, c.make),
		ascii(tagModel, c.model),
		{tag: tagExifIFD, typ: typeLong, count: 1, value: make([]byte, 4)}, // patched below
	}

	// Layout: header (8), IFD0, its out-of-line values, the Exif IFD, its values.
	const header = 8
	ifd0Off := uint32(header)
	exifOff := ifd0Off + ifdSize(ifd0)
	le.PutUint32(ifd0[2].value, exifOff)

	tiff := []byte{'I', 'I', 42, 0, 0, 0, 0, 0}
	le.PutUint32(tiff[4:], ifd0Off)
	tiff = appendIFD(tiff, ifd0, ifd0Off)
	tiff = appendIFD(tiff, exif, exifOff)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2)) //nolint:gosec // G115: the segment is a few hundred bytes
	return append(seg, payload...)
}

// ifdSize is the size of an IFD with its out-of-line values.
func ifdSize(es []entry) uint32 {
	size := uint32(2 + 12*len(es) + 4) //nolint:gosec // G115: a handful of entries
	for _, e := range es {
		if len(e.value) > 4 {
			size += uint32(len(e.value)+1) &^ 1 //nolint:gosec // G115: short values, word-aligned
		}
	}
	return size
}

// appendIFD appends the IFD es, which starts at offset off of the TIFF
// structure, and its out-of-line values (each word-aligned); the next-IFD
// offset is 0.
func appendIFD(b []byte, es []entry, off uint32) []byte {
	le := binary.LittleEndian
	data := off + uint32(2+12*len(es)+4)    //nolint:gosec // G115: a handful of entries
	b = le.AppendUint16(b, uint16(len(es))) //nolint:gosec // G115: a handful of entries
	var values []byte
	for _, e := range es {
		b = le.AppendUint16(b, e.tag)
		b = le.AppendUint16(b, e.typ)
		b = le.AppendUint32(b, e.count)
		if len(e.value) <= 4 {
			var inline [4]byte
			copy(inline[:], e.value)
			b = append(b, inline[:]...)
			continue
		}
		b = le.AppendUint32(b, data+uint32(len(values))) //nolint:gosec // G115: short values
		values = append(values, e.value...)
		if len(values)%2 == 1 {
			values = append(values, 0)
		}
	}
	b = le.AppendUint32(b, 0)
	return append(b, values...)
}
