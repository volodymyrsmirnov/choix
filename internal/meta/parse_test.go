package meta

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseIPhoneHEIC(t *testing.T) {
	m, err := Parse(loadFixture(t, "iphone_heic.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Make != "Apple" || m.Model != "iPhone 15 Pro" {
		t.Errorf("make/model: got %q / %q", m.Make, m.Model)
	}
	if m.SerialNumber != "F2LZQ8XKQM" {
		t.Errorf("serial: got %q", m.SerialNumber)
	}
	want := time.Date(2025, 8, 14, 18, 32, 11, 0, time.UTC)
	if !m.DateTimeOriginal.Equal(want) {
		t.Errorf("DateTimeOriginal: got %v want %v", m.DateTimeOriginal, want)
	}
	if m.Width != 4032 || m.Height != 3024 {
		t.Errorf("dimensions: got %dx%d", m.Width, m.Height)
	}
	if m.ISO != 64 {
		t.Errorf("iso: got %d", m.ISO)
	}
	if m.Aperture < 1.77 || m.Aperture > 1.79 {
		t.Errorf("aperture: got %v", m.Aperture)
	}
	if m.Shutter != "1/250" {
		t.Errorf("shutter: got %q want 1/250", m.Shutter)
	}
	if m.FocalLength < 6.85 || m.FocalLength > 6.87 {
		t.Errorf("focal length: got %v", m.FocalLength)
	}
}

func TestParseFujiRAF(t *testing.T) {
	m, err := Parse(loadFixture(t, "fuji_raf.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Make != "FUJIFILM" || m.Model != "X-T5" {
		t.Errorf("make/model: got %q / %q", m.Make, m.Model)
	}
	if m.SerialNumber != "A1B2C3D4" {
		t.Errorf("serial: got %q", m.SerialNumber)
	}
	if m.Width != 7728 || m.Height != 5152 {
		t.Errorf("dimensions: got %dx%d", m.Width, m.Height)
	}
	if m.Shutter != "1/200" {
		t.Errorf("shutter: got %q want 1/200", m.Shutter)
	}
	if m.FocalLength != 56.0 {
		t.Errorf("focal length: got %v want 56", m.FocalLength)
	}
}

func TestParseDroneMOV(t *testing.T) {
	m, err := Parse(loadFixture(t, "drone_mov.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Make != "DJI" || m.Model != "FC7303" {
		t.Errorf("make/model: got %q / %q", m.Make, m.Model)
	}
	if m.SerialNumber != "" {
		t.Errorf("serial: expected empty, got %q", m.SerialNumber)
	}
	want := time.Date(2026, 1, 9, 14, 5, 20, 0, time.UTC)
	if !m.CreateDate.Equal(want) {
		t.Errorf("CreateDate: got %v want %v", m.CreateDate, want)
	}
	if m.Width != 3840 || m.Height != 2160 {
		t.Errorf("dimensions: got %dx%d", m.Width, m.Height)
	}
	if m.ISO != 0 || m.Aperture != 0 || m.Shutter != "" || m.FocalLength != 0 {
		t.Errorf("expected zero photo fields on video: %+v", m)
	}
}

func TestParseGPS(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantHasGPS bool
		wantLat    float64
		wantLon    float64
		latTol     float64
	}{
		{
			name:       "northern east absolute",
			body:       `[{"EXIF:GPSLatitude": 52.5200, "EXIF:GPSLongitude": 13.4050, "EXIF:GPSLatitudeRef": "N", "EXIF:GPSLongitudeRef": "E"}]`,
			wantHasGPS: true, wantLat: 52.5200, wantLon: 13.4050, latTol: 0.0001,
		},
		{
			name:       "southern west flips sign via ref",
			body:       `[{"EXIF:GPSLatitude": 33.8688, "EXIF:GPSLongitude": 151.2093, "EXIF:GPSLatitudeRef": "S", "EXIF:GPSLongitudeRef": "W"}]`,
			wantHasGPS: true, wantLat: -33.8688, wantLon: -151.2093, latTol: 0.0001,
		},
		{
			name:       "composite supplies signed value when only that group is present",
			body:       `[{"Composite:GPSLatitude": -33.8688, "Composite:GPSLongitude": 151.2093}]`,
			wantHasGPS: true, wantLat: -33.8688, wantLon: 151.2093, latTol: 0.0001,
		},
		{
			name:       "no gps tags",
			body:       `[{"EXIF:Make": "Apple"}]`,
			wantHasGPS: false,
		},
		{
			name:       "null island filtered out",
			body:       `[{"EXIF:GPSLatitude": 0, "EXIF:GPSLongitude": 0, "EXIF:GPSLatitudeRef": "N", "EXIF:GPSLongitudeRef": "E"}]`,
			wantHasGPS: false,
		},
		{
			name:       "out of range filtered",
			body:       `[{"EXIF:GPSLatitude": 200, "EXIF:GPSLongitude": 13.4050, "EXIF:GPSLatitudeRef": "N", "EXIF:GPSLongitudeRef": "E"}]`,
			wantHasGPS: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := Parse([]byte(c.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if m.HasGPS != c.wantHasGPS {
				t.Fatalf("HasGPS = %v want %v", m.HasGPS, c.wantHasGPS)
			}
			if !c.wantHasGPS {
				return
			}
			if m.GPSLatitude < c.wantLat-c.latTol || m.GPSLatitude > c.wantLat+c.latTol {
				t.Errorf("GPSLatitude = %v want %v ±%v", m.GPSLatitude, c.wantLat, c.latTol)
			}
			if m.GPSLongitude < c.wantLon-c.latTol || m.GPSLongitude > c.wantLon+c.latTol {
				t.Errorf("GPSLongitude = %v want %v ±%v", m.GPSLongitude, c.wantLon, c.latTol)
			}
		})
	}
}

func TestParseEmptyJSON(t *testing.T) {
	if _, err := Parse([]byte(`[]`)); err == nil {
		t.Error("expected error on empty array, got nil")
	}
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Error("expected error on garbage input, got nil")
	}
}
