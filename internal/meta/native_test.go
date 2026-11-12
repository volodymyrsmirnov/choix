package meta

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// readParse runs r.Read on path and parses the record it returns.
func readParse(t *testing.T, r Reader, path string) (Metadata, map[string]any) {
	t.Helper()
	out, err := r.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var recs []map[string]any
	if err := json.Unmarshal(out, &recs); err != nil || len(recs) != 1 {
		t.Fatalf("Read returned %d records, %v; want one", len(recs), err)
	}
	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m, recs[0]
}

// propertiesCalls returns the Properties calls a fake received.
func propertiesCalls(s *platformtest.Stills) []platformtest.StillsCall {
	var out []platformtest.StillsCall
	for _, c := range s.Calls() {
		if c.Method == "Properties" {
			out = append(out, c)
		}
	}
	return out
}

func fujiDeviceKey(t *testing.T) string {
	t.Helper()
	tiff := sub(loadDict(t, "imageio", "fuji_raf_jpeg.json"), "{TIFF}")
	return str(t, tiff, "Make") + " " + str(t, tiff, "Model") + "#TESTSERIAL01"
}

func TestNativeReader_RAFUsesEmbeddedJPEG(t *testing.T) {
	embedded := platformtest.MakeJPEG(t, 160, 107)
	path := platformtest.MakeRAF(t, embedded)
	stills := &platformtest.Stills{
		Props: map[string]map[string]any{"": loadDict(t, "imageio", "fuji_raf_jpeg.json")},
		Fail:  map[string]error{path: errors.New("the RAF container must not be read")},
	}
	r := &NativeReader{Stills: stills, Video: &platformtest.Video{}}

	m, rec := readParse(t, r, path)
	calls := propertiesCalls(stills)
	if len(calls) != 1 || calls[0].Path != "" || !bytes.Equal(calls[0].Data, embedded) {
		t.Fatalf("Properties calls = %d (first Path %q); want one, with Data equal to the embedded JPEG", len(calls), firstPath(calls))
	}
	if got, want := DeviceKey(m), fujiDeviceKey(t); got != want {
		t.Errorf("DeviceKey = %q, want %q", got, want)
	}
	if rec["SourceFile"] != filepath.Base(path) || rec["File:FileModifyDate"] == nil {
		t.Errorf("SourceFile = %v, File:FileModifyDate = %v", rec["SourceFile"], rec["File:FileModifyDate"])
	}

	t.Run("no preview reads the container", func(t *testing.T) {
		path := platformtest.MakeRAF(t, []byte("not a jpeg at all"))
		stills := &platformtest.Stills{Props: map[string]map[string]any{path: {"PixelWidth": 7728, "PixelHeight": 5152}}}
		m, _ := readParse(t, &NativeReader{Stills: stills, Video: &platformtest.Video{}}, path)
		calls := propertiesCalls(stills)
		if len(calls) != 1 || calls[0].Path != path || calls[0].Data != nil {
			t.Fatalf("Properties calls = %+v; want one, of the RAF's path", calls)
		}
		if m.Width != 7728 {
			t.Errorf("Width = %d, want the container's 7728", m.Width)
		}
	})
}

func firstPath(calls []platformtest.StillsCall) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].Path
}

func TestNativeReader_DNGUsesContainer(t *testing.T) {
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		Orientation: 1,
		RawStrip:    make([]byte, 64),
		Previews:    []platformtest.DNGPreview{{JPEG: platformtest.MakeJPEG(t, 160, 107), Width: 160, Height: 107}},
	})
	stills := &platformtest.Stills{Props: map[string]map[string]any{path: loadDict(t, "imageio", "dng_container.json")}}
	m, _ := readParse(t, &NativeReader{Stills: stills, Video: &platformtest.Video{}}, path)

	calls := propertiesCalls(stills)
	if len(calls) != 1 || calls[0].Path != path || calls[0].Data != nil {
		t.Fatalf("Properties calls = %+v; want one, of the DNG's path, never Data", calls)
	}
	if key := DeviceKey(m); !strings.HasSuffix(key, "#TESTSERIAL01") {
		t.Errorf("DeviceKey = %q, want the container's serial", key)
	}
	if c, ok := Captured(m); !ok || c != fixedTime.Unix() {
		t.Errorf("Captured = %d, %v; want the container's DateTimeOriginal", c, ok)
	}
}

func dngProps6000x4000() map[string]any {
	return map[string]any{
		"PixelWidth": 6000, "PixelHeight": 4000,
		"{Exif}": map[string]any{"PixelXDimension": 6000, "PixelYDimension": 4000},
		"{TIFF}": map[string]any{"Make": "TESTCAM", "Model": "D1"},
	}
}

