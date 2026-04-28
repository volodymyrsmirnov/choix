package scanner

import "testing"

func TestDetectFromExt(t *testing.T) {
	cases := []struct {
		name   string
		want   FormatInfo
		wantOK bool
	}{
		// photos
		{"IMG_0001.RAF", FormatInfo{Kind: "photo", Format: "raf"}, true},
		{"IMG_0001.raf", FormatInfo{Kind: "photo", Format: "raf"}, true},
		{"clip.DNG", FormatInfo{Kind: "photo", Format: "raf"}, true}, // DNG handled as raw, mapped to "raf" family
		{"shot.heic", FormatInfo{Kind: "photo", Format: "heic"}, true},
		{"shot.HEIF", FormatInfo{Kind: "photo", Format: "heic"}, true},
		{"holiday.jpg", FormatInfo{Kind: "photo", Format: "jpeg"}, true},
		{"holiday.JPEG", FormatInfo{Kind: "photo", Format: "jpeg"}, true},
		{"diagram.png", FormatInfo{Kind: "photo", Format: "png"}, true},
		{"scan.tiff", FormatInfo{Kind: "photo", Format: "jpeg"}, true}, // tiff treated as photo, format jpeg-family for now
		{"scan.tif", FormatInfo{Kind: "photo", Format: "jpeg"}, true},
		// videos
		{"clip.mov", FormatInfo{Kind: "video", Format: "mov"}, true},
		{"clip.MOV", FormatInfo{Kind: "video", Format: "mov"}, true},
		{"reel.mp4", FormatInfo{Kind: "video", Format: "mp4"}, true},
		{"reel.M4V", FormatInfo{Kind: "video", Format: "mp4"}, true},
		// non-media
		{"notes.txt", FormatInfo{}, false},
		{"sidecar.xmp", FormatInfo{}, false},
		{"thumbs.db", FormatInfo{}, false},
		{"no-extension", FormatInfo{}, false},
		{".hidden", FormatInfo{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DetectFromExt(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("DetectFromExt(%q) ok=%v, want %v", tc.name, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("DetectFromExt(%q) = %+v, want %+v", tc.name, got, tc.want)
			}
		})
	}
}
