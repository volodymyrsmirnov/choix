package scanner

import (
	"path/filepath"
	"strings"
)

// FormatInfo classifies a media file by Kind ('photo'|'video') and
// Format (a small enum: 'raf'|'heic'|'jpeg'|'png'|'mov'|'mp4').
type FormatInfo struct {
	Kind   string // 'photo' | 'video'
	Format string // 'raf' | 'heic' | 'jpeg' | 'png' | 'mov' | 'mp4'
}

// extTable maps lowercased extension (with leading dot) to FormatInfo.
// Keep values aligned with the v1 storage enum on files.kind/files.format.
var extTable = map[string]FormatInfo{
	".raf":  {Kind: "photo", Format: "raf"},
	".dng":  {Kind: "photo", Format: "raf"},
	".heic": {Kind: "photo", Format: "heic"},
	".heif": {Kind: "photo", Format: "heic"},
	".jpg":  {Kind: "photo", Format: "jpeg"},
	".jpeg": {Kind: "photo", Format: "jpeg"},
	".tiff": {Kind: "photo", Format: "jpeg"},
	".tif":  {Kind: "photo", Format: "jpeg"},
	".png":  {Kind: "photo", Format: "png"},
	".mov":  {Kind: "video", Format: "mov"},
	".mp4":  {Kind: "video", Format: "mp4"},
	".m4v":  {Kind: "video", Format: "mp4"},
}

// DetectFromExt inspects the file extension (case-insensitive) and returns
// the matching FormatInfo. Returns ok=false when the extension is empty or
// not recognized as media.
func DetectFromExt(name string) (FormatInfo, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return FormatInfo{}, false
	}
	fi, ok := extTable[ext]
	return fi, ok
}
