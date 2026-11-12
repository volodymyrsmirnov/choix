package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// insertStatsFile inserts a file row with the given kind, scan_status and
// captured_at (Valid == false leaves it NULL).
func insertStatsFile(t *testing.T, s *Store, path, kind, status string, captured sql.NullInt64) int64 {
	t.Helper()
	id, err := s.Files().Insert(File{Path: path, Size: 1, Mtime: 1, ContentHash: path, Kind: kind, Format: "jpeg", ScanStatus: status})
	if err != nil {
		t.Fatalf("insert %s: %v", path, err)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE files SET captured_at = ? WHERE id = ?`, captured, id); err != nil {
		t.Fatalf("set captured_at for %s: %v", path, err)
	}
	return id
}

func capturedAtSec(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

func TestLibraryStats(t *testing.T) {
	s := newTestStore(t)
	insertStatsFile(t, s, "a.jpg", "photo", "analyzed", capturedAtSec(2000))
	insertStatsFile(t, s, "b.jpg", "photo", "thumbed", capturedAtSec(3000))
	insertStatsFile(t, s, "c.jpg", "photo", "discovered", sql.NullInt64{})
	insertStatsFile(t, s, "d.mov", "video", "analyzed", capturedAtSec(5000))
	// A missing photo is not counted, and its earlier date is not the first.
	insertStatsFile(t, s, "gone.jpg", "photo", "missing", capturedAtSec(100))
	// A failed photo still counts as a photo, and as failed.
	insertStatsFile(t, s, "bad.jpg", "photo", "failed", capturedAtSec(1500))

	got, err := s.Files().LibraryStats()
	if err != nil {
		t.Fatalf("LibraryStats: %v", err)
	}
	want := LibraryStats{Photos: 4, Videos: 1, Failed: 1, FirstCaptured: capturedAtSec(1500), LastCaptured: capturedAtSec(5000)}
	if got != want {
		t.Fatalf("LibraryStats = %+v, want %+v", got, want)
	}
}

func TestLibraryStats_Empty(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Files().LibraryStats()
	if err != nil {
		t.Fatalf("LibraryStats: %v", err)
	}
	if got != (LibraryStats{}) {
		t.Fatalf("LibraryStats on an empty folder = %+v, want zero value", got)
	}
}

func TestPreviewCandidates_SkipsRejectedAndThumbless(t *testing.T) {
	s := newTestStore(t)
	thumb := func(id int64, tier string) {
		t.Helper()
		if err := s.Thumbs().Upsert(Thumb{FileID: id, Tier: tier, RelPath: "x.jpg", Width: 1, Height: 1}); err != nil {
			t.Fatalf("thumb %d: %v", id, err)
		}
	}
	pick := func(id int64, state string) {
		t.Helper()
		if err := s.Picks().Upsert(Pick{FileID: id, State: state, PickedAt: 1}); err != nil {
			t.Fatalf("pick %d: %v", id, err)
		}
	}

	late := insertStatsFile(t, s, "late.jpg", "photo", "analyzed", capturedAtSec(3000))
	thumb(late, "thumb")
	undated := insertStatsFile(t, s, "undated.jpg", "photo", "analyzed", sql.NullInt64{})
	thumb(undated, "thumb")
	video := insertStatsFile(t, s, "clip.mov", "video", "analyzed", capturedAtSec(1000))
	thumb(video, "thumb")
	picked := insertStatsFile(t, s, "picked.jpg", "photo", "analyzed", capturedAtSec(2000))
	thumb(picked, "thumb")
	pick(picked, "picked")
	tie := insertStatsFile(t, s, "tie.jpg", "photo", "analyzed", capturedAtSec(2000))
	thumb(tie, "thumb")

	rejected := insertStatsFile(t, s, "rejected.jpg", "photo", "analyzed", capturedAtSec(500))
	thumb(rejected, "thumb")
	pick(rejected, "rejected")
	insertStatsFile(t, s, "nothumb.jpg", "photo", "metadata", capturedAtSec(600))
	previewOnly := insertStatsFile(t, s, "preview-only.jpg", "photo", "analyzed", capturedAtSec(700))
	thumb(previewOnly, "preview")
	missing := insertStatsFile(t, s, "missing.jpg", "photo", "missing", capturedAtSec(800))
	thumb(missing, "thumb")

	got, err := s.Files().PreviewCandidates()
	if err != nil {
		t.Fatalf("PreviewCandidates: %v", err)
	}
	want := []PreviewCandidate{
		{FileID: video, Kind: "video"},
		{FileID: picked, Kind: "photo"},
		{FileID: tie, Kind: "photo"},
		{FileID: late, Kind: "photo"},
		{FileID: undated, Kind: "photo"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewCandidates = %+v, want %+v", got, want)
	}
}
