package store

import (
	"context"
	"database/sql"
	"errors"
)

// Pick represents a row in the picks table.
type Pick struct {
	FileID       int64
	State        string // 'picked' | 'rejected'
	Rating       sql.NullInt64
	PickedAt     int64
	ExportedPath sql.NullString
}

// PicksRepo provides access to the picks table.
type PicksRepo struct{ db *sql.DB }

// Picks returns a PicksRepo backed by the store's DB connection.
func (s *Store) Picks() *PicksRepo { return &PicksRepo{db: s.db} }

const picksSelect = `SELECT file_id, state, rating, picked_at, exported_path FROM picks`

// Upsert inserts a pick row or replaces it on file_id collision. Replacing also
// clears the exported_path (transitioning state means the previous export is no
// longer authoritative; the picks package will re-export or unexport as needed).
func (r *PicksRepo) Upsert(p Pick) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO picks (file_id, state, rating, picked_at, exported_path)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		    state         = excluded.state,
		    rating        = excluded.rating,
		    picked_at     = excluded.picked_at,
		    exported_path = NULL`,
		p.FileID, p.State, p.Rating, p.PickedAt, p.ExportedPath)
	return err
}

// Get returns the pick for the given file. Returns ErrNotFound if missing.
func (r *PicksRepo) Get(fileID int64) (Pick, error) {
	row := r.db.QueryRowContext(context.Background(),
		picksSelect+` WHERE file_id = ?`, fileID)
	var p Pick
	err := row.Scan(&p.FileID, &p.State, &p.Rating, &p.PickedAt, &p.ExportedPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Pick{}, ErrNotFound
		}
		return Pick{}, err
	}
	return p, nil
}

// ListPicked returns every pick currently in state 'picked', ordered by file id.
func (r *PicksRepo) ListPicked() ([]Pick, error) {
	rows, err := r.db.QueryContext(context.Background(),
		picksSelect+` WHERE state = 'picked' ORDER BY file_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Pick
	for rows.Next() {
		var p Pick
		if err := rows.Scan(&p.FileID, &p.State, &p.Rating, &p.PickedAt, &p.ExportedPath); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetExportedPath records the relative path under the scan root where this
// pick has been copied (e.g. "picks/Day1/IMG_0123.RAF").
func (r *PicksRepo) SetExportedPath(fileID int64, path string) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE picks SET exported_path = ? WHERE file_id = ?`, path, fileID)
	return err
}

// ClearExportedPath sets exported_path back to NULL after the picks/ copy is removed.
func (r *PicksRepo) ClearExportedPath(fileID int64) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE picks SET exported_path = NULL WHERE file_id = ?`, fileID)
	return err
}

// PickWithFile joins a pick row with file metadata for display.
type PickWithFile struct {
	FileID       int64
	ExportedPath sql.NullString
	CapturedAt   sql.NullInt64
	DeviceKey    sql.NullString
}

// ListPickedWithFiles returns every pick currently in state 'picked', joined
// with the corresponding file row for display. Ordered by picked_at.
func (r *PicksRepo) ListPickedWithFiles() ([]PickWithFile, error) {
	rows, err := r.db.Query(`
		SELECT p.file_id, p.exported_path, f.captured_at, f.device_key
		FROM picks p JOIN files f ON f.id = p.file_id
		WHERE p.state = 'picked' ORDER BY p.picked_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PickWithFile
	for rows.Next() {
		var pw PickWithFile
		if err := rows.Scan(&pw.FileID, &pw.ExportedPath, &pw.CapturedAt, &pw.DeviceKey); err != nil {
			return nil, err
		}
		out = append(out, pw)
	}
	return out, rows.Err()
}

// SetState updates only the state column for the given file_id. If no row
// exists yet, it inserts one with picked_at = current unix time.
func (r *PicksRepo) SetState(fileID int64, state string) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO picks (file_id, state, picked_at)
		 VALUES (?, ?, strftime('%s','now'))
		 ON CONFLICT(file_id) DO UPDATE SET state = excluded.state`,
		fileID, state)
	return err
}

// Delete removes the pick row for the given file_id. No-op if missing.
func (r *PicksRepo) Delete(fileID int64) error {
	_, err := r.db.ExecContext(context.Background(),
		`DELETE FROM picks WHERE file_id = ?`, fileID)
	return err
}
