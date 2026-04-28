package deps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveFindsToolOnPath(t *testing.T) {
	r := NewResolver()
	tool, err := r.Resolve("ls")
	if err != nil {
		t.Fatalf("Resolve(ls): %v", err)
	}
	if tool.Name != "ls" {
		t.Errorf("Name = %q, want ls", tool.Name)
	}
	if tool.Source != SourcePath {
		t.Errorf("Source = %v, want SourcePath", tool.Source)
	}
	if tool.Path == "" {
		t.Error("Path is empty")
	}
}

func TestResolveMissingReturnsSourceMissing(t *testing.T) {
	r := NewResolver()
	tool, err := r.Resolve("definitely-not-a-real-binary-xyzzy")
	if err != nil {
		t.Fatalf("Resolve: unexpected err %v", err)
	}
	if tool.Source != SourceMissing {
		t.Errorf("Source = %v, want SourceMissing", tool.Source)
	}
	if tool.Path != "" {
		t.Errorf("Path = %q, want empty", tool.Path)
	}
}

func TestResolveFallsBackToSupportDir(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "exiftool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	r := &Resolver{
		// Force PATH lookup to fail for any name.
		LookPath:   func(string) (string, error) { return "", exec.ErrNotFound },
		SupportDir: dir,
	}
	tool, err := r.Resolve("exiftool")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tool.Source != SourceSupportDir {
		t.Errorf("Source = %v, want SourceSupportDir", tool.Source)
	}
	if tool.Path != bin {
		t.Errorf("Path = %q, want %q", tool.Path, bin)
	}
}

func TestResolveSupportDirIgnoredWhenFileMissing(t *testing.T) {
	dir := t.TempDir() // empty
	r := &Resolver{
		LookPath:   func(string) (string, error) { return "", exec.ErrNotFound },
		SupportDir: dir,
	}
	tool, _ := r.Resolve("exiftool")
	if tool.Source != SourceMissing {
		t.Errorf("Source = %v, want SourceMissing", tool.Source)
	}
}

// fakeFetcher records calls and writes a stub binary into the support dir.
type fakeFetcher struct {
	supportDir string
	calls      []string
}

func (f *fakeFetcher) Fetch(_ context.Context, name string) (string, error) {
	f.calls = append(f.calls, name)
	dest := filepath.Join(f.supportDir, name)
	if err := os.WriteFile(dest, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		return "", err
	}
	return dest, nil
}

func TestResolveFullPrecedence(t *testing.T) {
	supportDir := t.TempDir()

	t.Run("path wins over support dir", func(t *testing.T) {
		// Both PATH and support dir hold the tool; PATH must win.
		if err := os.WriteFile(filepath.Join(supportDir, "tool-a"),
			[]byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		ff := &fakeFetcher{supportDir: supportDir}
		r := &Resolver{
			LookPath:   func(string) (string, error) { return "/usr/bin/tool-a", nil },
			SupportDir: supportDir,
			Fetcher:    ff,
		}
		tool, err := r.Resolve("tool-a")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tool.Source != SourcePath {
			t.Errorf("Source = %v, want SourcePath", tool.Source)
		}
		if len(ff.calls) != 0 {
			t.Errorf("Fetcher called %v times, want 0", len(ff.calls))
		}
	})

	t.Run("support dir wins over fetch", func(t *testing.T) {
		bin := filepath.Join(supportDir, "tool-b")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		ff := &fakeFetcher{supportDir: supportDir}
		r := &Resolver{
			LookPath:   func(string) (string, error) { return "", exec.ErrNotFound },
			SupportDir: supportDir,
			Fetcher:    ff,
		}
		tool, err := r.Resolve("tool-b")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tool.Source != SourceSupportDir {
			t.Errorf("Source = %v, want SourceSupportDir", tool.Source)
		}
		if len(ff.calls) != 0 {
			t.Errorf("Fetcher called, want untouched (support dir hit)")
		}
	})

	t.Run("missing everywhere triggers fetch", func(t *testing.T) {
		ff := &fakeFetcher{supportDir: supportDir}
		r := &Resolver{
			LookPath:   func(string) (string, error) { return "", exec.ErrNotFound },
			SupportDir: supportDir,
			Fetcher:    ff,
		}
		tool, err := r.Resolve("tool-c")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tool.Source != SourceFetched {
			t.Errorf("Source = %v, want SourceFetched", tool.Source)
		}
		if tool.Path != filepath.Join(supportDir, "tool-c") {
			t.Errorf("Path = %q, want under supportDir", tool.Path)
		}
		if len(ff.calls) != 1 || ff.calls[0] != "tool-c" {
			t.Errorf("calls = %v, want [tool-c]", ff.calls)
		}
	})

	t.Run("missing with no fetcher returns SourceMissing", func(t *testing.T) {
		r := &Resolver{
			LookPath:   func(string) (string, error) { return "", exec.ErrNotFound },
			SupportDir: supportDir,
			// Fetcher: nil
		}
		tool, err := r.Resolve("tool-d")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tool.Source != SourceMissing {
			t.Errorf("Source = %v, want SourceMissing", tool.Source)
		}
	})
}
