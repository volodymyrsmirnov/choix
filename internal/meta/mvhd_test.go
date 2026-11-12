package meta

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// macEpochOffset is the number of seconds from 1904-01-01 to 1970-01-01.
const testMacEpochOffset = 2082844800

// atom returns a QuickTime atom with a 32-bit size.
func atom(typ string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := binary.BigEndian.AppendUint32(nil, uint32(n)) //nolint:gosec // test atoms are small
	b = append(b, typ...)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

// mvhd returns a movie header atom; times are seconds since 1904.
func mvhd(version byte, created, modified uint64) []byte {
	p := []byte{version, 0, 0, 0}
	if version == 1 {
		p = binary.BigEndian.AppendUint64(p, created)
		p = binary.BigEndian.AppendUint64(p, modified)
		p = binary.BigEndian.AppendUint32(p, 600) // time scale
		p = binary.BigEndian.AppendUint64(p, 1200)
	} else {
		p = binary.BigEndian.AppendUint32(p, uint32(created))  //nolint:gosec // test values fit
		p = binary.BigEndian.AppendUint32(p, uint32(modified)) //nolint:gosec // test values fit
		p = binary.BigEndian.AppendUint32(p, 600)
		p = binary.BigEndian.AppendUint32(p, 1200)
	}
	p = append(p, make([]byte, 80)...) // rate, volume, matrix, … next track id
	return atom("mvhd", p)
}

// macSeconds returns t as seconds since 1904-01-01 UTC.
func macSeconds(t time.Time) uint64 { return uint64(t.Unix() + testMacEpochOffset) } //nolint:gosec // dates after 1904

// writeMovie writes data to a temporary .mov file and returns its path.
func writeMovie(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "TEST0001.MOV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func ftyp() []byte { return atom("ftyp", []byte("qt  \x00\x00\x00\x00qt  ")) }

func TestReadMovieHeader_Clip(t *testing.T) {
	// clip.mov's mvhd times are fixed by testdata/media/_patch_mvhd.go
	// (see README-video.md).
	mh, err := ReadMovieHeader("../../testdata/media/clip.mov")
	if err != nil {
		t.Fatalf("ReadMovieHeader: %v", err)
	}
	if !mh.Created.Equal(fixedTime) || !mh.Modified.Equal(fixedTime) {
		t.Errorf("header = %v / %v, want %v for both", mh.Created, mh.Modified, fixedTime)
	}
	if mh.Created.Location() != time.UTC {
		t.Errorf("Created location = %v, want UTC", mh.Created.Location())
	}
}

func TestReadMovieHeader_Version0(t *testing.T) {
	path := writeMovie(t, append(ftyp(), atom("moov", mvhd(0, macSeconds(fixedTime), macSeconds(fixedTime)+2))...))
	mh, err := ReadMovieHeader(path)
	if err != nil {
		t.Fatalf("ReadMovieHeader: %v", err)
	}
	if !mh.Created.Equal(fixedTime) || !mh.Modified.Equal(fixedTime.Add(2*time.Second)) {
		t.Errorf("header = %v / %v", mh.Created, mh.Modified)
	}
}

func TestReadMovieHeader_Version1(t *testing.T) {
	// A 64-bit-size atom (size 1, largesize) and a free atom precede moov;
	// mvhd is not moov's first child.
	wide := binary.BigEndian.AppendUint32(nil, 1)
	wide = append(wide, "mdat"...)
	wide = binary.BigEndian.AppendUint64(wide, 16+4)
	wide = append(wide, 1, 2, 3, 4)
	data := append(ftyp(), wide...)
	data = append(data, atom("free", make([]byte, 8))...)
	data = append(data, atom("moov", atom("udta"), mvhd(1, macSeconds(fixedTime), macSeconds(fixedTime)+3))...)

	mh, err := ReadMovieHeader(writeMovie(t, data))
	if err != nil {
		t.Fatalf("ReadMovieHeader: %v", err)
	}
	if !mh.Created.Equal(fixedTime) || !mh.Modified.Equal(fixedTime.Add(3*time.Second)) {
		t.Errorf("header = %v / %v", mh.Created, mh.Modified)
	}
}

func TestReadMovieHeader_ZeroTimes(t *testing.T) {
	mh, err := ReadMovieHeader(writeMovie(t, atom("moov", mvhd(0, 0, 0))))
	if err != nil {
		t.Fatalf("ReadMovieHeader: %v", err)
	}
	if !mh.Created.IsZero() || !mh.Modified.IsZero() {
		t.Errorf("header = %v / %v, want zero times", mh.Created, mh.Modified)
	}
}

func TestReadMovieHeader_NoMoov(t *testing.T) {
	cases := map[string][]byte{
		"no moov":                      append(ftyp(), atom("mdat", []byte{1, 2, 3})...),
		"moov without mvhd":            atom("moov", atom("udta")),
		"empty file":                   {},
		"truncated mvhd":               atom("moov", atom("mvhd", []byte{0, 0, 0, 0, 1, 2})),
		"bad version":                  atom("moov", mvhd(2, 1, 1)),
		"atom smaller than its header": append(ftyp(), 0, 0, 0, 4, 'f', 'r', 'e', 'e'),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadMovieHeader(writeMovie(t, data)); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	if _, err := ReadMovieHeader(filepath.Join(t.TempDir(), "missing.mov")); err == nil {
		t.Fatal("missing file: want an error")
	}
}

func TestReadMovieHeader_HugeAtomSize(t *testing.T) {
	huge32 := append(ftyp(), 0xFF, 0xFF, 0xFF, 0xF0, 'm', 'o', 'o', 'v')
	huge32 = append(huge32, mvhd(0, 1, 1)...)
	huge64 := append(ftyp(), 0, 0, 0, 1, 'm', 'd', 'a', 't', 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	child := append(ftyp(), atom("moov", []byte{0, 0, 0x10, 0, 'm', 'v', 'h', 'd'})...) // mvhd past moov's end
	cases := map[string][]byte{"32-bit": huge32, "64-bit": huge64, "child past parent": child}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeMovie(t, data)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := ReadMovieHeader(path)
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatal("want an error for an atom past the end of the file")
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
				t.Fatalf("allocated %d bytes; the declared size must not be allocated", grew)
			}
		})
	}
}

