package library

import (
	"database/sql"
	"path/filepath"
	"strings"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// Folder states of a FolderSummary (design type FolderState).
const (
	summaryReady    = "ready"
	summaryScanning = "scanning"
	summaryQueued   = "queued"
	summaryOffline  = "offline"
	summaryError    = "error"
)

// FolderSummary is one row of the Library page (design type FolderSummary,
// frontend/src/design/types.ts). Month is the YYYY-MM of FirstCaptured in
// UTC; Month, FirstCaptured and LastCaptured are null for an undated folder.
// Scan is set while the folder scans, VolumeName while it is offline and
// Error while it is in error. AddedAt is library.db's folders.added_at (unix
// seconds) for every folder; the Library's "Recently added" order sorts on it.
type FolderSummary struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	ParentPath    string           `json:"parentPath"`
	State         string           `json:"state"`
	Month         *string          `json:"month"`
	FirstCaptured *string          `json:"firstCaptured"`
	LastCaptured  *string          `json:"lastCaptured"`
	Photos        int              `json:"photos"`
	Videos        int              `json:"videos"`
	Clusters      int              `json:"clusters"`
	Picked        int              `json:"picked"`
	Rejected      int              `json:"rejected"`
	Previews      []string         `json:"previews"`
	Scan          *events.Progress `json:"scan,omitempty"`
	VolumeName    string           `json:"volumeName,omitempty"`
	Error         string           `json:"error,omitempty"`
	AddedAt       int64            `json:"added_at"`
}

// Summarise returns the Library-page row of f, whose job the queue reports as
// jobState ("scanning", "queued" or "") with progress p. The state is, first
// match: presence offline ⇒ offline; presence missing ⇒ error with
// MissingMessage; presence access_lost ⇒ error with AccessLostMessage; job
// scanning or queued ⇒ that; job_pending ⇒ queued (present, pending, not yet
// queued again — e.g. just after a suspension); job_error ⇒ error with it;
// else ready. A folder with pending work is therefore never ready, and a
// pending job never hides why the folder cannot be reached.
//
// ParentPath is f's parent directory with a leading home replaced by "~"
// (home "" replaces nothing). Previews holds previewURL(f.ID, i,
// f.PreviewsStamp) for i < f.Previews (none when previewURL is nil); it is
// never nil, so it marshals as an array. p is copied.
func Summarise(f Folder, jobState string, p *events.Progress, home string, previewURL func(string, int, int64) string) FolderSummary {
	s := FolderSummary{
		ID:            f.ID,
		Name:          f.Name,
		ParentPath:    abbreviateHome(filepath.Dir(f.Path), home),
		Month:         utcFormat(f.Stats.FirstCaptured, "2006-01"),
		FirstCaptured: utcFormat(f.Stats.FirstCaptured, time.RFC3339),
		LastCaptured:  utcFormat(f.Stats.LastCaptured, time.RFC3339),
		Photos:        f.Stats.Photos,
		Videos:        f.Stats.Videos,
		Clusters:      f.Stats.Clusters,
		Picked:        f.Stats.Picked,
		Rejected:      f.Stats.Rejected,
		Previews:      previewURLs(f, previewURL),
		AddedAt:       f.AddedAt,
	}

	switch {
	case f.Presence == presenceOffline:
		s.State, s.VolumeName = summaryOffline, f.VolumeName
	case f.Presence == presenceMissing:
		s.State, s.Error = summaryError, MissingMessage
	case f.Presence == presenceAccessLost:
		s.State, s.Error = summaryError, AccessLostMessage
	case jobState == summaryScanning:
		s.State = summaryScanning
		if p != nil {
			cp := *p
			s.Scan = &cp
		}
	case jobState == summaryQueued, f.JobPending:
		s.State = summaryQueued
	case f.JobError != "":
		s.State, s.Error = summaryError, f.JobError
	default:
		s.State = summaryReady
	}
	return s
}

// utcFormat formats the Unix time v in UTC with layout, or returns nil when v
// is NULL.
func utcFormat(v sql.NullInt64, layout string) *string {
	if !v.Valid {
		return nil
	}
	out := time.Unix(v.Int64, 0).UTC().Format(layout)
	return &out
}

// abbreviateHome replaces a leading home in dir with "~": dir equal to home
// becomes "~", dir inside it "~/…". Anything else is returned unchanged.
func abbreviateHome(dir, home string) string {
	if home == "" {
		return dir
	}
	home = filepath.Clean(home)
	if home == string(filepath.Separator) {
		return dir // every path is "inside" /: abbreviating it says nothing
	}
	switch {
	case dir == home:
		return "~"
	case strings.HasPrefix(dir, home+string(filepath.Separator)):
		return "~" + dir[len(home):]
	}
	return dir
}

// previewURLs returns the URLs of f's preview strip, never nil.
func previewURLs(f Folder, previewURL func(string, int, int64) string) []string {
	if previewURL == nil || f.Previews <= 0 {
		return []string{}
	}
	out := make([]string, f.Previews)
	for i := range out {
		out[i] = previewURL(f.ID, i, f.PreviewsStamp)
	}
	return out
}
