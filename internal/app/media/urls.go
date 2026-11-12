package media

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// mountPrefix is the Route cmd/choix registers the Handler under (C14);
// Wails strips it before the Handler sees a request.
const mountPrefix = "/media"

// ThumbURL returns the URL of a file's cached thumbnail:
// /media/thumb/<id>/<escaped path>?tier=<tier>&v=<hash>. relPath is the
// file's slash-separated path in the folder; each of its elements is
// percent-escaped. An empty tier is "thumb"; an empty contentHash leaves
// out v (such a URL is not cached for long).
func ThumbURL(folderID, relPath, tier, contentHash string) string {
	if tier == "" {
		tier = thumb.TierThumb
	}
	u := mountPrefix + "/" + routeThumb + "/" + url.PathEscape(folderID) + "/" + escapePath(relPath) +
		"?tier=" + url.QueryEscape(tier)
	if contentHash != "" {
		u += "&v=" + url.QueryEscape(contentHash)
	}
	return u
}

// FullURL returns the URL of a file's full-size image or video:
// /media/full/<id>/<escaped path>?v=<hash>, escaped like ThumbURL.
func FullURL(folderID, relPath, contentHash string) string {
	u := mountPrefix + "/" + routeFull + "/" + url.PathEscape(folderID) + "/" + escapePath(relPath)
	if contentHash != "" {
		u += "?v=" + url.QueryEscape(contentHash)
	}
	return u
}

// PreviewURL returns the URL of image n of a folder's Library preview strip:
// /media/preview/<id>/<n>?v=<stamp>. The stamp changes whenever the strip
// is rebuilt.
func PreviewURL(folderID string, n int, stamp int64) string {
	return mountPrefix + "/" + routePreview + "/" + url.PathEscape(folderID) + "/" + strconv.Itoa(n) +
		"?v=" + strconv.FormatInt(stamp, 10)
}

// escapePath percent-escapes each slash-separated element of rel.
func escapePath(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
