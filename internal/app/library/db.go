// Package library holds the user's Library: the folders they have added, each
// with its security-scoped bookmark, the cached statistics the Library page
// shows, and the persisted background-job queue. It lives in its own SQLite
// database, library.db, separate from each folder's state.db.
//
// The package does not import Wails.
package library

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"

	_ "modernc.org/sqlite" // the "sqlite" database/sql driver
)

// ErrNotFound reports that no folder has the requested id.
var ErrNotFound = errors.New("library: folder not found")

// Job kinds stored in folders.job_kind. A "full" job does everything a
// "backfill" does, so a pending full job is never downgraded.
const (
	jobFull     = "full"
	jobBackfill = "backfill"
)

// kvJobSeq is the kv key holding the last job sequence number handed out.
const kvJobSeq = "job_seq"

// connPragmas are applied by the driver to every connection it opens.
var connPragmas = []string{
	"journal_mode(WAL)",
	"foreign_keys(on)",
	"busy_timeout(10000)",
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Stats are a folder's cached counts for the Library page. FirstCaptured and
// LastCaptured are NULL when no file has a capture time.
type Stats struct {
	Photos, Videos, Clusters, Picked, Rejected, Failed int
	FirstCaptured, LastCaptured                        sql.NullInt64
}

// Folder is one row of the folders table. Path is absolute (the only
// database path that is not relative to a folder root).
type Folder struct {
	ID, Path, Name         string
	Bookmark               []byte
	VolumeUUID, VolumeName string
	AddedAt                int64
	// Presence is present, offline, missing or access_lost.
	Presence string
	// JobPending is set while work is queued, running or was interrupted.
	JobPending bool
	// JobKind is "", "full" or "backfill".
	JobKind, JobError string
	Stats             Stats
	Previews          int
	PreviewsStamp     int64
}

// PendingJob is a queued (or interrupted) job: the folder, the kind of work
// and the sequence number of its latest enqueue.
type PendingJob struct {
	ID, Kind string
	Seq      int64
}

// DB is an open library.db.
type DB struct {
	db *sql.DB
}

// OpenDB opens (or creates) the library database file at path and migrates
// it to the latest version. path is a filesystem path, never a DSN: the URI
// is built with store.FileURI, carrying WAL, foreign keys and a 10 s busy
// timeout for every connection. The directory must already exist.
func OpenDB(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("library: open: empty database path")
	}
	db, err := sql.Open("sqlite", store.FileURI(path, connPragmas...))
	if err != nil {
		return nil, fmt.Errorf("library: open %s: %w", path, err)
	}
	// One connection, as in internal/store: writes never contend with each
	// other, and EnqueueJob's read-modify-write runs alone.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("library: ping %s: %w", path, err)
	}
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("library: migrate %s: %w", path, err)
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// migrate applies every embedded migration whose NNN_ prefix is above the
// database's user_version, each in its own transaction.
func (d *DB) migrate() error {
	ctx := context.Background()

	var current int
	if err := d.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var v int
		if _, err := fmt.Sscanf(name, "%03d_", &v); err != nil {
			return fmt.Errorf("parse migration version from %q: %w", name, err)
		}
		if v <= current {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("bump user_version after %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}
	return nil
}

const folderColumns = `id, path, name, bookmark, volume_uuid, volume_name, added_at,
	presence, job_pending, job_kind, job_error,
	photos, videos, clusters, picked, rejected, failed, first_captured, last_captured,
	previews, previews_stamp`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFolder(r rowScanner) (Folder, error) {
	var f Folder
	err := r.Scan(&f.ID, &f.Path, &f.Name, &f.Bookmark, &f.VolumeUUID, &f.VolumeName, &f.AddedAt,
		&f.Presence, &f.JobPending, &f.JobKind, &f.JobError,
		&f.Stats.Photos, &f.Stats.Videos, &f.Stats.Clusters, &f.Stats.Picked, &f.Stats.Rejected,
		&f.Stats.Failed, &f.Stats.FirstCaptured, &f.Stats.LastCaptured,
		&f.Previews, &f.PreviewsStamp)
	return f, err
}

// InsertFolder adds f. Its identity, location, bookmark, AddedAt and
// Presence are stored ("present" when empty); job state, statistics and
// previews start at their defaults and change only through their own
// methods. A path equal to an existing folder's, ignoring case, is refused.
func (d *DB) InsertFolder(f Folder) error {
	presence := f.Presence
	if presence == "" {
		presence = "present"
	}
	_, err := d.db.ExecContext(context.Background(),
		`INSERT INTO folders (id, path, name, bookmark, volume_uuid, volume_name, added_at, presence)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.Path, f.Name, f.Bookmark, f.VolumeUUID, f.VolumeName, f.AddedAt, presence)
	if err != nil {
		return fmt.Errorf("library: insert folder %s: %w", f.ID, err)
	}
	return nil
}

// Folders returns every folder, ordered by added_at, then id.
func (d *DB) Folders() ([]Folder, error) {
	rows, err := d.db.QueryContext(context.Background(),
		`SELECT `+folderColumns+` FROM folders ORDER BY added_at, id`)
	if err != nil {
		return nil, fmt.Errorf("library: list folders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("library: scan folder: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list folders: %w", err)
	}
	return out, nil
}

// Folder returns the folder with this id, or ErrNotFound.
func (d *DB) Folder(id string) (Folder, error) {
	f, err := scanFolder(d.db.QueryRowContext(context.Background(),
		`SELECT `+folderColumns+` FROM folders WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, fmt.Errorf("library: folder %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Folder{}, fmt.Errorf("library: folder %s: %w", id, err)
	}
	return f, nil
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execOne runs an UPDATE or DELETE addressed to one folder id and returns
// ErrNotFound when no row matched.
func execOne(ctx context.Context, e execer, op, id, query string, args ...any) error {
	res, err := e.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("library: %s %s: %w", op, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("library: %s %s: rows affected: %w", op, id, err)
	}
	if n == 0 {
		return fmt.Errorf("library: %s %s: %w", op, id, ErrNotFound)
	}
	return nil
}

// DeleteFolder removes the folder's row. ErrNotFound when there is none.
func (d *DB) DeleteFolder(id string) error {
	return execOne(context.Background(), d.db, "delete folder", id,
		`DELETE FROM folders WHERE id = ?`, id)
}

// UpdateLocation records that the folder now lives at path, named name, on
// volume v. A path equal to another folder's, ignoring case, is refused.
func (d *DB) UpdateLocation(id, path, name string, v platform.VolumeInfo) error {
	return execOne(context.Background(), d.db, "update location of", id,
		`UPDATE folders SET path = ?, name = ?, volume_uuid = ?, volume_name = ? WHERE id = ?`,
		path, name, v.UUID, v.Name, id)
}

// SetBookmark replaces the folder's security-scoped bookmark.
func (d *DB) SetBookmark(id string, bookmark []byte) error {
	return execOne(context.Background(), d.db, "set bookmark of", id,
		`UPDATE folders SET bookmark = ? WHERE id = ?`, bookmark, id)
}

// SetPresence records the folder's presence: present, offline, missing or
// access_lost.
func (d *DB) SetPresence(id, presence string) error {
	return execOne(context.Background(), d.db, "set presence of", id,
		`UPDATE folders SET presence = ? WHERE id = ?`, presence, id)
}

// EnqueueJob queues work of kind ("full" or "backfill") for the folder and
// returns the new sequence number. In one transaction it takes seq = the kv
// counter "job_seq" + 1, stores it back, and sets job_pending = 1,
// job_seq = seq, and empties job_error. job_kind becomes kind, except that a
// pending "full" job is not downgraded to "backfill". An unknown folder is
// ErrNotFound and consumes no sequence number.
func (d *DB) EnqueueJob(id, kind string) (seq int64, err error) {
	if kind != jobFull && kind != jobBackfill {
		return 0, fmt.Errorf("library: enqueue job for %s: unknown kind %q", id, kind)
	}
	ctx := context.Background()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("library: enqueue job for %s: begin: %w", id, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var last int64
	var raw string
	switch err = tx.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, kvJobSeq).Scan(&raw); {
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	case err != nil:
		return 0, fmt.Errorf("library: enqueue job for %s: read %s: %w", id, kvJobSeq, err)
	default:
		if last, err = strconv.ParseInt(raw, 10, 64); err != nil {
			return 0, fmt.Errorf("library: enqueue job for %s: parse %s %q: %w", id, kvJobSeq, raw, err)
		}
	}
	seq = last + 1

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
		kvJobSeq, strconv.FormatInt(seq, 10)); err != nil {
		return 0, fmt.Errorf("library: enqueue job for %s: store %s: %w", id, kvJobSeq, err)
	}
	if err = execOne(ctx, tx, "enqueue job for", id,
		`UPDATE folders SET
		   job_kind = CASE WHEN job_pending = 1 AND job_kind = 'full' THEN 'full' ELSE ? END,
		   job_pending = 1, job_seq = ?, job_error = ''
		 WHERE id = ?`,
		kind, seq, id); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("library: enqueue job for %s: commit: %w", id, err)
	}
	return seq, nil
}

// FinishJob records the end of the run identified by seq: it clears
// job_pending and job_kind and stores errText ("" for success), but only
// while the folder's job_seq is still seq. cleared is false when the folder
// was enqueued again meanwhile (the newer job stays pending) or is gone.
func (d *DB) FinishJob(id string, seq int64, errText string) (cleared bool, err error) {
	res, err := d.db.ExecContext(context.Background(),
		`UPDATE folders SET job_pending = 0, job_kind = '', job_error = ?
		 WHERE id = ? AND job_seq = ?`,
		errText, id, seq)
	if err != nil {
		return false, fmt.Errorf("library: finish job %d of %s: %w", seq, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("library: finish job %d of %s: rows affected: %w", seq, id, err)
	}
	return n > 0, nil
}

// ClearJob clears whatever job is pending for the folder (used when it is
// removed), whatever its sequence number.
func (d *DB) ClearJob(id string) error {
	return execOne(context.Background(), d.db, "clear job of", id,
		`UPDATE folders SET job_pending = 0, job_kind = '' WHERE id = ?`, id)
}

// PendingJobs returns the folders with job_pending = 1 in job_seq order —
// the order in which they were enqueued.
func (d *DB) PendingJobs() ([]PendingJob, error) {
	rows, err := d.db.QueryContext(context.Background(),
		`SELECT id, job_kind, job_seq FROM folders WHERE job_pending = 1 ORDER BY job_seq`)
	if err != nil {
		return nil, fmt.Errorf("library: pending jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PendingJob
	for rows.Next() {
		var j PendingJob
		if err := rows.Scan(&j.ID, &j.Kind, &j.Seq); err != nil {
			return nil, fmt.Errorf("library: scan pending job: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: pending jobs: %w", err)
	}
	return out, nil
}

// SetStats replaces the folder's cached statistics.
func (d *DB) SetStats(id string, s Stats) error {
	return execOne(context.Background(), d.db, "set stats of", id,
		`UPDATE folders SET photos = ?, videos = ?, clusters = ?, picked = ?, rejected = ?, failed = ?,
		   first_captured = ?, last_captured = ?
		 WHERE id = ?`,
		s.Photos, s.Videos, s.Clusters, s.Picked, s.Rejected, s.Failed,
		s.FirstCaptured, s.LastCaptured, id)
}

// SetPreviews records how many preview thumbnails the folder has and the
// stamp they were counted at.
func (d *DB) SetPreviews(id string, n int, stamp int64) error {
	return execOne(context.Background(), d.db, "set previews of", id,
		`UPDATE folders SET previews = ?, previews_stamp = ? WHERE id = ?`, n, stamp, id)
}

// KVGet returns the value stored under k; ok is false when there is none.
func (d *DB) KVGet(k string) (v string, ok bool, err error) {
	err = d.db.QueryRowContext(context.Background(), `SELECT v FROM kv WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("library: kv get %q: %w", k, err)
	}
	return v, true, nil
}

// KVSet stores v under k, replacing any previous value.
func (d *DB) KVSet(k, v string) error {
	if _, err := d.db.ExecContext(context.Background(),
		`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v); err != nil {
		return fmt.Errorf("library: kv set %q: %w", k, err)
	}
	return nil
}
