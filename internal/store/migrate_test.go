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
}
