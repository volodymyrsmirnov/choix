package catalog

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// The views are v1's /api/library and /api/clusters/{id} payloads
// (internal/server/library.go: buildLibraryJSON, handleClusterJSON), moved
// without behavioural change; JSON names are the design types Member,
// Cluster, ExifPayload and FocusResponse (frontend/src/design/types.ts).

// Member is one photo or video of a cluster. ContentHash lets the frontend
// build cache-busting media URLs; Kind ("photo"|"video") and Format ("jpeg",
// "heic", "mov", …) let it render a video as a video.
type Member struct {
	FileID      int64  `json:"file_id"`
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	Kind        string `json:"kind"`
	Format      string `json:"format"`
	Picked      bool   `json:"picked"`
	Rejected    bool   `json:"rejected"`
	// Quality is the advisory technical-quality rating, 0–10 with one
	// decimal (0 = worst). Omitted when the photo has no current rating or
	// the feature is off. Nothing in Choix acts on it.
	Quality *float64 `json:"quality,omitempty"`
}

// Cluster is one cluster strip.
type Cluster struct {
	ID      int64    `json:"id"`
	Device  string   `json:"device"`
	Time    string   `json:"time"`
	Members []Member `json:"members"`
}

// FolderView is the cluster grid of an open folder: v1's /api/library plus
// what the Folder screen needs besides.
type FolderView struct {
	Folder   string    `json:"folder"`
	Picked   int       `json:"picked"`
	Total    int       `json:"total"`
	Clusters []Cluster `json:"clusters"`
	// Devices are the sorted, distinct single-camera labels of the clusters
	// shown (a merged cluster contributes each of its cameras).
	Devices []string `json:"devices"`
	// Failed counts the folder's files whose scan failed.
	Failed int `json:"failed"`
	// PicksDir is the folder's picks directory, relative to its root.
	PicksDir string `json:"picks_dir"`
	// PicksBlocked is the reason code when this folder cannot take picks:
	// "has_originals", or a picks.DirError reason ("empty", "absolute",
	// "traversal", "reserved").
	PicksBlocked string `json:"picks_blocked,omitempty"`
	// PicksMissing counts picked photos whose copy is no longer in the picks
	// folder (R24): reported, never repaired.
	PicksMissing int `json:"picks_missing"`
}

// Exif is the Info panel of one member (the design's ExifPayload).
type Exif struct {
	Camera      string   `json:"camera,omitempty"`
	Aperture    string   `json:"aperture,omitempty"`
	Shutter     string   `json:"shutter,omitempty"`
	ISO         string   `json:"iso,omitempty"`
	FocalLength string   `json:"focal_length,omitempty"`
	Captured    string   `json:"captured,omitempty"`
	Size        string   `json:"size,omitempty"`
	GPSLat      *float64 `json:"gps_lat,omitempty"`
	GPSLon      *float64 `json:"gps_lon,omitempty"`
}

// FocusResponse is one cluster with the EXIF of each member (keyed by the
// decimal file id) and its neighbours in library order.
type FocusResponse struct {
	Cluster       Cluster         `json:"cluster"`
	ClusterIndex  int             `json:"cluster_index"` // 1-based
	ClusterCount  int             `json:"cluster_count"`
	Exif          map[string]Exif `json:"exif"`
	NextClusterID int64           `json:"next_cluster_id"` // 0: last
	PrevClusterID int64           `json:"prev_cluster_id"` // 0: first
}