func TestNativeReader_DNGDimensionsFromPreview(t *testing.T) {
	withPreview := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: make([]byte, 64),
		Previews: []platformtest.DNGPreview{{JPEG: platformtest.MakeJPEG(t, 1600, 1067), Width: 1600, Height: 1067}},
	})
	noPreview := platformtest.MakeDNG(t, platformtest.DNGOptions{RawStrip: make([]byte, 64)})
	cases := []struct {
		name         string
		path         string
		wantW, wantH int
	}{
		{"preview", withPreview, 1600, 1067},
		{"no preview", noPreview, 6000, 4000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stills := &platformtest.Stills{Props: map[string]map[string]any{tc.path: dngProps6000x4000()}}
			m, _ := readParse(t, &NativeReader{Stills: stills, Video: &platformtest.Video{}}, tc.path)
			if m.Width != tc.wantW || m.Height != tc.wantH {
				t.Errorf("size = %dx%d, want %dx%d", m.Width, m.Height, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestNativeReader_DNGDimensionsFromThePreviewThatDecodes(t *testing.T) {
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: make([]byte, 64),
		Previews: []platformtest.DNGPreview{
			{JPEG: platformtest.MakeJPEG(t, 1600, 1067), Width: 1600, Height: 1067, DamageBody: true},
			{JPEG: platformtest.MakeJPEG(t, 256, 171), Width: 256, Height: 171},
		},
	})
	// The fake's Thumbnail decodes Data fully and fails on the damaged body.
	stills := &platformtest.Stills{Props: map[string]map[string]any{path: dngProps6000x4000()}}
	m, _ := readParse(t, &NativeReader{Stills: stills, Video: &platformtest.Video{}}, path)
	if m.Width != 256 || m.Height != 171 {
		t.Errorf("size = %dx%d, want 256x171", m.Width, m.Height)
	}
}

func TestNativeReader_DNGCancelled(t *testing.T) {
	path := platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: make([]byte, 64),
		Previews: []platformtest.DNGPreview{{JPEG: platformtest.MakeJPEG(t, 64, 43), Width: 64, Height: 43}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &NativeReader{Stills: &platformtest.Stills{}, Video: &platformtest.Video{}}
	if _, err := r.Read(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read = %v, want context.Canceled", err)
	}
}

func TestNativeReader_VideoByExtension(t *testing.T) {
	movie := append(ftyp(), atom("moov", mvhd(0, macSeconds(fixedTime), macSeconds(fixedTime)))...)
	props := loadDict(t, "av", "iphone_mov.json")
	wantKey := str(t, props, "make") + " " + str(t, props, "model") + "#"
	for _, name := range []string{"clip.mov", "CLIP.MP4", "clip.m4v"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, movie, 0o600); err != nil {
				t.Fatal(err)
			}
			stills := &platformtest.Stills{}
			video := &platformtest.Video{Props: map[string]map[string]any{path: props}}
			m, _ := readParse(t, &NativeReader{Stills: stills, Video: video}, path)
			if len(stills.Calls()) != 0 {
				t.Errorf("Stills received %d calls for a video", len(stills.Calls()))
			}
			if got := DeviceKey(m); got != wantKey {
				t.Errorf("DeviceKey = %q, want %q", got, wantKey)
			}
			if c, ok := Captured(m); !ok || c != fixedTime.Unix() {
				t.Errorf("Captured = %d, %v; want the movie header's", c, ok)
			}
		})
	}
	t.Run("a still is not a video", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "photo.heic")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		video := &platformtest.Video{Fail: map[string]error{path: errors.New("video must not be used")}}
		stills := &platformtest.Stills{Props: map[string]map[string]any{path: loadDict(t, "imageio", "iphone_heic.json")}}
		readParse(t, &NativeReader{Stills: stills, Video: video}, path)
		if calls := propertiesCalls(stills); len(calls) != 1 || calls[0].Path != path {
			t.Errorf("Properties calls = %+v; want one, of the path", calls)
		}
	})
}

