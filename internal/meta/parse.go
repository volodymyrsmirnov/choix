package meta

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Metadata is the parsed, typed projection of a single exiftool record.
//
// Fields default to their zero values when the source EXIF/QuickTime is
// missing the corresponding tag. Callers must treat zero values as "unknown"
// rather than as semantically meaningful (e.g. ISO=0).
type Metadata struct {
	Make             string
	Model            string
	SerialNumber     string
	DateTimeOriginal time.Time
	CreateDate       time.Time
	ModifyDate       time.Time
	Width            int
	Height           int
	ISO              int
	Aperture         float64 // f-number, e.g. 5.6
	Shutter          string  // human-formatted, e.g. "1/250" or "2s"
	FocalLength      float64 // millimetres
	// GPS coordinates in signed decimal degrees. HasGPS is false when
	// either tag was absent; both fields are zero in that case so a caller
	// that forgets to check HasGPS won't accidentally render "0,0" — but
	// that location (Null Island) is a real point, so the explicit flag
	// is the only correct gate for downstream rendering.
	GPSLatitude  float64
	GPSLongitude float64
	HasGPS       bool
}

// exifTimeLayout is the canonical EXIF datetime format ("2025:08:14 18:32:11").
const exifTimeLayout = "2006:01:02 15:04:05"

