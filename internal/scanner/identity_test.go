package scanner

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestContentHashTinyFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "tiny.bin", []byte("hello world"))

	h1, err := ContentHash(p)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if h1 == "" {
		t.Fatal("empty hash")
	}
	// Stable: re-running on the same content yields the same hash.
	h2, err := ContentHash(p)
	if err != nil {
		t.Fatalf("ContentHash 2nd: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not stable: %s vs %s", h1, h2)
	}
}

func TestContentHashExactly128KB(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte{0xAB}, 128*1024)
	p := writeFile(t, dir, "boundary.bin", data)

	if _, err := ContentHash(p); err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
}

func TestContentHashLargeFileChangesAtEnd(t *testing.T) {
	dir := t.TempDir()
	a := bytes.Repeat([]byte{0x01}, 256*1024)
	b := append(bytes.Repeat([]byte{0x01}, 256*1024-1), 0x02) // last byte differs
	pa := writeFile(t, dir, "a.bin", a)
	pb := writeFile(t, dir, "b.bin", b)

	ha, err := ContentHash(pa)
	if err != nil {
		t.Fatalf("ContentHash a: %v", err)
	}
	hb, err := ContentHash(pb)
	if err != nil {
		t.Fatalf("ContentHash b: %v", err)
	}
	if ha == hb {
		t.Errorf("hashes match for files differing in last byte; first+last 64KB must include the tail")
	}
}

// ContentHash refuses a FIFO without waiting for a writer.
func TestContentHashRefusesAFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe.jpg")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ContentHash(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotRegular) {
			t.Fatalf("ContentHash(FIFO) = %v, want errNotRegular", err)
		}
	case <-time.After(walkGuard):
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatalf("ContentHash blocked on a FIFO")
	}
}

// hashRegular follows a symlink only when asked to, and reports the status of
// the file it read (a symlink's target).
func TestHashRegularFollowOnlyWhenAsked(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "target.jpg", []byte("target bytes"))
	link := filepath.Join(dir, "link.jpg")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, _, err := hashRegular(link, false); !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("hashRegular(link, follow=false) = %v, want ELOOP", err)
	}
	hash, info, err := hashRegular(link, true)
	if err != nil {
		t.Fatalf("hashRegular(link, follow=true): %v", err)
	}
	want, err := ContentHash(target)
	if err != nil {
		t.Fatal(err)
	}
	if hash != want || info.Size() != int64(len("target bytes")) {
		t.Fatalf("hashRegular(link) = %q size %d, want %q size %d", hash, info.Size(), want, len("target bytes"))
	}
	if _, _, err := hashRegular(dir, true); !errors.Is(err, errNotRegular) {
		t.Fatalf("hashRegular(directory) = %v, want errNotRegular", err)
	}
}
