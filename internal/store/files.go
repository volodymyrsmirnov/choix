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

// getByIDsChunk is how many ids GetByIDs binds per statement: far below
// SQLite's limit on bound parameters (32 766).
const getByIDsChunk = 500

// GetByIDs returns a map[id]File for the supplied ids, querying at most
// getByIDsChunk ids per statement so any number of ids can be asked for.
// IDs not present in the table are simply absent from the map; missing rows
// do not produce an error.
func (r *FilesRepo) GetByIDs(ids []int64) (map[int64]File, error) {
	out := make(map[int64]File, len(ids))
	for start := 0; start < len(ids); start += getByIDsChunk {
		end := min(start+getByIDsChunk, len(ids))
		if err := r.getByIDsInto(out, ids[start:end]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// getByIDsInto adds the rows of ids (one statement, len(ids) parameters) to out.
func (r *FilesRepo) getByIDsInto(out map[int64]File, ids []int64) error {
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
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return err
		}
		out[f.ID] = f
	}
	return rows.Err()
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

// CountAnalyzedMissingEmbedding returns how many 'analyzed' files have no
// CLIP embedding — exactly the rows BumpAnalyzedMissingEmbedding would
// rewind.
func (r *FilesRepo) CountAnalyzedMissingEmbedding() (int, error) {
	var n int
	err := r.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM files
		 WHERE scan_status = 'analyzed'
		   AND id NOT IN (SELECT file_id FROM ai_signals WHERE clip_embedding IS NOT NULL)`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountAnalyzedMissingEmbedding: %w", err)
	}
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

// DeleteUnderPath removes every files row whose path equals dir or lives
// underneath it. Used by the scanner to evict picks-dir contents that an
// earlier build may have ingested as originals. The match is
// case-insensitive to align with the LOWER(path) identity rules. Returns
// the number of rows deleted.
//
// Picks/rejects/cluster_members rows referencing those files are removed
// by ON DELETE CASCADE. Empty dir is a no-op. dir is matched literally (no
// LIKE wildcards), so "my_picks" never evicts "myXpicks/…".
func (r *FilesRepo) DeleteUnderPath(ctx context.Context, dir string) (int64, error) {
	if dir == "" {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM files
		  WHERE LOWER(path) = LOWER(?1) OR `+underDirSQL,
		dir)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// underDirSQL matches rows whose path lies under the directory bound to ?1,
// case-insensitively (like the LOWER(path) identity of migration 003) and
// without LIKE, whose '_' and '%' wildcards a directory name may contain.
const underDirSQL = `LOWER(substr(path, 1, length(?1) + 1)) = LOWER(?1 || '/')`

// PathKnownTx reports, inside tx, whether a files row exists for path
// (case-insensitively), whatever its scan_status: a 'missing' row still names
// an original that may be restored before the next scan.
func (r *FilesRepo) PathKnownTx(tx *sql.Tx, path string) (bool, error) {
	var known bool
	err := tx.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM files WHERE LOWER(path) = LOWER(?))`, path).Scan(&known)
	if err != nil {
		return false, fmt.Errorf("PathKnownTx: %w", err)
	}
	return known, nil
}

// CountUnderPath returns how many files rows that are not 'missing' have a
// path under dir (relative, slash-separated; matched case-insensitively and
// literally). One query, no filesystem access.
func (r *FilesRepo) CountUnderPath(dir string) (int, error) {
	if dir == "" {
		return 0, errors.New("CountUnderPath: empty directory")
	}
	var n int
	err := r.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM files WHERE scan_status <> 'missing' AND `+underDirSQL,
		dir).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountUnderPath: %w", err)
	}
	return n, nil
}

// NewWalkRun returns the generation for a new walk of the folder: one more
// than the largest seen_run stored, so no row holds it yet. The walk stamps
// it on every file it visits (UpsertOnRescan, RecordDiscoveryFailure) and,
// once it has seen the whole folder, hands it to MarkMissingNotSeen.
//
// Walks of one folder are not concurrent (a folder has one job at a time),
// so two walks never draw the same generation.
func (r *FilesRepo) NewWalkRun() (int64, error) {
	var run int64
	if err := r.db.QueryRowContext(context.Background(),
		`SELECT COALESCE(MAX(seen_run), 0) + 1 FROM files`).Scan(&run); err != nil {
		return 0, fmt.Errorf("NewWalkRun: %w", err)
	}
	return run, nil
}

