package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// isUniqueConstraintError reports whether err is a SQLite UNIQUE constraint
// violation as returned by modernc.org/sqlite.
func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

var ErrNotFound = errors.New("not found")

// File represents a row in the files table.
type File struct {
	ID          int64
	Path        string
	Size        int64
	Mtime       int64
	ContentHash string
	Kind        string // 'photo' | 'video'
	Format      string // 'raf' | 'heic' | 'jpeg' | 'mov' | ...
	DeviceKey   sql.NullString
	CapturedAt  sql.NullInt64
	Width       sql.NullInt64
	Height      sql.NullInt64
	ISO         sql.NullInt64
	Aperture    sql.NullFloat64
	Shutter     sql.NullString
	FocalLength sql.NullFloat64
	GPSLat      sql.NullFloat64
	GPSLon      sql.NullFloat64
	RawExif     []byte
	ScanStatus  string
	ErrMsg      sql.NullString
}

// FilesRepo provides access to the files table.
type FilesRepo struct{ db *sql.DB }

// Files returns a FilesRepo backed by the store's DB connection.
func (s *Store) Files() *FilesRepo { return &FilesRepo{db: s.db} }

const filesSelect = `SELECT id, path, size, mtime, content_hash, kind, format,
	device_key, captured_at, width, height, iso, aperture, shutter, focal_length,
	gps_lat, gps_lon, raw_exif, scan_status, error FROM files`

// Insert inserts a new file row and returns its id. If a row with the same
// path (case-insensitive) already exists, Insert is a no-op and returns the
// existing row's id. The atomic UPSERT closes the TOCTOU window between a
// "does this path exist?" lookup and the actual INSERT, so concurrent
// scanners or rapid re-runs cannot create duplicate rows for the same file.
func (r *FilesRepo) Insert(f File) (int64, error) {
	row := r.db.QueryRowContext(context.Background(), `
		INSERT INTO files (path, size, mtime, content_hash, kind, format, scan_status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET path = files.path
		RETURNING id`,
		f.Path, f.Size, f.Mtime, f.ContentHash, f.Kind, f.Format, f.ScanStatus)
	var id int64
	if err := row.Scan(&id); err != nil {
		// ON CONFLICT(path) only catches exact-case collisions. If a row
		// already exists with a different casing (e.g., "Day/A.JPG" vs
		// "day/a.jpg"), idx_files_path_nocase fires a UNIQUE constraint
		// error. Fall back to a case-insensitive lookup so the caller
		// still gets the existing row's id rather than an error.
		if isUniqueConstraintError(err) {
			existing, lerr := r.GetByPath(f.Path)
			if lerr == nil {
				return existing.ID, nil
			}
		}
		return 0, err
	}
	return id, nil
}

// GetByPath looks up a file by its path. Lookup is case-insensitive so that
// `IMG.heic` and `img.heic` resolve to the same row on macOS APFS, matching
// the unique-by-LOWER(path) invariant added in migration 003. Returns
// ErrNotFound if missing.
func (r *FilesRepo) GetByPath(path string) (File, error) {
	row := r.db.QueryRowContext(context.Background(), filesSelect+` WHERE LOWER(path) = LOWER(?)`, path)
	return scanFile(row)
}

// GetByID looks up a file by its primary key. Returns ErrNotFound if missing.
func (r *FilesRepo) GetByID(id int64) (File, error) {
	row := r.db.QueryRowContext(context.Background(), filesSelect+` WHERE id = ?`, id)
	return scanFile(row)
}

// GetByIDs returns a map[id]File for the supplied ids in one round-trip.
// IDs not present in the table are simply absent from the map; missing rows
// do not produce an error.
func (r *FilesRepo) GetByIDs(ids []int64) (map[int64]File, error) {
	out := make(map[int64]File, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	//nolint:gosec // placeholders are "?" literals; only ids flow through args
	q := filesSelect + ` WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.db.QueryContext(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out[f.ID] = f
	}
	return out, rows.Err()
}

// UpdateMetadata sets EXIF-derived fields and transitions scan_status to 'metadata'.
// Only the metadata fields and scan_status are written; other columns are left untouched.
func (r *FilesRepo) UpdateMetadata(f File) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE files
		    SET device_key   = ?,
		        captured_at  = ?,
		        width        = ?,
		        height       = ?,
		        iso          = ?,
		        aperture     = ?,
		        shutter      = ?,
		        focal_length = ?,
		        raw_exif     = ?,
		        scan_status  = 'metadata',
		        error        = NULL
		  WHERE id = ?`,
		f.DeviceKey, f.CapturedAt, f.Width, f.Height, f.ISO,
		f.Aperture, f.Shutter, f.FocalLength, f.RawExif, f.ID)
	return err
}

