package store

import (
	"context"
	"database/sql"
	"errors"
)

// ClusterMemberView is a joined view of a cluster member with file path
// and content hash. ContentHash is included so the web layer can build
// cache-busting media URLs (`?v=<hash>`) without a second roundtrip.
// Kind and Format are included so the web layer can render videos with
// <video> instead of <img> without an additional roundtrip.
type ClusterMemberView struct {
	FileID      int64
	Path        string
	ContentHash string
	Kind        string // 'photo' | 'video'
	Format      string // 'jpeg' | 'heic' | 'mov' | 'mp4' | ...
}

// AllOrdered returns all clusters ordered by device_key, time_bucket (nulls
// last), id. Used by the library view to render cluster strips in a
// deterministic order.
func (r *ClustersRepo) AllOrdered() ([]Cluster, error) {
	rows, err := r.db.QueryContext(context.Background(),
		clustersSelect+` ORDER BY device_key, time_bucket IS NULL, time_bucket, id`)
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

// Members returns the cluster members joined with file paths, content
// hashes, kind, and format, ordered by file_id. The hash is included so a
// single SQL query is enough to render the library and cluster JSON —
// without it, callers fall into an N+1 fetching `files.content_hash` per
// member. Kind and format are included so the web layer can render videos
// with <video> instead of <img> without a second roundtrip.
func (r *ClustersRepo) Members(clusterID int64) ([]ClusterMemberView, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT cm.file_id, f.path, f.content_hash, f.kind, f.format
		 FROM cluster_members cm
		 JOIN files f ON f.id = cm.file_id
		 WHERE cm.cluster_id = ?
		 ORDER BY cm.file_id`,
		clusterID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ClusterMemberView
	for rows.Next() {
		var m ClusterMemberView
		if err := rows.Scan(&m.FileID, &m.Path, &m.ContentHash, &m.Kind, &m.Format); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// NextAfter returns the id of the next cluster after c in library order
// (device_key, time_bucket, id). Returns ErrNotFound if c is last.
func (r *ClustersRepo) NextAfter(c Cluster) (int64, error) {
	var nextID int64
	var err error
	if c.TimeBucket.Valid {
		err = r.db.QueryRowContext(context.Background(),
			`SELECT id FROM clusters
			 WHERE (device_key > ? OR (device_key = ? AND (time_bucket IS NULL OR time_bucket > ?) OR (device_key = ? AND time_bucket = ? AND id > ?)))
			 ORDER BY device_key, time_bucket IS NULL, time_bucket, id
			 LIMIT 1`,
			c.DeviceKey,
			c.DeviceKey, c.TimeBucket.Int64,
			c.DeviceKey, c.TimeBucket.Int64, c.ID).Scan(&nextID)
	} else {
		err = r.db.QueryRowContext(context.Background(),
			`SELECT id FROM clusters
			 WHERE (device_key > ? OR (device_key = ? AND id > ?))
			 ORDER BY device_key, time_bucket IS NULL, time_bucket, id
			 LIMIT 1`,
			c.DeviceKey, c.DeviceKey, c.ID).Scan(&nextID)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return nextID, nil
}

// AddMember is a convenience alias for ClusterMembersRepo.AddMember
// accessible directly on ClustersRepo for test helpers.
func (r *ClustersRepo) AddMember(clusterID, fileID int64) error {
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO cluster_members (cluster_id, file_id) VALUES (?, ?)
		 ON CONFLICT(cluster_id, file_id) DO NOTHING`,
		clusterID, fileID)
	return err
}

// InsertCluster inserts a cluster from a Cluster struct. Returns the assigned id.
func (r *ClustersRepo) InsertCluster(c Cluster) (int64, error) {
	res, err := r.db.ExecContext(context.Background(),
		`INSERT INTO clusters (device_key, time_bucket) VALUES (?, ?)`,
		c.DeviceKey, c.TimeBucket)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
