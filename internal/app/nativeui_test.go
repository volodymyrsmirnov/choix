package app_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The native UI (window, menu, dialogs) is design-gated: it is built only in
// the three gated packages, so the approved design is the one that ships.
// These tests pin that statically. Files are parsed whatever their build
// constraints (a `//go:build server` shell is checked like the desktop one);
// only *_test.go files and `//go:build ignore` scripts, which are never part
// of a package, are left out.

// wailsApplication is the import path of the Wails application package, the
// one that holds the native UI constructors.
const wailsApplication = "github.com/wailsapp/wails/v3/pkg/application"

// gatedPackages are the design-gated packages, relative to the module root:
// the only places native UI may be built.
var gatedPackages = []string{
	"internal/app/menu",
	"internal/app/dialogs",
	"internal/app/window",
}

// wailsImporters are the only packages that may import Wails: the command
// and the gated packages. Matching is on the exact package directory.
var wailsImporters = append([]string{"cmd/choix"}, gatedPackages...)

// nativeUIIdents are the Wails constructors and option types that build
// native UI. Any identifier spelled exactly so is flagged in a file that
// imports the application package.
var nativeUIIdents = []string{
	"NewMenu",
	"NewContextMenu",
	"NewMenuItem",
	"NewMenuItemSeparator",
	"NewMenuItemCheckbox",
	"NewMenuItemRadio",
	"NewSubMenuItem",
	"NewSubmenu",
	"NewRole",
	"NewMenuFromItems",
	"WebviewWindowOptions",
	"MacWindow",
	"OpenFileDialogOptions",
	"MessageDialog",
}

// nativeUISelectors are the application's native-UI managers (and the two,
// Env and Browser, whose file-manager and URL openers run /usr/bin/open).
// Any selector `x.<name>` is flagged in a file that imports the application
// package, so such a file never names a field Menu, Window or Dialog (the
// runtime's field is MenuState).
var nativeUISelectors = []string{"Window", "Menu", "ContextMenu", "Dialog", "Env", "Browser"}

// allowNativeUI exempts exact names in one file from
// TestNativeUIConstructorsAreConfined. Keys are paths relative to the module
// root; each name (an identifier, or a selector written ".Name") maps to the
// reason it does not build native UI. An entry that no longer matches a use
// in its file fails the test, so the list cannot rot.
var allowNativeUI = map[string]map[string]string{}

// nativeUIUse is one flagged identifier or selector.
type nativeUIUse struct {
	name string // "NewMenu" for an identifier, ".Menu" for a selector
	pos  token.Pos
}

// nativeUIUses returns every native-UI identifier and selector in f, or
// nothing when f does not import the Wails application package.
func nativeUIUses(f *ast.File) []nativeUIUse {
	if !importsApplication(f) {
		return nil
	}
	var uses []nativeUIUse
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if slices.Contains(nativeUISelectors, n.Sel.Name) {
				uses = append(uses, nativeUIUse{name: "." + n.Sel.Name, pos: n.Sel.Pos()})
			}
		case *ast.Ident:
			if slices.Contains(nativeUIIdents, n.Name) {
				uses = append(uses, nativeUIUse{name: n.Name, pos: n.Pos()})
			}
		}
		return true
	})
	return uses
}

// wailsImports returns the Wails import paths of f when the file at rel (a
// slash path relative to the module root) is outside the packages allowed to
// import Wails.
func wailsImports(rel string, f *ast.File) []string {
	if slices.Contains(wailsImporters, path.Dir(rel)) {
		return nil
	}
	var bad []string
	for _, imp := range f.Imports {
		if p := importPath(imp); isWails(p) {
			bad = append(bad, p)
		}
	}
	return bad
}

// importsApplication reports whether f imports the Wails application package.
func importsApplication(f *ast.File) bool {
	return slices.ContainsFunc(f.Imports, func(imp *ast.ImportSpec) bool { return importPath(imp) == wailsApplication })
}

// isWails reports whether the import path p is in the Wails module (any
// major version), matched on whole path elements.
func isWails(p string) bool {
	return p == "github.com/wailsapp/wails" || strings.HasPrefix(p, "github.com/wailsapp/wails/")
}

// importPath returns the unquoted path of imp.
func importPath(imp *ast.ImportSpec) string {
	p, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		return ""
	}
	return p
}

// goFile is one parsed non-test Go file of the module.
type goFile struct {
	rel  string // slash path relative to the module root
	file *ast.File
}

// moduleGoFiles parses every non-test Go file under cmd/ and internal/,
// whatever its build constraints, except `//go:build ignore` scripts.
func moduleGoFiles(t *testing.T, fset *token.FileSet) []goFile {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moduleRoot, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", moduleRoot, err)
	}
	var files []goFile
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(moduleRoot, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(moduleRoot, p)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			if buildIgnored(f) {
				return nil
			}
			files = append(files, goFile{rel: filepath.ToSlash(rel), file: f})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no Go files found")
	}
	return files
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

// parseSource parses an in-memory self-check source.
func parseSource(t *testing.T, fset *token.FileSet, name, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("self-check source %s: %v", name, err)
	}
	return f
}

// shellWithNativeUI builds a window and opens the file manager and a URL
// through Wails: everything the tests exist to keep out.
const shellWithNativeUI = `package main

import "github.com/wailsapp/wails/v3/pkg/application"

func attach(app *application.App) {
	app.Window.NewWithOptions(application.WebviewWindowOptions{})
	app.Env.OpenFileManager("/", false)
	app.Browser.OpenURL("https://example.invalid")
}
`

