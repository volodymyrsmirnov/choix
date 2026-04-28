package store

import (
	"context"
	"database/sql"
	"errors"
)

// Thumb represents a row in the thumbs table.
type Thumb struct {
	FileID  int64
	Tier    string // 'thumb' | 'preview'
	RelPath string
	Width   int64
	Height  int64
}

// ThumbsRepo provides access to the thumbs table.
type ThumbsRepo struct{ db *sql.DB }

// Thumbs returns a ThumbsRepo backed by the store's DB connection.
func (s *Store) Thumbs() *ThumbsRepo { return &ThumbsRepo{db: s.db} }

// Insert is an alias for Upsert for API compatibility with the web layer.
func (r *ThumbsRepo) Insert(t Thumb) error { return r.Upsert(t) }

// Upsert inserts a thumb row or replaces it on (file_id, tier) collision.
func (r *ThumbsRepo) Upsert(t Thumb) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO thumbs (file_id, tier, rel_path, width, height)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(file_id, tier) DO UPDATE SET
		    rel_path = excluded.rel_path,
		    width    = excluded.width,
		    height   = excluded.height`,
		t.FileID, t.Tier, t.RelPath, t.Width, t.Height)
	return err
}

// Get returns the Thumb row for the given fileID and tier. Returns ErrNotFound
// when no matching row exists.
func (r *ThumbsRepo) Get(fileID int64, tier string) (Thumb, error) {
	row := r.db.QueryRowContext(context.Background(),
		`SELECT file_id, tier, rel_path, width, height FROM thumbs WHERE file_id = ? AND tier = ?`,
		fileID, tier)
	var t Thumb
	err := row.Scan(&t.FileID, &t.Tier, &t.RelPath, &t.Width, &t.Height)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Thumb{}, ErrNotFound
		}
		return Thumb{}, err
	}
	return t, nil
}

// GetByFile returns a map of tier -> Thumb for the given file. Returns an empty
// (non-nil) map if there are no rows.
func (r *ThumbsRepo) GetByFile(fileID int64) (map[string]Thumb, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT file_id, tier, rel_path, width, height FROM thumbs WHERE file_id = ?`,
		fileID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]Thumb)
	for rows.Next() {
		var t Thumb
		if err := rows.Scan(&t.FileID, &t.Tier, &t.RelPath, &t.Width, &t.Height); err != nil {
			return nil, err
		}
		out[t.Tier] = t
	}
	return out, rows.Err()
}
