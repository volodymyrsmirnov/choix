package store

import (
	"context"
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
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "h1", Kind: "photo", Format: "jpeg", ScanStatus: "discovered"}, 1); err != nil {
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
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 2, Mtime: 2, ContentHash: "h2", Kind: "photo", Format: "jpeg", ScanStatus: "discovered"}, 1); err != nil {
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

// TestUpsertOnRescan_ClearsPrint: a rescan that finds new content drops the
// file's stored print (so it is made again from the new content); an
// unchanged rescan keeps it; the pick row is untouched either way.
func TestUpsertOnRescan_ClearsPrint(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	if err := s.AISignals().UpsertPrint(id, testPrint(1), testPrintID); err != nil {
		t.Fatal(err)
	}
	pick := Pick{FileID: id, State: "picked", PickedAt: 42, Rating: sql.NullInt64{Int64: 3, Valid: true}}
	if err := s.Picks().Upsert(pick); err != nil {
		t.Fatal(err)
	}
	checkPick := func() {
		t.Helper()
		got, err := s.Picks().Get(id)
		if err != nil || got != pick {
			t.Fatalf("pick = %+v, %v; want %+v", got, err, pick)
		}
	}

	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "h1", Kind: "photo", Format: "jpeg"}, 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AISignals().GetByFileID(id); err != nil || got.Print == nil || got.PrintID != testPrintID {
		t.Fatalf("after an unchanged rescan: print len %d, id %q, %v; want it kept", len(got.Print), got.PrintID, err)
	}
	checkPick()

	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 2, Mtime: 2, ContentHash: "h2", Kind: "photo", Format: "jpeg"}, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.AISignals().GetByFileID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Print != nil || got.PrintID != "" {
		t.Fatalf("after new content: print len %d, id %q; want both cleared", len(got.Print), got.PrintID)
	}
	checkPick()
	if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Files().CountAnalyzedMissingPrint(testPrintID); err != nil || n != 1 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, %v; want 1", n, err)
	}

	// Both updates commit together or not at all: when clearing the print
	// fails, the file keeps its old content hash and its rating too.
	b := insertAnalyzed(t, s, "b.jpg", "photo", "b1")
	if err := s.AISignals().UpsertPrint(b, testPrint(2), testPrintID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Files().SetQuality(b, "b1", sql.NullFloat64{Float64: 0.5, Valid: true}, testScorer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(context.Background(), `
		CREATE TRIGGER test_refuse_print_update BEFORE UPDATE ON ai_signals
		BEGIN SELECT RAISE(ABORT, 'refused for a test'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "b.jpg", Size: 3, Mtime: 3, ContentHash: "b2", Kind: "photo", Format: "jpeg"}, 3); err == nil {
		t.Fatal("UpsertOnRescan succeeded although clearing the print failed")
	}
	f, err := s.Files().GetByID(b)
	if err != nil {
		t.Fatal(err)
	}
	var raw sql.NullFloat64
	var model sql.NullString
	if err := s.DB().QueryRowContext(context.Background(),
		`SELECT quality_raw, quality_model FROM files WHERE id = ?`, b).Scan(&raw, &model); err != nil {
		t.Fatal(err)
	}
	if f.ContentHash != "b1" || f.ScanStatus != "analyzed" || !raw.Valid || model.String != testScorer {
		t.Fatalf("after the failed rescan: hash %q, status %q, rating %+v %q; want the row untouched (b1, analyzed, 0.5 %q)",
			f.ContentHash, f.ScanStatus, raw, model.String, testScorer)
	}
	if got, err := s.AISignals().GetByFileID(b); err != nil || got.Print == nil || got.PrintID != testPrintID {
		t.Fatalf("after the failed rescan: print len %d, id %q, %v; want it kept", len(got.Print), got.PrintID, err)
	}
}