// countdownCtx reports itself cancelled from its (after+1)-th Err call on,
// so a test can cancel in the middle of a traversal.
type countdownCtx struct {
	context.Context
	after int32
	calls atomic.Int32
}

func (c *countdownCtx) Err() error {
	if c.calls.Add(1) > c.after {
		return context.Canceled
	}
	return nil
}

// freeAtoms returns n empty free atoms.
func freeAtoms(n int) []byte {
	var b []byte
	for range n {
		b = append(b, atom("free")...)
	}
	return b
}

func TestReadMovieHeader_CancelledDuringTraversal(t *testing.T) {
	data := append(ftyp(), freeAtoms(500)...)
	data = append(data, atom("moov", mvhd(0, macSeconds(fixedTime), macSeconds(fixedTime)))...)
	path := writeMovie(t, data)

	ctx := &countdownCtx{Context: context.Background(), after: 10}
	if _, err := readMovieHeader(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("readMovieHeader = %v, want context.Canceled", err)
	}
	if n := ctx.calls.Load(); n != 11 {
		t.Errorf("ctx checked %d times; want the walk to stop at the first check after cancellation (11)", n)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readMovieHeader(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("readMovieHeader on a cancelled context = %v, want context.Canceled", err)
	}
}

func TestReadMovieHeader_TooManyAtoms(t *testing.T) {
	header := atom("moov", mvhd(0, macSeconds(fixedTime), macSeconds(fixedTime)))
	within := append(freeAtoms(maxAtoms-1), header...)
	if mh, err := ReadMovieHeader(writeMovie(t, within)); err != nil || !mh.Created.Equal(fixedTime) {
		t.Fatalf("moov as atom %d: %v, %v; want the header", maxAtoms, mh.Created, err)
	}
	past := append(freeAtoms(maxAtoms), header...)
	if _, err := ReadMovieHeader(writeMovie(t, past)); err == nil {
		t.Fatalf("moov as atom %d: want an error", maxAtoms+1)
	}
}
