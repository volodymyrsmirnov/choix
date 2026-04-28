package store

import (
	"context"
	"database/sql"
	"errors"
)

// Cluster represents a row in the clusters table. The schema still has
// label / ai_top_file_id / cloud_reasoning columns from the retired AI
// top-pick flow; we no longer read or write them.
type Cluster struct {
	ID         int64
	DeviceKey  string
	TimeBucket sql.NullInt64
}

// ClustersRepo provides access to the clusters table.
type ClustersRepo struct{ db *sql.DB }

// Clusters returns a ClustersRepo backed by the store's DB connection.
func (s *Store) Clusters() *ClustersRepo { return &ClustersRepo{db: s.db} }

const clustersSelect = `SELECT id, device_key, time_bucket FROM clusters`

// Insert inserts a new cluster row for the given device key and time bucket, and returns the assigned id.
func (r *ClustersRepo) Insert(deviceKey string, timeBucket sql.NullInt64) (int64, error) {
	res, err := r.db.ExecContext(context.Background(),
		`INSERT INTO clusters (device_key, time_bucket) VALUES (?, ?)`,
		deviceKey, timeBucket)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get returns the cluster with the given id. Returns ErrNotFound if missing.
func (r *ClustersRepo) Get(id int64) (Cluster, error) {
	row := r.db.QueryRowContext(context.Background(), clustersSelect+` WHERE id = ?`, id)
	return scanCluster(row)
}

// GetByID returns the cluster with the given id. Returns ErrNotFound if missing.
//
// Deprecated: use Get.
func (r *ClustersRepo) GetByID(id int64) (Cluster, error) {
	return r.Get(id)
}

// ListByBucket returns all clusters for a (device_key, time_bucket) pair.
// Pass an invalid TimeBucket to match clusters with NULL time_bucket.
func (r *ClustersRepo) ListByBucket(deviceKey string, timeBucket sql.NullInt64) ([]Cluster, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if timeBucket.Valid {
		rows, err = r.db.QueryContext(context.Background(),
			clustersSelect+` WHERE device_key = ? AND time_bucket = ? ORDER BY id`,
			deviceKey, timeBucket.Int64)
	} else {
		rows, err = r.db.QueryContext(context.Background(),
			clustersSelect+` WHERE device_key = ? AND time_bucket IS NULL ORDER BY id`,
			deviceKey)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Cluster
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListByDeviceTime returns all clusters for a (device_key, time_bucket) pair.
//
// Deprecated: use ListByBucket.
func (r *ClustersRepo) ListByDeviceTime(deviceKey string, timeBucket sql.NullInt64) ([]Cluster, error) {
	return r.ListByBucket(deviceKey, timeBucket)
}

// ListAll returns all clusters ordered by id.
func (r *ClustersRepo) ListAll() ([]Cluster, error) {
	rows, err := r.db.QueryContext(context.Background(),
		clustersSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Cluster
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteByBucket deletes clusters (cascading to cluster_members) matching the device/time-bucket key.
// timeBucket.Valid==false matches the rows where time_bucket IS NULL.
func (r *ClustersRepo) DeleteByBucket(deviceKey string, timeBucket sql.NullInt64) error {
	var err error
	if timeBucket.Valid {
		_, err = r.db.ExecContext(context.Background(),
			`DELETE FROM clusters WHERE device_key = ? AND time_bucket = ?`,
			deviceKey, timeBucket.Int64)
	} else {
		_, err = r.db.ExecContext(context.Background(),
			`DELETE FROM clusters WHERE device_key = ? AND time_bucket IS NULL`,
			deviceKey)
	}
	return err
}

// DeleteByDeviceTime removes every cluster (and via ON DELETE CASCADE every
// cluster_members row) for the given (device_key, time_bucket) pair.
//
// Deprecated: use DeleteByBucket.
func (r *ClustersRepo) DeleteByDeviceTime(deviceKey string, timeBucket sql.NullInt64) error {
	return r.DeleteByBucket(deviceKey, timeBucket)
}

// DeleteAll removes every cluster (cascading to cluster_members). Used by
// the gap-based grouper, which rebuilds the entire cluster table on each
// run because gap-bucket boundaries can shift when settings change.
func (r *ClustersRepo) DeleteAll() error {
	_, err := r.db.ExecContext(context.Background(), `DELETE FROM clusters`)
	return err
}

func scanCluster(row rowScanner) (Cluster, error) {
	var c Cluster
	err := row.Scan(&c.ID, &c.DeviceKey, &c.TimeBucket)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Cluster{}, ErrNotFound
		}
		return Cluster{}, err
	}
	return c, nil
}

// ClusterMembersRepo provides access to the cluster_members table.
type ClusterMembersRepo struct{ db *sql.DB }

// ClusterMembers returns a ClusterMembersRepo backed by the store's DB connection.
func (s *Store) ClusterMembers() *ClusterMembersRepo { return &ClusterMembersRepo{db: s.db} }

// AddMember inserts a cluster_members row. Duplicate (cluster_id, file_id) pairs
// are silently ignored.
func (r *ClusterMembersRepo) AddMember(clusterID, fileID int64) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO cluster_members (cluster_id, file_id) VALUES (?, ?)
		 ON CONFLICT(cluster_id, file_id) DO NOTHING`,
		clusterID, fileID)
	return err
}

// InsertMany inserts multiple cluster_members rows for the given cluster.
// Duplicate (cluster_id, file_id) pairs are silently ignored.
func (r *ClusterMembersRepo) InsertMany(clusterID int64, fileIDs []int64) error {
	for _, fid := range fileIDs {
		if err := r.AddMember(clusterID, fid); err != nil {
			return err
		}
	}
	return nil
}

// ListByCluster returns the file ids belonging to the given cluster, ordered by file_id.
func (r *ClusterMembersRepo) ListByCluster(clusterID int64) ([]int64, error) {
	return r.MembersOf(clusterID)
}

// MembersOf returns the file ids belonging to the given cluster.
func (r *ClusterMembersRepo) MembersOf(clusterID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT file_id FROM cluster_members WHERE cluster_id = ? ORDER BY file_id`,
		clusterID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []int64
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return nil, err
		}
		out = append(out, fid)
	}
	return out, rows.Err()
}

// MembershipOf returns the cluster id a file belongs to. ErrNotFound if the file
// is not a member of any cluster.
func (r *ClusterMembersRepo) MembershipOf(fileID int64) (int64, error) {
	var cid int64
	err := r.db.QueryRowContext(context.Background(),
		`SELECT cluster_id FROM cluster_members WHERE file_id = ?`, fileID).Scan(&cid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return cid, nil
}
