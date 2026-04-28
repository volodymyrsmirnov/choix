package scanner

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
