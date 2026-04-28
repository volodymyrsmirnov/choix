package deps

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunnerRunSuccess(t *testing.T) {
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skipf("echo not on PATH: %v", err)
	}
	r := &Runner{Path: echo}
	out, err := r.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("stdout = %q, want %q", out, "hello")
	}
}

func TestRunnerRunNonZeroIncludesStderr(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	r := &Runner{Path: sh}
	_, err = r.Run(context.Background(), "-c", "echo boom >&2; exit 7")
	if err == nil {
		t.Fatal("Run: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want it to contain stderr text 'boom'", err)
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("err = %v, want it to mention exit status", err)
	}
}

func TestRunnerRunRespectsContextCancel(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	r := &Runner{Path: sh}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = r.Run(ctx, "-c", "sleep 5")
	if err == nil {
		t.Fatal("Run: expected cancellation error, got nil")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("ctx.Err = %v, want DeadlineExceeded", ctx.Err())
	}
}
