package ui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestDistFSContainsIndexHTML(t *testing.T) {
	body, err := fs.ReadFile(DistFS(), "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if !strings.Contains(string(body), "<div id=\"root\">") {
		t.Fatal("index.html missing #root mount point")
	}
}

func TestIndexHTMLHelper(t *testing.T) {
	body, err := IndexHTML()
	if err != nil {
		t.Fatalf("IndexHTML: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("IndexHTML returned empty body")
	}
}
