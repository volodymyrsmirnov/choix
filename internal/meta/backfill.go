package meta

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// BackfillGPS populates files.gps_lat / gps_lon from each row's stored
// raw_exif blob. Run once after the gps columns are added in migration 004
// — files that completed the metadata stage before the migration have
// raw_exif but NULL coordinates, and re-running exiftool on each one would
// be wasteful when the JSON is already on disk.
//
// The function is idempotent: rows that already have gps_lat populated, or
// whose raw_exif is empty, are skipped. Per-row decode failures are logged
// at debug level and the loop keeps going — a single corrupt blob shouldn't
// block startup.
//
// Returns the number of rows that received non-NULL coordinates.
func BackfillGPS(ctx context.Context, st *store.Store) (int, error) {
	db := st.DB()
	rows, err := db.QueryContext(ctx,
		`SELECT id, raw_exif FROM files
		 WHERE gps_lat IS NULL AND raw_exif IS NOT NULL AND length(raw_exif) > 0`)
	if err != nil {
		return 0, fmt.Errorf("scan rows for gps backfill: %w", err)
	}
	type pending struct {
		id  int64
		lat float64
		lon float64
	}
	var todo []pending
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan row: %w", err)
		}
		plain, err := gunzip(raw)
		if err != nil {
			slog.Debug("gps backfill: gunzip failed", "id", id, "err", err)
			continue
		}
		m, err := Parse(plain)
		if err != nil {
			slog.Debug("gps backfill: parse failed", "id", id, "err", err)
			continue
		}
		if !m.HasGPS {
			continue
		}
		todo = append(todo, pending{id: id, lat: m.GPSLatitude, lon: m.GPSLongitude})
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, p := range todo {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		_, err := db.ExecContext(ctx,
			`UPDATE files SET gps_lat = ?, gps_lon = ? WHERE id = ? AND gps_lat IS NULL`,
			sql.NullFloat64{Float64: p.lat, Valid: true},
			sql.NullFloat64{Float64: p.lon, Valid: true},
			p.id)
		if err != nil {
			return 0, fmt.Errorf("update gps for %d: %w", p.id, err)
		}
	}
	return len(todo), nil
}

func gunzip(in []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}
