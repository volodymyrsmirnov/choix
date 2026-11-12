package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFileURI_Escapes(t *testing.T) {
	cases := []struct {
		path    string
		pragmas []string
		want    string
	}{
		{
			path:    "/t/a #1?b%41/.choix/state.db",
			pragmas: []string{"journal_mode(WAL)", "foreign_keys(on)"},
			want:    "file:/t/a %231%3Fb%2541/.choix/state.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)",
		},
		{
			path: "/t/a #1?b%41/.choix/state.db",
			want: "file:/t/a %231%3Fb%2541/.choix/state.db",
		},
		{
			path: "/Users/tester/Photos/Trip/.choix/state.db",
			want: "file:/Users/tester/Photos/Trip/.choix/state.db",
		},
		{
			path:    "/Users/tester/Photos/Trip/.choix/state.db",
			pragmas: []string{"busy_timeout(10000)"},
			want:    "file:/Users/tester/Photos/Trip/.choix/state.db?_pragma=busy_timeout(10000)",
		},
		{
			// '%' is escaped first, so an already-escaped sequence survives.
			path: "/t/%23",
			want: "file:/t/%2523",
		},
	}
	for _, tc := range cases {
		if got := FileURI(tc.path, tc.pragmas...); got != tc.want {
			t.Errorf("FileURI(%q, %q) = %q, want %q", tc.path, tc.pragmas, got, tc.want)
		}
	}
}

// openAndRoundTrip opens the store at dbPath, writes a row, reopens it and
// reads the row back.
func openAndRoundTrip(t *testing.T, dbPath string) {
	t.Helper()
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	if _, err := s.Files().Insert(File{
		Path: "x.jpg", Size: 1, Mtime: 1, ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Files().GetByPath("x.jpg"); err != nil {
		t.Fatalf("row not read back after reopen: %v", err)
	}
	if fi, err := os.Stat(dbPath); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("database file %q: %v", dbPath, err)
	}
}

func TestOpen_FolderNameWithURICharacters(t *testing.T) {
	tmp := t.TempDir()
	folder := filepath.Join(tmp, "a #1?b%41")
	dotChoix := filepath.Join(folder, ".choix")
	if err := os.MkdirAll(dotChoix, 0o750); err != nil {
		t.Fatal(err)
	}
	openAndRoundTrip(t, filepath.Join(dotChoix, "state.db"))

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a #1?b%41" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s holds %q, want only the folder itself (nothing beside it)", tmp, names)
	}
	inside, err := os.ReadDir(dotChoix)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range inside {
		if e.Name() == "state.db" {
			found = true
		}
	}
	if !found {
		t.Fatalf("state.db not created inside %s", dotChoix)
	}
}

func TestOpen_PathWithSpacesAndUnicode(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "Été à Kyiv — 2026 ✈", ".choix")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	openAndRoundTrip(t, filepath.Join(dir, "state.db"))
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s holds %d entries, want 1", tmp, len(entries))
	}
}

// TestNoHandBuiltDSN keeps every database open going through FileURI: outside
// internal/store, no non-test Go file in the module may contain a string
// literal starting with "file:" (with SQLite's URI parsing, a folder named
// "Day #2" in a concatenated DSN would put the database beside the folder).
func TestNoHandBuiltDSN(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moduleRoot, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", moduleRoot, err)
	}
	storeDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != moduleRoot && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata") {
				return fs.SkipDir
			}
			if path == storeDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			// Case-sensitive, like SQLite's and the driver's own check
			// (exiftool's "File:ImageWidth" tags are not URIs).
			if strings.HasPrefix(v, "file:") {
				rel, _ := filepath.Rel(moduleRoot, path)
				t.Errorf("%s:%d: string literal %s builds a SQLite URI by hand; use store.Open / store.FileURI",
					rel, fset.Position(lit.Pos()).Line, lit.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", moduleRoot, err)
	}
}
