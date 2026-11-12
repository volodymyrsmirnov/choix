package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
)

// Durable runs fn in a transaction that is committed with PRAGMA
// synchronous=FULL and PRAGMA fullfsync=1, so a successful return means the
// change has reached stable storage (on darwin SQLite issues F_FULLFSYNC).
// It is how a pick, reject or undo is recorded ("never lose a pick").
//
// Durable takes the store's only connection, so nothing else runs on the
// database meanwhile; fn must use tx and nothing else from the store, or it
// waits forever for the connection Durable holds.
//
// The result is fn's error or the commit's error when either fails (nothing
// was committed), and nil after a successful commit — even when restoring
// synchronous=NORMAL and fullfsync=0 afterwards fails. In that case the
// failure is logged and the connection is discarded, so the pool opens a
// fresh one configured by Open's URI. Callers may therefore treat a non-nil
// result as "not recorded" (and, e.g., delete an export) and nil as recorded.
func (s *Store) Durable(ctx context.Context, fn func(tx *sql.Tx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("durable: get connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	err = runDurable(ctx, conn, fn)

	if rerr := restoreSettings(context.WithoutCancel(ctx), conn); rerr != nil {
		slog.Warn("store: restoring synchronous after a durable transaction failed; discarding the connection", "err", rerr)
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return err
}

// runDurable switches conn to full sync and runs fn in a transaction on it.
func runDurable(ctx context.Context, conn *sql.Conn, fn func(tx *sql.Tx) error) error {
	for _, p := range []string{"PRAGMA synchronous=FULL", "PRAGMA fullfsync=1"} {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("durable: %s: %w", p, err)
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			slog.Warn("store: rollback of a durable transaction failed", "err", rerr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable: commit: %w", err)
	}
	return nil
}

// restoreSettings puts conn back to the settings Open's URI gives every
// connection. A variable so tests can make it fail.
var restoreSettings = func(ctx context.Context, conn *sql.Conn) error {
	for _, p := range []string{"PRAGMA synchronous=NORMAL", "PRAGMA fullfsync=0"} {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}
