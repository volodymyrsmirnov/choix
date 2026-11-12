package internal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// allow lists the files exempt from TestNoExternalTools, relative to the
// module root.
var allow []string

// allowLiterals exempts exact banned literals in one file from the literal
// check only (the os/exec import check still applies). Keys are paths
// relative to the module root; each literal maps to the reason it is not a
// program name. An entry that no longer matches a literal in its file fails
// the test, so the list cannot rot.
var allowLiterals = map[string]map[string]string{
	"internal/platform/darwin/workspace/workspace.go": {
		"open": "operation name for the NSWorkspace openURL: call; no process is run",
	},
	"internal/safefs/safefs.go": {
		"open": "os.PathError.Op, as the standard library reports open failures",
	},
}

// bannedLiterals are the external programs choix no longer runs. A string
// literal equal to one of them is the name of a program about to be run.
var bannedLiterals = []string{"exiftool", "ffmpeg", "ffprobe", "sips", "open"}

// TestNoExternalTools keeps choix free of external programs: a sandboxed app
// cannot run them, and metadata and thumbnails come from Apple frameworks
// in-process. No non-test Go file under cmd/ or internal/ may import os/exec
// or contain a string literal equal to the name of a program choix used to run.
func TestNoExternalTools(t *testing.T) {
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moduleRoot, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", moduleRoot, err)
	}
	fset := token.NewFileSet()
	var checked int
	used := map[string]map[string]bool{} // allowLiterals entries that matched
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(moduleRoot, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(moduleRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if slices.Contains(allow, rel) {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			if buildIgnored(f) {
				return nil
			}
			checked++
			for _, imp := range f.Imports {
				if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == "os/exec" {
					t.Errorf("%s: imports os/exec", rel)
				}
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
				if !slices.Contains(bannedLiterals, v) {
					return true
				}
				if _, ok := allowLiterals[rel][v]; ok {
					if used[rel] == nil {
						used[rel] = map[string]bool{}
					}
					used[rel][v] = true
				} else {
					t.Errorf("%s:%d: string literal %q names an external program", rel, fset.Position(lit.Pos()).Line, v)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no Go files checked")
	}
	for file, lits := range allowLiterals {
		for lit := range lits {
			if !used[file][lit] {
				t.Errorf("allowLiterals: %s no longer contains the literal %q; remove the stale entry", file, lit)
			}
		}
	}
}

// buildIgnored reports whether f carries a //go:build ignore line before its
// package clause.
func buildIgnored(f *ast.File) bool {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.TrimSpace(c.Text) == "//go:build ignore" {
				return true
			}
		}
	}
	return false
}
