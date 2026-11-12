package internal_test

import (
	"bufio"
	"bytes"
	"errors"
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

// retiredPaths are paths that must stay deleted, relative to the module
// root: v1's shell (Task 62: the cobra CLI, the HTTP server, the embedded
// SPA, the background quality worker, the Makefile, the install script and
// the HTTP test fixtures), and the downloaded models with everything that
// existed only for them (the built-in grouping and quality spec, §7: the
// ONNX Runtime package, the inference engine, the session's inference turn,
// the models binding with its download manager, and the build-time fetch
// and export scripts).
var retiredPaths = []string{
	"internal/cli",
	"internal/server",
	"internal/ui",
	"internal/quality/worker.go",
	"internal/quality/worker_test.go",
	"Makefile",
	"install.sh",
	"testdata/http",
	"internal/ai/local",
	"internal/app/inference",
	"internal/app/session/inference.go",
	"internal/app/models",
	"build/onnxruntime",
	"build/quality-model",
}

// netHTTPAllowed are the directories (relative to the module root) whose
// non-test Go files may import net/http: the media handler the webview loads
// thumbnails from, and the app's shells. The app makes no network request
// (hard rule 6).
var netHTTPAllowed = []string{
	"internal/app/media",
	"cmd/choix",
}

// onnxModule is the ONNX Runtime binding the app used to load its models.
const onnxModule = "github.com/yalue/onnxruntime_go"

// onnxTerm is what no non-test Go file under cmd/ or internal/ may contain.
const onnxTerm = "onnxruntime"

// retiredModules are v1's HTTP router, SSE hub and CLI framework.
var retiredModules = []string{
	"github.com/go-chi/chi/v5",
	"github.com/r3labs/sse/v2",
	"github.com/spf13/cobra",
}

// retiredTerms are v1 shell concepts with no place in 2.0's Go sources: the
// background-request header, the idle-shutdown flag and the browser launcher.
// Split so this file does not contain them.
var retiredTerms = []string{
	"X-Choix-" + "Background",
	"idle-" + "after",
	"Open" + "Browser",
}

// TestV1ShellIsGone pins the removal of v1's shell: its paths are gone, no
// non-test Go file outside netHTTPAllowed imports net/http, go.mod requires
// none of retiredModules, no Go source names a retired term, and
// internal/quality keeps its Scorer interface (in pass.go).
func TestV1ShellIsGone(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}

	for _, rel := range retiredPaths {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s still exists", rel)
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %s: %v", rel, err)
		}
	}

	for _, mod := range retiredModules {
		sc := bufio.NewScanner(bytes.NewReader(gomod))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			for i, f := range fields {
				// "require mod v", "\tmod v" (inside a require block).
				if f == mod && (i == 0 || (i == 1 && fields[0] == "require")) {
					t.Errorf("go.mod still requires %s: %q", mod, strings.TrimSpace(sc.Text()))
				}
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	}

	self := filepath.Join(root, "internal", "retired_test.go")
	fset := token.NewFileSet()
	var checked int
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		checked++

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if path != self {
			for _, term := range retiredTerms {
				if bytes.Contains(src, []byte(term)) {
					t.Errorf("%s contains %q", rel, term)
				}
			}
		}

		if strings.HasSuffix(path, "_test.go") || underAny(rel, netHTTPAllowed) {
			return nil
		}
		f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err == nil && (p == "net/http" || strings.HasPrefix(p, "net/http/")) {
				t.Errorf("%s imports %s", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go files checked")
	}

	pass := filepath.Join(root, "internal", "quality", "pass.go")
	f, err := parser.ParseFile(fset, pass, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("internal/quality/pass.go: %v", err)
	}
	if !declaresType(f, "Scorer") {
		t.Error("internal/quality/pass.go no longer declares Scorer")
	}
}

// TestNoONNX pins the removal of ONNX Runtime (built-in grouping and quality
// spec, §12): no non-test Go file under cmd/ or internal/ imports onnxModule
// or contains onnxTerm, and go.mod does not name onnxModule. Every .go file
// is read, whatever its build constraints.
func TestNoONNX(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	if bytes.Contains(gomod, []byte(onnxModule)) {
		t.Errorf("go.mod names %s", onnxModule)
	}

	fset := token.NewFileSet()
	var checked int
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); strings.HasPrefix(name, ".") || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			checked++

			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(src, []byte(onnxTerm)) {
				t.Errorf("%s contains %q", rel, onnxTerm)
			}
			f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err == nil && (p == onnxModule || strings.HasPrefix(p, onnxModule+"/")) {
					t.Errorf("%s imports %s", rel, p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no Go files checked")
	}
}

// underAny reports whether rel is inside one of dirs.
func underAny(rel string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// declaresType reports whether f declares a top-level type named name.
func declaresType(f *ast.File, name string) bool {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
				return true
			}
		}
	}
	return false
}
