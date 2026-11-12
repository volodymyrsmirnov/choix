//go:build ignore

// _patch_mvhd.go sets the creation and modification times of a QuickTime
// file's movie header (moov → mvhd) to one fixed instant, so a fixture made
// by AVAssetWriter (which stamps the current time) is deterministic. Only
// those two fields change; every other byte and every offset stay as they
// are. Usage, from the repository root (see README-video.md):
//
//	tmp=$(mktemp -d)
//	cp testdata/media/_patch_mvhd.go "$tmp/patch_mvhd.go"
//	go run "$tmp/patch_mvhd.go" testdata/media/clip.mov 2026-01-02T03:04:05Z
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// macEpochOffset is the number of seconds from 1904-01-01 to 1970-01-01.
const macEpochOffset = 2082844800

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: patch_mvhd FILE RFC3339-TIME")
	}
	path := os.Args[1]
	at, err := time.Parse(time.RFC3339, os.Args[2])
	if err != nil {
		log.Fatalf("time: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	moovStart, moovEnd, err := find(data, 0, len(data), "moov")
	if err != nil {
		log.Fatalf("moov: %v", err)
	}
	start, end, err := find(data, moovStart, moovEnd, "mvhd")
	if err != nil {
		log.Fatalf("mvhd: %v", err)
	}
	secs := uint64(at.Unix() + macEpochOffset)
	p := data[start:end]
	switch {
	case len(p) >= 12 && p[0] == 0:
		if secs > 1<<32-1 {
			log.Fatalf("%s does not fit a version-0 mvhd", at)
		}
		binary.BigEndian.PutUint32(p[4:8], uint32(secs))
		binary.BigEndian.PutUint32(p[8:12], uint32(secs))
	case len(p) >= 20 && p[0] == 1:
		binary.BigEndian.PutUint64(p[4:12], secs)
		binary.BigEndian.PutUint64(p[12:20], secs)
	default:
		log.Fatal("mvhd: unsupported version or truncated")
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Fatal(err)
	}
}

// find returns the payload range of the first atom of type typ in
// data[start:end] (32-bit sizes and 64-bit largesize).
func find(data []byte, start, end int, typ string) (int, int, error) {
	for off := start; end-off >= 8; {
		size := int(binary.BigEndian.Uint32(data[off:]))
		hdr := 8
		switch size {
		case 0:
			size = end - off
		case 1:
			if end-off < 16 {
				return 0, 0, fmt.Errorf("truncated atom at %d", off)
			}
			size = int(binary.BigEndian.Uint64(data[off+8:]))
			hdr = 16
		}
		if size < hdr || size > end-off {
			return 0, 0, fmt.Errorf("bad atom size %d at %d", size, off)
		}
		if string(data[off+4:off+8]) == typ {
			return off + hdr, off + size, nil
		}
		off += size
	}
	return 0, 0, fmt.Errorf("no %s atom", typ)
}
