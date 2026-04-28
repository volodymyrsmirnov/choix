package meta

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestExifToolRun(t *testing.T) {
	path, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}

	tool := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := tool.Run(ctx, "../../testdata/exif/sample.jpg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(out), []byte("[")) {
		t.Fatalf("expected JSON array output, got: %s", out)
	}
	if !bytes.Contains(out, []byte("SourceFile")) {
		t.Fatalf("expected SourceFile key in output: %s", out)
	}
}

// fakeRunner lets tests assert on the args passed to exiftool without
// requiring the binary on PATH.
type fakeRunner struct {
	args []string
	out  []byte
	err  error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.args = append([]string(nil), args...)
	return f.out, f.err
}

func TestExifToolPassesCorrectArgs(t *testing.T) {
	fr := &fakeRunner{out: []byte("[]")}
	tool := newWithRunner(fr)

	if _, err := tool.Run(context.Background(), "/abs/path/file.jpg"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"-j", "-G", "-n", "/abs/path/file.jpg"}
	if len(fr.args) != len(want) {
		t.Fatalf("args: got %v want %v", fr.args, want)
	}
	for i := range want {
		if fr.args[i] != want[i] {
			t.Errorf("args[%d]: got %q want %q", i, fr.args[i], want[i])
		}
	}
}
