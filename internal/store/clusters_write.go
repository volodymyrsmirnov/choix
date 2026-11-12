package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ClusterDef is one (device, bucket, members) tuple ready to be persisted
// by Store.WriteClusters. The grouper builds these in memory after walking
// the per-device timeline; persistence is a pure I/O step.
type ClusterDef struct {
	DeviceKey  string
	TimeBucket sql.NullInt64
	Members    []int64
}

// WriteClusters atomically replaces the clusters and cluster_members
// tables. The whole replacement runs inside a single transaction so
// in-flight readers either see the old clusters or the new ones (never a
// half-rebuild) and the per-row fsyncs that previously dominated a
// recluster collapse to one. Picks live in a separate table and are
// untouched.
func (s *Store) WriteClusters(ctx context.Context, defs []ClusterDef) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM clusters`); err != nil {
		return fmt.Errorf("delete clusters: %w", err)
	}
	if len(defs) == 0 {
		return tx.Commit()
	}

	insClu, err := tx.PrepareContext(ctx,
		`INSERT INTO clusters (device_key, time_bucket) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert cluster: %w", err)
	}
	defer func() { _ = insClu.Close() }()

	insMem, err := tx.PrepareContext(ctx,
		`INSERT INTO cluster_members (cluster_id, file_id) VALUES (?, ?)
		 ON CONFLICT(cluster_id, file_id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("prepare insert member: %w", err)
	}
	defer func() { _ = insMem.Close() }()

	for _, d := range defs {
		res, err := insClu.ExecContext(ctx, d.DeviceKey, d.TimeBucket)
		if err != nil {
			return fmt.Errorf("insert cluster: %w", err)
		}
		cid, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("last insert id: %w", err)
		}
		for _, fid := range d.Members {
			if _, err := insMem.ExecContext(ctx, cid, fid); err != nil {
				return fmt.Errorf("insert member: %w", err)
			}
		}
	}
	return tx.Commit()
}
