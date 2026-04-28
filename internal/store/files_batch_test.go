package store

import (
	"context"
	"testing"
	"time"
)

func TestFilesPickByStatus(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 5; i++ {
		_, err := s.Files().Insert(File{
			Path: filenameI(i), Size: 1, Mtime: time.Now().Unix(),
			ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "discovered",
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	got, err := s.Files().PickByStatus(context.Background(), "discovered", 3)
	if err != nil {
		t.Fatalf("PickByStatus: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len: got %d want 3", len(got))
	}
	if got[0].ID >= got[1].ID || got[1].ID >= got[2].ID {
		t.Errorf("not ordered by id: %v", []int64{got[0].ID, got[1].ID, got[2].ID})
	}

	none, err := s.Files().PickByStatus(context.Background(), "metadata", 100)
	if err != nil {
		t.Fatalf("PickByStatus(metadata): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 metadata-status rows, got %d", len(none))
	}
}

func filenameI(i int) string {
	return "file_" + string(rune('a'+i)) + ".jpg"
}
