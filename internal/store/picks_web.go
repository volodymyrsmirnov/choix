package store

import (
	"context"
)

// All returns every row in the picks table (any state), ordered by file_id.
func (r *PicksRepo) All() ([]Pick, error) {
	rows, err := r.db.QueryContext(context.Background(),
		picksSelect+` ORDER BY file_id`)
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

// GetByFileID returns the pick for the given file. Returns ErrNotFound if missing.
// Alias for Get to match the name used by the web layer.
func (r *PicksRepo) GetByFileID(fileID int64) (Pick, error) {
	return r.Get(fileID)
}

// StateMap returns a map of file_id -> state for all picks. Used for O(1)
// state lookup when building library views.
func (r *PicksRepo) StateMap() (map[int64]string, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT file_id, state FROM picks`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]string)
	for rows.Next() {
		var fid int64
		var state string
		if err := rows.Scan(&fid, &state); err != nil {
			return nil, err
		}
		out[fid] = state
	}
	return out, rows.Err()
}

// CountState returns the number of picks in the given state.
func (r *PicksRepo) CountState(state string) (int, error) {
	var n int
	err := r.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM picks WHERE state = ?`, state).Scan(&n)
	return n, err
}