// Parse decodes a single record from exiftool's `-j -G -n` JSON output.
//
// exiftool emits an array (one element per file). Parse expects exactly one
// element. The `-G` flag prefixes every key with its group name (e.g.
// "EXIF:Make", "QuickTime:CreateDate"); we tolerate prefix variation by
// stripping the group on lookup.
func Parse(jsonBytes []byte) (Metadata, error) {
	var records []map[string]any
	if err := json.Unmarshal(jsonBytes, &records); err != nil {
		return Metadata{}, fmt.Errorf("decode exiftool json: %w", err)
	}
	if len(records) == 0 {
		return Metadata{}, fmt.Errorf("exiftool json had no records")
	}
	raw := records[0]

	// Build an index keyed by the un-prefixed tag name. When the same tag
	// appears under multiple groups (e.g. EXIF:Make and QuickTime:Make),
	// the first non-empty value wins; deterministic group priority comes
	// from the lookup helpers below using explicit group keys.
	unprefixed := make(map[string]any, len(raw))
	for k, v := range raw {
		key := k
		if i := strings.Index(k, ":"); i >= 0 {
			key = k[i+1:]
		}
		if _, ok := unprefixed[key]; !ok {
			unprefixed[key] = v
		}
	}

	get := func(prefixedKeys ...string) any {
		for _, k := range prefixedKeys {
			if v, ok := raw[k]; ok && v != nil {
				return v
			}
		}
		// Fallback: try unprefixed lookup using the last key's tail.
		if len(prefixedKeys) > 0 {
			tail := prefixedKeys[len(prefixedKeys)-1]
			if i := strings.Index(tail, ":"); i >= 0 {
				tail = tail[i+1:]
			}
			if v, ok := unprefixed[tail]; ok && v != nil {
				return v
			}
		}
		return nil
	}

	m := Metadata{
		Make:         asString(get("EXIF:Make", "QuickTime:Make", "MakerNotes:Make")),
		Model:        asString(get("EXIF:Model", "QuickTime:Model", "MakerNotes:Model")),
		SerialNumber: asString(get("MakerNotes:SerialNumber", "EXIF:SerialNumber", "MakerNotes:InternalSerialNumber")),
		Width:        asInt(get("EXIF:ExifImageWidth", "EXIF:ImageWidth", "QuickTime:ImageWidth", "File:ImageWidth")),
		Height:       asInt(get("EXIF:ExifImageHeight", "EXIF:ImageHeight", "QuickTime:ImageHeight", "File:ImageHeight")),
		ISO:          asInt(get("EXIF:ISO", "EXIF:ISOSpeed")),
		Aperture:     asFloat(get("EXIF:FNumber", "Composite:Aperture")),
		FocalLength:  asFloat(get("EXIF:FocalLength", "Composite:FocalLength")),
	}

	// DJI drone MP4s and some action cams (GoPro, Insta360) omit the standard
	// Make/Model tags but stamp `QuickTime:Encoder = "<Brand> <Model>"`. When
	// neither field came back from the proper tags, fall back to splitting the
	// Encoder string. Requires two whitespace-separated tokens so single-token
	// software encoders like "Lavf60.16.100" or "x264" don't pollute the device key.
	if strings.TrimSpace(m.Make) == "" && strings.TrimSpace(m.Model) == "" {
		if enc := strings.TrimSpace(asString(get("QuickTime:Encoder"))); enc != "" {
			if i := strings.IndexAny(enc, " \t"); i > 0 {
				rest := strings.TrimSpace(enc[i+1:])
				if rest != "" {
					m.Make = enc[:i]
					m.Model = rest
				}
			}
		}
	}

	if exposure, ok := asFloatOK(get("EXIF:ExposureTime", "Composite:ShutterSpeed")); ok {
		m.Shutter = formatShutter(exposure)
	}

	// GPS — exiftool's `-n` flag emits the magnitude; the hemisphere is in
	// the Ref tags ("N"/"S" for latitude, "E"/"W" for longitude). Composite
	// fallbacks (`Composite:GPSLatitude`/`GPSLongitude`) carry a signed
	// value already, but only when both magnitude + ref were parseable, so
	// they're a safe second source.
	if lat, latOK := asFloatOK(get("EXIF:GPSLatitude", "QuickTime:GPSLatitude", "Composite:GPSLatitude")); latOK {
		if lon, lonOK := asFloatOK(get("EXIF:GPSLongitude", "QuickTime:GPSLongitude", "Composite:GPSLongitude")); lonOK {
			latRef := strings.ToUpper(strings.TrimSpace(asString(get("EXIF:GPSLatitudeRef", "QuickTime:GPSLatitudeRef"))))
			lonRef := strings.ToUpper(strings.TrimSpace(asString(get("EXIF:GPSLongitudeRef", "QuickTime:GPSLongitudeRef"))))
			if latRef == "S" && lat > 0 {
				lat = -lat
			}
			if lonRef == "W" && lon > 0 {
				lon = -lon
			}
			// Coordinates outside legal range are exiftool noise; drop them.
			if lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && !(lat == 0 && lon == 0) {
				m.GPSLatitude = lat
				m.GPSLongitude = lon
				m.HasGPS = true
			}
		}
	}

	m.DateTimeOriginal = parseExifTime(asString(get("EXIF:DateTimeOriginal"))) // photos
	m.CreateDate = parseExifTime(asString(get("EXIF:CreateDate", "QuickTime:CreateDate")))
	m.ModifyDate = parseExifTime(asString(get("EXIF:ModifyDate", "QuickTime:ModifyDate", "File:FileModifyDate")))

	return m, nil
}

// formatShutter converts a numeric exposure time (seconds) to the canonical
// human form: "1/250" for sub-second, "2s" / "1.5s" for ≥1s, "" for zero.
func formatShutter(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	if seconds >= 1 {
		return fmt.Sprintf("%gs", seconds)
	}
	denom := int(math.Round(1.0 / seconds))
	if denom <= 0 {
		return ""
	}
	return fmt.Sprintf("1/%d", denom)
}

// parseExifTime parses an EXIF-format datetime string. Unparseable or empty
// inputs yield the zero time.Time, which callers detect via t.IsZero().
//
// EXIF datetimes are timezone-naive; we treat them as UTC. The choix UI
// renders timestamps in the user's local TZ; we never compare across files
// using this datetime alone, so the UTC anchor is internally consistent.
func parseExifTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// Some exiftool outputs append a "Z" or offset (e.g. "2025:08:14 18:32:11+02:00").
	// Try the bare layout first, then variants with timezone offsets.
	if t, err := time.Parse(exifTimeLayout, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(exifTimeLayout+"-07:00", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(exifTimeLayout+"Z07:00", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asInt(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		var n int
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func asFloat(v any) float64 {
	f, _ := asFloatOK(v)
	return f
}

func asFloatOK(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}
