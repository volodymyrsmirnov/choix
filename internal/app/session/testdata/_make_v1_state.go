//go:build ignore

// _make_v1_state.go writes the "folder last scanned by v1" fixture used by
// internal/app/session's tests (Review Focus 4):
//
//	testdata/v1-state.db       a state.db at user_version 5, shaped as v1
//	                           wrote it
//	testdata/v1-folder/        the folder it describes: six originals and the
//	                           two exported copies under exports/out/
//	testdata/v1-thumbs/        v1's thumbnail cache: the content of the
//	                           folder's .choix/thumbs/ (kept outside a
//	                           ".choix" directory, which git ignores)
//
// How to regenerate it is in testdata/README.md. It replaces v1-state.db,
// v1-folder/ and v1-thumbs/, and its output is deterministic.
//
// The schema comes from the current internal/store migrations (store.Open);
// the generator refuses to write anything other than user_version 5, so if a
// later migration is added the committed fixture stays v1's and this program
// must be changed deliberately. The rows are inserted by hand the way v1's
// engine stored them: exiftool-keyed raw_exif (gzipped `-j -G -n` JSON), the
// thumbs/ai_signals rows of v1's thumb and analyze stages, two clusters, two
// picks exported under exports/out/ and no cluster_params key in kv.
//
// Every value is synthetic: camera TESTCAM D1, serial TESTSERIAL01, capture
// date 2026-01-02, no GPS, flat-colour 64x48 JPEGs. The originals' and
// copies' and thumbnails' modification time is fixedMtime; git does not keep
// it, so the tests set it again after copying (session's sessionTestV1Mtime
// must equal fixedMtime).
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// fixedMtime is the modification time of every file in v1-folder.
var fixedMtime = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

const (
	outDir     = "internal/app/session/testdata"
	dbName     = "v1-state.db"
	folderName = "v1-folder"
	thumbsName = "v1-thumbs"
	exportDir  = "exports/out"
	deviceKey  = "TESTCAM D1#TESTSERIAL01"
	width      = 64
	height     = 48
)

type original struct {
	id       int64
	rel      string
	captured time.Time
	rgb      color.RGBA
	picked   bool
}

func originals() []original {
	day := func(h, m, s int) time.Time { return time.Date(2026, 1, 2, h, m, s, 0, time.UTC) }
	return []original{
		{1, "DCIM/100TEST/TEST0001.JPG", day(10, 0, 0), color.RGBA{200, 40, 40, 255}, false},
		{2, "DCIM/100TEST/TEST0002.JPG", day(10, 0, 5), color.RGBA{40, 200, 40, 255}, true},
		{3, "DCIM/100TEST/TEST0003.JPG", day(10, 0, 10), color.RGBA{40, 40, 200, 255}, false},
		{4, "DCIM/100TEST/TEST0004.JPG", day(14, 0, 0), color.RGBA{200, 200, 40, 255}, false},
		{5, "DCIM/100TEST/TEST0005.JPG", day(14, 0, 5), color.RGBA{40, 200, 200, 255}, true},
		{6, "DCIM/100TEST/TEST0006.JPG", day(14, 0, 10), color.RGBA{200, 40, 200, 255}, false},
	}
}

// v1's clusters: ids left behind by its per-bucket rebuilds, one per burst.
var clusters = []struct {
	id      int64
	bucket  time.Time
	members []int64
}{
	{5, time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC), []int64{1, 2, 3}},
	{6, time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC), []int64{4, 5, 6}},
}

func main() {
	if _, err := os.Stat("go.mod"); err != nil {
		log.Fatal("run from the module root (see testdata/README.md)")
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	folder := filepath.Join(outDir, folderName)
	thumbs := filepath.Join(outDir, thumbsName)
	dbPath := filepath.Join(outDir, dbName)
	for _, p := range []string{folder, thumbs, dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	if err := fill(st, folder, thumbs); err != nil {
		_ = st.Close()
		return err
	}
	if err := st.Close(); err != nil {
		return err
	}
	for _, p := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s left behind: the database was not checkpointed", p)
		}
	}
	fmt.Println("wrote", dbPath, folder, "and", thumbs)
	return nil
}

