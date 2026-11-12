package thumb

import "testing"

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
	if TierFullJPEG != "full" || WidthFullJPEG != 4096 {
		t.Errorf("full tier = %q/%d, want full/4096", TierFullJPEG, WidthFullJPEG)
	}
}
