package store

import (
	"database/sql"
	"testing"
)

const testScorer = "scorer/v1"

func insertAnalyzed(t *testing.T, s *Store, path, kind, hash string) int64 {
	t.Helper()
	id, err := s.Files().Insert(File{Path: path, Size: 1, Mtime: 1, ContentHash: hash, Kind: kind, Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatalf("insert %s: %v", path, err)
	}
	return id
}

func pendingIDs(t *testing.T, s *Store, scorer string) []int64 {
	t.Helper()
	items, err := s.Files().ListQualityPending(scorer, 0, 100)
	if err != nil {
		t.Fatalf("ListQualityPending: %v", err)
	}
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.FileID
	}
	return ids
}

func TestQualityPendingSelectsAnalyzedPhotosOnly(t *testing.T) {
	s := newTestStore(t)
	photo := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	insertAnalyzed(t, s, "b.mov", "video", "h2")
	thumbed, err := s.Files().Insert(File{Path: "c.jpg", Size: 1, Mtime: 1, ContentHash: "h3", Kind: "photo", Format: "jpeg", ScanStatus: "thumbed"})
	if err != nil {
		t.Fatal(err)
	}

	got := pendingIDs(t, s, testScorer)
	if len(got) != 1 || got[0] != photo {
		t.Fatalf("pending = %v, want [%d] (video and file %d not yet analyzed must be excluded)", got, photo, thumbed)
	}
	n, err := s.Files().CountQualityPending(testScorer)
	if err != nil || n != 1 {
		t.Fatalf("CountQualityPending = %d, %v; want 1", n, err)
	}
}

func TestSetQualityRemovesFromPendingAndIsVisibleToMembers(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	cid, err := s.Clusters().InsertCluster(Cluster{DeviceKey: "cam"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Clusters().AddMember(cid, id); err != nil {
		t.Fatal(err)
	}

	ok, err := s.Files().SetQuality(id, "h1", sql.NullFloat64{Float64: 0.61, Valid: true}, testScorer)
	if err != nil || !ok {
		t.Fatalf("SetQuality = %v, %v; want applied", ok, err)
	}
	if got := pendingIDs(t, s, testScorer); len(got) != 0 {
		t.Fatalf("pending after rating = %v, want none", got)
	}
	// A different scorer (new model or recipe) makes the photo pending again.
	if got := pendingIDs(t, s, "scorer/v2"); len(got) != 1 {
		t.Fatalf("pending for a new scorer = %v, want the photo", got)
	}

	members, err := s.Clusters().Members(cid)
	if err != nil || len(members) != 1 {
		t.Fatalf("Members = %v, %v", members, err)
	}
	m := members[0]
	if !m.QualityRaw.Valid || m.QualityRaw.Float64 != 0.61 || m.QualityModel.String != testScorer {
		t.Fatalf("member quality = %+v / %+v, want 0.61 by %s", m.QualityRaw, m.QualityModel, testScorer)
	}
}

func TestSetQualityUnratableStampsScorerWithNullScore(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	if ok, err := s.Files().SetQuality(id, "h1", sql.NullFloat64{}, testScorer); err != nil || !ok {
		t.Fatalf("SetQuality = %v, %v", ok, err)
	}
	if got := pendingIDs(t, s, testScorer); len(got) != 0 {
		t.Fatalf("an unratable photo must not stay pending, got %v", got)
	}
}

func TestSetQualityIgnoresResultForChangedContent(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "new-hash")
	ok, err := s.Files().SetQuality(id, "old-hash", sql.NullFloat64{Float64: 0.9, Valid: true}, testScorer)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a score computed from superseded bytes must not be stored")
	}
	if got := pendingIDs(t, s, testScorer); len(got) != 1 {
		t.Fatalf("photo should still be pending, got %v", got)
	}
}

func TestUpsertOnRescanClearsQualityWhenContentChanges(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	if _, err := s.Files().SetQuality(id, "h1", sql.NullFloat64{Float64: 0.7, Valid: true}, testScorer); err != nil {
		t.Fatal(err)
	}

	// Unchanged rescan keeps the rating.
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "h1", Kind: "photo", Format: "jpeg", ScanStatus: "discovered"}); err != nil {
		t.Fatal(err)
	}
	var raw sql.NullFloat64
	var model sql.NullString
	read := func() {
		t.Helper()
		if err := s.db.QueryRow(`SELECT quality_raw, quality_model FROM files WHERE id = ?`, id).Scan(&raw, &model); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if !raw.Valid {
		t.Fatal("an unchanged rescan must keep the rating")
	}

	// Changed bytes drop it.
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 2, Mtime: 2, ContentHash: "h2", Kind: "photo", Format: "jpeg", ScanStatus: "discovered"}); err != nil {
		t.Fatal(err)
	}
	read()
	if raw.Valid || model.Valid {
		t.Fatalf("changed content must clear the rating, got raw=%v model=%v", raw, model)
	}
}

func TestListQualityPendingPagesByCursor(t *testing.T) {
	s := newTestStore(t)
	a := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	b := insertAnalyzed(t, s, "b.jpg", "photo", "h2")
	c := insertAnalyzed(t, s, "c.jpg", "photo", "h3")

	first, err := s.Files().ListQualityPending(testScorer, 0, 2)
	if err != nil || len(first) != 2 || first[0].FileID != a || first[1].FileID != b {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	// Nothing was rated, yet the cursor still moves past the first page.
	second, err := s.Files().ListQualityPending(testScorer, first[1].FileID, 2)
	if err != nil || len(second) != 1 || second[0].FileID != c {
		t.Fatalf("second page = %+v, %v", second, err)
	}
}
