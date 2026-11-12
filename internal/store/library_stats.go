package store

import (
	"context"
	"database/sql"
	"fmt"
)

// LibraryStats summarises a folder's files, ignoring those gone from disk
// (scan_status 'missing').
type LibraryStats struct {
	Photos, Videos int
	// Failed counts files with scan_status 'failed'; they are also counted
	// in Photos or Videos.
	Failed int
	// FirstCaptured and LastCaptured are the earliest and latest
	// captured_at (Unix seconds); invalid when no file has a capture time.
	FirstCaptured, LastCaptured sql.NullInt64
}

// LibraryStats counts the folder's photos, videos and failed files and
// finds its capture-time range, over rows whose scan_status is not
// 'missing'.
func (r *FilesRepo) LibraryStats() (LibraryStats, error) {
	var st LibraryStats
	err := r.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(kind = 'photo'), 0),
		       COALESCE(SUM(kind = 'video'), 0),
		       COALESCE(SUM(scan_status = 'failed'), 0),
		       MIN(captured_at),
		       MAX(captured_at)
		  FROM files
		 WHERE scan_status <> 'missing'`).Scan(
		&st.Photos, &st.Videos, &st.Failed, &st.FirstCaptured, &st.LastCaptured)
	if err != nil {
		return LibraryStats{}, fmt.Errorf("LibraryStats: %w", err)
	}
	return st, nil
}

// PreviewCandidate is a file whose 'thumb' tier can stand for its folder.
type PreviewCandidate struct {
	FileID int64
	Kind   string // 'photo' | 'video'
}

// PreviewCandidates lists the files that are neither missing nor rejected
// and have a 'thumb' row, ordered by captured_at (files without one last),
// then id.
func (r *FilesRepo) PreviewCandidates() ([]PreviewCandidate, error) {
	rows, err := r.db.QueryContext(context.Background(), `
		SELECT f.id, f.kind
		  FROM files f
		  JOIN thumbs t ON t.file_id = f.id AND t.tier = 'thumb'
		 WHERE f.scan_status <> 'missing'
		   AND NOT EXISTS (SELECT 1 FROM picks p WHERE p.file_id = f.id AND p.state = 'rejected')
		 ORDER BY f.captured_at IS NULL, f.captured_at, f.id`)
	if err != nil {
		return nil, fmt.Errorf("PreviewCandidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PreviewCandidate
	for rows.Next() {
		var c PreviewCandidate
		if err := rows.Scan(&c.FileID, &c.Kind); err != nil {
			return nil, fmt.Errorf("PreviewCandidates: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PreviewCandidates: %w", err)
	}
	return out, nil
}
