//go:build darwin

package server

import (
	"os/exec"
	"sync/atomic"
	"testing"
)

func TestOpenBrowserInvokesOpen(t *testing.T) {
	var called atomic.Int32
	prev := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		called.Add(1)
		if name != "open" {
			t.Errorf("name = %q, want open", name)
		}
		if len(args) != 1 || args[0] != "http://example/" {
			t.Errorf("args = %v", args)
		}
		// Run /usr/bin/true so Start() succeeds without launching anything real.
		return exec.Command("/usr/bin/true")
	}
	t.Cleanup(func() { execCommand = prev })

	if err := OpenBrowser("http://example/"); err != nil {
		t.Fatalf("OpenBrowser: %v", err)
	}
	if called.Load() != 1 {
		t.Errorf("called = %d, want 1", called.Load())
	}
}
