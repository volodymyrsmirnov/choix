// Package deps locates external command-line tools (exiftool, ffmpeg) used by
// choix. Resolution order: system $PATH, then the choix support directory
// (~/.choix/bin/), then auto-fetch on demand.
package deps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// Source identifies where a resolved Tool was found.
type Source int

const (
	// SourceMissing means the tool could not be located and was not fetched.
	SourceMissing Source = iota
	// SourcePath means the tool was found via $PATH lookup.
	SourcePath
	// SourceSupportDir means the tool was found in the choix support directory.
	SourceSupportDir
	// SourceFetched means the tool was downloaded into the support directory.
	SourceFetched
)

// String returns a human-readable name for the Source.
func (s Source) String() string {
	switch s {
	case SourcePath:
		return "path"
	case SourceSupportDir:
		return "support-dir"
	case SourceFetched:
		return "fetched"
	default:
		return "missing"
	}
}

// Tool describes a resolved (or missing) external command.
type Tool struct {
	// Name is the unqualified command name (e.g. "exiftool").
	Name string
	// Path is the absolute filesystem path to the executable, or "" if missing.
	Path string
	// Source records which resolution step succeeded.
	Source Source
}

// Fetcher downloads a named tool into the support directory and returns its
// final absolute path. Implementations verify the binary against pinned
// SHA-256 hashes before installing it. The interface is satisfied by
// (*Downloader) defined in download.go and by test stubs.
type Fetcher interface {
	Fetch(ctx context.Context, name string) (string, error)
}

// Resolver locates external tools. Fields are exposed for test injection;
// production code uses NewResolver() which fills them with the real defaults.
type Resolver struct {
	// LookPath looks a binary up on $PATH. Defaults to exec.LookPath.
	LookPath func(string) (string, error)
	// SupportDir is the directory checked after $PATH (e.g.
	// ~/.choix/bin). Empty disables that step.
	SupportDir string
	// Fetcher downloads missing tools. Nil disables auto-fetch.
	Fetcher Fetcher
}

// NewResolver returns a Resolver with production defaults: exec.LookPath for
// $PATH, ~/.choix/bin for the support dir, and no fetcher (callers wire one
// in via the Fetcher field once they want auto-fetch behavior).
func NewResolver() *Resolver {
	return &Resolver{
		LookPath:   exec.LookPath,
		SupportDir: appdir.Bin(),
	}
}

// ErrToolMissing is returned by callers that want a hard error when Resolve
// produces SourceMissing. Resolve itself does not return this error; it
// returns a Tool with Source == SourceMissing.
var ErrToolMissing = errors.New("tool not found")

// ErrNotFound is an alias for ErrToolMissing that aligns with the naming
// convention used by internal/firstrun and its tests.
var ErrNotFound = ErrToolMissing

// ProgressEvent is emitted by Fetcher implementations to report download
// progress. Stage describes what is happening ("fetch", "verify", "extract");
// PercentDone is in [0, 1].
type ProgressEvent struct {
	Stage       string  `json:"stage"`
	PercentDone float64 `json:"percent_done"`
	Message     string  `json:"message,omitempty"`
}

// Resolve attempts to locate name. It returns a Tool whose Source records the
// resolution step that succeeded, or SourceMissing if every step failed. A
// non-nil error indicates an unexpected failure (e.g. fetcher I/O error);
// "tool not on this machine" is reported via Source, not error.
func (r *Resolver) Resolve(name string) (Tool, error) {
	if name == "" {
		return Tool{}, fmt.Errorf("deps: empty tool name")
	}
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if path, err := lookPath(name); err == nil && path != "" {
		return Tool{Name: name, Path: path, Source: SourcePath}, nil
	}
	if r.SupportDir != "" {
		candidate := filepath.Join(r.SupportDir, name)
		if isExecutableFile(candidate) {
			return Tool{Name: name, Path: candidate, Source: SourceSupportDir}, nil
		}
	}
	if r.Fetcher != nil {
		path, err := r.Fetcher.Fetch(context.Background(), name)
		if err != nil {
			return Tool{Name: name, Source: SourceMissing}, fmt.Errorf("deps: fetch %s: %w", name, err)
		}
		return Tool{Name: name, Path: path, Source: SourceFetched}, nil
	}
	return Tool{Name: name, Source: SourceMissing}, nil
}

// isExecutableFile reports whether path is a regular file with at least one
// executable bit set. Returns false on any stat error.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}
