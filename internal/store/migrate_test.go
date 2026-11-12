package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesV1(t *testing.T) {
	s := newTestStore(t)

	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	// user_version equals the number of applied migrations.
	// Update this constant when a new migration file is added.
	const wantVersion = 7
	if v != wantVersion {
		t.Fatalf("user_version = %d, want %d", v, wantVersion)
	}

	tables := []string{"files", "thumbs", "clusters", "cluster_members", "ai_signals", "picks", "scan_runs", "kv"}
	for _, tbl := range tables {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", tbl, err)
		}
	}
}

// TestMigrate_V6ToV7: a database left at version 6 by an earlier build
// gains the two print columns, keeps its rows, and reaches version 7.
func TestMigrate_V6ToV7(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", FileURI(path, connPragmas...))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"001_initial.sql", "002_kv.sql", "003_path_nocase.sql", "004_gps.sql", "005_quality.sql", "006_seen_run.sql"} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", 6)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO files (id, path, size, mtime, content_hash, kind, format, scan_status)
		VALUES (1, 'a.jpg', 1, 1, 'h', 'photo', 'jpeg', 'analyzed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_signals (file_id, clip_embedding, computed_at) VALUES (1, x'01020304', 7)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO picks (file_id, state, picked_at) VALUES (1, 'picked', 9)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 7 {
		t.Fatalf("user_version = %d, %v; want 7", v, err)
	}
	var clip []byte
	var vp []byte
	var vpID sql.NullString
	if err := s.db.QueryRow(`SELECT clip_embedding, vision_print, vision_print_id FROM ai_signals WHERE file_id = 1`).Scan(&clip, &vp, &vpID); err != nil {
		t.Fatalf("read ai_signals: %v", err)
	}
	if len(clip) != 4 || vp != nil || vpID.Valid {
		t.Fatalf("clip %x, print %x, id %v; want the old blob kept and no print", clip, vp, vpID)
	}
	if p, err := s.Picks().Get(1); err != nil || p.State != "picked" {
		t.Fatalf("pick = %+v, %v; want it kept", p, err)
	}
	if f, err := s.Files().GetByPath("a.jpg"); err != nil || f.ScanStatus != "analyzed" {
		t.Fatalf("file = %+v, %v; want it kept", f, err)
	}
	if !hasPrintTrigger(t, s.db) {
		t.Fatal("the migrated database has no print trigger")
	}
}

// hasPrintTrigger reports whether the schema holds the trigger of migration
// 007 that drops a file's print when its content hash changes.
func hasPrintTrigger(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'trigger' AND name = 'files_content_changed_clears_print'`).Scan(&n); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	return n == 1
}

// TestMigrate_PrintTrigger: migration 007 installs the trigger, whether it
// runs on an empty database or on one left at version 6.
func TestMigrate_PrintTrigger(t *testing.T) {
	s := newTestStore(t)
	if !hasPrintTrigger(t, s.db) {
		t.Fatal("a new database has no print trigger")
	}
}

// v2RescanChanged and v2UpsertSignals are the statements the released 2.0.0 build (user_version 6,
// no Vision columns) runs against a state.db, copied from main's
// internal/store/files.go (UpsertOnRescan, a changed file) and
// internal/store/ai_signals.go (Upsert). That build opens a version-7
// database as it is and runs them unchanged.
const (
	v2RescanChanged = `UPDATE files SET size=?, mtime=?, content_hash=?, kind=?, format=?, scan_status='discovered', error=NULL,
		        quality_raw=NULL, quality_model=NULL, seen_run=? WHERE id=?`
	v2UpsertSignals = `INSERT INTO ai_signals (file_id, clip_embedding, computed_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		    clip_embedding = excluded.clip_embedding,
		    computed_at    = excluded.computed_at`
)

// TestPrintTrigger_OlderWriter: a folder printed by this build, then
// rescanned by 2.0.0 after the photo was edited, comes back with no print
// and no stamp, so the backfill makes the print again from the new bytes.
// The pick row is untouched.
func TestPrintTrigger_OlderWriter(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	if err := s.AISignals().UpsertPrint(id, testPrint(1), testPrintID); err != nil {
		t.Fatal(err)
	}
	pick := Pick{FileID: id, State: "picked", PickedAt: 42, Rating: sql.NullInt64{Int64: 3, Valid: true},
		ExportedPath: sql.NullString{String: "picks/a.jpg", Valid: true}}
	if err := s.Picks().Upsert(pick); err != nil {
		t.Fatal(err)
	}

	// 2.0.0 rescans the edited photo, re-analyzes it (its CLIP upsert) and
	// moves it back to analyzed.
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, v2RescanChanged, 2, 2, "h2", "photo", "jpeg", 5, id); err != nil {
		t.Fatalf("2.0.0 rescan: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, v2UpsertSignals, id, []byte{1, 2, 3, 4}, 9); err != nil {
		t.Fatalf("2.0.0 signals upsert: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET scan_status = 'analyzed' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	got, err := s.AISignals().GetByFileID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Print != nil || got.PrintID != "" {
		t.Fatalf("after 2.0.0 rescanned new content: print len %d, id %q; want both cleared", len(got.Print), got.PrintID)
	}
	if n, err := s.Files().CountAnalyzedMissingPrint(testPrintID); err != nil || n != 1 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, %v; want 1", n, err)
	}
	if p, err := s.Picks().Get(id); err != nil || p != pick {
		t.Fatalf("pick = %+v, %v; want %+v", p, err, pick)
	}
}

// TestPrintTrigger_SameHashKeepsPrint: an update of a file row that leaves
// content_hash as it was (re-set to the same value, or not set at all)
// keeps the print.
func TestPrintTrigger_SameHashKeepsPrint(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h1")
	if err := s.AISignals().UpsertPrint(id, testPrint(1), testPrintID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, v2RescanChanged, 1, 7, "h1", "photo", "jpeg", 5, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET scan_status = 'analyzed', seen_run = 6 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.AISignals().GetByFileID(id)
	if err != nil || got.Print == nil || got.PrintID != testPrintID {
		t.Fatalf("print len %d, id %q, %v; want it kept", len(got.Print), got.PrintID, err)
	}
	if n, err := s.Files().CountAnalyzedMissingPrint(testPrintID); err != nil || n != 0 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, %v; want 0", n, err)
	}
}
