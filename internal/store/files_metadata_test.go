package store

import (
	"context"
	"testing"
	"time"
)

func TestFilesUpdateMetadataFull(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Files().Insert(File{
		Path: "a.jpg", Size: 1, Mtime: time.Now().Unix(),
		ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	u := MetadataUpdate{
		DeviceKey:   "Apple iPhone 15 Pro#F2LZQ8XKQM",
		CapturedAt:  1723659131,
		HasCaptured: true,
		Width:       4032, Height: 3024,
		ISO: 64, Aperture: 1.78, Shutter: "1/250", FocalLength: 6.86,
		RawExif:    []byte{0x1f, 0x8b, 0x08},
		ScanStatus: "metadata",
	}
	if err := s.Files().UpdateMetadataFull(context.Background(), id, u); err != nil {
		t.Fatalf("UpdateMetadataFull: %v", err)
	}

	got, err := s.Files().GetByID(id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ScanStatus != "metadata" {
		t.Errorf("ScanStatus: got %q want metadata", got.ScanStatus)
	}
	if !got.DeviceKey.Valid || got.DeviceKey.String != u.DeviceKey {
		t.Errorf("DeviceKey: got %+v", got.DeviceKey)
	}
	if !got.CapturedAt.Valid || got.CapturedAt.Int64 != u.CapturedAt {
		t.Errorf("CapturedAt: got %+v", got.CapturedAt)
	}
	if !got.Width.Valid || got.Width.Int64 != 4032 {
		t.Errorf("Width: got %+v", got.Width)
	}
	if !got.Aperture.Valid || got.Aperture.Float64 < 1.77 || got.Aperture.Float64 > 1.79 {
		t.Errorf("Aperture: got %+v", got.Aperture)
	}
	if !got.Shutter.Valid || got.Shutter.String != "1/250" {
		t.Errorf("Shutter: got %+v", got.Shutter)
	}
	if len(got.RawExif) != 3 {
		t.Errorf("RawExif: got %d bytes want 3", len(got.RawExif))
	}
}

func TestFilesUpdateMetadataFullMissingRow(t *testing.T) {
	s := newTestStore(t)
	err := s.Files().UpdateMetadataFull(context.Background(), 9999, MetadataUpdate{ScanStatus: "metadata"})
	if err == nil {
		t.Error("expected error for missing row, got nil")
	}
}
