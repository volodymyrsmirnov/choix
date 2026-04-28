package store

import (
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
	const wantVersion = 4
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
