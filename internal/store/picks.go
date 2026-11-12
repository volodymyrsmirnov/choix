package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
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
//
// Test fixtures only: it writes outside Store.Durable, so it is not a pick
// commit. Every pick, reject or undo goes through Store.Durable and SetTx.
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
//
// Test fixtures only: it writes outside Store.Durable, so it is not a pick
// commit. Every pick, reject or undo goes through Store.Durable and SetTx.
func (r *PicksRepo) SetState(fileID int64, state string) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO picks (file_id, state, picked_at)
		 VALUES (?, ?, strftime('%s','now'))
		 ON CONFLICT(file_id) DO UPDATE SET state = excluded.state`,
		fileID, state)
	return err
}

// SetTx records the pick state of fileID inside tx (normally the transaction
// Store.Durable runs). state "" deletes the row; otherwise the row is
// inserted or updated with that state and exportedPath ("" stores NULL). An
// existing row keeps its picked_at and rating.
func (r *PicksRepo) SetTx(tx *sql.Tx, fileID int64, state, exportedPath string) error {
	ctx := context.Background()
	if state == "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM picks WHERE file_id = ?`, fileID); err != nil {
			return fmt.Errorf("delete pick %d: %w", fileID, err)
		}
		return nil
	}
	var exported sql.NullString
	if exportedPath != "" {
		exported = sql.NullString{String: exportedPath, Valid: true}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO picks (file_id, state, picked_at, exported_path)
		 VALUES (?, ?, strftime('%s','now'), ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		    state         = excluded.state,
		    exported_path = excluded.exported_path`,
		fileID, state, exported)
	if err != nil {
		return fmt.Errorf("set pick %d to %s: %w", fileID, state, err)
	}
	return nil
}

// ExportReferencedTx reports, inside tx, whether a pick row other than
// exceptFileID records exportedPath as its copy. Paths are compared
// case-insensitively and without a leading "/" (v1 stored an absolute
// picks_dir joined under the root that way).
func (r *PicksRepo) ExportReferencedTx(tx *sql.Tx, exportedPath string, exceptFileID int64) (bool, error) {
	var referenced bool
	err := tx.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM picks
		  WHERE file_id <> ?
		    AND exported_path IS NOT NULL
		    AND LOWER(LTRIM(exported_path, '/')) = LOWER(LTRIM(?, '/')))`,
		exceptFileID, exportedPath).Scan(&referenced)
	if err != nil {
		return false, fmt.Errorf("ExportReferencedTx: %w", err)
	}
	return referenced, nil
}

// ExportDir is a directory (relative to the scan root, slash-separated) that
// holds exported picks, and how many pick rows were exported under it.
type ExportDir struct {
	Dir   string
	Count int
}

// ExportDirs derives, from every pick row with an exported_path, the
// directory the copy was exported under: for exported path E of the file at
// path P, path.Dir(E) with path.Dir(P) stripped from its end and any leading
// "/" trimmed (v1 joined an absolute picks_dir under the scan root). So
// "picks/a/b_2.jpg" for "a/b.jpg" gives "picks". The most used directory comes
// first; ties are ordered by name. Rows whose directory cannot be derived (the
// copy sits at the scan root) are left out.
func (r *PicksRepo) ExportDirs() ([]ExportDir, error) {
	rows, err := r.db.QueryContext(context.Background(), `
		SELECT p.exported_path, f.path
		  FROM picks p JOIN files f ON f.id = p.file_id
		 WHERE p.exported_path IS NOT NULL AND p.exported_path <> ''`)
	if err != nil {
		return nil, fmt.Errorf("export dirs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := map[string]int{}
	for rows.Next() {
		var exported, file string
		if err := rows.Scan(&exported, &file); err != nil {
			return nil, fmt.Errorf("export dirs: %w", err)
		}
		if dir := ExportDirOf(exported, file); dir != "" {
			counts[dir]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export dirs: %w", err)
	}
	out := make([]ExportDir, 0, len(counts))
	for d, n := range counts {
		out = append(out, ExportDir{Dir: d, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

// ExportDirOf returns the directory a copy at exportedPath was exported under,
// for the file at filePath (see ExportDirs), or "" when there is none. The
// suffix match is case-insensitive, like file identity.
func ExportDirOf(exportedPath, filePath string) string {
	dir := path.Dir(exportedPath)
	if sub := path.Dir(filePath); sub != "." {
		suffix := "/" + sub
		if len(dir) >= len(suffix) && strings.EqualFold(dir[len(dir)-len(suffix):], suffix) {
			dir = dir[:len(dir)-len(suffix)]
		}
	}
	dir = strings.TrimLeft(dir, "/")
	if dir == "." {
		return ""
	}
	return dir
}
