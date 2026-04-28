package store

import (
	"context"
	"fmt"
)

// PickByStatus returns up to limit files currently in the given scan_status,
// ordered by id (deterministic across runs). The returned slice is empty (not
// nil) when no rows match.
func (r *FilesRepo) PickByStatus(ctx context.Context, status string, limit int) ([]File, error) {
	rows, err := r.db.QueryContext(ctx, filesSelect+` WHERE scan_status = ? ORDER BY id LIMIT ?`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("query files by status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]File, 0, limit)
	for rows.Next() {
		f, serr := scanFile(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
