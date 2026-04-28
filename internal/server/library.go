package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// memberJSON is the per-photo payload returned by /api/library and
// /api/clusters/{id}. ContentHash is shipped so the React client can
// build cache-busting `?v=<hash>` query strings on /thumb/<path> and
// /full/<path> URLs — when the underlying file changes the hash changes
// and the browser refetches even though the URL path is stable.
// Kind ("photo"|"video") and Format ("jpeg","heic","mov","mp4",...) let
// the React client render <video> instead of <img> for video members.
type memberJSON struct {
	FileID      int64  `json:"file_id"`
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	Kind        string `json:"kind"`
	Format      string `json:"format"`
	Picked      bool   `json:"picked"`
	Rejected    bool   `json:"rejected"`
}

// clusterJSON is one cluster strip.
type clusterJSON struct {
	ID      int64        `json:"id"`
	Device  string       `json:"device"`
	Time    string       `json:"time"`
	Members []memberJSON `json:"members"`
}

// libraryJSON is the full library response.
type libraryJSON struct {
	Folder   string        `json:"folder"`
	Picked   int           `json:"picked"`
	Total    int           `json:"total"`
	Clusters []clusterJSON `json:"clusters"`
}

// focusJSON is the focus payload — one cluster plus EXIF for each member
// and prev/next cluster IDs for in-app navigation.
type focusJSON struct {
	Cluster       clusterJSON         `json:"cluster"`
	ClusterIndex  int                 `json:"cluster_index"` // 1-based
	ClusterCount  int                 `json:"cluster_count"` // total clusters
	Exif          map[string]exifJSON `json:"exif"`
	NextClusterID int64               `json:"next_cluster_id"`
	PrevClusterID int64               `json:"prev_cluster_id"`
}

type exifJSON struct {
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

func (s *Server) handleLibraryJSON(w http.ResponseWriter, r *http.Request) {
	resp, err := s.buildLibraryJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) buildLibraryJSON() (libraryJSON, error) {
	clusters, err := s.cfg.Store.Clusters().AllOrdered()
	if err != nil {
		return libraryJSON{}, err
	}
	picksMap, err := s.cfg.Store.Picks().StateMap()
	if err != nil {
		return libraryJSON{}, err
	}
	pickedCount, err := s.cfg.Store.Picks().CountState("picked")
	if err != nil {
		return libraryJSON{}, err
	}
	hideRejected := s.liveCfgOrDefaults().HideRejectedPhotos

	resp := libraryJSON{
		Folder:   filepath.Base(s.cfg.ScanRoot),
		Picked:   pickedCount,
		Clusters: make([]clusterJSON, 0, len(clusters)),
	}

	// Pre-collect member ids for merged clusters so we can fetch every
	// file's device_key in one round-trip rather than per-cluster.
	mergedMemberIDs := []int64{}
	clusterMembers := make(map[int64][]store.ClusterMemberView, len(clusters))
	for _, c := range clusters {
		members, err := s.cfg.Store.Clusters().Members(c.ID)
		if err != nil {
			return libraryJSON{}, err
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
		files, err := s.cfg.Store.Files().GetByIDs(mergedMemberIDs)
		if err != nil {
			return libraryJSON{}, err
		}
		for id, f := range files {
			deviceLabelByFileID[id] = formatDeviceLabel(f.DeviceKey.String)
		}
	}

	total := 0
	for _, c := range clusters {
		members := clusterMembers[c.ID]
		out := make([]memberJSON, 0, len(members))
		for _, m := range members {
			rejected := picksMap[m.FileID] == "rejected"
			if hideRejected && rejected {
				continue
			}
			out = append(out, memberJSON{
				FileID:      m.FileID,
				Path:        m.Path,
				ContentHash: m.ContentHash,
				Kind:        m.Kind,
				Format:      m.Format,
				Picked:      picksMap[m.FileID] == "picked",
				Rejected:    rejected,
			})
		}
		if hideRejected && len(out) == 0 {
			continue
		}
		total += len(out)
		resp.Clusters = append(resp.Clusters, clusterJSON{
			ID:      c.ID,
			Device:  formatClusterDeviceLabel(c, members, deviceLabelByFileID),
			Time:    formatBucketLabel(c.TimeBucket.Int64),
			Members: out,
		})
	}
	resp.Total = total
	return resp, nil
}

func (s *Server) handleClusterJSON(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "clusterID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad cluster id", http.StatusBadRequest)
		return
	}
	c, err := s.cfg.Store.Clusters().Get(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	all, err := s.cfg.Store.Clusters().AllOrdered()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
	members, err := s.cfg.Store.Clusters().Members(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(members) == 0 {
		http.NotFound(w, r)
		return
	}
	picksMap, err := s.cfg.Store.Picks().StateMap()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ids := make([]int64, len(members))
	for i, m := range members {
		ids[i] = m.FileID
	}
	files, err := s.cfg.Store.Files().GetByIDs(ids)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	memJSON := make([]memberJSON, 0, len(members))
	exif := make(map[string]exifJSON, len(members))
	for _, m := range members {
		memJSON = append(memJSON, memberJSON{
			FileID:      m.FileID,
			Path:        m.Path,
			ContentHash: m.ContentHash,
			Kind:        m.Kind,
			Format:      m.Format,
			Picked:      picksMap[m.FileID] == "picked",
			Rejected:    picksMap[m.FileID] == "rejected",
		})
		f, ok := files[m.FileID]
		if !ok {
			continue
		}
		ex := exifJSON{}
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
		exif[strconv.FormatInt(m.FileID, 10)] = ex
	}

	nextID, _ := s.cfg.Store.Clusters().NextAfter(c)

	deviceLabelByFileID := map[int64]string{}
	if c.DeviceKey == group.MergedDeviceKey {
		for id, f := range files {
			deviceLabelByFileID[id] = formatDeviceLabel(f.DeviceKey.String)
		}
	}

	resp := focusJSON{
		Cluster: clusterJSON{
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
	}
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func formatDeviceLabel(key string) string {
	if key == "" {
		return "Unknown device"
	}
	for i, ch := range key {
		if ch == '#' {
			return key[:i]
		}
	}
	return key
}

// formatClusterDeviceLabel renders the header label for a cluster. For
// single-device clusters this is the camera name (formatDeviceLabel of
// device_key). For cross-device merged clusters (sentinel
// group.MergedDeviceKey) it's the sorted, deduped list of involved
// cameras joined with " + ", e.g. "X-T5 + iPhone 15".
//
// memberDeviceLabels is keyed by file_id and must contain entries for
// every member when c.DeviceKey is the merged sentinel; otherwise it is
// unused.
func formatClusterDeviceLabel(c store.Cluster, members []store.ClusterMemberView, memberDeviceLabels map[int64]string) string {
	if c.DeviceKey != group.MergedDeviceKey {
		return formatDeviceLabel(c.DeviceKey)
	}
	seen := map[string]struct{}{}
	labels := make([]string, 0, 2)
	for _, m := range members {
		label, ok := memberDeviceLabels[m.FileID]
		if !ok || label == "" {
			continue
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return "Multiple devices"
	}
	sort.Strings(labels)
	return strings.Join(labels, " + ")
}

func formatBucketLabel(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("Jan 2 · 15:04")
}
