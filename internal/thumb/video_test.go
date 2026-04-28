package thumb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/deps"
)

func requireFfmpeg(t *testing.T) *deps.Runner {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping")
	}
	return deps.NewRunner("ffmpeg")
}

// generateTestVideo creates a 2-second synthetic test video using ffmpeg's
// lavfi testsrc source and writes it to dst. Skips the test if ffmpeg is
// unavailable.
func generateTestVideo(t *testing.T, dst string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := exec.Command("ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=2:size=320x240:rate=10",
		"-pix_fmt", "yuv420p",
		dst,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v\n%s", err, out)
	}
}

func TestBuildVideoKeyframes(t *testing.T) {
	ff := requireFfmpeg(t)

	// Prefer a checked-in fixture (trimmed.mov) but fall back to a
	// synthetically generated video so the test still runs on CI where
	// binary fixtures are absent. fixturePath skips if the file is missing,
	// so we only use it when it exists.
	var src string
	fixtureAbs, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "trimmed.mov"))
	if _, err := os.Stat(fixtureAbs); err == nil {
		src = fixtureAbs
	} else {
		// Generate a synthetic 2-second video into a temp dir.
		tmp := t.TempDir()
		src = filepath.Join(tmp, "test.mov")
		generateTestVideo(t, src)
	}

	out := t.TempDir()
	pattern := filepath.Join(out, "%d.jpg")

	paths, err := BuildVideoKeyframes(context.Background(), ff, src, pattern)
	if err != nil {
		t.Fatalf("BuildVideoKeyframes: %v", err)
	}
	if len(paths) != 5 {
		t.Fatalf("got %d keyframes, want 5", len(paths))
	}
	for i, p := range paths {
		if !filepath.IsAbs(p) {
			t.Errorf("path[%d] = %q is not absolute", i, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Size() == 0 {
			t.Errorf("keyframe %s is empty", p)
		}
	}
}

// TestBuildVideoKeyframesNoDuplicatesFromPriorRun verifies that a second call
// to BuildVideoKeyframes does not mix frames from a prior run. A fresh temp
// dir is created each time, so stale frames can never appear.
func TestBuildVideoKeyframesNoDuplicatesFromPriorRun(t *testing.T) {
	ff := requireFfmpeg(t)

	tmp := t.TempDir()
	src := filepath.Join(tmp, "test.mov")
	generateTestVideo(t, src)

	// Simulate a shared parent directory (mirrors how KeyframesDir works).
	parent := t.TempDir()

	// First extraction.
	pattern1 := filepath.Join(parent, "run1", "%d.jpg")
	if err := os.MkdirAll(filepath.Dir(pattern1), 0o755); err != nil {
		t.Fatal(err)
	}
	paths1, err := BuildVideoKeyframes(context.Background(), ff, src, pattern1)
	if err != nil {
		t.Fatalf("first extraction: %v", err)
	}
	if len(paths1) != 5 {
		t.Fatalf("first extraction: got %d frames, want 5", len(paths1))
	}

	// Second extraction into the same parent.
	pattern2 := filepath.Join(parent, "run2", "%d.jpg")
	if err := os.MkdirAll(filepath.Dir(pattern2), 0o755); err != nil {
		t.Fatal(err)
	}
	paths2, err := BuildVideoKeyframes(context.Background(), ff, src, pattern2)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}
	if len(paths2) != 5 {
		t.Fatalf("second extraction: got %d frames, want 5", len(paths2))
	}

	// The two sets of paths must be disjoint — no frame from run1 should
	// appear in run2, confirming that each call uses its own fresh temp dir.
	set1 := make(map[string]struct{}, len(paths1))
	for _, p := range paths1 {
		set1[p] = struct{}{}
	}
	for _, p := range paths2 {
		if _, dup := set1[p]; dup {
			t.Errorf("path %q appears in both extractions (stale dir reuse)", p)
		}
	}
}
