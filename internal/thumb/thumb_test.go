package thumb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCachePathBucketAndJoin(t *testing.T) {
	root := "/tmp/scan-root"

	cases := []struct {
		fileID int64
		tier   string
		want   string
	}{
		{1, TierThumb, "/tmp/scan-root/.choix/thumbs/01/1-thumb.jpg"},
		{255, TierPreview, "/tmp/scan-root/.choix/thumbs/ff/255-preview.jpg"},
		{256, TierThumb, "/tmp/scan-root/.choix/thumbs/00/256-thumb.jpg"},
		{4097, TierPreview, "/tmp/scan-root/.choix/thumbs/01/4097-preview.jpg"},
	}
	for _, c := range cases {
		got := CachePath(root, c.fileID, c.tier)
		if got != c.want {
			t.Errorf("CachePath(%d, %q) = %q, want %q", c.fileID, c.tier, got, c.want)
		}
	}
}

func TestEnsureDirCreatesParents(t *testing.T) {
	root := t.TempDir()
	dst := CachePath(root, 42, TierThumb)
	if err := ensureDir(dst); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	parent := filepath.Dir(dst)
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory at %s", parent)
	}
	if !strings.HasSuffix(parent, filepath.Join(".choix", "thumbs", "2a")) {
		t.Errorf("unexpected parent path %s", parent)
	}
}

func TestTierConstants(t *testing.T) {
	if TierThumb != "thumb" {
		t.Errorf("TierThumb = %q, want %q", TierThumb, "thumb")
	}
	if TierPreview != "preview" {
		t.Errorf("TierPreview = %q, want %q", TierPreview, "preview")
	}
	if WidthThumb != 256 {
		t.Errorf("WidthThumb = %d, want 256", WidthThumb)
	}
	if WidthPreview != 1600 {
		t.Errorf("WidthPreview = %d, want 1600", WidthPreview)
	}
}
