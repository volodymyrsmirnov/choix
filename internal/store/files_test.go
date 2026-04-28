package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestFilesInsertAndGetByPath(t *testing.T) {
	s := newTestStore(t)

	rec := File{
		Path:        "Day1/IMG_0001.RAF",
		Size:        12345,
		Mtime:       time.Now().Unix(),
		ContentHash: "abc123",
		Kind:        "photo",
		Format:      "raf",
		ScanStatus:  "discovered",
	}
	id, err := s.Files().Insert(rec)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == 0 {
		t.Fatal("Insert returned id=0")
	}
	got, err := s.Files().GetByPath("Day1/IMG_0001.RAF")
	if err != nil {
		t.Fatalf("GetByPath: %v", err)
	}
	if got.ID != id || got.ContentHash != "abc123" || got.Format != "raf" {
		t.Errorf("got %+v want id=%d hash=abc123 format=raf", got, id)
	}

	if _, err := s.Files().GetByPath("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByPath missing: got %v want ErrNotFound", err)
	}
}

func TestFilesUpdateMetadata(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Files().Insert(File{
		Path: "Day1/IMG_0010.RAF", Size: 100, Mtime: 1000, ContentHash: "h",
		Kind: "photo", Format: "raf", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	upd := File{
		ID:          id,
		DeviceKey:   sql.NullString{String: "Fujifilm X-T5#A1B2", Valid: true},
		CapturedAt:  sql.NullInt64{Int64: 1700000000, Valid: true},
		Width:       sql.NullInt64{Int64: 6240, Valid: true},
		Height:      sql.NullInt64{Int64: 4160, Valid: true},
		ISO:         sql.NullInt64{Int64: 400, Valid: true},
		Aperture:    sql.NullFloat64{Float64: 2.8, Valid: true},
		Shutter:     sql.NullString{String: "1/250", Valid: true},
		FocalLength: sql.NullFloat64{Float64: 35.0, Valid: true},
		RawExif:     []byte{0x1f, 0x8b, 0x08, 0x00},
	}
	if err := s.Files().UpdateMetadata(upd); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	got, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ScanStatus != "metadata" {
		t.Errorf("scan_status = %q, want metadata", got.ScanStatus)
	}
	if !got.DeviceKey.Valid || got.DeviceKey.String != "Fujifilm X-T5#A1B2" {
		t.Errorf("device_key = %+v", got.DeviceKey)
	}
	if !got.CapturedAt.Valid || got.CapturedAt.Int64 != 1700000000 {
		t.Errorf("captured_at = %+v", got.CapturedAt)
	}
	if !got.ISO.Valid || got.ISO.Int64 != 400 {
		t.Errorf("iso = %+v", got.ISO)
	}
	if got.Aperture.Float64 != 2.8 {
		t.Errorf("aperture = %v", got.Aperture.Float64)
	}
	if len(got.RawExif) != 4 {
		t.Errorf("raw_exif len = %d, want 4", len(got.RawExif))
	}
}

func TestFilesUpdateStatus(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Files().Insert(File{
		Path: "Day1/IMG_0011.RAF", Size: 1, Mtime: 1, ContentHash: "x",
		Kind: "photo", Format: "raf", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := s.Files().UpdateStatus(id, "thumbed", ""); err != nil {
		t.Fatalf("UpdateStatus thumbed: %v", err)
	}
	got, _ := s.Files().GetByID(id)
	if got.ScanStatus != "thumbed" || got.ErrMsg.Valid {
		t.Errorf("after thumbed: status=%q err=%+v", got.ScanStatus, got.ErrMsg)
	}

	if err := s.Files().UpdateStatus(id, "failed", "decode error: bad header"); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	got, _ = s.Files().GetByID(id)
	if got.ScanStatus != "failed" {
		t.Errorf("status = %q, want failed", got.ScanStatus)
	}
	if !got.ErrMsg.Valid || got.ErrMsg.String != "decode error: bad header" {
		t.Errorf("error = %+v", got.ErrMsg)
	}

	if err := s.Files().UpdateStatus(id, "analyzed", ""); err != nil {
		t.Fatalf("UpdateStatus analyzed: %v", err)
	}
	got, _ = s.Files().GetByID(id)
	if got.ScanStatus != "analyzed" || got.ErrMsg.Valid {
		t.Errorf("after recovery: status=%q err=%+v (error must clear)", got.ScanStatus, got.ErrMsg)
	}
}

func TestFilesListByStatus(t *testing.T) {
	s := newTestStore(t)

	mk := func(path, status string) int64 {
		id, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h", Kind: "photo", Format: "jpeg",
			ScanStatus: status,
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", path, err)
		}
		return id
	}
	a := mk("a.jpg", "discovered")
	b := mk("b.jpg", "discovered")
	mk("c.jpg", "metadata")
	d := mk("d.jpg", "discovered")

	got, err := s.Files().ListByStatus("discovered", 10)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantIDs := map[int64]bool{a: true, b: true, d: true}
	for _, f := range got {
		if !wantIDs[f.ID] {
			t.Errorf("unexpected id %d in result", f.ID)
		}
		if f.ScanStatus != "discovered" {
			t.Errorf("status = %q, want discovered", f.ScanStatus)
		}
	}

	limited, err := s.Files().ListByStatus("discovered", 2)
	if err != nil {
		t.Fatalf("ListByStatus limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited len = %d, want 2", len(limited))
	}

	none, err := s.Files().ListByStatus("analyzed", 10)
	if err != nil {
		t.Fatalf("ListByStatus empty: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("len = %d, want 0", len(none))
	}
}

func TestFilesMarkMissing(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Files().Insert(File{
		Path: "gone.jpg", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := s.Files().MarkMissing("gone.jpg"); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	got, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ScanStatus != "missing" {
		t.Errorf("scan_status = %q, want missing", got.ScanStatus)
	}

	// MarkMissing on an unknown path is a no-op (no error).
	if err := s.Files().MarkMissing("never-existed.jpg"); err != nil {
		t.Errorf("MarkMissing unknown: %v", err)
	}
}

func TestFilesPathsByStatus(t *testing.T) {
	s := newTestStore(t)

	mk := func(path, status string) {
		if _, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h",
			Kind: "photo", Format: "jpeg", ScanStatus: status,
		}); err != nil {
			t.Fatalf("Insert %s: %v", path, err)
		}
	}
	mk("a.jpg", "analyzed")
	mk("b.jpg", "analyzed")
	mk("c.jpg", "missing")
	mk("d.jpg", "discovered")

	paths, err := s.Files().PathsByStatus("analyzed")
	if err != nil {
		t.Fatalf("PathsByStatus analyzed: %v", err)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	if !got["a.jpg"] || !got["b.jpg"] || got["c.jpg"] || got["d.jpg"] {
		t.Errorf("paths = %v, want only a.jpg, b.jpg", paths)
	}

	missing, err := s.Files().PathsByStatus("missing")
	if err != nil {
		t.Fatalf("PathsByStatus missing: %v", err)
	}
	if len(missing) != 1 || missing[0] != "c.jpg" {
		t.Errorf("missing = %v, want [c.jpg]", missing)
	}
}

// TestInsertCaseMismatchReturnsSameID verifies that inserting a path that
// differs only in casing from an already-stored row returns the existing row's
// id without error. Migration 003 enforces UNIQUE(LOWER(path)), so the insert
// would trip that index; the Insert fallback must resolve to the existing row.
func TestInsertCaseMismatchReturnsSameID(t *testing.T) {
	s := newTestStore(t)

	id1, err := s.Files().Insert(File{
		Path: "day/a.jpg", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert lower: %v", err)
	}

	id2, err := s.Files().Insert(File{
		Path: "Day/A.JPG", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert upper: %v", err)
	}
	if id1 != id2 {
		t.Errorf("Insert case-mismatch: id1=%d id2=%d, want same id", id1, id2)
	}
}
