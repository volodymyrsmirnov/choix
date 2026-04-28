package deps

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Runner executes a single resolved tool. Construct via Runner{Path: tool.Path}
// where tool was returned by Resolver.Resolve, or use NewRunner for tools
// resolved at runtime via $PATH.
type Runner struct {
	// Path is the absolute path to the executable.
	Path string
	// Env, if non-nil, replaces the child process environment. nil inherits.
	Env []string
}

// NewRunner constructs a Runner for the named binary. name may be a bare name
// (e.g. "ffmpeg") resolved via $PATH at exec time, or an absolute path.
func NewRunner(name string) *Runner {
	return &Runner{Path: name}
}

// Run executes the configured binary with args, returning stdout bytes on
// success. On failure the error text includes the command line, exit status,
// and captured stderr to make ad-hoc debugging painless. ctx cancellation
// kills the child process.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if r.Path == "" {
		return nil, fmt.Errorf("deps: Runner.Path is empty")
	}
	// r.Path comes from a Resolver.Resolve() result (PATH lookup, support dir,
	// or hash-pinned download), not from arbitrary user input — gosec G204 is
	// not applicable here.
	cmd := exec.CommandContext(ctx, r.Path, args...) //nolint:gosec // resolved tool path, not user input
	if r.Env != nil {
		cmd.Env = r.Env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("deps: %s %v: %w: %s",
			r.Path, args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

// RunWithStdin executes the configured binary with args, piping r into stdin.
// Useful for feeding pre-extracted JPEG bytes into ffmpeg via pipe:0.
func (r *Runner) RunWithStdin(ctx context.Context, stdin io.Reader, args ...string) error {
	if r.Path == "" {
		return fmt.Errorf("deps: Runner.Path is empty")
	}
	cmd := exec.CommandContext(ctx, r.Path, args...) //nolint:gosec // resolved tool path, not user input
	if r.Env != nil {
		cmd.Env = r.Env
	}
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("deps: %s %v: %w: %s",
			r.Path, args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}