// buildFolderView builds the cluster grid of st (v1's buildLibraryJSON),
// with the folder's name, its device labels and its failed count. The picks
// fields are left to the caller.
func buildFolderView(st *store.Store, folder string, cfg config.Config) (FolderView, error) {
	clusters, err := st.Clusters().AllOrdered()
	if err != nil {
		return FolderView{}, fmt.Errorf("catalog: list clusters: %w", err)
	}
	picksMap, err := st.Picks().StateMap()
	if err != nil {
		return FolderView{}, fmt.Errorf("catalog: read picks: %w", err)
	}
	pickedCount, err := st.Picks().CountState("picked")
	if err != nil {
		return FolderView{}, fmt.Errorf("catalog: count picks: %w", err)
	}
	failed, err := st.Files().CountByStatus("failed")
	if err != nil {
		return FolderView{}, fmt.Errorf("catalog: count failed files: %w", err)
	}
	hideRejected := cfg.HideRejectedPhotos
	showQuality := cfg.QualityRating

	view := FolderView{
		Folder:   folder,
		Picked:   pickedCount,
		Clusters: make([]Cluster, 0, len(clusters)),
		Devices:  []string{},
		Failed:   failed,
	}

	// Pre-collect member ids for merged clusters so every file's device_key
	// is fetched in one round-trip rather than per cluster.
	mergedMemberIDs := []int64{}
	clusterMembers := make(map[int64][]store.ClusterMemberView, len(clusters))
	for _, c := range clusters {
		members, err := st.Clusters().Members(c.ID)
		if err != nil {
			return FolderView{}, fmt.Errorf("catalog: members of cluster %d: %w", c.ID, err)
		}
		clusterMembers[c.ID] = members
		if c.DeviceKey == group.MergedDeviceKey {
			for _, m := range members {
				mergedMemberIDs = append(mergedMemberIDs, m.FileID)
			}
		}
	}
	deviceLabelByFileID := map[int64]string{}
	if len(mergedMemberIDs) > 0 {
		files, err := st.Files().GetByIDs(mergedMemberIDs)
		if err != nil {
			return FolderView{}, fmt.Errorf("catalog: devices of merged clusters: %w", err)
		}
		for id, f := range files {
			deviceLabelByFileID[id] = formatDeviceLabel(f.DeviceKey.String)
		}
	}

	devices := map[string]struct{}{}
	total := 0
	for _, c := range clusters {
		members := clusterMembers[c.ID]
		out := make([]Member, 0, len(members))
		for _, m := range members {
			rejected := picksMap[m.FileID] == "rejected"
			if hideRejected && rejected {
				continue
			}
			out = append(out, Member{
				FileID:      m.FileID,
				Path:        m.Path,
				ContentHash: m.ContentHash,
				Kind:        m.Kind,
				Format:      m.Format,
				Picked:      picksMap[m.FileID] == "picked",
				Rejected:    rejected,
				Quality:     memberQuality(m, showQuality),
			})
		}
		if hideRejected && len(out) == 0 {
			continue
		}
		total += len(out)
		view.Clusters = append(view.Clusters, Cluster{
			ID:      c.ID,
			Device:  formatClusterDeviceLabel(c, members, deviceLabelByFileID),
			Time:    formatBucketLabel(c.TimeBucket.Int64),
			Members: out,
		})
		if c.DeviceKey != group.MergedDeviceKey {
			devices[formatDeviceLabel(c.DeviceKey)] = struct{}{}
			continue
		}
		for _, m := range members {
			if label := deviceLabelByFileID[m.FileID]; label != "" {
				devices[label] = struct{}{}
			}
		}
	}
	view.Total = total
	for d := range devices {
		view.Devices = append(view.Devices, d)
	}
	slices.Sort(view.Devices)
	return view, nil
}

