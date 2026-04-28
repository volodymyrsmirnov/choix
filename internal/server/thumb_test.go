package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

func TestThumbServesCachedJPEG(t *testing.T) {
	env := newTestServer(t)
	id := seedFile(t, env.store, "Day1/A.JPG")

	// Create a tier-1 thumb on disk, register it in the store. RelPath is
	// stored relative to the scan root (matching what thumb.Builder writes
	// via filepath.Rel), so it includes the ".choix/thumbs/" prefix.
	dst := thumb.CachePath(env.scanRoot, id, thumb.TierThumb)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	relPath, err := filepath.Rel(env.scanRoot, dst)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	src, err := os.ReadFile("../../testdata/http/fixture_thumb.jpg")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	if err := env.store.Thumbs().Insert(store.Thumb{
		FileID: id, Tier: "thumb", RelPath: relPath, Width: 256, Height: 171,
	}); err != nil {
		t.Fatalf("thumbs insert: %v", err)
	}

	resp := env.get("/thumb/Day1/A.JPG?tier=thumb")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_ = id
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Error("missing ETag")
	}
	got, _ := io.ReadAll(resp.Body)
	wantHash := sha256.Sum256(src)
	gotHash := sha256.Sum256(got)
	if hex.EncodeToString(wantHash[:]) != hex.EncodeToString(gotHash[:]) {
		t.Error("body mismatch")
	}
}

func TestThumbReturns404WhenMissing(t *testing.T) {
	env := newTestServer(t)
	resp := env.get("/thumb/Day1/Nope.JPG?tier=thumb")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

func TestThumbDefaultsToTier1WhenTierMissing(t *testing.T) {
	env := newTestServer(t)
	_ = seedFile(t, env.store, "Day1/A.JPG")
	// Skip creating thumb on disk — the handler should still try the default
	// tier and 404 cleanly when not present.
	resp := env.get("/thumb/Day1/A.JPG")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no thumb on disk)", resp.StatusCode)
	}
}

func TestThumbRejectsBadTier(t *testing.T) {
	env := newTestServer(t)
	resp := env.get("/thumb/Day1/A.JPG?tier=banana")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

// TestThumbRejectsCorruptDBPathTraversal verifies that a thumb DB row with a
// rel_path that escapes the scan root (e.g. "../../escape") returns 403.
// This guards against a corrupt or maliciously modified database row.
func TestThumbRejectsCorruptDBPathTraversal(t *testing.T) {
	env := newTestServer(t)
	id := seedFile(t, env.store, "Day1/A.JPG")

	// "../../escape" joined with scanRoot resolves to scanRoot's grandparent
	// directory — clearly outside the scan root.
	traversalRelPath := "../../escape"
	if err := env.store.Thumbs().Insert(store.Thumb{
		FileID: id, Tier: "thumb", RelPath: traversalRelPath, Width: 256, Height: 171,
	}); err != nil {
		t.Fatalf("thumbs insert: %v", err)
	}

	resp := env.get("/thumb/Day1/A.JPG?tier=thumb")
	resp.Body.Close()
	// The handler must refuse to serve the file (403 Forbidden).
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for traversal path", resp.StatusCode)
	}
}

// TestThumbRejectsSymlinkOutsideScanRoot verifies that a symlink inside the
// .choix/thumbs cache directory pointing to a file outside the scan root is
// rejected by the thumb handler. This guards against symlink-based traversal.
func TestThumbRejectsSymlinkOutsideScanRoot(t *testing.T) {
	env := newTestServer(t)
	id := seedFile(t, env.store, "Day1/A.JPG")

	// Create a "secret" file outside the scan root.
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	// Create the cache dir inside the scan root and place a symlink there.
	cacheDir := filepath.Join(env.scanRoot, ".choix", "thumbs", "00")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(cacheDir, "symlinked.jpg")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Register the symlink as the thumb path for the file.
	relPath, err := filepath.Rel(env.scanRoot, symlinkPath)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if err := env.store.Thumbs().Insert(store.Thumb{
		FileID: id, Tier: "thumb", RelPath: relPath, Width: 256, Height: 171,
	}); err != nil {
		t.Fatalf("thumbs insert: %v", err)
	}

	resp := env.get("/thumb/Day1/A.JPG?tier=thumb")
	resp.Body.Close()
	// The handler must refuse to serve via the escaping symlink.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for symlink escaping scan root", resp.StatusCode)
	}
}
