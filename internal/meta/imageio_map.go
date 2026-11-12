package meta

import (
	"strings"
	"time"
)

// FileInfo is what a native record takes from the file system: the file's
// base name (the record's SourceFile) and its modification time.
type FileInfo struct {
	Name    string
	ModTime time.Time
}

// fileModifyLayout is exiftool's File:FileModifyDate format.
const fileModifyLayout = exifTimeLayout + "-07:00"

// newRecord starts a native record with the keys every record has.
func newRecord(fi FileInfo) map[string]any {
	return map[string]any{
		"SourceFile":          fi.Name,
		"File:FileModifyDate": fi.ModTime.Format(fileModifyLayout),
	}
}

// imageIOKey maps one ImageIO dictionary value to an exiftool key. dict ""
// is the top level.
type imageIOKey struct {
	exiftool, dict, key string
}

// imageIOKeys is the key table of FromImageIO, apart from the serial and
// ISO, which need more than a copy.
var imageIOKeys = []imageIOKey{
	{"EXIF:Make", "{TIFF}", "Make"},
	{"EXIF:Model", "{TIFF}", "Model"},
	{"EXIF:ExifImageWidth", "{Exif}", "PixelXDimension"},
	{"EXIF:ExifImageHeight", "{Exif}", "PixelYDimension"},
	{"File:ImageWidth", "", "PixelWidth"},
	{"File:ImageHeight", "", "PixelHeight"},
	{"EXIF:FNumber", "{Exif}", "FNumber"},
	{"EXIF:ExposureTime", "{Exif}", "ExposureTime"},
	{"EXIF:FocalLength", "{Exif}", "FocalLength"},
	{"EXIF:DateTimeOriginal", "{Exif}", "DateTimeOriginal"},
	{"EXIF:CreateDate", "{Exif}", "DateTimeDigitized"},
	{"EXIF:ModifyDate", "{TIFF}", "DateTime"},
	{"EXIF:GPSLatitude", "{GPS}", "Latitude"},
	{"EXIF:GPSLatitudeRef", "{GPS}", "LatitudeRef"},
	{"EXIF:GPSLongitude", "{GPS}", "Longitude"},
	{"EXIF:GPSLongitudeRef", "{GPS}", "LongitudeRef"},
}

// serialSources are the ImageIO sources of EXIF:SerialNumber, in order.
//
// Spike S4 measured them on Fujifilm: v1's serial is EXIF:SerialNumber,
// which is {Exif}.BodySerialNumber; {ExifAux}.SerialNumber carries the same
// body serial and is only a fallback. exiftool reports no
// MakerNotes:SerialNumber or MakerNotes:InternalSerialNumber from either,
// so neither is emitted. Other makers follow the same rule until the parity
// test shows otherwise.
var serialSources = []imageIOKey{
	{"EXIF:SerialNumber", "{Exif}", "BodySerialNumber"},
	{"EXIF:SerialNumber", "{ExifAux}", "SerialNumber"},
}

// FromImageIO turns ImageIO's property dictionary for a still into a record
// with the keys exiftool's `-j -G -n` output uses, so that Parse reads it
// like a v1 record. A key is emitted only when its source value is present
// and not empty; numbers stay numbers. The record always has SourceFile and
// File:FileModifyDate.
func FromImageIO(props map[string]any, fi FileInfo) map[string]any {
	rec := newRecord(fi)
	lookup := func(k imageIOKey) (any, bool) {
		d := props
		if k.dict != "" {
			d, _ = props[k.dict].(map[string]any)
		}
		return present(d[k.key])
	}
	for _, k := range imageIOKeys {
		if v, ok := lookup(k); ok {
			rec[k.exiftool] = v
		}
	}
	for _, k := range serialSources {
		if v, ok := lookup(k); ok {
			rec[k.exiftool] = v
			break
		}
	}
	exif, _ := props["{Exif}"].(map[string]any)
	if v, ok := firstElement(exif["ISOSpeedRatings"]); ok {
		rec["EXIF:ISO"] = v
	}
	return rec
}

// present returns v when it carries a value: not nil, not an empty or
// blank string, not an empty list or dictionary.
func present(v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, false
	case string:
		return x, strings.TrimSpace(x) != ""
	case []any:
		return x, len(x) > 0
	case map[string]any:
		return x, len(x) > 0
	}
	return v, true
}

// firstElement returns the first present element of a list, or a present
// scalar as it is.
func firstElement(v any) (any, bool) {
	if list, ok := v.([]any); ok {
		if len(list) == 0 {
			return nil, false
		}
		return present(list[0])
	}
	return present(v)
}
