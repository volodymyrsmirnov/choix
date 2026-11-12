package meta

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The native fixtures are public: they hold only the keys the mappers read
// and fixed synthetic values (R22).

// Allowed keys of an ImageIO dictionary: top level, then per sub-dictionary.
var imageIOAllowed = map[string][]string{
	"":          {"PixelWidth", "PixelHeight", "{TIFF}", "{Exif}", "{ExifAux}", "{GPS}"},
	"{TIFF}":    {"Make", "Model", "DateTime"},
	"{Exif}":    {"BodySerialNumber", "PixelXDimension", "PixelYDimension", "ISOSpeedRatings", "FNumber", "ExposureTime", "FocalLength", "DateTimeOriginal", "DateTimeDigitized"},
	"{ExifAux}": {"SerialNumber"},
	"{GPS}":     {"Latitude", "LatitudeRef", "Longitude", "LongitudeRef"},
}

// Allowed keys of an AVFoundation dictionary.
var avAllowed = []string{"make", "model", "software", "encoder", "location", "width", "height", "duration"}

// The fixtures the tests name; each must exist.
var nativeFixtures = map[string][]string{
	"imageio": {"fuji_raf_jpeg.json", "iphone_heic.json", "plain_jpeg.json", "dng_container.json"},
	"av":      {"iphone_mov.json", "dji_mp4.json"},
}

const (
	fixtureSerial = "TESTSERIAL01"
	fixtureDate   = "2026:01:02 03:04:05"
	fixtureLat    = 10.5
	fixtureLon    = 20.25
	maxFixtureStr = 64
)

func TestFixtures_OnlyAllowListedKeys(t *testing.T) {
	for dir, want := range nativeFixtures {
		files, err := filepath.Glob(filepath.Join("..", "..", "testdata", dir, "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range files {
			names = append(names, filepath.Base(f))
		}
		for _, w := range want {
			if !slices.Contains(names, w) {
				t.Errorf("testdata/%s/%s is missing", dir, w)
			}
		}
		for _, f := range files {
			t.Run(dir+"/"+filepath.Base(f), func(t *testing.T) {
				b, err := os.ReadFile(f) //nolint:gosec // a test fixture path from Glob
				if err != nil {
					t.Fatal(err)
				}
				var d map[string]any
				if err := json.Unmarshal(b, &d); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if dir == "imageio" {
					checkImageIOFixture(t, d)
				} else {
					checkAVFixture(t, d)
				}
				checkStrings(t, "", d)
			})
		}
	}
}

func checkImageIOFixture(t *testing.T, d map[string]any) {
	t.Helper()
	for k, v := range d {
		if !slices.Contains(imageIOAllowed[""], k) {
			t.Errorf("key %q is not allow-listed", k)
			continue
		}
		inner, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for ik, iv := range inner {
			if !slices.Contains(imageIOAllowed[k], ik) {
				t.Errorf("key %s.%s is not allow-listed", k, ik)
				continue
			}
			checkValue(t, k+"."+ik, ik, iv)
		}
	}
}

func checkAVFixture(t *testing.T, d map[string]any) {
	t.Helper()
	for k, v := range d {
		if !slices.Contains(avAllowed, k) {
			t.Errorf("key %q is not allow-listed", k)
			continue
		}
		if k == "location" {
			s, _ := v.(string)
			lat, lon, ok := parseISO6709(s)
			if !ok || math.Abs(lat) != fixtureLat || math.Abs(lon) != fixtureLon {
				t.Errorf("location is not the fixed pair (%v, %v)", fixtureLat, fixtureLon)
			}
		}
	}
}

// checkValue enforces the fixed synthetic values by key.
func checkValue(t *testing.T, where, key string, v any) {
	t.Helper()
	switch key {
	case "BodySerialNumber", "SerialNumber":
		if v != fixtureSerial {
			t.Errorf("%s is not %s", where, fixtureSerial)
		}
	case "DateTime", "DateTimeOriginal", "DateTimeDigitized":
		if v != fixtureDate {
			t.Errorf("%s is not %s", where, fixtureDate)
		}
	case "Latitude":
		if v != fixtureLat {
			t.Errorf("%s is not %v", where, fixtureLat)
		}
	case "Longitude":
		if v != fixtureLon {
			t.Errorf("%s is not %v", where, fixtureLon)
		}
	}
}

// checkStrings rejects any string longer than maxFixtureStr (maker-note
// blobs and the like), at any depth.
func checkStrings(t *testing.T, where string, v any) {
	t.Helper()
	switch x := v.(type) {
	case string:
		if len(x) > maxFixtureStr {
			t.Errorf("%s: string of %d characters", where, len(x))
		}
	case map[string]any:
		for k, iv := range x {
			checkStrings(t, where+"/"+k, iv)
		}
	case []any:
		for _, iv := range x {
			checkStrings(t, where+"[]", iv)
		}
	}
}
