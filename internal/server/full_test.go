package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestFullStreamsOriginalReadOnly(t *testing.T) {
	env := newTestServer(t)
	rel := "Day1/A.JPG"
	abs := filepath.Join(env.scanRoot, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../testdata/http/fixture_thumb.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, src, 0o444); err != nil { // read-only on disk
		t.Fatal(err)
	}
	_ = seedFile(t, env.store, rel)

	resp := env.get("/full/" + rel)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if len(got) != len(src) {
		t.Errorf("len = %d, want %d", len(got), len(src))
	}
}

func TestFullRejectsTraversalPath(t *testing.T) {
	env := newTestServer(t)
	// Unknown path 404s cleanly.
	resp := env.get("/full/Day1/Nope.JPG")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}