// UpsertOnRescan records that the walk of generation run found the file f
// on disk: it inserts a new files row for f.Path, or updates the existing
// row, and stamps run on it (seen_run).
//
// When size, mtime and content hash match the row, its scan_status and
// metadata are preserved (so a re-scan never re-triggers thumb generation or
// analysis for unchanged files) — except that a row marked 'missing' goes
// back to 'discovered': the file is there again, and the stages, which are
// idempotent, take it up once more. Its pick record is not touched. When
// anything differs, scan_status resets to 'discovered' so downstream stages
// re-run on the new contents; the quality rating described the old bytes,
// so it is cleared as well.
//
// The row is found by path case-insensitively (the LOWER(path) unique index
// of migration 003), so a re-scan that visits the same file with a
// different casing — common on macOS APFS — does not create a duplicate
// row, and keeps the stored path. An insert that loses a race to another
// casing falls back to updating the row that won.
//
// Returns the row id and a boolean reporting whether the file was newly
// inserted (true) or already existed (false).
func (r *FilesRepo) UpsertOnRescan(f File, run int64) (id int64, inserted bool, err error) {
	existing, found, err := r.findOrInsert(f.Path, `INSERT INTO files
		(path, size, mtime, content_hash, kind, format, scan_status, seen_run)
		VALUES (?, ?, ?, ?, ?, ?, 'discovered', ?) RETURNING id`,
		f.Path, f.Size, f.Mtime, f.ContentHash, f.Kind, f.Format, run)
	if err != nil {
		return 0, false, fmt.Errorf("UpsertOnRescan: %w", err)
	}
	if !found {
		return existing.ID, true, nil
	}
	if existing.Size == f.Size && existing.Mtime == f.Mtime && existing.ContentHash == f.ContentHash {
		// Unchanged: stamp the walk; a missing row is back.
		_, err = r.db.ExecContext(context.Background(), `UPDATE files SET seen_run = ?,
			error       = CASE WHEN scan_status = 'missing' THEN NULL ELSE error END,
			scan_status = CASE WHEN scan_status = 'missing' THEN 'discovered' ELSE scan_status END
			WHERE id = ?`, run, existing.ID)
		if err != nil {
			return 0, false, fmt.Errorf("UpsertOnRescan: mark %s seen: %w", f.Path, err)
		}
		return existing.ID, false, nil
	}
	// Modified — reset scan_status, refresh metadata fields.
	_, err = r.db.ExecContext(context.Background(),
		`UPDATE files SET size=?, mtime=?, content_hash=?, kind=?, format=?, scan_status='discovered', error=NULL,
		        quality_raw=NULL, quality_model=NULL, seen_run=? WHERE id=?`,
		f.Size, f.Mtime, f.ContentHash, f.Kind, f.Format, run, existing.ID)
	if err != nil {
		return 0, false, fmt.Errorf("UpsertOnRescan: update %s: %w", f.Path, err)
	}
	return existing.ID, false, nil
}

// RecordDiscoveryFailure records that the walk of generation run found the
// media file f.Path but could not read it. f.Kind and f.Format come from its
// extension; f.Size and f.Mtime are zero when even its status could not be
// read. The row — inserted, or the existing one found case-insensitively —
// gets scan_status 'failed', the error text and seen_run = run, so the file
// is not marked missing and its pick record is kept.
//
// The content hash is set to "" (the bytes were not read), so the next walk
// that reads the file finds it changed and resets it to 'discovered'
// (UpsertOnRescan). An existing row keeps its size, mtime and metadata until
// then. Returns the row id.
func (r *FilesRepo) RecordDiscoveryFailure(f File, run int64, errMsg string) (int64, error) {
	existing, found, err := r.findOrInsert(f.Path, `INSERT INTO files
		(path, size, mtime, content_hash, kind, format, scan_status, error, seen_run)
		VALUES (?, ?, ?, '', ?, ?, 'failed', ?, ?) RETURNING id`,
		f.Path, f.Size, f.Mtime, f.Kind, f.Format, errMsg, run)
	if err != nil {
		return 0, fmt.Errorf("RecordDiscoveryFailure: %w", err)
	}
	if !found {
		return existing.ID, nil
	}
	_, err = r.db.ExecContext(context.Background(),
		`UPDATE files SET content_hash = '', scan_status = 'failed', error = ?, seen_run = ? WHERE id = ?`,
		errMsg, run, existing.ID)
	if err != nil {
		return 0, fmt.Errorf("RecordDiscoveryFailure: update %s: %w", f.Path, err)
	}
	return existing.ID, nil
}

// findOrInsert looks path up case-insensitively. When there is no row it
// runs insert (an INSERT … RETURNING id, with args) and returns found=false
// with the new id in File.ID. When there is one — or the insert lost a race
// to a row with the same path in another casing — it returns that row with
// found=true.
func (r *FilesRepo) findOrInsert(path, insert string, args ...any) (File, bool, error) {
	existing, err := r.GetByPath(path)
	switch {
	case err == nil:
		return existing, true, nil
	case !errors.Is(err, ErrNotFound):
		return File{}, false, fmt.Errorf("look up %s: %w", path, err)
	}
	var id int64
	err = r.db.QueryRowContext(context.Background(), insert, args...).Scan(&id)
	if err == nil {
		return File{ID: id}, false, nil
	}
	if !isUniqueConstraintError(err) {
		return File{}, false, fmt.Errorf("insert %s: %w", path, err)
	}
	existing, err = r.GetByPath(path)
	if err != nil {
		return File{}, false, fmt.Errorf("look up %s after a conflicting insert: %w", path, err)
	}
	return existing, true, nil
}

// MarkMissingNotSeen sets scan_status='missing' on every row that the walk
// of generation run did not see (seen_run differs from run, or is NULL), in
// one statement whatever the size of the folder. Rows already 'missing' are
// left untouched, and so are pick records: a missing file keeps its row and
// its pick until it comes back. Call it only after a walk that visited the
// whole folder. ctx ends the statement early (nothing is changed then).
// Returns the number of rows updated.
func (r *FilesRepo) MarkMissingNotSeen(ctx context.Context, run int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE files SET scan_status = 'missing' WHERE scan_status != 'missing' AND seen_run IS NOT ?`, run)
	if err != nil {
		return 0, fmt.Errorf("MarkMissingNotSeen: %w", err)
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
