package store

import (
	"testing"
)

func TestThumbsUpsertAndGetByFile(t *testing.T) {
	s := newTestStore(t)

	fid, err := s.Files().Insert(File{
		Path: "x.jpg", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	if err := s.Thumbs().Upsert(Thumb{
		FileID: fid, Tier: "thumb", RelPath: "ab/1-thumb.jpg", Width: 256, Height: 170,
	}); err != nil {
		t.Fatalf("Upsert thumb: %v", err)
	}
	if err := s.Thumbs().Upsert(Thumb{
		FileID: fid, Tier: "preview", RelPath: "ab/1-preview.jpg", Width: 1600, Height: 1067,
	}); err != nil {
		t.Fatalf("Upsert preview: %v", err)
	}

	// Update an existing tier (Upsert == insert-or-replace).
	if err := s.Thumbs().Upsert(Thumb{
		FileID: fid, Tier: "thumb", RelPath: "ab/1-thumb-v2.jpg", Width: 256, Height: 170,
	}); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}

	m, err := s.Thumbs().GetByFile(fid)
	if err != nil {
		t.Fatalf("GetByFile: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("len = %d, want 2", len(m))
	}
	if m["thumb"].RelPath != "ab/1-thumb-v2.jpg" {
		t.Errorf("thumb.rel_path = %q, want overwritten value", m["thumb"].RelPath)
	}
	if m["preview"].Width != 1600 {
		t.Errorf("preview.width = %d, want 1600", m["preview"].Width)
	}

	// File with no thumbs returns an empty (non-nil) map.
	other, _ := s.Files().Insert(File{
		Path: "y.jpg", Size: 1, Mtime: 1, ContentHash: "h2",
		Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	em, err := s.Thumbs().GetByFile(other)
	if err != nil {
		t.Fatalf("GetByFile empty: %v", err)
	}
	if em == nil || len(em) != 0 {
		t.Errorf("empty result = %v, want non-nil empty map", em)
	}
}
