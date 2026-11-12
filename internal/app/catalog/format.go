package catalog

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/quality"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Moved without behavioural change from v1's internal/server (library.go
// and quality.go), which keeps its own copies until it is deleted.

// memberQuality returns the 0–10 rating to show for a cluster member, or nil:
// quality.Display of its raw score, rounded to one decimal as the badge shows
// it. A rating is shown only for photos, only while the feature is on, and
// only when it was produced by the scorer this binary uses (quality.ScorerID)
// — during a re-score after a formula change, old-scale numbers are hidden
// rather than mixed with new ones.
func memberQuality(m store.ClusterMemberView, show bool) *float64 {
	if !show || m.Kind != "photo" || !m.QualityRaw.Valid || m.QualityModel.String != quality.ScorerID {
		return nil
	}
	v := math.Round(quality.Display(m.QualityRaw.Float64)*10) / 10
	return &v
}

// formatDeviceLabel renders a device key as the camera name: the part
// before the first '#' (the serial), or "Unknown device" for an empty key.
func formatDeviceLabel(key string) string {
	if key == "" {
		return "Unknown device"
	}
	for i, ch := range key {
		if ch == '#' {
			return key[:i]
		}
	}
	return key
}

// formatClusterDeviceLabel renders the header label for a cluster. For
// single-device clusters this is the camera name (formatDeviceLabel of
// device_key). For cross-device merged clusters (sentinel
// group.MergedDeviceKey) it's the sorted, deduped list of involved
// cameras joined with " + ", e.g. "X-T5 + iPhone 15".
//
// memberDeviceLabels is keyed by file_id and must contain entries for
// every member when c.DeviceKey is the merged sentinel; otherwise it is
// unused.
func formatClusterDeviceLabel(c store.Cluster, members []store.ClusterMemberView, memberDeviceLabels map[int64]string) string {
	if c.DeviceKey != group.MergedDeviceKey {
		return formatDeviceLabel(c.DeviceKey)
	}
	seen := map[string]struct{}{}
	labels := make([]string, 0, 2)
	for _, m := range members {
		label, ok := memberDeviceLabels[m.FileID]
		if !ok || label == "" {
			continue
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return "Multiple devices"
	}
	sort.Strings(labels)
	return strings.Join(labels, " + ")
}

// formatBucketLabel renders a cluster's time bucket (Unix seconds) in local
// time, or "" for none.
func formatBucketLabel(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("Jan 2 · 15:04")
}
