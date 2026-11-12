package choix

import (
	"io/fs"
	"testing"
)

func TestFrontend_RootedAtDist(t *testing.T) {
	b, err := fs.ReadFile(Frontend(), "index.html")
	if err != nil {
		t.Fatalf("index.html at the root of Frontend(): %v", err)
	}
	if len(b) == 0 {
		t.Fatal("index.html is empty")
	}
	if _, err := fs.Stat(Frontend(), "frontend"); err == nil {
		t.Error("Frontend() is not rooted at frontend/dist")
	}
}
