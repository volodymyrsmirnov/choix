package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalPath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, in, want string }{
		{"existing", filepath.Join(target, "sub"), filepath.Join(target, "sub")},
		{"through a link", filepath.Join(link, "sub"), filepath.Join(target, "sub")},
		{"missing under a link", filepath.Join(link, "a", "b"), filepath.Join(target, "a", "b")},
		{"missing", filepath.Join(root, "none", "x"), filepath.Join(root, "none", "x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalPath(tt.in)
			if err != nil || got != tt.want {
				t.Fatalf("canonicalPath(%s) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestRelation_PrefixIsNotInside(t *testing.T) {
	if got := relation("/a/Trip", "/a/Trip2"); got != "" {
		t.Fatalf(`relation("/a/Trip", "/a/Trip2") = %q, want ""`, got)
	}
	if got := relation("/a/Trip2", "/a/Trip"); got != "" {
		t.Fatalf(`relation("/a/Trip2", "/a/Trip") = %q, want ""`, got)
	}
}

func TestRelation(t *testing.T) {
	tests := []struct {
		name                string
		existing, candidate string
		want                string
	}{
		{"same", "/Volumes/X/Trip", "/Volumes/X/Trip", relSame},
		{"same ignoring case", "/Volumes/X/Trip", "/volumes/x/trip", relSame},
		{"same with trailing slash", "/Volumes/X/Trip", "/Volumes/X/Trip/", relSame},
		{"same uncleaned", "/Volumes/X/./Trip", "/Volumes/X/a/../Trip", relSame},
		{"same non-ASCII case", "/Volumes/X/Été", "/Volumes/X/éTÉ", relSame},
		{"same NFC and NFD", "/Volumes/X/Café", "/Volumes/X/Café", relSame},
		{"same NFD and NFC with case", "/Volumes/X/CAFÉ", "/volumes/x/café", relSame},
		{"inside NFD parent", "/Volumes/X/Café", "/Volumes/X/Café/Day 1", relInside},
		{"contains NFC parent", "/Volumes/X/Café/Day 1", "/Volumes/X/Café", relContains},
		{"inside", "/Volumes/X/Trip", "/Volumes/X/Trip/Day 1", relInside},
		{"inside picks", "/Volumes/X/Trip", "/Volumes/X/Trip/picks", relInside},
		{"inside ignoring case", "/Volumes/X/Trip", "/VOLUMES/x/trip/day", relInside},
		{"contains", "/Volumes/X/Trip/Day 1", "/Volumes/X/Trip", relContains},
		{"contains ignoring case", "/Volumes/X/Trip/Day 1", "/volumes/x/TRIP", relContains},
		{"root contains everything", "/Volumes/X/Trip", "/", relContains},
		{"everything is inside root", "/", "/Volumes/X/Trip", relInside},
		{"siblings", "/Volumes/X/Trip", "/Volumes/X/Other", ""},
		{"longer sibling", "/Volumes/X/Trip", "/Volumes/X/Trip 2", ""},
		{"other volume", "/Volumes/X/Trip", "/Volumes/Y/Trip", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relation(tt.existing, tt.candidate); got != tt.want {
				t.Fatalf("relation(%q, %q) = %q, want %q", tt.existing, tt.candidate, got, tt.want)
			}
		})
	}
}
