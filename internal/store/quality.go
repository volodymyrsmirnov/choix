package store

import (
	"context"
	"database/sql"
	"fmt"
)

// QualityItem is one photo waiting for a quality rating. ContentHash is
// carried so the eventual write can be made conditional on the bytes the
// scorer actually looked at.
type QualityItem struct {
	FileID      int64
	ContentHash string
}

// qualityPendingWhere selects photos that finished the per-file pipeline and
// have not been handled by the given scorer: never rated, or rated (or found
// unratable) by a scorer with another id.
const qualityPendingWhere = ` WHERE kind = 'photo' AND scan_status = 'analyzed'
	AND (quality_model IS NULL OR quality_model <> ?)`

// CountQualityPending returns how many photos still need a rating from the
// scorer identified by scorerID.
func (r *FilesRepo) CountQualityPending(scorerID string) (int, error) {
	row := r.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM files`+qualityPendingWhere, scorerID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("CountQualityPending: %w", err)
	}
	return n, nil
}

// ListQualityPending returns up to limit pending photos with id > afterID,
// ordered by id. Callers sweep the set once by passing the last id they saw:
// a photo that could not be handled this time (and so stays pending) is
// stepped over instead of being returned again and again.
func (r *FilesRepo) ListQualityPending(scorerID string, afterID int64, limit int) ([]QualityItem, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT id, content_hash FROM files`+qualityPendingWhere+` AND id > ? ORDER BY id LIMIT ?`,
		scorerID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("ListQualityPending: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []QualityItem
	for rows.Next() {
		var it QualityItem
		if err := rows.Scan(&it.FileID, &it.ContentHash); err != nil {
			return nil, fmt.Errorf("ListQualityPending: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetQuality records the outcome of rating one photo: raw is the scorer's
// raw score, or NULL when the photo cannot be rated (which still stamps scorerID so it
// is not retried forever). The write only applies while the file still has
// the content hash the scorer read; it reports false when the file changed
// or vanished in the meantime, in which case the result is simply dropped —
// the rescan has already reset the row and it will be rated again later.
func (r *FilesRepo) SetQuality(id int64, contentHash string, raw sql.NullFloat64, scorerID string) (bool, error) {
	res, err := r.db.ExecContext(context.Background(),
		`UPDATE files SET quality_raw = ?, quality_model = ?
		  WHERE id = ? AND content_hash = ? AND scan_status = 'analyzed'`,
		raw, scorerID, id, contentHash)
	if err != nil {
		return false, fmt.Errorf("SetQuality: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
