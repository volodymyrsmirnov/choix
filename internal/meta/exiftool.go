package meta

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Runner executes an external tool. Accepts interfaces, returns structs.
// Both *deps.Runner and test fakes satisfy this interface via duck typing.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExifTool runs the exiftool binary and returns its raw JSON output.
//
// The path to the binary is resolved by the deps package (Phase 2). Internally
// we delegate execution to a Runner so production code can swap a $PATH
// shim for a support-dir binary transparently, and tests can substitute a
// fake without spawning a process.
type ExifTool struct {
	path   string
	runner Runner
}

// New constructs an ExifTool that invokes the binary at toolPath through a
// default exec-based runner.
func New(toolPath string) *ExifTool {
	return &ExifTool{path: toolPath, runner: pathRunner{path: toolPath}}
}

// NewExifTool constructs an ExifTool that invokes the binary at toolPath,
// validating that path is non-empty. Returns an error if toolPath is empty.
func NewExifTool(toolPath string) (*ExifTool, error) {
	if toolPath == "" {
		return nil, fmt.Errorf("exiftool: empty tool path")
	}
	return New(toolPath), nil
}

// Close is a no-op for ExifTool (the binary is spawned per-call, not persistent).
// It exists to satisfy lifecycle interfaces in the thumb pipeline.
func (e *ExifTool) Close() error { return nil }

// ExtractPreviewImage runs `exiftool -b -PreviewImage <src>` and returns the
// raw JPEG bytes. Returns a nil slice (not an error) when no preview is embedded.
func (e *ExifTool) ExtractPreviewImage(ctx context.Context, src string) ([]byte, error) {
	out, err := e.runner.Run(ctx, "-b", "-PreviewImage", src)
	if err != nil {
		return nil, fmt.Errorf("exiftool ExtractPreviewImage %s: %w", src, err)
	}
	return bytes.TrimSpace(out), nil
}

// ExtractJpgFromRaw runs `exiftool -b -JpgFromRaw <src>` and returns the
// full-resolution embedded JPEG (present in Fujifilm RAF files and some others).
// Returns a nil slice (not an error) when no such tag exists.
func (e *ExifTool) ExtractJpgFromRaw(ctx context.Context, src string) ([]byte, error) {
	out, err := e.runner.Run(ctx, "-b", "-JpgFromRaw", src)
	if err != nil {
		return nil, fmt.Errorf("exiftool ExtractJpgFromRaw %s: %w", src, err)
	}
	return bytes.TrimSpace(out), nil
}

// newWithRunner is the test seam: it wires an explicit Runner instead of
// the default path-based one. Unexported because production callers should go
// through New.
func newWithRunner(r Runner) *ExifTool {
	return &ExifTool{runner: r}
}

// Path returns the configured exiftool path. Empty when the runner was
// injected directly (tests).
func (e *ExifTool) Path() string { return e.path }

// Run invokes `exiftool -j -G -n <filePath>` and returns its stdout.
//
// Flags:
//
//	-j   JSON output (an array with one object per file).
//	-G   Group prefix every key (e.g. "EXIF:Make", "QuickTime:CreateDate").
//	-n   Numeric output (no human-readable rounding) — required so we can
//	     recover precise apertures, focal lengths, and shutter times.
func (e *ExifTool) Run(ctx context.Context, filePath string) ([]byte, error) {
	out, err := e.runner.Run(ctx, "-j", "-G", "-n", filePath)
	if err != nil {
		return nil, fmt.Errorf("exiftool %s: %w", filePath, err)
	}
	return out, nil
}

// pathRunner is the default Runner used when callers go through New.
// In production, deps.Resolve returns a richer Runner that knows about the
// support dir; this fallback covers the common "binary on PATH" case so the
// meta package does not import deps internals.
type pathRunner struct{ path string }

func (p pathRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.path, args...) //nolint:gosec // resolved tool path, not user input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w (stderr: %s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}
