package catalog

import (
	"database/sql"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestFormatDeviceLabel(t *testing.T) {
	tests := []struct{ key, want string }{
		{"", "Unknown device"},
		{"FUJIFILM X-T5#B2", "FUJIFILM X-T5"},
		{"Apple iPhone 15", "Apple iPhone 15"},
		{"#serial-only", ""},
		{"A#B#C", "A"},
	}
	for _, tt := range tests {
		if got := formatDeviceLabel(tt.key); got != tt.want {
			t.Errorf("formatDeviceLabel(%q) = %q; want %q", tt.key, got, tt.want)
		}
	}
}

func TestFormatClusterDeviceLabel(t *testing.T) {
	members := []store.ClusterMemberView{{FileID: 1}, {FileID: 2}, {FileID: 3}}
	tests := []struct {
		name   string
		key    string
		labels map[int64]string
		want   string
	}{
		{"single device", "X-T5#A1", nil, "X-T5"},
		{"single device ignores member labels", "X-T5#A1", map[int64]string{1: "Other"}, "X-T5"},
		{"merged, sorted and deduped", group.MergedDeviceKey,
			map[int64]string{1: "iPhone 15", 2: "X-T5", 3: "iPhone 15"}, "X-T5 + iPhone 15"},
		{"merged without labels", group.MergedDeviceKey, map[int64]string{}, "Multiple devices"},
		{"merged with empty labels only", group.MergedDeviceKey, map[int64]string{1: ""}, "Multiple devices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := store.Cluster{DeviceKey: tt.key}
			if got := formatClusterDeviceLabel(c, members, tt.labels); got != tt.want {
				t.Errorf("= %q; want %q", got, tt.want)
			}
		})
	}
}

func TestFormatBucketLabel(t *testing.T) {
	if got := formatBucketLabel(0); got != "" {
		t.Errorf("formatBucketLabel(0) = %q; want empty", got)
	}
	const unix = 1714185600
	if got, want := formatBucketLabel(unix), time.Unix(unix, 0).Format("Jan 2 · 15:04"); got != want {
		t.Errorf("formatBucketLabel(%d) = %q; want %q", unix, got, want)
	}
}

func TestMemberQuality(t *testing.T) {
	raw := sql.NullFloat64{Float64: 0.6, Valid: true}
	current := sql.NullString{String: local.QualityScorerID, Valid: true}
	want := local.QualityDisplay(0.6)
	tests := []struct {
		name string
		m    store.ClusterMemberView
		show bool
		want *float64
	}{
		{"rated photo", store.ClusterMemberView{Kind: "photo", QualityRaw: raw, QualityModel: current}, true, &want},
		{"feature off", store.ClusterMemberView{Kind: "photo", QualityRaw: raw, QualityModel: current}, false, nil},
		{"video", store.ClusterMemberView{Kind: "video", QualityRaw: raw, QualityModel: current}, true, nil},
		{"unrated", store.ClusterMemberView{Kind: "photo", QualityModel: current}, true, nil},
		{"another scorer", store.ClusterMemberView{Kind: "photo", QualityRaw: raw,
			QualityModel: sql.NullString{String: "old", Valid: true}}, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memberQuality(tt.m, tt.show)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("= %v; want nil", *got)
			case tt.want != nil && (got == nil || *got != *tt.want):
				t.Errorf("= %v; want %v", got, *tt.want)
			}
		})
	}
}
