package buildinfo

import "testing"

// TestDefaults pins what a build without -ldflags -X reports.
func TestDefaults(t *testing.T) {
	if Version != "0.0.0-dev" {
		t.Errorf("Version = %q, want %q", Version, "0.0.0-dev")
	}
	if Commit != "unknown" {
		t.Errorf("Commit = %q, want %q", Commit, "unknown")
	}
}

// TestSettable checks that Version and Commit are string variables, which
// is what the linker's -X flag can set (it cannot set a constant). The
// assignments do not compile otherwise.
func TestSettable(t *testing.T) {
	v, c := Version, Commit
	t.Cleanup(func() { Version, Commit = v, c })
	Version, Commit = "1.2.3", "abc1234"
	if Version != "1.2.3" || Commit != "abc1234" {
		t.Fatalf("Version, Commit = %q, %q after assignment", Version, Commit)
	}
}
