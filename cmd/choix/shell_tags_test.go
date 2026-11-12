package main

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modulePath is the module's import path (go.mod).
const modulePath = "github.com/volodymyrsmirnov/choix"

// testSupportPkg is the server build's remote control for end-to-end tests.
const testSupportPkg = modulePath + "/internal/app/testsupport"

// buildContext is the release target's build context with tags set.
func buildContext(tags ...string) build.Context {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = "darwin", "arm64", true
	ctx.BuildTags = tags
	return ctx
}

// moduleRoot returns the module root (two levels above cmd/choix).
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	return root
}

// moduleDeps returns the module's packages cmd/choix depends on, itself
// included, when built with ctx.
func moduleDeps(t *testing.T, ctx build.Context) map[string]bool {
	t.Helper()
	root := moduleRoot(t)
	seen := map[string]bool{}
	var visit func(path string)
	visit = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(path, modulePath), "/")))
		pkg, err := ctx.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("import %s: %v", path, err)
		}
		for _, imp := range pkg.Imports {
			if imp == modulePath || strings.HasPrefix(imp, modulePath+"/") {
				visit(imp)
			}
		}
	}
	visit(modulePath + "/cmd/choix")
	return seen
}

// TestDefaultBuildHasNoTestSupport: the test-support service — a remote
// control that adds folders by path — exists only in the server build. The
// default build neither compiles shell_server.go nor depends on the
// testsupport package; the server build does both (which also proves the
// dependency walk sees the package when it is there).
func TestDefaultBuildHasNoTestSupport(t *testing.T) {
	for _, tc := range []struct {
		tags        []string
		shell       string // the shell file compiled
		other       string // the shell file left out
		testSupport bool
	}{
		{nil, "shell_desktop.go", "shell_server.go", false},
		{[]string{"production"}, "shell_desktop.go", "shell_server.go", false},
		{[]string{"server"}, "shell_server.go", "shell_desktop.go", true},
	} {
		ctx := buildContext(tc.tags...)
		pkg, err := ctx.ImportDir(".", 0)
		if err != nil {
			t.Fatalf("tags %v: import cmd/choix: %v", tc.tags, err)
		}
		if !slices.Contains(pkg.GoFiles, tc.shell) || slices.Contains(pkg.GoFiles, tc.other) {
			t.Errorf("tags %v: GoFiles = %v; want %s and not %s", tc.tags, pkg.GoFiles, tc.shell, tc.other)
		}
		if got := moduleDeps(t, ctx)[testSupportPkg]; got != tc.testSupport {
			t.Errorf("tags %v: cmd/choix depends on %s = %v; want %v", tc.tags, testSupportPkg, got, tc.testSupport)
		}
	}
}

// TestTestSupportIsServerOnly: every file of the testsupport package, its
// tests included, carries the server constraint, so nothing of it is
// compiled into a build without the tag.
func TestTestSupportIsServerOnly(t *testing.T) {
	dir := filepath.Join(moduleRoot(t), "internal", "app", "testsupport")
	ctx := buildContext()
	_, err := ctx.ImportDir(dir, 0)
	var noGo *build.NoGoError
	if !errors.As(err, &noGo) {
		t.Fatalf("import testsupport without tags: err = %v; want *build.NoGoError (every file excluded)", err)
	}

	srv := buildContext("server")
	pkg, err := srv.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("import testsupport with the server tag: %v", err)
	}
	if len(pkg.GoFiles) == 0 || len(pkg.TestGoFiles) == 0 {
		t.Errorf("server build: GoFiles %v, TestGoFiles %v; want both non-empty", pkg.GoFiles, pkg.TestGoFiles)
	}
	all := slices.Concat(pkg.GoFiles, pkg.TestGoFiles, pkg.XTestGoFiles)
	if excluded := slices.Concat(pkg.IgnoredGoFiles, pkg.InvalidGoFiles); len(excluded) > 0 {
		t.Errorf("server build leaves out %v; every file must build with the tag", excluded)
	}
	if len(all) == 0 {
		t.Fatal("no files in testsupport")
	}
}
