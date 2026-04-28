package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureScanRootAcceptsExistingDir(t *testing.T) {
	dir := t.TempDir()
	if err := ensureScanRoot(dir); err != nil {
		t.Errorf("ensureScanRoot on existing dir: %v", err)
	}
}

func TestEnsureScanRootRejectsMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := ensureScanRoot(missing); err == nil {
		t.Error("ensureScanRoot on missing path should return error")
	}
}

func TestEnsureScanRootRejectsRegularFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ensureScanRoot(f); err == nil {
		t.Error("ensureScanRoot on regular file should return error")
	}
}
