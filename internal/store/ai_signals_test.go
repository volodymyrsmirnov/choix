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
		FileID:          fid,
		Sharpness:       sql.NullFloat64{Float64: 0.82, Valid: true},
		FaceCount:       sql.NullInt64{Int64: 2, Valid: true},
		FacesEyesClosed: sql.NullInt64{Int64: 0, Valid: true},
		ExposureClipPct: sql.NullFloat64{Float64: 0.01, Valid: true},
		MeanLuma:        sql.NullFloat64{Float64: 0.45, Valid: true},
		NIMAScore:       sql.NullFloat64{Float64: 6.4, Valid: true},
		PHash:           []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04},
		ClipEmbedding:   bytes.Repeat([]byte{0x01}, 512*4),
		ComputedAt:      sql.NullInt64{Int64: 1700000000, Valid: true},
	}
	if err := s.AISignals().Upsert(rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Overwrite to confirm upsert semantics.
	rec.NIMAScore = sql.NullFloat64{Float64: 7.1, Valid: true}
	if err := s.AISignals().Upsert(rec); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}

	got, err := s.AISignals().GetByFile(fid)
	if err != nil {
		t.Fatalf("GetByFile: %v", err)
	}
	if got.FileID != fid {
		t.Errorf("FileID = %d, want %d", got.FileID, fid)
	}
	if got.NIMAScore.Float64 != 7.1 {
		t.Errorf("NIMAScore = %v, want 7.1", got.NIMAScore.Float64)
	}
	if !bytes.Equal(got.PHash, rec.PHash) {
		t.Errorf("phash mismatch: got %x", got.PHash)
	}
	if len(got.ClipEmbedding) != 512*4 {
		t.Errorf("clip_embedding len = %d, want 2048", len(got.ClipEmbedding))
	}

	if _, err := s.AISignals().GetByFile(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByFile missing: got %v, want ErrNotFound", err)
	}
}

func TestAISignalsListClipEmbeddings(t *testing.T) {
	s := newTestStore(t)

	mk := func(path, dev string, captured int64) int64 {
		id, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h",
			Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", path, err)
		}
		if err := s.Files().UpdateMetadata(File{
			ID:         id,
			DeviceKey:  sql.NullString{String: dev, Valid: true},
			CapturedAt: sql.NullInt64{Int64: captured, Valid: true},
		}); err != nil {
			t.Fatalf("UpdateMetadata %s: %v", path, err)
		}
		emb := bytes.Repeat([]byte{byte(captured % 256)}, 512*4)
		if err := s.AISignals().Upsert(AISignals{FileID: id, ClipEmbedding: emb}); err != nil {
			t.Fatalf("Upsert AI %s: %v", path, err)
		}
		return id
	}

	a := mk("a.jpg", "Cam#1", 1700000000)
	b := mk("b.jpg", "Cam#1", 1700000300)
	mk("c.jpg", "Cam#1", 1700001000) // outside bucket
	mk("d.jpg", "Cam#2", 1700000100) // wrong device

	got, err := s.AISignals().ListClipEmbeddings("Cam#1", 1700000000, 1700000600)
	if err != nil {
		t.Fatalf("ListClipEmbeddings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	ids := map[int64]bool{got[0].FileID: true, got[1].FileID: true}
	if !ids[a] || !ids[b] {
		t.Errorf("ids = %v, want {%d, %d}", ids, a, b)
	}
	for _, r := range got {
		if len(r.ClipEmbedding) != 512*4 {
			t.Errorf("file %d: embedding len %d", r.FileID, len(r.ClipEmbedding))
		}
	}
}