// UpdateStatus transitions scan_status. If errMsg is non-empty, error is set; otherwise
// the error column is cleared (so a recovered file no longer carries a stale error).
func (r *FilesRepo) UpdateStatus(id int64, status, errMsg string) error {
	var errVal sql.NullString
	if errMsg != "" {
		errVal = sql.NullString{String: errMsg, Valid: true}
	}
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE files SET scan_status = ?, error = ? WHERE id = ?`,
		status, errVal, id)
	return err
}

// BumpAnalyzedMissingEmbedding rewinds every 'analyzed' file row that
// lacks a CLIP embedding back to 'thumbed' (the analyze stage's input
// status). The first-run wizard calls this after installing the CLIP
// model so files that were analyzed without one get re-processed on the
// next pipeline run.
func (r *FilesRepo) BumpAnalyzedMissingEmbedding() (int64, error) {
	res, err := r.db.ExecContext(context.Background(), `
		UPDATE files
		   SET scan_status = 'thumbed', error = NULL
		 WHERE scan_status = 'analyzed'
		   AND id NOT IN (SELECT file_id FROM ai_signals WHERE clip_embedding IS NOT NULL)`)
	if err != nil {
		return 0, fmt.Errorf("BumpAnalyzedMissingEmbedding: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CountByStatus returns the number of files at the given scan_status.
// Used by the pipeline to stabilize progress denominators.
func (r *FilesRepo) CountByStatus(status string) (int, error) {
	row := r.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM files WHERE scan_status = ?`, status)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("CountByStatus: %w", err)
	}
	return n, nil
}

// IDsByStatus returns up to limit file IDs with the given scan_status, ordered
// by id. Used by pipeline workers (e.g. the thumb builder) that only need IDs.
func (r *FilesRepo) IDsByStatus(status string, limit int) ([]int64, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT id FROM files WHERE scan_status = ? ORDER BY id LIMIT ?`,
		status, limit)
	if err != nil {
		return nil, fmt.Errorf("IDsByStatus: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetScanStatus transitions a file's scan_status. When errMsg is non-empty the
// error column is set; otherwise it is cleared. Mirrors UpdateStatus semantics
// with a more descriptive name for pipeline-stage code.
func (r *FilesRepo) SetScanStatus(id int64, status, errMsg string) error {
	return r.UpdateStatus(id, status, errMsg)
}

// MarkScanFailure sets scan_status='failed' and records errMsg in the error
// column for the given file, leaving all metadata columns (device_key,
// captured_at, raw_exif, etc.) untouched. Use this instead of
// UpdateMetadataFull when recording a transient processing failure so that
// previously persisted EXIF is not overwritten with NULL/zeros.
func (r *FilesRepo) MarkScanFailure(ctx context.Context, fileID int64, errMsg string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE files SET scan_status = 'failed', error = ? WHERE id = ?`,
		errMsg, fileID)
	return err
}

