package meta

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedTime is the date every committed native fixture carries.
var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// loadDict reads a native property dictionary from testdata/<dir>/<name>.
func loadDict(t *testing.T, dir, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", dir, name))
	if err != nil {
		t.Fatalf("read %s/%s: %v", dir, name, err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("decode %s/%s: %v", dir, name, err)
	}
	return d
}

// sub returns the nested dictionary d[key], or nil.
func sub(d map[string]any, key string) map[string]any {
	m, _ := d[key].(map[string]any)
	return m
}

// str returns the string d[key], failing the test when it is missing.
func str(t *testing.T, d map[string]any, key string) string {
	t.Helper()
	s, ok := d[key].(string)
	if !ok || s == "" {
		t.Fatalf("fixture has no string %q", key)
	}
	return s
}

// num returns the number d[key], failing the test when it is missing.
func num(t *testing.T, d map[string]any, key string) float64 {
	t.Helper()
	f, ok := d[key].(float64)
	if !ok {
		t.Fatalf("fixture has no number %q", key)
	}
	return f
}

// parseRecord runs a native record through the one parser, the way the
// Extractor does with the bytes a Reader returns.
func parseRecord(t *testing.T, rec map[string]any) Metadata {
	t.Helper()
	b, err := json.Marshal([]map[string]any{rec})
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func testFileInfo() FileInfo {
	return FileInfo{Name: "TEST0001.JPG", ModTime: time.Date(2030, 5, 6, 7, 8, 9, 0, time.FixedZone("test", 2*3600))}
}

func TestFromImageIO_FujiDeviceKey(t *testing.T) {
	props := loadDict(t, "imageio", "fuji_raf_jpeg.json")
	tiff, exif := sub(props, "{TIFF}"), sub(props, "{Exif}")
	wantKey := str(t, tiff, "Make") + " " + str(t, tiff, "Model") + "#TESTSERIAL01"

	rec := FromImageIO(props, testFileInfo())
	m := parseRecord(t, rec)

	if got := DeviceKey(m); got != wantKey {
		t.Errorf("DeviceKey = %q, want %q", got, wantKey)
	}
	if !m.DateTimeOriginal.Equal(fixedTime) {
		t.Errorf("DateTimeOriginal = %v, want %v", m.DateTimeOriginal, fixedTime)
	}
	if want := int(num(t, exif, "PixelXDimension")); m.Width != want {
		t.Errorf("Width = %d, want %d", m.Width, want)
	}
	if want := int(num(t, exif, "PixelYDimension")); m.Height != want {
		t.Errorf("Height = %d, want %d", m.Height, want)
	}
	iso, _ := exif["ISOSpeedRatings"].([]any)
	if len(iso) == 0 || m.ISO != int(iso[0].(float64)) {
		t.Errorf("ISO = %d, want the first ISOSpeedRatings %v", m.ISO, iso)
	}
	if m.Aperture != num(t, exif, "FNumber") || m.FocalLength != num(t, exif, "FocalLength") || m.Shutter == "" {
		t.Errorf("exposure = f/%v %v mm %q; want the fixture's", m.Aperture, m.FocalLength, m.Shutter)
	}
	// Numbers stay numbers.
	if _, ok := rec["EXIF:ISO"].(float64); !ok {
		t.Errorf("EXIF:ISO = %#v, want a number", rec["EXIF:ISO"])
	}
	if rec["SourceFile"] != "TEST0001.JPG" {
		t.Errorf("SourceFile = %v", rec["SourceFile"])
	}

	// Spike S4: {ExifAux}.SerialNumber carries the same body serial and is
	// only a fallback for EXIF:SerialNumber; no MakerNotes serial is made.
	t.Run("ExifAux fallback", func(t *testing.T) {
		alt := loadDict(t, "imageio", "fuji_raf_jpeg.json")
		delete(sub(alt, "{Exif}"), "BodySerialNumber")
		alt["{ExifAux}"] = map[string]any{"SerialNumber": "TESTSERIAL01"}
		rec := FromImageIO(alt, testFileInfo())
		if got := DeviceKey(parseRecord(t, rec)); got != wantKey {
			t.Errorf("DeviceKey = %q, want %q", got, wantKey)
		}
		for k := range rec {
			if strings.HasPrefix(k, "MakerNotes:") {
				t.Errorf("record has %s; no MakerNotes key may be emitted", k)
			}
		}
	})
}

func TestFromImageIO_IPhoneHEIC(t *testing.T) {
	props := loadDict(t, "imageio", "iphone_heic.json")
	tiff := sub(props, "{TIFF}")
	m := parseRecord(t, FromImageIO(props, testFileInfo()))
	if got, want := DeviceKey(m), str(t, tiff, "Make")+" "+str(t, tiff, "Model")+"#"; got != want {
		t.Errorf("DeviceKey = %q, want %q", got, want)
	}
	if !m.HasGPS || m.GPSLatitude != 10.5 || m.GPSLongitude != 20.25 {
		t.Errorf("GPS = %v %v,%v; want 10.5,20.25", m.HasGPS, m.GPSLatitude, m.GPSLongitude)
	}
	if !m.DateTimeOriginal.Equal(fixedTime) {
		t.Errorf("DateTimeOriginal = %v, want %v", m.DateTimeOriginal, fixedTime)
	}
}

func TestFromImageIO_DNGContainer(t *testing.T) {
	props := loadDict(t, "imageio", "dng_container.json")
	tiff := sub(props, "{TIFF}")
	fi := testFileInfo() // a file time far from the capture time
	m := parseRecord(t, FromImageIO(props, fi))

	key := DeviceKey(m)
	if !strings.HasSuffix(key, "#TESTSERIAL01") || !strings.HasPrefix(key, str(t, tiff, "Make")+" "+str(t, tiff, "Model")) {
		t.Errorf("DeviceKey = %q, want the fixture's make and model with #TESTSERIAL01", key)
	}
	captured, ok := Captured(m)
	if !ok || captured != fixedTime.Unix() {
		t.Errorf("Captured = %d, %v; want %d from DateTimeOriginal (file time %d)", captured, ok, fixedTime.Unix(), fi.ModTime.Unix())
	}
}

func TestFromImageIO_GPSSouthWest(t *testing.T) {
	props := map[string]any{
		"{GPS}": map[string]any{"Latitude": 10.5, "LatitudeRef": "S", "Longitude": 20.25, "LongitudeRef": "W"},
	}
	rec := FromImageIO(props, testFileInfo())
	if rec["EXIF:GPSLatitude"] != 10.5 || rec["EXIF:GPSLatitudeRef"] != "S" ||
		rec["EXIF:GPSLongitude"] != 20.25 || rec["EXIF:GPSLongitudeRef"] != "W" {
		t.Errorf("GPS keys = %v %v %v %v", rec["EXIF:GPSLatitude"], rec["EXIF:GPSLatitudeRef"], rec["EXIF:GPSLongitude"], rec["EXIF:GPSLongitudeRef"])
	}
	m := parseRecord(t, rec)
	if !m.HasGPS || math.Abs(m.GPSLatitude+10.5) > 1e-9 || math.Abs(m.GPSLongitude+20.25) > 1e-9 {
		t.Errorf("GPS = %v %v,%v; want -10.5,-20.25", m.HasGPS, m.GPSLatitude, m.GPSLongitude)
	}
}

func TestFromImageIO_NoExifFallsBackToMtime(t *testing.T) {
	props := loadDict(t, "imageio", "plain_jpeg.json")
	fi := testFileInfo()
	rec := FromImageIO(props, fi)
	if got, want := rec["File:FileModifyDate"], "2030:05:06 07:08:09+02:00"; got != want {
		t.Errorf("File:FileModifyDate = %v, want %v", got, want)
	}
	m := parseRecord(t, rec)
	captured, ok := Captured(m)
	if !ok || captured != fi.ModTime.Unix() {
		t.Errorf("Captured = %d, %v; want %d", captured, ok, fi.ModTime.Unix())
	}
	if got := DeviceKey(m); got != "Unknown" {
		t.Errorf("DeviceKey = %q, want Unknown", got)
	}
	if m.Width != int(num(t, props, "PixelWidth")) || m.Height != int(num(t, props, "PixelHeight")) {
		t.Errorf("size = %dx%d, want the fixture's PixelWidth x PixelHeight", m.Width, m.Height)
	}
}

func TestFromImageIO_OmitsEmptyValues(t *testing.T) {
	props := map[string]any{
		"PixelWidth": nil,
		"{TIFF}":     map[string]any{"Make": "", "Model": "  ", "DateTime": ""},
		"{Exif}": map[string]any{
			"BodySerialNumber": "",
			"ISOSpeedRatings":  []any{},
			"FNumber":          nil,
			"DateTimeOriginal": "",
		},
		"{ExifAux}": map[string]any{"SerialNumber": ""},
		"{GPS}":     map[string]any{},
	}
	rec := FromImageIO(props, testFileInfo())
	for k := range rec {
		if k != "SourceFile" && k != "File:FileModifyDate" {
			t.Errorf("record has %s = %#v; want it omitted", k, rec[k])
		}
	}
	if _, ok := rec["SourceFile"]; !ok {
		t.Error("record has no SourceFile")
	}
	if _, ok := rec["File:FileModifyDate"]; !ok {
		t.Error("record has no File:FileModifyDate")
	}
}