// buildFocus builds the focus payload of one cluster of st (v1's
// handleClusterJSON). An unknown cluster, or one without members, is
// store.ErrNotFound.
func buildFocus(st *store.Store, id int64, showQuality bool) (FocusResponse, error) {
	c, err := st.Clusters().Get(id)
	if err != nil {
		return FocusResponse{}, fmt.Errorf("catalog: cluster %d: %w", id, err)
	}
	all, err := st.Clusters().AllOrdered()
	if err != nil {
		return FocusResponse{}, fmt.Errorf("catalog: list clusters: %w", err)
	}
	clusterIndex, prevID := 0, int64(0)
	for i, x := range all {
		if x.ID == id {
			clusterIndex = i + 1
			if i > 0 {
				prevID = all[i-1].ID
			}
			break
		}
	}
	members, err := st.Clusters().Members(id)
	if err != nil {
		return FocusResponse{}, fmt.Errorf("catalog: members of cluster %d: %w", id, err)
	}
	if len(members) == 0 {
		return FocusResponse{}, fmt.Errorf("catalog: cluster %d has no members: %w", id, store.ErrNotFound)
	}
	picksMap, err := st.Picks().StateMap()
	if err != nil {
		return FocusResponse{}, fmt.Errorf("catalog: read picks: %w", err)
	}

	ids := make([]int64, len(members))
	for i, m := range members {
		ids[i] = m.FileID
	}
	files, err := st.Files().GetByIDs(ids)
	if err != nil {
		return FocusResponse{}, fmt.Errorf("catalog: files of cluster %d: %w", id, err)
	}

	memJSON := make([]Member, 0, len(members))
	exif := make(map[string]Exif, len(members))
	for _, m := range members {
		memJSON = append(memJSON, Member{
			FileID:      m.FileID,
			Path:        m.Path,
			ContentHash: m.ContentHash,
			Kind:        m.Kind,
			Format:      m.Format,
			Picked:      picksMap[m.FileID] == "picked",
			Rejected:    picksMap[m.FileID] == "rejected",
			Quality:     memberQuality(m, showQuality),
		})
		f, ok := files[m.FileID]
		if !ok {
			continue
		}
		exif[strconv.FormatInt(m.FileID, 10)] = exifOf(f)
	}

	// The last cluster has no next (ErrNotFound); like v1, a failed lookup
	// also shows none.
	nextID, err := st.Clusters().NextAfter(c)
	if err != nil {
		nextID = 0
		if !errors.Is(err, store.ErrNotFound) {
			slog.Warn("catalog: next cluster", "cluster", id, "err", err)
		}
	}

	deviceLabelByFileID := map[int64]string{}
	if c.DeviceKey == group.MergedDeviceKey {
		for id, f := range files {
			deviceLabelByFileID[id] = formatDeviceLabel(f.DeviceKey.String)
		}
	}

	return FocusResponse{
		Cluster: Cluster{
			ID:      c.ID,
			Device:  formatClusterDeviceLabel(c, members, deviceLabelByFileID),
			Time:    formatBucketLabel(c.TimeBucket.Int64),
			Members: memJSON,
		},
		ClusterIndex:  clusterIndex,
		ClusterCount:  len(all),
		Exif:          exif,
		NextClusterID: nextID,
		PrevClusterID: prevID,
	}, nil
}

// exifOf is the Info panel of f.
func exifOf(f store.File) Exif {
	ex := Exif{}
	if f.DeviceKey.Valid {
		ex.Camera = formatDeviceLabel(f.DeviceKey.String)
	}
	if f.Aperture.Valid {
		ex.Aperture = fmt.Sprintf("f/%.1f", f.Aperture.Float64)
	}
	if f.Shutter.Valid {
		ex.Shutter = f.Shutter.String
	}
	if f.ISO.Valid {
		ex.ISO = strconv.FormatInt(f.ISO.Int64, 10)
	}
	if f.FocalLength.Valid {
		ex.FocalLength = fmt.Sprintf("%.0fmm", f.FocalLength.Float64)
	}
	if f.CapturedAt.Valid {
		ex.Captured = time.Unix(f.CapturedAt.Int64, 0).Format("Jan 2 2006 · 15:04:05")
	}
	if f.Width.Valid && f.Height.Valid {
		ex.Size = fmt.Sprintf("%d × %d", f.Width.Int64, f.Height.Int64)
	}
	if f.GPSLat.Valid && f.GPSLon.Valid {
		lat, lon := f.GPSLat.Float64, f.GPSLon.Float64
		ex.GPSLat = &lat
		ex.GPSLon = &lon
	}
	return ex
}
