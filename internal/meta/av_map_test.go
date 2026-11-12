package meta

import (
	"math"
	"testing"
	"time"
)

func TestFromAVAsset_Location(t *testing.T) {
	cases := []struct {
		location string
		lat, lon float64
	}{
		{"+10.5000+020.2500+010.000/", 10.5, 20.25},
		{"-10.5000-020.2500/", -10.5, -20.25},
	}
	for _, tc := range cases {
		t.Run(tc.location, func(t *testing.T) {
			m := parseRecord(t, FromAVAsset(map[string]any{"location": tc.location}, MovieHeader{}, testFileInfo()))
			if !m.HasGPS || math.Abs(m.GPSLatitude-tc.lat) > 1e-9 || math.Abs(m.GPSLongitude-tc.lon) > 1e-9 {
				t.Errorf("GPS = %v %v,%v; want %v,%v", m.HasGPS, m.GPSLatitude, m.GPSLongitude, tc.lat, tc.lon)
			}
		})
	}
	for _, bad := range []string{"", "garbage", "+10.5/", "+1x.5+020.25/", "+95.0+020.0/"} {
		t.Run("invalid "+bad, func(t *testing.T) {
			rec := FromAVAsset(map[string]any{"location": bad}, MovieHeader{}, testFileInfo())
			if _, ok := rec["Composite:GPSLatitude"]; ok {
				t.Errorf("location %q gave Composite:GPSLatitude %v", bad, rec["Composite:GPSLatitude"])
			}
		})
	}
}

func TestFromAVAsset_IPhoneMOV(t *testing.T) {
	props := loadDict(t, "av", "iphone_mov.json")
	mh := MovieHeader{Created: fixedTime, Modified: fixedTime.Add(time.Second)}
	rec := FromAVAsset(props, mh, testFileInfo())
	if got := rec["QuickTime:CreateDate"]; got != "2026:01:02 03:04:05" {
		t.Errorf("QuickTime:CreateDate = %v", got)
	}
	m := parseRecord(t, rec)
	if got, want := DeviceKey(m), str(t, props, "make")+" "+str(t, props, "model")+"#"; got != want {
		t.Errorf("DeviceKey = %q, want %q", got, want)
	}
	if c, ok := Captured(m); !ok || c != fixedTime.Unix() {
		t.Errorf("Captured = %d, %v; want %d from the movie header", c, ok, fixedTime.Unix())
	}
	if m.Width != int(num(t, props, "width")) || m.Height != int(num(t, props, "height")) {
		t.Errorf("size = %dx%d, want the fixture's width x height", m.Width, m.Height)
	}
	if !m.HasGPS || m.GPSLatitude != 10.5 || m.GPSLongitude != 20.25 {
		t.Errorf("GPS = %v %v,%v; want 10.5,20.25", m.HasGPS, m.GPSLatitude, m.GPSLongitude)
	}
}

func TestFromAVAsset_EncoderFallback(t *testing.T) {
	dji := loadDict(t, "av", "dji_mp4.json")
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"encoder", map[string]any{"encoder": "ACME D100"}, "ACME D100#"},
		{"software when no encoder", map[string]any{"software": "ACME D100"}, "ACME D100#"},
		{"encoder before software", map[string]any{"encoder": "ACME D100", "software": "OTHER X1"}, "ACME D100#"},
		{"single-token encoder", map[string]any{"encoder": "Lavf61.7.100"}, "Unknown"},
		{"make and model win", map[string]any{"make": "TESTCAM", "model": "T1", "encoder": "ACME D100"}, "TESTCAM T1#"},
		{"dji fixture", dji, str(t, dji, "encoder") + "#"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeviceKey(parseRecord(t, FromAVAsset(tc.props, MovieHeader{}, testFileInfo()))); got != tc.want {
				t.Errorf("DeviceKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFromAVAsset_ZeroHeaderUsesMtime(t *testing.T) {
	fi := testFileInfo()
	rec := FromAVAsset(map[string]any{"width": 64.0, "height": 36.0}, MovieHeader{}, fi)
	for _, k := range []string{"QuickTime:CreateDate", "QuickTime:ModifyDate"} {
		if _, ok := rec[k]; ok {
			t.Errorf("record has %s = %v for a zero header", k, rec[k])
		}
	}
	m := parseRecord(t, rec)
	if c, ok := Captured(m); !ok || c != fi.ModTime.Unix() {
		t.Errorf("Captured = %d, %v; want the file time %d", c, ok, fi.ModTime.Unix())
	}
	if m.Width != 64 || m.Height != 36 {
		t.Errorf("size = %dx%d, want 64x36", m.Width, m.Height)
	}
}
