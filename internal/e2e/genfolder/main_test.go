//go:build darwin

package main

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/imageio"
)

// TestGenFolder_WritesDatedJPEGs generates the E2E specs' default folder and
// reads every file back through the app's own metadata path (ImageIO's
// native record, meta.Parse): 24 decodable 640×480 JPEGs, two device keys,
// every capture time inside one of the three 10-minute bursts and every
// burst used.
func TestGenFolder_WritesDatedJPEGs(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	if code := run([]string{"-out", dir, "-n", "24"}, &stderr); code != 0 {
		t.Fatalf("run exit code %d, stderr: %s", code, stderr.String())
	}

	names, err := filepath.Glob(filepath.Join(dir, "*.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 24 {
		t.Fatalf("got %d JPEGs, want 24", len(names))
	}

	reader := &meta.NativeReader{Stills: imageio.New()}
	devices := map[string]int{}
	bursts := map[int]int{}
	colours := map[[3]uint32]string{}
	for _, name := range names {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s: decode: %v", filepath.Base(name), err)
		}
		if b := img.Bounds(); b.Dx() != 640 || b.Dy() != 480 {
			t.Errorf("%s: size %dx%d, want 640x480", filepath.Base(name), b.Dx(), b.Dy())
		}
		r, g, b, _ := img.At(320, 240).RGBA()
		key := [3]uint32{r >> 8, g >> 8, b >> 8}
		if other, dup := colours[key]; dup {
			t.Errorf("%s and %s have the same colour %v", other, filepath.Base(name), key)
		}
		colours[key] = filepath.Base(name)

		rec, err := reader.Read(context.Background(), name)
		if err != nil {
			t.Fatalf("%s: native record: %v", filepath.Base(name), err)
		}
		m, err := meta.Parse(rec)
		if err != nil {
			t.Fatalf("%s: parse: %v", filepath.Base(name), err)
		}
		dk := meta.DeviceKey(m)
		if dk == "Unknown" || !strings.Contains(dk, "#SN-") {
			t.Errorf("%s: device key %q, want make, model and serial", filepath.Base(name), dk)
		}
		devices[dk]++

		at, ok := meta.Captured(m)
		if !ok {
			t.Fatalf("%s: no capture time", filepath.Base(name))
		}
		k, in := burstOf(time.Unix(at, 0).UTC())
		if !in {
			t.Errorf("%s: captured %s, outside every burst", filepath.Base(name), time.Unix(at, 0).UTC())
		}
		bursts[k]++
	}
	if len(devices) != 2 {
		t.Errorf("device keys %v, want 2", devices)
	}
	for _, n := range devices {
		if n != 12 {
			t.Errorf("device keys %v, want 12 files each", devices)
			break
		}
	}
	if len(bursts) != 3 {
		t.Errorf("bursts used %v, want 3", bursts)
	}
}

// burstOf returns the burst t falls in (start + k·2 h, 10 minutes long).
func burstOf(t time.Time) (int, bool) {
	start := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	for k := range 3 {
		b := start.Add(time.Duration(k) * 2 * time.Hour)
		if !t.Before(b) && t.Before(b.Add(10*time.Minute)) {
			return k, true
		}
	}
	return -1, false
}

// TestGenFolder_Options covers -cameras and -bursts and the burst layout:
// shots of one burst are spread over its ten minutes, in order.
func TestGenFolder_Options(t *testing.T) {
	shots := plan(30, 3, 2)
	if len(shots) != 30 {
		t.Fatalf("got %d shots, want 30", len(shots))
	}
	cams := map[int]int{}
	for i, s := range shots {
		cams[s.camera]++
		k, in := burstOf(s.at)
		if !in || k > 1 {
			t.Errorf("shot %d at %s: not inside burst 0 or 1", i, s.at)
		}
		if i > 0 && !s.at.After(shots[i-1].at) {
			t.Errorf("shot %d at %s is not after shot %d at %s", i, s.at, i-1, shots[i-1].at)
		}
	}
	if len(cams) != 3 || cams[0] != 10 || cams[1] != 10 || cams[2] != 10 {
		t.Errorf("shots per camera %v, want 10 each on 3 cameras", cams)
	}
}

// TestGenFolder_RefusesBadFlags: a missing -out, a non-positive count or
// option, and an -out that is a file all exit 2 without writing anything.
func TestGenFolder_RefusesBadFlags(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-n", "3"},
		{"-out", dir, "-n", "0"},
		{"-out", dir, "-n", "3", "-cameras", "0"},
		{"-out", dir, "-n", "3", "-bursts", "0"},
		{"-out", dir, "-n", "3", "extra"},
		{"-out", file, "-n", "3"},
	} {
		var stderr bytes.Buffer
		if code := run(args, &stderr); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
	got, err := filepath.Glob(filepath.Join(dir, "*.jpg"))
	if err != nil || len(got) != 0 {
		t.Errorf("JPEGs written for refused flags: %v (%v)", got, err)
	}
}

// TestGenFolder_RefusesExisting: a file already at a name genfolder would
// write is left alone and the run fails.
func TestGenFolder_RefusesExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, fileName(0))
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"-out", dir, "-n", "2"}, &stderr); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if b, err := os.ReadFile(existing); err != nil || string(b) != "keep" {
		t.Errorf("existing file changed: %q (%v)", b, err)
	}
}

// TestExifSegment checks the APP1 segment's framing and that every value
// it was given is in it.
func TestExifSegment(t *testing.T) {
	at := time.Date(2026, 3, 4, 8, 1, 2, 0, time.UTC)
	seg := exifSegment(camera{make: "Acme", model: "Model One", serial: "SN-0001"}, at)
	if seg[0] != 0xFF || seg[1] != 0xE1 {
		t.Fatalf("marker % x, want ff e1", seg[:2])
	}
	if n := int(seg[2])<<8 | int(seg[3]); n != len(seg)-2 {
		t.Errorf("segment length %d, want %d", n, len(seg)-2)
	}
	if !bytes.HasPrefix(seg[4:], []byte("Exif\x00\x00II*\x00")) {
		t.Errorf("header % x, want Exif + little-endian TIFF", seg[4:14])
	}
	for _, want := range []string{"Acme\x00", "Model One\x00", "SN-0001\x00", "2026:03:04 08:01:02\x00"} {
		if !bytes.Contains(seg, []byte(want)) {
			t.Errorf("segment has no %q", want)
		}
	}
}
