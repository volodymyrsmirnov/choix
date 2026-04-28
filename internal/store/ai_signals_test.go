package store

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
)

func TestAISignalsUpsertAndGet(t *testing.T) {
	s := newTestStore(t)

	fid, err := s.Files().Insert(File{
		Path: "p.jpg", Size: 1, Mtime: 1, ContentHash: "h",
		Kind: "photo", Format: "jpeg", ScanStatus: "thumbed",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	rec := AISignals{
		FileID:        fid,
		ClipEmbedding: bytes.Repeat([]byte{0x01}, 512*4),
		ComputedAt:    sql.NullInt64{Int64: 1700000000, Valid: true},
	}
	if err := s.AISignals().Upsert(rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	rec.ClipEmbedding = bytes.Repeat([]byte{0x02}, 512*4)
	if err := s.AISignals().Upsert(rec); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}

	got, err := s.AISignals().GetByFileID(fid)
	if err != nil {
		t.Fatalf("GetByFileID: %v", err)
	}
	if got.FileID != fid {
		t.Errorf("FileID = %d, want %d", got.FileID, fid)
	}
	if !bytes.Equal(got.ClipEmbedding, rec.ClipEmbedding) {
		t.Errorf("clip_embedding mismatch after upsert")
	}
	if len(got.Embedding) != 512 {
		t.Errorf("decoded embedding len = %d, want 512", len(got.Embedding))
	}

	if _, err := s.AISignals().GetByFileID(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByFileID missing: got %v, want ErrNotFound", err)
	}
}

func TestAISignalsAllEmbeddings(t *testing.T) {
	s := newTestStore(t)

	mk := func(path string, withEmbed bool) int64 {
		id, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h",
			Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", path, err)
		}
		rec := AISignals{FileID: id}
		if withEmbed {
			rec.ClipEmbedding = bytes.Repeat([]byte{0x01}, 4*4)
		}
		if err := s.AISignals().Upsert(rec); err != nil {
			t.Fatalf("Upsert AI %s: %v", path, err)
		}
		return id
	}

	a := mk("a.jpg", true)
	b := mk("b.jpg", true)
	mk("c.jpg", false) // no embedding — must be absent from the map

	got, err := s.AISignals().AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if _, ok := got[a]; !ok {
		t.Errorf("missing id %d", a)
	}
	if _, ok := got[b]; !ok {
		t.Errorf("missing id %d", b)
	}
}
