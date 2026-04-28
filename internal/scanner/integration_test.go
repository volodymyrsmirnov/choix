package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type fixture struct {
	rel  string
	data []byte
}

func TestScannerIntegrationRepresentativeTree(t *testing.T) {
	root := t.TempDir()

	// 10 media files across 3 subdirs, plus skip-dir noise and non-media.
	files := []fixture{
		{"Day1/IMG_0001.JPG", []byte("photo-1")},
		{"Day1/IMG_0002.JPG", []byte("photo-2-different")},
		{"Day1/IMG_0003.RAF", []byte("raw-3")},
		{"Day2/IMG_0010.HEIC", []byte("heic-10")},
		{"Day2/IMG_0011.heic", []byte("heic-11")},
		{"Day2/clip.MOV", []byte("video-12")},
		{"Day3/sub/PANO.tif", []byte("tiff-13")},
		{"Day3/sub/snap.png", []byte("png-14")},
		{"Day3/sub/reel.mp4", []byte("video-15")},
		{"Day3/sub/short.m4v", []byte("video-16")},
		// Non-media and skip-dir noise:
		{"Day1/notes.txt", []byte("not media")},
		{"Day2/sidecar.xmp", []byte("xmp")},
		{".choix/state.db", []byte("internal")},
		{"picks/already.jpg", []byte("already exported")},
		{"node_modules/foo.jpg", []byte("dep")},
		{".git/HEAD.jpg", []byte("vcs")},
	}
	for _, f := range files {
		mustWrite(t, root, f.rel, f.data)
	}

	s := newScannerStore(t)
	sc := New(root, s)

	// First walk — discovers all 10 media files, skips noise.
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	got := listPaths(t, s)
	want := []string{
		"Day1/IMG_0001.JPG",
		"Day1/IMG_0002.JPG",
		"Day1/IMG_0003.RAF",
		"Day2/IMG_0010.HEIC",
		"Day2/IMG_0011.heic",
		"Day2/clip.MOV",
		"Day3/sub/PANO.tif",
		"Day3/sub/reel.mp4",
		"Day3/sub/short.m4v",
		"Day3/sub/snap.png",
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("first walk paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Sanity check formats: one .RAF must be 'photo'/'raf', .MOV must be 'video'/'mov'.
	rows, err := s.DB().Query(`SELECT path, kind, format FROM files`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	formats := map[string]struct{ kind, format string }{}
	for rows.Next() {
		var p, k, f string
		if err := rows.Scan(&p, &k, &f); err != nil {
			t.Fatal(err)
		}
		formats[p] = struct{ kind, format string }{k, f}
	}
	if formats["Day1/IMG_0003.RAF"].kind != "photo" || formats["Day1/IMG_0003.RAF"].format != "raf" {
		t.Errorf("RAF classified as %+v", formats["Day1/IMG_0003.RAF"])
	}
	if formats["Day2/clip.MOV"].kind != "video" || formats["Day2/clip.MOV"].format != "mov" {
		t.Errorf("MOV classified as %+v", formats["Day2/clip.MOV"])
	}

	// Second walk on the same tree — must be a no-op for counts and statuses.
	for path := range formats {
		setStatus(t, sc, path, "analyzed") // simulate downstream progress
	}
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("second walk: %v", err)
	}
	got2 := listPaths(t, s)
	if len(got2) != len(want) {
		t.Fatalf("idempotency violated: count %d → %d", len(want), len(got2))
	}
	for path := range formats {
		if got := statusOf(t, sc, path); got != "analyzed" {
			t.Errorf("status of unchanged %q = %q, want preserved 'analyzed'", path, got)
		}
	}

	// Delete one file and rescan — that one row flips to 'missing'.
	if err := os.Remove(filepath.Join(root, "Day2", "clip.MOV")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := sc.Walk(context.Background()); err != nil {
		t.Fatalf("third walk: %v", err)
	}
	if got := statusOf(t, sc, "Day2/clip.MOV"); got != "missing" {
		t.Errorf("deleted file status = %q, want 'missing'", got)
	}
	// Surviving files keep their analyzed status.
	if got := statusOf(t, sc, "Day1/IMG_0001.JPG"); got != "analyzed" {
		t.Errorf("survivor status = %q, want 'analyzed'", got)
	}
}
