package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestPicksUpsertAndGet(t *testing.T) {
	s := newTestStore(t)

	fid, _ := s.Files().Insert(File{
		Path: "x.jpg", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})

	now := time.Now().Unix()
	if err := s.Picks().Upsert(Pick{
		FileID: fid, State: "picked",
		Rating:   sql.NullInt64{Int64: 4, Valid: true},
		PickedAt: now,
	}); err != nil {
		t.Fatalf("Upsert picked: %v", err)
	}

	got, err := s.Picks().Get(fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "picked" || got.Rating.Int64 != 4 || got.PickedAt != now {
		t.Errorf("got %+v", got)
	}

	// Transition picked -> rejected (upsert overwrites).
	if err := s.Picks().Upsert(Pick{
		FileID: fid, State: "rejected", PickedAt: now + 1,
	}); err != nil {
		t.Fatalf("Upsert rejected: %v", err)
	}
	got, _ = s.Picks().Get(fid)
	if got.State != "rejected" {
		t.Errorf("State = %q, want rejected", got.State)
	}
	if got.Rating.Valid {
		t.Errorf("Rating should clear on overwrite, got %+v", got.Rating)
	}

	if _, err := s.Picks().Get(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: got %v, want ErrNotFound", err)
	}
}

func TestPicksListPicked(t *testing.T) {
	s := newTestStore(t)

	mkPick := func(path, state string) int64 {
		fid, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h",
			Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
		})
		if err != nil {
			t.Fatalf("Insert file: %v", err)
		}
		if err := s.Picks().Upsert(Pick{
			FileID: fid, State: state, PickedAt: time.Now().Unix(),
		}); err != nil {
			t.Fatalf("Upsert pick: %v", err)
		}
		return fid
	}

	a := mkPick("a.jpg", "picked")
	b := mkPick("b.jpg", "picked")
	mkPick("c.jpg", "rejected")

	got, err := s.Picks().ListPicked()
	if err != nil {
		t.Fatalf("ListPicked: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	ids := map[int64]bool{got[0].FileID: true, got[1].FileID: true}
	if !ids[a] || !ids[b] {
		t.Errorf("ids = %v, want {%d, %d}", ids, a, b)
	}
	for _, p := range got {
		if p.State != "picked" {
			t.Errorf("non-picked in result: %+v", p)
		}
	}
}

func TestPicksExportedPath(t *testing.T) {
	s := newTestStore(t)

	fid, _ := s.Files().Insert(File{
		Path: "x.jpg", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err := s.Picks().Upsert(Pick{
		FileID: fid, State: "picked", PickedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := s.Picks().SetExportedPath(fid, "picks/Day1/x.jpg"); err != nil {
		t.Fatalf("SetExportedPath: %v", err)
	}
	got, _ := s.Picks().Get(fid)
	if !got.ExportedPath.Valid || got.ExportedPath.String != "picks/Day1/x.jpg" {
		t.Errorf("ExportedPath = %+v", got.ExportedPath)
	}

	if err := s.Picks().ClearExportedPath(fid); err != nil {
		t.Fatalf("ClearExportedPath: %v", err)
	}
	got, _ = s.Picks().Get(fid)
	if got.ExportedPath.Valid {
		t.Errorf("ExportedPath should be NULL, got %+v", got.ExportedPath)
	}
}
