package store

import (
	"context"
	"database/sql"
	"errors"
)

// KVRepo provides access to the kv table (per-folder key-value settings).
type KVRepo struct{ db *sql.DB }

// KV returns a KVRepo backed by the store's DB connection.
func (s *Store) KV() *KVRepo { return &KVRepo{db: s.db} }

// Get returns the value for the given key. Returns ErrNotFound if missing.
func (r *KVRepo) Get(k string) (string, error) {
	var v string
	err := r.db.QueryRowContext(context.Background(),
		`SELECT value FROM kv WHERE key = ?`, k).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

// Set inserts or replaces the value for the given key.
func (r *KVRepo) Set(k, v string) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO kv (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		k, v)
	return err
}

// Delete removes the row for the given key. A missing key is not an error.
func (r *KVRepo) Delete(k string) error {
	_, err := r.db.ExecContext(context.Background(),
		`DELETE FROM kv WHERE key = ?`, k)
	return err
}
