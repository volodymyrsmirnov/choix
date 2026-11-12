package meta

import (
	"strconv"
	"strings"
)

// avKeys maps AVFoundation property names (platform.Video.Properties) to
// exiftool keys.
var avKeys = []struct{ exiftool, key string }{
	{"QuickTime:Make", "make"},
	{"QuickTime:Model", "model"},
	{"QuickTime:ImageWidth", "width"},
	{"QuickTime:ImageHeight", "height"},
}

// FromAVAsset turns AVFoundation's properties of a video and its movie
// header into a record with the keys exiftool's `-j -G -n` output uses, so
// that Parse reads it like a v1 record. A key is emitted only when its
// source value is present and not empty; numbers stay numbers. The record
// always has SourceFile and File:FileModifyDate.
//
// QuickTime:Encoder is the encoder string, else the software string (a
// drone's ©too arrives as either). The dates are the movie header's, in UTC
// like exiftool's (a zero time is omitted), and the ISO 6709 location gives
// signed Composite:GPSLatitude/GPSLongitude.
func FromAVAsset(props map[string]any, mh MovieHeader, fi FileInfo) map[string]any {
	rec := newRecord(fi)
	for _, k := range avKeys {
		if v, ok := present(props[k.key]); ok {
			rec[k.exiftool] = v
		}
	}
	if v, ok := present(props["encoder"]); ok {
		rec["QuickTime:Encoder"] = v
	} else if v, ok := present(props["software"]); ok {
		rec["QuickTime:Encoder"] = v
	}
	if !mh.Created.IsZero() {
		rec["QuickTime:CreateDate"] = mh.Created.UTC().Format(exifTimeLayout)
	}
	if !mh.Modified.IsZero() {
		rec["QuickTime:ModifyDate"] = mh.Modified.UTC().Format(exifTimeLayout)
	}
	if loc, ok := props["location"].(string); ok {
		if lat, lon, ok := parseISO6709(loc); ok {
			rec["Composite:GPSLatitude"] = lat
			rec["Composite:GPSLongitude"] = lon
		}
	}
	return rec
}

// parseISO6709 reads the latitude and longitude of an ISO 6709 point such
// as "+10.5000+020.2500+010.000/" as signed decimal degrees. Degrees,
// degrees-minutes and degrees-minutes-seconds forms are accepted (±DD.D,
// ±DDMM.M, ±DDMMSS.S for latitude; one more degree digit for longitude);
// altitude and anything after it are ignored.
func parseISO6709(s string) (lat, lon float64, ok bool) {
	s = strings.TrimSpace(s)
	latStr, rest, ok := signedField(s)
	if !ok {
		return 0, 0, false
	}
	lonStr, _, ok := signedField(rest)
	if !ok {
		return 0, 0, false
	}
	lat, ok = iso6709Angle(latStr, 2)
	if !ok || lat < -90 || lat > 90 {
		return 0, 0, false
	}
	lon, ok = iso6709Angle(lonStr, 3)
	if !ok || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

// signedField splits s after its first field: a sign followed by digits and
// at most one dot.
func signedField(s string) (field, rest string, ok bool) {
	if s == "" || (s[0] != '+' && s[0] != '-') {
		return "", "", false
	}
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 1 {
		return "", "", false
	}
	return s[:i], s[i:], true
}

// iso6709Angle converts one signed field whose degree part has degDigits
// digits into decimal degrees.
func iso6709Angle(field string, degDigits int) (float64, bool) {
	sign := 1.0
	if field[0] == '-' {
		sign = -1
	}
	body := field[1:]
	intPart, frac, _ := strings.Cut(body, ".")
	if strings.Contains(frac, ".") {
		return 0, false
	}
	parse := func(s string) (float64, bool) {
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	var deg, minutes, seconds float64
	var ok bool
	switch len(intPart) {
	case degDigits:
		deg, ok = parse(body)
	case degDigits + 2:
		if deg, ok = parse(intPart[:degDigits]); ok {
			minutes, ok = parse(body[degDigits:])
		}
	case degDigits + 4:
		if deg, ok = parse(intPart[:degDigits]); ok {
			if minutes, ok = parse(intPart[degDigits : degDigits+2]); ok {
				seconds, ok = parse(body[degDigits+2:])
			}
		}
	}
	if !ok || minutes >= 60 || seconds >= 60 {
		return 0, false
	}
	return sign * (deg + minutes/60 + seconds/3600), true
}
