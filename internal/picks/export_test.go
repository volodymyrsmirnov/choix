package picks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuffixedName(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want string
	}{
		{"a.jpg", 1, "a.jpg"},
		{"a.jpg", 2, "a_2.jpg"},
		{"IMG.0001.RAF", 3, "IMG.0001_3.RAF"},
		{"noext", 2, "noext_2"},
	} {
		if got := suffixedName(tc.name, tc.n); got != tc.want {
			t.Errorf("suffixedName(%q, %d) = %q, want %q", tc.name, tc.n, got, tc.want)
		}
	}
}

// countingReader cancels ctx on its first read and counts reads.
type countingReader struct {
	r      io.Reader
	reads  int
	cancel context.CancelFunc
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	c.cancel()
	return c.r.Read(p)
}

func TestSameBytes(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
	other := bytes.Clone(data)
	other[len(other)/2] ^= 1

	for _, tc := range []struct {
		name string
		a, b []byte
		want bool
	}{
		{"identical", data, data, true},
		{"middle byte differs", data, other, false},
		{"prefix", data[:1000], data, false},
		{"empty", nil, nil, true},
	} {
		got, err := sameBytes(context.Background(), bytes.NewReader(tc.a), bytes.NewReader(tc.b))
		if err != nil || got != tc.want {
			t.Errorf("%s: sameBytes = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}

	// Cancellation during the comparison stops it at the next chunk.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &countingReader{r: bytes.NewReader(data), cancel: cancel}
	if _, err := sameBytes(ctx, a, bytes.NewReader(data)); !errors.Is(err, context.Canceled) {
		t.Fatalf("sameBytes after cancel = %v, want context.Canceled", err)
	}
	if a.reads > 2 {
		t.Errorf("read %d chunks after cancellation, want it to stop at the next chunk", a.reads)
	}
}

func TestPickAutoExports(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "Day1/IMG_0001.JPG", "PIXELS")

	if err := e.pick(id); err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got := readFile(t, e.root, "picks/Day1/IMG_0001.JPG"); got != "PIXELS" {
		t.Errorf("copy = %q, want PIXELS", got)
	}
	if st, exp := e.pickRow(t, id); st != "picked" || exp != "picks/Day1/IMG_0001.JPG" {
		t.Errorf("row = (%q, %q), want (picked, picks/Day1/IMG_0001.JPG)", st, exp)
	}
	if err := e.pick(id); err != nil {
		t.Fatalf("Pick second call: %v", err)
	}
	if got := regularFilesUnder(t, e.root, "picks"); len(got) != 1 {
		t.Errorf("picks/ holds %q, want one copy", got)
	}
}

func TestPickNestedPicksDir(t *testing.T) {
	e := newEnv(t, "exports/luminar")
	id := e.addFile(t, "Day1/a.jpg", "PIXELS")
	res, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	if res.ExportedPath != "exports/luminar/Day1/a.jpg" {
		t.Errorf("exported = %q", res.ExportedPath)
	}
}

func TestPickMissingSource(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "ghost.jpg", "x")
	if err := os.Remove(filepath.Join(e.root, "ghost.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := e.pick(id); err == nil {
		t.Fatal("Pick of a missing source should error")
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want unchanged", st)
	}
	if got := regularFilesUnder(t, e.root, "picks"); len(got) != 0 {
		t.Errorf("files under picks/: %q", got)
	}
}

// A same-named file that differs only in length (one is a prefix of the other)
// is a different-content collision.
func TestPickCollisionPrefixContent(t *testing.T) {
	e := newEnv(t, "picks")
	writeFile(t, e.root, "picks/a.jpg", "MINE-AND-MORE")
	id := e.addFile(t, "a.jpg", "MINE")
	res, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExportedPath != "picks/a_2.jpg" {
		t.Errorf("exported = %q, want picks/a_2.jpg", res.ExportedPath)
	}
}

// A directory or a symlink occupying the target name is never reused.
func TestPickTargetOccupiedByNonFile(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "MINE")
	if err := os.MkdirAll(filepath.Join(e.root, "picks", "a.jpg"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../a.jpg", filepath.Join(e.root, "picks", "a_2.jpg")); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExportedPath != "picks/a_3.jpg" {
		t.Errorf("exported = %q, want picks/a_3.jpg", res.ExportedPath)
	}
	if target, _ := os.Readlink(filepath.Join(e.root, "picks", "a_2.jpg")); !strings.HasSuffix(target, "a.jpg") {
		t.Errorf("symlink changed: %q", target)
	}
}
