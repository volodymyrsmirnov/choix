package group

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestBucketStartFloors(t *testing.T) {
	cases := []struct {
		captured  int64
		bucketSec int64
		want      int64
	}{
		{0, 600, 0},
		{599, 600, 0},
		{600, 600, 600},
		{1234, 600, 1200},
		{1759983999, 600, 1759983600},
	}
	for _, c := range cases {
		got := BucketStart(c.captured, c.bucketSec)
		if got != c.want {
			t.Errorf("BucketStart(%d, %d) = %d, want %d", c.captured, c.bucketSec, got, c.want)
		}
	}
}

func TestAssignBucketsGroupsByDeviceAndTime(t *testing.T) {
	files := []store.File{
		{ID: 1, DeviceKey: sql.NullString{String: "Fuji#A", Valid: true}, CapturedAt: sql.NullInt64{Int64: 1000, Valid: true}},
		{ID: 2, DeviceKey: sql.NullString{String: "Fuji#A", Valid: true}, CapturedAt: sql.NullInt64{Int64: 1100, Valid: true}}, // same bucket as 1 (600s): both floor to 600
		{ID: 3, DeviceKey: sql.NullString{String: "Fuji#A", Valid: true}, CapturedAt: sql.NullInt64{Int64: 2000, Valid: true}}, // next bucket: 2000 -> 1800
		{ID: 4, DeviceKey: sql.NullString{String: "Sony#B", Valid: true}, CapturedAt: sql.NullInt64{Int64: 1100, Valid: true}}, // different device
		{ID: 5, DeviceKey: sql.NullString{String: "Unknown", Valid: true}, CapturedAt: sql.NullInt64{Valid: false}},            // no timestamp
	}
	got := AssignBuckets(files, 600)

	want := map[BucketKey][]int64{
		{DeviceKey: "Fuji#A", TimeBucket: sql.NullInt64{Int64: 600, Valid: true}}:  {1, 2},
		{DeviceKey: "Fuji#A", TimeBucket: sql.NullInt64{Int64: 1800, Valid: true}}: {3},
		{DeviceKey: "Sony#B", TimeBucket: sql.NullInt64{Int64: 600, Valid: true}}:  {4},
		{DeviceKey: "Unknown", TimeBucket: sql.NullInt64{Valid: false}}:            {5},
	}

	if len(got) != len(want) {
		t.Fatalf("buckets: got %d want %d (%+v)", len(got), len(want), got)
	}
	for k, ids := range got {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		w, ok := want[k]
		if !ok {
			t.Errorf("unexpected bucket %+v", k)
			continue
		}
		if !reflect.DeepEqual(ids, w) {
			t.Errorf("bucket %+v: got %v want %v", k, ids, w)
		}
	}
}
