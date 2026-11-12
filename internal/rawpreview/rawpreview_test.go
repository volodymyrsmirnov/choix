package rawpreview_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
)

func TestKind(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"DSCF0001.RAF", "raf"},
		{"dscf0001.raf", "raf"},
		{"a/b/x.Raf", "raf"},
		{"IMG_0001.DNG", "dng"},
		{"img_0001.dng", "dng"},
		{"a/b/x.DnG", "dng"},
		{"IMG_0001.JPG", ""},
		{"IMG_0001.HEIC", ""},
		{"raf", ""},
		{"dng", ""},
		{"noext", ""},
		{"x.raf.jpg", ""},
		{"dir.raf/file", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := rawpreview.Kind(tt.path); got != tt.want {
				t.Fatalf("Kind(%q) = %q; want %q", tt.path, got, tt.want)
			}
		})
	}
}

// sharedJPEGs caches the larger fixture JPEGs, which are slow to encode
// under the race detector.
var sharedJPEGs sync.Map // [2]int{w, h} → []byte

// sharedJPEG returns a w×h fixture JPEG shared between tests; callers must
// not modify it.
func sharedJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	key := [2]int{w, h}
	if v, ok := sharedJPEGs.Load(key); ok {
		return v.([]byte)
	}
	v, _ := sharedJPEGs.LoadOrStore(key, platformtest.MakeJPEG(t, w, h))
	return v.([]byte)
}

// recorder is a try that records the width of every candidate it is
// offered and answers with the next error from errs (nil once errs runs
// out).
type recorder struct {
	widths []int
	errs   []error
}

func (r *recorder) try(p rawpreview.Preview) error {
	r.widths = append(r.widths, p.Width)
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return err
}

// twoPreviewDNG is a DNG with valid 256- and 1600-wide previews.
func twoPreviewDNG(t *testing.T) string {
	t.Helper()
	return platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: bytes.Repeat([]byte{0x55}, 1024),
		Previews: []platformtest.DNGPreview{
			{JPEG: sharedJPEG(t, 256, 171), Width: 256, Height: 171},
			{JPEG: sharedJPEG(t, 1600, 1067), Width: 1600, Height: 1067},
		},
	})
}

func TestEmbedded_TryContextErrorStopsSearch(t *testing.T) {
	path := twoPreviewDNG(t)
	r := &recorder{errs: []error{context.Canceled}}
	_, err := rawpreview.Embedded(path, r.try)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("err = %v; a context error must not be reported as ErrNoPreview", err)
	}
	if len(r.widths) != 1 || r.widths[0] != 1600 {
		t.Fatalf("offered widths = %v; want only [1600]", r.widths)
	}

	t.Run("deadline", func(t *testing.T) {
		r := &recorder{errs: []error{context.DeadlineExceeded}}
		if _, err := rawpreview.Embedded(path, r.try); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v; want context.DeadlineExceeded", err)
		}
		if len(r.widths) != 1 {
			t.Fatalf("offered widths = %v; want one candidate", r.widths)
		}
	})

	t.Run("Usable with a cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := rawpreview.Embedded(path, rawpreview.Usable(ctx, &platformtest.Stills{}))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want context.Canceled", err)
		}
	})
}

func TestEmbedded_AllCandidatesRejected(t *testing.T) {
	path := twoPreviewDNG(t)
	reject := errors.New("not decodable")
	r := &recorder{errs: []error{reject, reject}}
	p, err := rawpreview.Embedded(path, r.try)
	if !errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("err = %v; want ErrNoPreview", err)
	}
	if p.JPEG != nil {
		t.Fatalf("preview has %d bytes; want none", len(p.JPEG))
	}
	if len(r.widths) != 2 || r.widths[0] != 1600 || r.widths[1] != 256 {
		t.Fatalf("offered widths = %v; want [1600 256]", r.widths)
	}
}

func TestEmbedded_NotRAWOrMissing(t *testing.T) {
	jpgPath := filepath.Join(t.TempDir(), "IMG_0001.JPG")
	if err := os.WriteFile(jpgPath, platformtest.MakeJPEG(t, 32, 24), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rawpreview.Embedded(jpgPath, nil); !errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("JPEG file: err = %v; want ErrNoPreview", err)
	}
	// A file that cannot be opened is an I/O error, not a missing preview.
	_, err := rawpreview.Embedded(filepath.Join(t.TempDir(), "GONE.RAF"), nil)
	if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("missing file: err = %v; want fs.ErrNotExist and not ErrNoPreview", err)
	}
}

func TestEmbedded_OpensReadOnly(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 64, 48)
	paths := map[string]string{
		"raf": platformtest.MakeRAF(t, jpg),
		"dng": platformtest.MakeDNG(t, platformtest.DNGOptions{
			RawStrip: []byte{1, 2, 3, 4},
			Previews: []platformtest.DNGPreview{{JPEG: jpg, Width: 64, Height: 48}},
		}),
	}
	for kind, path := range paths {
		t.Run(kind, func(t *testing.T) {
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// A read-only file cannot be opened for writing, so any
			// attempt to do so fails the call.
			if err := os.Chmod(path, 0o400); err != nil {
				t.Fatal(err)
			}
			if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
				f.Close()
				t.Skip("a 0400 file is writable here (running as root?)")
			}
			p, err := rawpreview.Embedded(path, nil)
			if err != nil {
				t.Fatalf("Embedded: %v", err)
			}
			if !bytes.Equal(p.JPEG, jpg) {
				t.Fatal("returned JPEG differs from the embedded one")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("the file changed")
			}
		})
	}
}
