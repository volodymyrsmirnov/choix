package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelStorePathAndHas(t *testing.T) {
	dir := t.TempDir()
	store := NewModelStore(dir)

	got := store.Path(ModelCLIP)
	want := filepath.Join(dir, KnownModels[ModelCLIP].FileName)
	if got != want {
		t.Errorf("Path(CLIP) = %q, want %q", got, want)
	}

	if store.Has(ModelCLIP) {
		t.Errorf("Has(CLIP) = true on empty store")
	}

	if err := os.WriteFile(store.Path(ModelCLIP), []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !store.Has(ModelCLIP) {
		t.Errorf("Has(CLIP) = false after writing file")
	}
}

func TestKnownCLIPModelIsPinned(t *testing.T) {
	m, ok := KnownModels[ModelCLIP]
	if !ok {
		t.Fatal("KnownModels missing CLIP entry")
	}
	if m.FileName == "" || m.URL == "" || m.SHA256 == "" {
		t.Errorf("KnownModels[CLIP] has empty field: %+v", m)
	}
}
