package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// manyFiles is above SQLite's 32 766 bound-parameter limit.
const manyFiles = 40000

// manyPath is the synthetic path of row i of insertMany.
func manyPath(i int) string { return fmt.Sprintf("d%03d/IMG_%05d.JPG", i/1000, i) }

// insertMany inserts n synthetic "discovered" photo rows, seen by no walk,
// in one statement, and returns their ids in insertion order (row i has
// path manyPath(i)).
func insertMany(t *testing.T, s *Store, n int) []int64 {
	t.Helper()
	_, err := s.db.Exec(`WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM seq WHERE i + 1 < ?)
		INSERT INTO files (path, size, mtime, content_hash, kind, format, scan_status)
		SELECT printf('d%03d/IMG_%05d.JPG', i / 1000, i), 1, 1, 'h' || i, 'photo', 'jpeg', 'discovered' FROM seq`, n)
	if err != nil {
		t.Fatalf("insert %d rows: %v", n, err)
	}
	rows, err := s.db.Query(`SELECT id FROM files ORDER BY id`)
	if err != nil {
		t.Fatalf("ids: %v", err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0, n)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != n {
		t.Fatalf("ids = %d rows, %v; want %d", len(ids), err, n)
	}
	return ids
}

func countStatus(t *testing.T, s *Store, status string) int {
	t.Helper()
	n, err := s.Files().CountByStatus(status)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	return n
}

func newWalkRun(t *testing.T, s *Store) int64 {
	t.Helper()
	run, err := s.Files().NewWalkRun()
	if err != nil {
		t.Fatalf("NewWalkRun: %v", err)
	}
	return run
}

func TestGetByIDsAboveTheParameterLimit(t *testing.T) {
	s := newTestStore(t)
	ids := insertMany(t, s, manyFiles)
	ids = append(ids, 1<<40) // an unknown id is simply absent

	got, err := s.Files().GetByIDs(ids)
	if err != nil {
		t.Fatalf("GetByIDs(%d ids): %v", len(ids), err)
	}
	if len(got) != manyFiles {
		t.Fatalf("GetByIDs returned %d rows, want %d", len(got), manyFiles)
	}
	for _, i := range []int{0, getByIDsChunk - 1, getByIDsChunk, 32766, manyFiles - 1} {
		f, ok := got[ids[i]]
		if !ok || f.Path != manyPath(i) {
			t.Fatalf("GetByIDs[%d] = %+v (present %t), want path %s", ids[i], f, ok, manyPath(i))
		}
	}

	if got, err := s.Files().GetByIDs(nil); err != nil || len(got) != 0 {
		t.Fatalf("GetByIDs(nil) = %v, %v; want an empty map", got, err)
	}
}

func TestMarkMissingAboveTheParameterLimit(t *testing.T) {
	s := newTestStore(t)
	insertMany(t, s, manyFiles)
	run := newWalkRun(t, s)

	// The walk sees every row but the last 10. Most are stamped in one
	// statement (40 000 upserts take close to a minute under -race); a
	// stretch of 500 goes through UpsertOnRescan under another casing.
	const upserted = 500
	if _, err := s.db.Exec(`UPDATE files SET seen_run = ? WHERE path < ?`, run, manyPath(manyFiles-10-upserted)); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	for i := manyFiles - 10 - upserted; i < manyFiles-10; i++ {
		rec := File{Path: "D" + manyPath(i)[1:], Size: 1, Mtime: 1, ContentHash: fmt.Sprintf("h%d", i), Kind: "photo", Format: "jpeg"}
		if _, _, err := s.Files().UpsertOnRescan(rec, run); err != nil {
			t.Fatalf("UpsertOnRescan %d: %v", i, err)
		}
	}
	n, err := s.Files().MarkMissingNotSeen(context.Background(), run)
	if err != nil {
		t.Fatalf("MarkMissingNotSeen: %v", err)
	}
	if n != 10 || countStatus(t, s, "missing") != 10 || countStatus(t, s, "discovered") != manyFiles-10 {
		t.Fatalf("marked %d missing (%d missing, %d discovered in table), want 10 and %d",
			n, countStatus(t, s, "missing"), countStatus(t, s, "discovered"), manyFiles-10)
	}
	for i := manyFiles - 10; i < manyFiles; i++ {
		if f, err := s.Files().GetByPath(manyPath(i)); err != nil || f.ScanStatus != "missing" {
			t.Fatalf("%s = %q, %v; want missing", manyPath(i), f.ScanStatus, err)
		}
	}
}

func TestWalkRunGenerations(t *testing.T) {
	s := newTestStore(t)
	if run := newWalkRun(t, s); run != 1 {
		t.Fatalf("first NewWalkRun on an empty table = %d, want 1", run)
	}
	a, err := s.Files().Insert(File{Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "ha", Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	pickFile(t, s, a)

	// Walk 1 sees a.jpg and a new b.jpg.
	run1 := newWalkRun(t, s)
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "ha", Kind: "photo", Format: "jpeg"}, run1); err != nil {
		t.Fatal(err)
	}
	b, inserted, err := s.Files().UpsertOnRescan(File{Path: "b.jpg", Size: 2, Mtime: 2, ContentHash: "hb", Kind: "photo", Format: "jpeg"}, run1)
	if err != nil || !inserted {
		t.Fatalf("UpsertOnRescan b.jpg = %d, %t, %v; want a new row", b, inserted, err)
	}
	if n, err := s.Files().MarkMissingNotSeen(context.Background(), run1); err != nil || n != 0 {
		t.Fatalf("MarkMissingNotSeen(context.Background(), run1) = %d, %v; want 0", n, err)
	}

	// Walk 2 draws a new generation and sees only b.jpg: a.jpg is missing,
	// keeps its row and its pick.
	run2 := newWalkRun(t, s)
	if run2 <= run1 {
		t.Fatalf("NewWalkRun = %d after %d, want a new generation", run2, run1)
	}
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "b.jpg", Size: 2, Mtime: 2, ContentHash: "hb", Kind: "photo", Format: "jpeg"}, run2); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Files().MarkMissingNotSeen(context.Background(), run2); err != nil || n != 1 {
		t.Fatalf("MarkMissingNotSeen(context.Background(), run2) = %d, %v; want 1", n, err)
	}
	if f, _ := s.Files().GetByID(a); f.ScanStatus != "missing" {
		t.Fatalf("a.jpg = %q, want missing", f.ScanStatus)
	}
	if f, _ := s.Files().GetByID(b); f.ScanStatus != "discovered" {
		t.Fatalf("b.jpg = %q, want discovered", f.ScanStatus)
	}
	if p, err := s.Picks().Get(a); err != nil || p.State != "picked" {
		t.Fatalf("pick of the missing file = %+v, %v; want it kept", p, err)
	}

	// A row that was never stamped (NULL seen_run) is not seen either.
	c, err := s.Files().Insert(File{Path: "c.jpg", Size: 3, Mtime: 3, ContentHash: "hc", Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	run3 := newWalkRun(t, s)
	if _, err := s.Files().MarkMissingNotSeen(context.Background(), run3); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Files().GetByID(c); f.ScanStatus != "missing" {
		t.Fatalf("c.jpg = %q, want missing", f.ScanStatus)
	}
}

// pickFile records a pick for id the way the app does.
func pickFile(t *testing.T, s *Store, id int64) {
	t.Helper()
	if err := s.Durable(context.Background(), func(tx *sql.Tx) error {
		return s.Picks().SetTx(tx, id, "picked", "picks/a.jpg")
	}); err != nil {
		t.Fatalf("pick: %v", err)
	}
}

func TestUpsertOnRescanRestoresAMissingFileSeenUnchanged(t *testing.T) {
	s := newTestStore(t)
	rec := File{Path: "Trip/a.jpg", Size: 10, Mtime: 100, ContentHash: "h1", Kind: "photo", Format: "jpeg", ScanStatus: "discovered"}
	id, err := s.Files().Insert(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Files().SetQuality(id, "h1", sql.NullFloat64{Float64: 0.5, Valid: true}, "scorer"); err != nil {
		t.Fatal(err)
	}
	pickFile(t, s, id)
	if _, err := s.Files().MarkMissingNotSeen(context.Background(), newWalkRun(t, s)); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Files().GetByID(id); f.ScanStatus != "missing" {
		t.Fatalf("status = %q, want missing", f.ScanStatus)
	}

	// Seen again, byte for byte the same, under another casing.
	run := newWalkRun(t, s)
	rec.Path = "trip/A.JPG"
	got, inserted, err := s.Files().UpsertOnRescan(rec, run)
	if err != nil {
		t.Fatalf("UpsertOnRescan: %v", err)
	}
	if got != id || inserted {
		t.Fatalf("UpsertOnRescan = (%d, %t), want (%d, false)", got, inserted, id)
	}
	f, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.ScanStatus != "discovered" || f.ErrMsg.Valid {
		t.Fatalf("restored row = status %q error %v, want discovered and no error", f.ScanStatus, f.ErrMsg)
	}
	if f.Path != "Trip/a.jpg" || f.ContentHash != "h1" {
		t.Fatalf("restored row = path %q hash %q, want the stored path and hash kept", f.Path, f.ContentHash)
	}
	var raw sql.NullFloat64
	if err := s.db.QueryRow(`SELECT quality_raw FROM files WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !raw.Valid {
		t.Errorf("unchanged bytes keep their rating; it was cleared")
	}
	p, err := s.Picks().Get(id)
	if err != nil || p.State != "picked" || p.ExportedPath.String != "picks/a.jpg" {
		t.Fatalf("pick = %+v, %v; want it untouched", p, err)
	}
	if n, err := s.Files().MarkMissingNotSeen(context.Background(), run); err != nil || n != 0 {
		t.Fatalf("MarkMissingNotSeen after the row was seen = %d, %v; want 0", n, err)
	}

	// An unchanged row in any other status keeps it.
	if err := s.Files().UpdateStatus(id, "failed", "corrupt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Files().UpsertOnRescan(rec, newWalkRun(t, s)); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Files().GetByID(id); f.ScanStatus != "failed" || f.ErrMsg.String != "corrupt" {
		t.Fatalf("unchanged failed row = %q %q, want it kept failed", f.ScanStatus, f.ErrMsg.String)
	}
}

func TestRecordDiscoveryFailure(t *testing.T) {
	s := newTestStore(t)

	// A new file that cannot be read: a failed row, seen by the walk.
	run := newWalkRun(t, s)
	id, err := s.Files().RecordDiscoveryFailure(File{Path: "new.jpg", Size: 5, Mtime: 50, Kind: "photo", Format: "jpeg"}, run, "open new.jpg: permission denied")
	if err != nil {
		t.Fatalf("RecordDiscoveryFailure (new): %v", err)
	}
	f, _ := s.Files().GetByID(id)
	if f.ScanStatus != "failed" || f.ErrMsg.String != "open new.jpg: permission denied" || f.ContentHash != "" || f.Size != 5 || f.Mtime != 50 {
		t.Fatalf("new failed row = %+v", f)
	}

	// An analyzed, picked file that cannot be read: same row, failed, its
	// metadata and pick kept.
	old, err := s.Files().Insert(File{Path: "Old.jpg", Size: 9, Mtime: 90, ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "analyzed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE files SET device_key = 'Cam' WHERE id = ?`, old); err != nil {
		t.Fatal(err)
	}
	pickFile(t, s, old)
	got, err := s.Files().RecordDiscoveryFailure(File{Path: "old.JPG", Kind: "photo", Format: "jpeg"}, run, "read old.jpg: input/output error")
	if err != nil || got != old {
		t.Fatalf("RecordDiscoveryFailure (existing) = %d, %v; want %d", got, err, old)
	}
	f, _ = s.Files().GetByID(old)
	if f.ScanStatus != "failed" || f.ErrMsg.String != "read old.jpg: input/output error" || f.ContentHash != "" {
		t.Fatalf("existing failed row = status %q error %q hash %q", f.ScanStatus, f.ErrMsg.String, f.ContentHash)
	}
	if f.Path != "Old.jpg" || f.Size != 9 || f.Mtime != 90 || f.DeviceKey.String != "Cam" {
		t.Fatalf("existing failed row lost data: %+v", f)
	}
	if p, err := s.Picks().Get(old); err != nil || p.State != "picked" {
		t.Fatalf("pick = %+v, %v; want kept", p, err)
	}
	if n, err := s.Files().MarkMissingNotSeen(context.Background(), run); err != nil || n != 0 {
		t.Fatalf("MarkMissingNotSeen = %d, %v; failed rows were seen by the walk", n, err)
	}

	// Readable again: the next walk finds it changed and starts it over.
	next := newWalkRun(t, s)
	if _, _, err := s.Files().UpsertOnRescan(File{Path: "Old.jpg", Size: 9, Mtime: 90, ContentHash: "h", Kind: "photo", Format: "jpeg"}, next); err != nil {
		t.Fatal(err)
	}
	f, _ = s.Files().GetByID(old)
	if f.ScanStatus != "discovered" || f.ErrMsg.Valid || f.ContentHash != "h" {
		t.Fatalf("recovered row = status %q error %v hash %q, want discovered, no error, hash h", f.ScanStatus, f.ErrMsg, f.ContentHash)
	}
}

func TestMarkMissingNotSeenHonoursItsContext(t *testing.T) {
	s := newTestStore(t)
	insertMany(t, s, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Files().MarkMissingNotSeen(ctx, newWalkRun(t, s)); !errors.Is(err, context.Canceled) {
		t.Fatalf("MarkMissingNotSeen(cancelled) = %v, want context.Canceled", err)
	}
	if n := countStatus(t, s, "missing"); n != 0 {
		t.Fatalf("%d rows marked missing by a cancelled call, want 0", n)
	}
}