// shellWithAliasedMenu builds a menu through an aliased import.
const shellWithAliasedMenu = `package main

import wails "github.com/wailsapp/wails/v3/pkg/application"

func attach(app *wails.App) {
	app.Menu.SetApplicationMenu(wails.NewMenu())
}
`

// shellWithMenuState wires the runtime's menu state and uses the application
// for nothing native: it must pass.
const shellWithMenuState = `package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
)

func wire(app *application.App, deps runtime.Deps) {
	deps.MenuState = nil
	app.Event.Emit("menu:action")
	app.OnShutdown(func() {})
	app.RegisterService(application.NewService(deps))
}
`

// runtimeWithMenuState is a non-Wails package using the runtime's menu
// state: it must pass.
const runtimeWithMenuState = `package runtime

type Deps struct{ MenuState any }

func menuState(deps Deps) any { return deps.MenuState }
`

// runtimeWithoutWails uses names that would be native UI in a Wails file; a
// file that does not import the application package is not inspected.
const runtimeWithoutWails = `package runtime

type ui struct{ Window, Menu, Env, Browser any }

func use(app ui) { _, _, _, _ = app.Window, app.Menu, app.Env, app.Browser }
`

// importsWailsEvents imports another Wails package.
const importsWailsEvents = `package runtime

import "github.com/wailsapp/wails/v3/pkg/events"

var _ = events.Common.ApplicationStarted
`

// TestNativeUIConstructorsAreConfined keeps native UI in the design-gated
// packages: outside internal/app/{menu,dialogs,window}, no non-test Go file
// that imports the Wails application package may use one of its native-UI
// constructors or option types, or select one of the application's native-UI
// managers (Window, Menu, ContextMenu, Dialog) or the Env and Browser
// managers, whose openers run /usr/bin/open.
func TestNativeUIConstructorsAreConfined(t *testing.T) {
	fset := token.NewFileSet()

	// Self-check: the detector flags exactly the native-UI names, and
	// nothing in a file that wires the runtime's MenuState.
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"shellWithNativeUI", shellWithNativeUI, []string{".Window", "WebviewWindowOptions", ".Env", ".Browser"}},
		{"shellWithAliasedMenu", shellWithAliasedMenu, []string{".Menu", "NewMenu"}},
		{"shellWithMenuState", shellWithMenuState, nil},
		{"runtimeWithoutWails", runtimeWithoutWails, nil},
	} {
		var got []string
		for _, u := range nativeUIUses(parseSource(t, fset, tc.name+".go", tc.src)) {
			got = append(got, u.name)
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("self-check %s: flagged %q, want %q", tc.name, got, tc.want)
		}
	}

	used := map[string]map[string]bool{} // allowNativeUI entries that matched
	var inspected int
	for _, gf := range moduleGoFiles(t, fset) {
		if slices.Contains(gatedPackages, path.Dir(gf.rel)) {
			continue
		}
		if importsApplication(gf.file) {
			inspected++
		}
		for _, u := range nativeUIUses(gf.file) {
			if _, ok := allowNativeUI[gf.rel][u.name]; ok {
				if used[gf.rel] == nil {
					used[gf.rel] = map[string]bool{}
				}
				used[gf.rel][u.name] = true
				continue
			}
			t.Errorf("%s:%d: uses %s; native UI is built only in %s",
				gf.rel, fset.Position(u.pos).Line, u.name, strings.Join(gatedPackages, ", "))
		}
	}
	if inspected == 0 {
		t.Fatalf("no file outside the gated packages imports %s; the walk checked nothing", wailsApplication)
	}
	for file, names := range allowNativeUI {
		for name := range names {
			if !used[file][name] {
				t.Errorf("allowNativeUI: %s no longer uses %s; remove the stale entry", file, name)
			}
		}
	}
}

// TestWailsImportIsConfined keeps Wails out of the engine, the platform
// layer and the services: only cmd/choix and the three design-gated packages
// may import any Wails package.
func TestWailsImportIsConfined(t *testing.T) {
	fset := token.NewFileSet()

	// Self-check: a Wails import is flagged by package directory, matched
	// exactly, and a package using the runtime's MenuState passes.
	for _, tc := range []struct {
		rel, src string
		flagged  bool
	}{
		{"internal/app/runtime/shell.go", shellWithNativeUI, true},
		{"internal/app/runtime/events.go", importsWailsEvents, true},
		{"internal/app/menus/menu.go", shellWithNativeUI, true},
		{"internal/app/menu/native/menu.go", shellWithNativeUI, true},
		{"cmd/choix/shell/shell.go", shellWithNativeUI, true},
		{"cmd/choix/shell.go", shellWithNativeUI, false},
		{"internal/app/menu/menu.go", shellWithAliasedMenu, false},
		{"internal/app/dialogs/dialogs.go", shellWithNativeUI, false},
		{"internal/app/window/window.go", shellWithNativeUI, false},
		{"internal/app/runtime/runtime.go", runtimeWithMenuState, false},
	} {
		got := wailsImports(tc.rel, parseSource(t, fset, tc.rel, tc.src))
		if (len(got) > 0) != tc.flagged {
			t.Fatalf("self-check %s: flagged %q, want flagged=%v", tc.rel, got, tc.flagged)
		}
	}

	var importers int
	for _, gf := range moduleGoFiles(t, fset) {
		if slices.ContainsFunc(gf.file.Imports, func(imp *ast.ImportSpec) bool { return isWails(importPath(imp)) }) {
			importers++
		}
		for _, p := range wailsImports(gf.rel, gf.file) {
			t.Errorf("%s: imports %s; only %s may import Wails", gf.rel, p, strings.Join(wailsImporters, ", "))
		}
	}
	if importers == 0 {
		t.Fatal("no file imports Wails; the walk checked nothing")
	}
}