// insertDiscovered inserts a discovered photo row for rel.
func insertDiscovered(t *testing.T, st *store.Store, rel, kind string) int64 {
	t.Helper()
	id, err := st.Files().Insert(store.File{
		Path: rel, Size: 1, Mtime: 1, ContentHash: rel, Kind: kind, Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return id
}

func TestExtractor_StoresGzippedRecord(t *testing.T) {
	st := store.NewTestStore(t)
	root := t.TempDir()
	abs := filepath.Join(root, "a.jpg")
	if err := os.WriteFile(abs, []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := insertDiscovered(t, st, "a.jpg", "photo")
	stills := &platformtest.Stills{Props: map[string]map[string]any{abs: loadDict(t, "imageio", "fuji_raf_jpeg.json")}}
	ext := NewExtractor(&NativeReader{Stills: stills, Video: &platformtest.Video{}}, st, root)

	if n, err := ext.ProcessByStatus(context.Background(), "discovered", 10); err != nil || n != 1 {
		t.Fatalf("ProcessByStatus = %d, %v; want 1, nil", n, err)
	}
	got, err := st.Files().GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScanStatus != "metadata" {
		t.Errorf("scan_status = %q, want metadata", got.ScanStatus)
	}
	if !got.DeviceKey.Valid || got.DeviceKey.String != fujiDeviceKey(t) {
		t.Errorf("device_key = %+v, want %q", got.DeviceKey, fujiDeviceKey(t))
	}
	zr, err := gzip.NewReader(bytes.NewReader(got.RawExif))
	if err != nil {
		t.Fatalf("raw_exif is not gzip: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var recs []map[string]any
	if err := json.Unmarshal(plain, &recs); err != nil || len(recs) == 0 {
		t.Fatalf("raw_exif = %s, %v; want a JSON array", plain, err)
	}
	if recs[0]["SourceFile"] != "a.jpg" {
		t.Errorf("first record's SourceFile = %v, want a.jpg", recs[0]["SourceFile"])
	}
}

func TestExtractor_UnreadableFileIsFailed(t *testing.T) {
	st := store.NewTestStore(t)
	root := t.TempDir()
	cases := map[string]error{
		"missing.jpg": nil, // no file on disk
		"broken.jpg":  errors.New("imageio: cannot read the file"),
	}
	stills := &platformtest.Stills{Fail: map[string]error{}}
	for rel, failure := range cases {
		if failure != nil {
			abs := filepath.Join(root, rel)
			if err := os.WriteFile(abs, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			stills.Fail[abs] = failure
		}
	}
	ids := map[string]int64{}
	for rel := range cases {
		ids[rel] = insertDiscovered(t, st, rel, "photo")
	}
	ext := NewExtractor(&NativeReader{Stills: stills, Video: &platformtest.Video{}}, st, root)
	if n, err := ext.ProcessByStatus(context.Background(), "discovered", 10); err != nil || n != 0 {
		t.Fatalf("ProcessByStatus = %d, %v; want 0, nil", n, err)
	}
	for rel, id := range ids {
		got, err := st.Files().GetByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ScanStatus != "failed" || !got.ErrMsg.Valid || got.ErrMsg.String == "" {
			t.Errorf("%s: scan_status = %q, error = %+v; want failed with a reason", rel, got.ScanStatus, got.ErrMsg)
		}
	}
}

// readerFunc adapts a function to Reader.
type readerFunc func(ctx context.Context, absPath string) ([]byte, error)

func (f readerFunc) Read(ctx context.Context, absPath string) ([]byte, error) { return f(ctx, absPath) }

func TestExtractor_CancelLeavesRowUntouched(t *testing.T) {
	st := store.NewTestStore(t)
	id := insertDiscovered(t, st, "a.jpg", "photo")
	cancelled := readerFunc(func(context.Context, string) ([]byte, error) {
		return nil, context.Canceled
	})
	ext := NewExtractor(cancelled, st, t.TempDir())
	if err := ext.Process(context.Background(), id, "/x/a.jpg"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Process = %v, want context.Canceled", err)
	}
	got, err := st.Files().GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScanStatus != "discovered" || got.ErrMsg.Valid {
		t.Errorf("scan_status = %q, error = %+v; want discovered and no error", got.ScanStatus, got.ErrMsg)
	}

	t.Run("native reader on a cancelled context", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "b.jpg"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		id := insertDiscovered(t, st, "b.jpg", "photo")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ext := NewExtractor(&NativeReader{Stills: &platformtest.Stills{}, Video: &platformtest.Video{}}, st, root)
		if err := ext.Process(ctx, id, filepath.Join(root, "b.jpg")); !errors.Is(err, context.Canceled) {
			t.Fatalf("Process = %v, want context.Canceled", err)
		}
		if got, _ := st.Files().GetByID(id); got.ScanStatus != "discovered" {
			t.Errorf("scan_status = %q, want discovered", got.ScanStatus)
		}
	})
}

func TestExtractor_FailureGuard(t *testing.T) {
	broken := readerFunc(func(context.Context, string) ([]byte, error) {
		return nil, errors.New("open: no such file or directory")
	})
	for _, tc := range []struct {
		name       string
		guard      func() bool
		wantStatus string
		wantOutage bool
	}{
		{"no guard records the failure", nil, "failed", false},
		{"folder reachable records the failure", func() bool { return true }, "failed", false},
		{"folder gone leaves the row pending", func() bool { return false }, "discovered", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewTestStore(t)
			id := insertDiscovered(t, st, "a.jpg", "photo")
			ext := NewExtractor(broken, st, t.TempDir())
			ext.FailureGuard = tc.guard
			err := ext.Process(context.Background(), id, "/x/a.jpg")
			if err == nil {
				t.Fatal("Process = nil; want an error")
			}
			if got := errors.Is(err, pipeline.ErrFolderUnavailable); got != tc.wantOutage {
				t.Fatalf("Process = %v; ErrFolderUnavailable %v, want %v", err, got, tc.wantOutage)
			}
			got, err := st.Files().GetByID(id)
			if err != nil {
				t.Fatal(err)
			}
			if got.ScanStatus != tc.wantStatus || got.ErrMsg.Valid != (tc.wantStatus == "failed") {
				t.Errorf("scan_status = %q, error = %+v; want %s", got.ScanStatus, got.ErrMsg, tc.wantStatus)
			}
		})
	}
}

// cancelAfterProperties is a platform.Video whose Properties succeeds and
// then cancels the run, so the cancellation lands in the movie-header walk.
type cancelAfterProperties struct {
	platform.Video
	cancel context.CancelFunc
}

func (v cancelAfterProperties) Properties(ctx context.Context, path string) (map[string]any, error) {
	props, err := v.Video.Properties(ctx, path)
	v.cancel()
	return props, err
}

func TestExtractor_CancelDuringMovieHeaderLeavesRowUntouched(t *testing.T) {
	st := store.NewTestStore(t)
	root := t.TempDir()
	abs := filepath.Join(root, "clip.mov")
	movie := append(ftyp(), atom("moov", mvhd(0, macSeconds(fixedTime), macSeconds(fixedTime)))...)
	if err := os.WriteFile(abs, movie, 0o600); err != nil {
		t.Fatal(err)
	}
	props := map[string]map[string]any{abs: loadDict(t, "av", "iphone_mov.json")}

	// The reader itself reports the cancellation instead of a record.
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	reader := &NativeReader{Stills: &platformtest.Stills{}, Video: cancelAfterProperties{&platformtest.Video{Props: props}, rcancel}}
	if out, err := reader.Read(rctx, abs); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read = %d bytes, %v; want context.Canceled", len(out), err)
	}

	id := insertDiscovered(t, st, "clip.mov", "video")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	video := cancelAfterProperties{Video: &platformtest.Video{Props: props}, cancel: cancel}
	ext := NewExtractor(&NativeReader{Stills: &platformtest.Stills{}, Video: video}, st, root)
	if err := ext.Process(ctx, id, abs); !errors.Is(err, context.Canceled) {
		t.Fatalf("Process = %v, want context.Canceled", err)
	}
	got, err := st.Files().GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScanStatus != "discovered" || got.ErrMsg.Valid {
		t.Errorf("scan_status = %q, error = %+v; want discovered and no error", got.ScanStatus, got.ErrMsg)
	}
}

// Review Focus 4: a row written by the native reader is re-derivable by
// BackfillDeviceKey exactly like a v1 exiftool row, so a camera never splits.
func TestBackfillDeviceKey_OnNativeRow(t *testing.T) {
	st := store.NewTestStore(t)
	ctx := context.Background()
	root := t.TempDir()
	abs := filepath.Join(root, "DJI_0001.MP4")
	movie := append(ftyp(), atom("moov", mvhd(0, macSeconds(fixedTime), macSeconds(fixedTime)))...)
	if err := os.WriteFile(abs, movie, 0o600); err != nil {
		t.Fatal(err)
	}
	dji := loadDict(t, "av", "dji_mp4.json")
	wantKey := str(t, dji, "encoder") + "#"
	id := insertDiscovered(t, st, "DJI_0001.MP4", "video")
	video := &platformtest.Video{Props: map[string]map[string]any{abs: dji}}
	ext := NewExtractor(&NativeReader{Stills: &platformtest.Stills{}, Video: video}, st, root)
	if err := ext.Process(ctx, id, abs); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got, _ := st.Files().GetByID(id); got.DeviceKey.String != wantKey {
		t.Fatalf("device_key after Process = %q, want %q", got.DeviceKey.String, wantKey)
	}

	// As if an older parser had keyed the row "Unknown".
	if _, err := st.DB().ExecContext(ctx, `UPDATE files SET device_key = ? WHERE id = ?`,
		sql.NullString{String: "Unknown", Valid: true}, id); err != nil {
		t.Fatal(err)
	}
	n, err := BackfillDeviceKey(ctx, st)
	if err != nil || n != 1 {
		t.Fatalf("BackfillDeviceKey = %d, %v; want 1, nil", n, err)
	}
	if got, _ := st.Files().GetByID(id); got.DeviceKey.String != wantKey {
		t.Errorf("device_key after backfill = %q, want %q", got.DeviceKey.String, wantKey)
	}
}
