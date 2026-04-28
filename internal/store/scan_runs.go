package store

import (
	"context"
	"database/sql"
	"errors"
)

// ScanRun represents a row in the scan_runs table.
type ScanRun struct {
	ID          int64
	StartedAt   int64
	FinishedAt  sql.NullInt64
	Status      string // 'running' | 'paused' | 'completed' | 'failed'
	FilesTotal  sql.NullInt64
	FilesDone   sql.NullInt64
	AITotal     sql.NullInt64
	AIDone      sql.NullInt64
	CancelToken sql.NullString
}

// ScanRunsRepo provides access to the scan_runs table.
type ScanRunsRepo struct{ db *sql.DB }

// ScanRuns returns a ScanRunsRepo backed by the store's DB connection.
func (s *Store) ScanRuns() *ScanRunsRepo { return &ScanRunsRepo{db: s.db} }

const scanRunsSelect = `SELECT id, started_at, finished_at, status,
	files_total, files_done, ai_total, ai_done, cancel_token FROM scan_runs`

// Start records a new scan run with status='running' and returns its id.
func (r *ScanRunsRepo) Start(startedAt int64, cancelToken string) (int64, error) {
	var tok sql.NullString
	if cancelToken != "" {
		tok = sql.NullString{String: cancelToken, Valid: true}
	}
	res, err := r.db.ExecContext(context.Background(),
		`INSERT INTO scan_runs (started_at, status, cancel_token) VALUES (?, 'running', ?)`,
		startedAt, tok)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateProgress overwrites the four counters on the scan run.
func (r *ScanRunsRepo) UpdateProgress(id int64, filesTotal, filesDone, aiTotal, aiDone int64) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE scan_runs SET files_total = ?, files_done = ?, ai_total = ?, ai_done = ? WHERE id = ?`,
		filesTotal, filesDone, aiTotal, aiDone, id)
	return err
}

// Finish marks a scan run terminal: status is set ('completed', 'failed', etc.)
// and finished_at is recorded.
func (r *ScanRunsRepo) Finish(id int64, status string, finishedAt int64) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE scan_runs SET status = ?, finished_at = ? WHERE id = ?`,
		status, finishedAt, id)
	return err
}

// GetLatest returns the most recent scan run by id (highest id wins). ErrNotFound
// if no scan has ever run.
func (r *ScanRunsRepo) GetLatest() (ScanRun, error) {
	row := r.db.QueryRowContext(context.Background(),
		scanRunsSelect+` ORDER BY id DESC LIMIT 1`)
	var sr ScanRun
	err := row.Scan(&sr.ID, &sr.StartedAt, &sr.FinishedAt, &sr.Status,
		&sr.FilesTotal, &sr.FilesDone, &sr.AITotal, &sr.AIDone, &sr.CancelToken)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScanRun{}, ErrNotFound
		}
		return ScanRun{}, err
	}
	return sr, nil
}
