// Package group partitions analyzed files into clusters by device, time bucket, and CLIP-visual similarity.
package group

import (
	"database/sql"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// BucketKey identifies a (device, time-bucket) pair. TimeBucket may be NULL for files without a timestamp.
type BucketKey struct {
	DeviceKey  string
	TimeBucket sql.NullInt64
}

// BucketStart returns floor(captured/bucketSec)*bucketSec.
// Both inputs are in unix seconds; bucketSec must be > 0.
func BucketStart(captured int64, bucketSec int64) int64 {
	if bucketSec <= 0 {
		return captured
	}
	return (captured / bucketSec) * bucketSec
}

// AssignBuckets groups files into BucketKey -> []file_id.
// Files lacking a captured_at fall into a {DeviceKey: <whatever>, TimeBucket: NULL} bucket.
// Files lacking a device_key are assigned device_key="Unknown".
func AssignBuckets(files []store.File, bucketSec int64) map[BucketKey][]int64 {
	out := make(map[BucketKey][]int64)
	for _, f := range files {
		dev := "Unknown"
		if f.DeviceKey.Valid && f.DeviceKey.String != "" {
			dev = f.DeviceKey.String
		}
		var tb sql.NullInt64
		if f.CapturedAt.Valid {
			tb = sql.NullInt64{Int64: BucketStart(f.CapturedAt.Int64, bucketSec), Valid: true}
		}
		k := BucketKey{DeviceKey: dev, TimeBucket: tb}
		out[k] = append(out[k], f.ID)
	}
	return out
}