func fill(st *store.Store, folder, thumbs string) error {
	ctx := context.Background()
	db := st.DB()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != 5 {
		return fmt.Errorf("store migrates to user_version %d; the v1 fixture must stay at 5", version)
	}

	for _, o := range originals() {
		data, err := encodeJPEG(o.rgb)
		if err != nil {
			return err
		}
		abs := filepath.Join(folder, filepath.FromSlash(o.rel))
		if err := writeFile(abs, data); err != nil {
			return err
		}
		hash, err := scanner.ContentHash(abs)
		if err != nil {
			return err
		}
		raw, err := exiftoolRecord(o)
		if err != nil {
			return err
		}
		m, err := meta.Parse(raw)
		if err != nil {
			return err
		}
		if k := meta.DeviceKey(m); k != deviceKey {
			return fmt.Errorf("raw_exif parses to device %q, want %q", k, deviceKey)
		}
		gz, err := gzipBytes(raw)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO files (id, path, size, mtime, content_hash, kind, format,
			                   device_key, captured_at, width, height, iso, aperture,
			                   shutter, focal_length, raw_exif, scan_status, error)
			VALUES (?, ?, ?, ?, ?, 'photo', 'jpeg', ?, ?, ?, ?, 200, 5.6, '1/250', 23, ?, 'analyzed', NULL)`,
			o.id, o.rel, len(data), fixedMtime.Unix(), hash, deviceKey, o.captured.Unix(),
			width, height, gz); err != nil {
			return fmt.Errorf("insert %s: %w", o.rel, err)
		}
		for i, tier := range []string{"thumb", "preview"} {
			bucket := fmt.Sprintf("%02x", o.id%256)
			name := fmt.Sprintf("%d-%s.jpg", o.id, tier)
			rel := path.Join(".choix/thumbs", bucket, name)
			// v1's cached tiers: never scaled up from a 64x48 original, so
			// both are 64x48; a lower quality keeps their bytes distinct
			// from the original's and from each other.
			img, err := encodeJPEGQuality(o.rgb, 70-10*i)
			if err != nil {
				return err
			}
			if err := writeFile(filepath.Join(thumbs, bucket, name), img); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO thumbs (file_id, tier, rel_path, width, height) VALUES (?, ?, ?, ?, ?)`,
				o.id, tier, rel, width, height); err != nil {
				return err
			}
		}
		// v1 that computed no embedding: the analyze stage wrote the row with
		// NULL in the legacy embedding column (clip_embedding), which 1.x and
		// 2.0.0 wrote.
		if _, err := db.ExecContext(ctx,
			`INSERT INTO ai_signals (file_id, clip_embedding, computed_at) VALUES (?, NULL, ?)`,
			o.id, fixedMtime.Add(30*time.Minute).Unix()); err != nil {
			return err
		}
		if o.picked {
			exported := path.Join(exportDir, o.rel)
			if err := writeFile(filepath.Join(folder, filepath.FromSlash(exported)), data); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO picks (file_id, state, rating, picked_at, exported_path) VALUES (?, 'picked', NULL, ?, ?)`,
				o.id, fixedMtime.Add(time.Hour).Unix(), exported); err != nil {
				return err
			}
		}
	}
	for _, c := range clusters {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO clusters (id, device_key, time_bucket) VALUES (?, ?, ?)`,
			c.id, deviceKey, c.bucket.Unix()); err != nil {
			return err
		}
		for _, m := range c.members {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO cluster_members (cluster_id, file_id) VALUES (?, ?)`, c.id, m); err != nil {
				return err
			}
		}
	}
	return nil
}

// exiftoolRecord returns the `exiftool -j -G -n` record v1 stored for o.
func exiftoolRecord(o original) ([]byte, error) {
	ts := o.captured.Format("2006:01:02 15:04:05")
	rec := map[string]any{
		"SourceFile":                       "/Users/test/Pictures/v1-folder/" + o.rel,
		"ExifTool:ExifToolVersion":         12.76,
		"File:FileName":                    path.Base(o.rel),
		"File:FileModifyDate":              fixedMtime.Format("2006:01:02 15:04:05-07:00"),
		"File:FileType":                    "JPEG",
		"File:MIMEType":                    "image/jpeg",
		"File:ImageWidth":                  width,
		"File:ImageHeight":                 height,
		"EXIF:Make":                        "TESTCAM",
		"EXIF:Model":                       "D1",
		"EXIF:SerialNumber":                "TESTSERIAL01",
		"EXIF:Orientation":                 1,
		"EXIF:ExposureTime":                0.004,
		"EXIF:FNumber":                     5.6,
		"EXIF:ISO":                         200,
		"EXIF:DateTimeOriginal":            ts,
		"EXIF:CreateDate":                  ts,
		"EXIF:ModifyDate":                  ts,
		"EXIF:FocalLength":                 23,
		"EXIF:ExifImageWidth":              width,
		"EXIF:ExifImageHeight":             height,
		"Composite:ShutterSpeed":           0.004,
		"Composite:ImageSize":              fmt.Sprintf("%d %d", width, height),
		"Composite:Megapixels":             0.003072,
		"Composite:SubSecDateTimeOriginal": ts,
	}
	return json.Marshal([]map[string]any{rec})
}

func encodeJPEG(c color.RGBA) ([]byte, error) { return encodeJPEGQuality(c, 85) }

func encodeJPEGQuality(c color.RGBA, quality int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeFile(abs string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return err
	}
	return os.Chtimes(abs, fixedMtime, fixedMtime)
}

func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
