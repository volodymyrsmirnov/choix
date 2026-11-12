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

// TestKnownModelSizes pins the exact byte length of each model file. The
// values were read from local copies whose SHA-256 equals the pinned hash;
// they change only together with the URL and SHA256.
func TestKnownModelSizes(t *testing.T) {
	want := map[ModelKind]int64{
		ModelCLIP:    351686194,
		ModelQuality: 176803037,
	}
	for kind, size := range want {
		if got := KnownModels[kind].Size; got != size {
			t.Errorf("KnownModels[%s].Size = %d, want %d", kind, got, size)
		}
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
