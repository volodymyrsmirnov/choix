//go:build ignore

// _make_sample.go writes a minimal JPEG with a hand-crafted EXIF header to
// testdata/exif/sample.jpg. Run with:
//
//	go run testdata/exif/_make_sample.go
//
// The output is a valid JPEG that exiftool can parse. It embeds a 1-pixel
// image plus a minimal APP1/EXIF segment with Make and DateTimeOriginal tags.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
)

func main() {
	// Build minimal APP1/EXIF bytes containing Make + DateTimeOriginal.
	exifSegment := buildEXIF()

	// Encode the actual JPEG image (1x1 pixel, grey).
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.SetGray(0, 0, color.Gray{Y: 128})
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, img, &jpeg.Options{Quality: 85}); err != nil {
		panic(err)
	}

	// Insert the APP1 segment right after the SOI marker (first 2 bytes).
	jpegBytes := raw.Bytes()
	out := make([]byte, 0, len(jpegBytes)+len(exifSegment)+4)
	out = append(out, jpegBytes[:2]...) // SOI
	// APP1 marker + length (2-byte big-endian = len(exifSegment)+2 for the length field itself)
	app1Len := uint16(len(exifSegment) + 2)
	out = append(out, 0xFF, 0xE1)
	out = append(out, byte(app1Len>>8), byte(app1Len))
	out = append(out, exifSegment...)
	out = append(out, jpegBytes[2:]...) // rest of JPEG

	if err := os.WriteFile("sample.jpg", out, 0o644); err != nil {
		panic(err)
	}
}

// buildEXIF returns raw APP1 segment content (without the APP1 marker and
// length prefix, which the caller adds). It embeds minimal IFD0 tags.
func buildEXIF() []byte {
	// EXIF header: "Exif\x00\x00" + TIFF header
	var buf bytes.Buffer
	buf.WriteString("Exif\x00\x00")

	// TIFF header: little-endian byte order, magic 42, offset to IFD0.
	// TIFF data starts at offset 0 relative to the TIFF header start (after "Exif\0\0").
	tiffBase := 6 // offset of TIFF header within APP1 content
	_ = tiffBase

	var tiff bytes.Buffer
	tiff.WriteString("II") // little-endian
	writeU16LE(&tiff, 42)  // magic
	writeU32LE(&tiff, 8)   // IFD0 offset (relative to TIFF header start = 8)

	// IFD0: 2 tags (Make, DateTime)
	const (
		tagMake     = 0x010F
		tagDateTime = 0x0132
		typeASCII   = 2
	)

	// Value area starts right after the IFD:
	//   2 bytes (count) + n*12 bytes (entries) + 4 bytes (next IFD) = 2 + 2*12 + 4 = 30
	// Values are stored inline when ≤4 bytes, otherwise in the value area.
	makeStr := "TestCam\x00"           // 8 bytes
	dtStr := "2024:06:01 12:00:00\x00" // 20 bytes

	ifdOffset := uint32(8)
	valueAreaOffset := ifdOffset + 2 + 2*12 + 4

	// Write IFD
	writeU16LE(&tiff, 2) // 2 entries

	// Tag 0: Make (ASCII, 8 bytes, in value area)
	writeU16LE(&tiff, tagMake)
	writeU16LE(&tiff, typeASCII)
	writeU32LE(&tiff, uint32(len(makeStr)))
	writeU32LE(&tiff, valueAreaOffset)

	// Tag 1: DateTime (ASCII, 20 bytes, in value area)
	writeU16LE(&tiff, tagDateTime)
	writeU16LE(&tiff, typeASCII)
	writeU32LE(&tiff, uint32(len(dtStr)))
	writeU32LE(&tiff, valueAreaOffset+uint32(len(makeStr)))

	writeU32LE(&tiff, 0) // next IFD offset = 0 (none)

	// Value area
	tiff.WriteString(makeStr)
	tiff.WriteString(dtStr)

	buf.Write(tiff.Bytes())
	return buf.Bytes()
}

func writeU16LE(buf *bytes.Buffer, v uint16) {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	buf.Write(b)
}

func writeU32LE(buf *bytes.Buffer, v uint32) {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	buf.Write(b)
}