// ListByStatus returns up to limit files with the given scan_status, ordered by id.
// Used by pipeline workers to dequeue work for the next stage.
func (r *FilesRepo) ListByStatus(status string, limit int) ([]File, error) {
	rows, err := r.db.QueryContext(context.Background(),
		filesSelect+` WHERE scan_status = ? ORDER BY id LIMIT ?`,
		status, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MarkMissing transitions a file's scan_status to 'missing'. Used during re-scan when a
// previously known path is no longer present on disk. A no-op if the path is unknown.
func (r *FilesRepo) MarkMissing(path string) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE files SET scan_status = 'missing' WHERE path = ?`, path)
	return err
}

// DeleteUnderPath removes every files row whose path equals dir or lives
// underneath it. Used by the scanner to evict picks-dir contents that an
// earlier build may have ingested as originals. The match is
// case-insensitive to align with the LOWER(path) identity rules. Returns
// the number of rows deleted.
//
// Picks/rejects/cluster_members rows referencing those files are removed
// by ON DELETE CASCADE. Empty dir is a no-op.
func (r *FilesRepo) DeleteUnderPath(ctx context.Context, dir string) (int64, error) {
	if dir == "" {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM files
		  WHERE LOWER(path) = LOWER(?)
		     OR LOWER(path) LIKE LOWER(?) || '/%'`,
		dir, dir)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UpsertOnRescan inserts a new files row for path, or updates the existing
// row's size/mtime/content_hash/kind/format atomically. When the content
// hash matches, scan_status and existing metadata are preserved (so a
// re-scan never re-triggers thumb generation or analysis for unchanged
// files). When the hash differs, scan_status resets to 'discovered' so
// downstream stages re-run on the new contents.
//
// The upsert keys on path (case-insensitive via the LOWER(path) unique
// index added in migration 003), so a re-scan that visits the same file
// with a different casing — common on macOS APFS — does not create a
// duplicate row.
//
// Returns the row id and a boolean reporting whether the file was newly
// inserted (true) or already existed (false).
func (r *FilesRepo) UpsertOnRescan(f File) (id int64, inserted bool, err error) {
	existing, err := r.GetByPath(f.Path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, false, err
	}
	if errors.Is(err, ErrNotFound) {
		id, err = r.Insert(f)
		return id, err == nil, err
	}
	if existing.Size == f.Size && existing.Mtime == f.Mtime && existing.ContentHash == f.ContentHash {
		return existing.ID, false, nil // unchanged
	}
	// Modified — reset scan_status, refresh metadata fields.
	_, err = r.db.Exec(
		`UPDATE files SET size=?, mtime=?, content_hash=?, kind=?, format=?, scan_status='discovered', error=NULL WHERE id=?`,
		f.Size, f.Mtime, f.ContentHash, f.Kind, f.Format, existing.ID)
	if err != nil {
		return 0, false, err
	}
	return existing.ID, false, nil
}

// MarkMissingExcept sets scan_status='missing' for every files row whose
// path is NOT in the seen set. Rows already at status='missing' are left
// untouched. Returns the number of rows updated.
func (r *FilesRepo) MarkMissingExcept(seen map[string]int64) (int64, error) {
	if len(seen) == 0 {
		// No files seen — mark everything that isn't already missing.
		res, err := r.db.Exec(
			`UPDATE files SET scan_status='missing' WHERE scan_status != 'missing'`)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	// Build a parameterized NOT IN (...) of seen paths, lowercased so the
	// comparison matches the LOWER(path) unique index. Without this, a
	// re-scan that walks the same file with different casing would mark
	// the existing row missing on the next run.
	args := make([]any, 0, len(seen))
	placeholders := make([]string, 0, len(seen))
	for p := range seen {
		args = append(args, strings.ToLower(p))
		placeholders = append(placeholders, "?")
	}
	//nolint:gosec // placeholders are all "?" literals; only user data flows through args
	q := `UPDATE files SET scan_status='missing'
	      WHERE scan_status != 'missing' AND LOWER(path) NOT IN (` +
		strings.Join(placeholders, ",") + `)`
	res, err := r.db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PathsByStatus returns every file path with the given scan_status. Used during re-scan
// to diff the set of known paths against what was just walked on disk.
func (r *FilesRepo) PathsByStatus(status string) ([]string, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT path FROM files WHERE scan_status = ?`, status)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListAllAnalyzed returns every analyzed file ordered by (device_key,
// captured_at, id). Used by the gap-based grouper, which assigns bucket
// boundaries by walking the per-device timeline rather than aligning to
// absolute clock buckets. Files with NULL captured_at sort last within
// their device.
func (r *FilesRepo) ListAllAnalyzed() ([]File, error) {
	rows, err := r.db.QueryContext(context.Background(),
		filesSelect+` WHERE scan_status = 'analyzed'
			ORDER BY COALESCE(device_key, 'Unknown'),
			         captured_at IS NULL,
			         captured_at,
			         id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListAnalyzedInBucket returns analyzed files whose (device_key, floor(captured_at/bucketSec)*bucketSec)
// matches the supplied bucket. timeBucket.Valid==false selects rows with NULL captured_at.
func (r *FilesRepo) ListAnalyzedInBucket(deviceKey string, timeBucket sql.NullInt64, bucketSec int64) ([]File, error) {
	ctx := context.Background()
	var rows *sql.Rows
	var err error
	if timeBucket.Valid {
		rows, err = r.db.QueryContext(ctx,
			filesSelect+` WHERE scan_status = 'analyzed' AND device_key = ?
			              AND captured_at IS NOT NULL
			              AND (captured_at / ?) * ? = ?`,
			deviceKey, bucketSec, bucketSec, timeBucket.Int64)
	} else {
		rows, err = r.db.QueryContext(ctx,
			filesSelect+` WHERE scan_status = 'analyzed' AND device_key = ? AND captured_at IS NULL`,
			deviceKey)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListAnalyzedDistinctBuckets returns every distinct (device_key, time_bucket) pair
// over rows with scan_status='analyzed'. time_bucket is the floored bucket-start in
// unix seconds, computed using bucketSec; null captured_at yields a NULL TimeBucket.
// Returns the bucket keys ordered by (device_key, time_bucket nulls last).
func (r *FilesRepo) ListAnalyzedDistinctBuckets(bucketSec int64) ([]struct {
	DeviceKey  string
	TimeBucket sql.NullInt64
}, error) {
	ctx := context.Background()
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT
		    COALESCE(device_key, 'Unknown') AS dk,
		    CASE WHEN captured_at IS NULL THEN NULL
		         ELSE (captured_at / ?) * ? END AS tb
		FROM files
		WHERE scan_status = 'analyzed'
		ORDER BY dk, tb IS NULL, tb`,
		bucketSec, bucketSec)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []struct {
		DeviceKey  string
		TimeBucket sql.NullInt64
	}
	for rows.Next() {
		var rec struct {
			DeviceKey  string
			TimeBucket sql.NullInt64
		}
		if err := rows.Scan(&rec.DeviceKey, &rec.TimeBucket); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// MetadataUpdate is the payload for FilesRepo.UpdateMetadataFull.
//
// All fields are unconditionally written: pass zero values for the ones the
// caller cannot determine. RawExif must already be compressed; the meta
// package gzips it before calling here.
type MetadataUpdate struct {
	DeviceKey   string
	CapturedAt  int64
	HasCaptured bool
	Width       int
	Height      int
	ISO         int
	Aperture    float64
	Shutter     string
	FocalLength float64
	GPSLat      float64
	GPSLon      float64
	HasGPS      bool
	RawExif     []byte
	ScanStatus  string
	ErrMsg      string
}

// UpdateMetadataFull writes a parsed metadata record to the files row identified
// by id. The update runs inside its own transaction so a partial write cannot
// leave the row in an inconsistent state.
func (r *FilesRepo) UpdateMetadataFull(ctx context.Context, id int64, u MetadataUpdate) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var capturedAt sql.NullInt64
	if u.HasCaptured {
		capturedAt = sql.NullInt64{Int64: u.CapturedAt, Valid: true}
	}
	var deviceKey sql.NullString
	if u.DeviceKey != "" {
		deviceKey = sql.NullString{String: u.DeviceKey, Valid: true}
	}
	width := nullInt(u.Width)
	height := nullInt(u.Height)
	iso := nullInt(u.ISO)
	aperture := nullFloat(u.Aperture)
	focal := nullFloat(u.FocalLength)
	shutter := nullString(u.Shutter)
	errMsg := nullString(u.ErrMsg)
	var gpsLat, gpsLon sql.NullFloat64
	if u.HasGPS {
		gpsLat = sql.NullFloat64{Float64: u.GPSLat, Valid: true}
		gpsLon = sql.NullFloat64{Float64: u.GPSLon, Valid: true}
	}

	res, err := tx.ExecContext(ctx, `UPDATE files SET
		device_key   = ?,
		captured_at  = ?,
		width        = ?,
		height       = ?,
		iso          = ?,
		aperture     = ?,
		shutter      = ?,
		focal_length = ?,
		gps_lat      = ?,
		gps_lon      = ?,
		raw_exif     = ?,
		scan_status  = ?,
		error        = ?
		WHERE id = ?`,
		deviceKey, capturedAt, width, height, iso, aperture, shutter, focal,
		gpsLat, gpsLon, u.RawExif, u.ScanStatus, errMsg, id)
	if err != nil {
		return fmt.Errorf("update files row: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func nullInt(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func nullFloat(v float64) sql.NullFloat64 {
	if v == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFile(row rowScanner) (File, error) {
	var f File
	err := row.Scan(&f.ID, &f.Path, &f.Size, &f.Mtime, &f.ContentHash, &f.Kind, &f.Format,
		&f.DeviceKey, &f.CapturedAt, &f.Width, &f.Height, &f.ISO, &f.Aperture, &f.Shutter, &f.FocalLength,
		&f.GPSLat, &f.GPSLon, &f.RawExif, &f.ScanStatus, &f.ErrMsg)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrNotFound
		}
		return File{}, err
	}
	return f, nil
}
