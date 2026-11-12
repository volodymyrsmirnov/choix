package rawpreview_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
)

// patchFile rewrites path with patch applied to its contents.
func patchFile(t *testing.T, path string, patch func([]byte) []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, patch(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRAF_ReturnsEmbeddedJPEG(t *testing.T) {
	jpg := platformtest.MakeJPEG(t, 160, 120)
	path := platformtest.MakeRAF(t, jpg)
	p, err := rawpreview.Embedded(path, nil)
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if !bytes.Equal(p.JPEG, jpg) {
		t.Fatal("returned JPEG differs from the embedded one")
	}
	if p.Orientation != 0 {
		t.Fatalf("Orientation = %d; want 0", p.Orientation)
	}
	if p.Width != 160 || p.Height != 120 {
		t.Fatalf("size = %dx%d; want 160x120", p.Width, p.Height)
	}
}

// assertNoPreview checks that Embedded refuses path with ErrNoPreview and
// never offers a candidate.
func assertNoPreview(t *testing.T, path string) {
	t.Helper()
	r := &recorder{}
	p, err := rawpreview.Embedded(path, r.try)
	if !errors.Is(err, rawpreview.ErrNoPreview) {
		t.Fatalf("err = %v; want ErrNoPreview", err)
	}
	if p.JPEG != nil {
		t.Fatalf("preview has %d bytes; want none", len(p.JPEG))
	}
	if len(r.widths) != 0 {
		t.Fatalf("offered widths = %v; want none", r.widths)
	}
}

func TestRAF_BadMagic(t *testing.T) {
	path := platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 32, 24))
	patchFile(t, path, func(b []byte) []byte { b[0] = 'X'; return b })
	assertNoPreview(t, path)
}

func TestRAF_OffsetPastEOF(t *testing.T) {
	path := platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 32, 24))
	patchFile(t, path, func(b []byte) []byte {
		binary.BigEndian.PutUint32(b[88:], 0xFFFFFFF0)
		return b
	})
	assertNoPreview(t, path)
	// The reason is readable, for the file's scan_status error text.
	_, err := rawpreview.Embedded(path, nil)
	const want = "rawpreview: no embedded JPEG: raf: preview of 4294967280 bytes at offset 148 is outside the file"
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err, want)
	}

	t.Run("offset", func(t *testing.T) {
		path := platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 32, 24))
		patchFile(t, path, func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[84:], 0xFFFFFFF0)
			return b
		})
		assertNoPreview(t, path)
	})
}

func TestRAF_Truncated(t *testing.T) {
	path := platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 32, 24))
	if err := os.Truncate(path, 90); err != nil {
		t.Fatal(err)
	}
	assertNoPreview(t, path)
}

func TestRAF_ZeroBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "EMPTY.RAF")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	assertNoPreview(t, path)
}

func TestRAF_NotJPEGAtOffset(t *testing.T) {
	path := platformtest.MakeRAF(t, []byte("this is not a JPEG, only some bytes"))
	assertNoPreview(t, path)
}
