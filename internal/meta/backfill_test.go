package meta

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func gzipForTest(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestBackfillDeviceKey covers the DJI MP4 case — a row landed with
// device_key='Unknown' before the parser learned the QuickTime:Encoder
// fallback, but the cached raw_exif blob still carries the Encoder tag.
// The backfill should rewrite the device_key without touching rows that
// already have a real key or whose blob still resolves to Unknown.
func TestBackfillDeviceKey(t *testing.T) {
	st := store.NewTestStore(t)
	ctx := context.Background()

	insert := func(path, deviceKey string, rawExif []byte) int64 {
		t.Helper()
		id, err := st.Files().Insert(store.File{
			Path:        path,
			Size:        1,
			Mtime:       1,
			ContentHash: path,
			Kind:        "video",
			Format:      "mp4",
			ScanStatus:  "metadata",
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", path, err)
		}
		_, err = st.DB().ExecContext(ctx,
			`UPDATE files SET device_key = ?, raw_exif = ? WHERE id = ?`,
			sql.NullString{String: deviceKey, Valid: true}, rawExif, id)
		if err != nil {
			t.Fatalf("seed device_key: %v", err)
		}
		return id
	}

	djiBlob := gzipForTest(t, []byte(`[{"QuickTime:Encoder": "DJI Air3s"}]`))
	emptyBlob := gzipForTest(t, []byte(`[{}]`))

	dji := insert("Drone/DJI_0001.MP4", "Unknown", djiBlob)
	empty := insert("Drone/DJI_0002.MP4", "Unknown", emptyBlob) // still Unknown after re-parse
	already := insert("Cam/IMG_0001.RAF", "FUJIFILM X-T5#A1B2", djiBlob)

	n, err := BackfillDeviceKey(ctx, st)
	if err != nil {
		t.Fatalf("BackfillDeviceKey: %v", err)
	}
	if n != 1 {
		t.Errorf("backfilled rows = %d, want 1", n)
	}

	get := func(id int64) string {
		t.Helper()
		f, err := st.Files().GetByID(id)
		if err != nil {
			t.Fatalf("GetByID %d: %v", id, err)
		}
		if !f.DeviceKey.Valid {
			return ""
		}
		return f.DeviceKey.String
	}

	if got := get(dji); got != "DJI Air3s#" {
		t.Errorf("dji device_key = %q, want %q", got, "DJI Air3s#")
	}
	if got := get(empty); got != "Unknown" {
		t.Errorf("empty device_key = %q, want Unknown", got)
	}
	if got := get(already); got != "FUJIFILM X-T5#A1B2" {
		t.Errorf("already device_key = %q, want it untouched", got)
	}

	// Idempotent re-run.
	n2, err := BackfillDeviceKey(ctx, st)
	if err != nil {
		t.Fatalf("BackfillDeviceKey 2nd run: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second-run rows = %d, want 0", n2)
	}
}
