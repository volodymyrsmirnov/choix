package meta

import (
	"testing"
	"time"
)

func TestCapturedPrefersDateTimeOriginal(t *testing.T) {
	dto := time.Date(2025, 8, 14, 18, 32, 11, 0, time.UTC)
	cd := time.Date(2025, 8, 14, 18, 32, 12, 0, time.UTC)
	md := time.Date(2025, 8, 14, 18, 32, 13, 0, time.UTC)

	ts, ok := Captured(Metadata{DateTimeOriginal: dto, CreateDate: cd, ModifyDate: md})
	if !ok {
		t.Fatal("Captured: ok=false, want true")
	}
	if ts != dto.Unix() {
		t.Errorf("Captured: got %d want %d (DateTimeOriginal)", ts, dto.Unix())
	}
}

func TestCapturedFallsBackToCreateDate(t *testing.T) {
	cd := time.Date(2026, 1, 9, 14, 5, 20, 0, time.UTC)
	md := time.Date(2026, 1, 9, 14, 5, 21, 0, time.UTC)

	ts, ok := Captured(Metadata{CreateDate: cd, ModifyDate: md})
	if !ok {
		t.Fatal("Captured: ok=false, want true")
	}
	if ts != cd.Unix() {
		t.Errorf("Captured: got %d want %d (CreateDate)", ts, cd.Unix())
	}
}

func TestCapturedFallsBackToModifyDate(t *testing.T) {
	md := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	ts, ok := Captured(Metadata{ModifyDate: md})
	if !ok {
		t.Fatal("Captured: ok=false, want true")
	}
	if ts != md.Unix() {
		t.Errorf("Captured: got %d want %d (ModifyDate)", ts, md.Unix())
	}
}

func TestCapturedNoTimestamps(t *testing.T) {
	ts, ok := Captured(Metadata{})
	if ok {
		t.Errorf("Captured: ok=true want false (no timestamps); ts=%d", ts)
	}
	if ts != 0 {
		t.Errorf("Captured: ts=%d want 0", ts)
	}
}
