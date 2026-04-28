package firstrun

import (
	"context"
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/deps"
)

// Downloader is the subset of deps.Downloader the wizard needs. It
// must close the progress channel when finished, success or failure.
type Downloader interface {
	Fetch(ctx context.Context, name string, progress chan<- deps.ProgressEvent) error
}

// ModelInstaller is the subset of internal/ai/local used to materialize
// missing ONNX models.
type ModelInstaller interface {
	Install(ctx context.Context, kind local.ModelKind, progress chan<- deps.ProgressEvent) error
}

// ProgressFunc is invoked once per progress event.
type ProgressFunc func(deps.ProgressEvent)

// InstallTool fetches `name` (one of "exiftool", "ffmpeg") via the
// supplied Downloader and forwards each progress event to cb. The cb
// is invoked from the same goroutine, in order.
func InstallTool(ctx context.Context, d Downloader, name string, cb ProgressFunc) error {
	if name != "exiftool" && name != "ffmpeg" {
		return fmt.Errorf("firstrun: install: unknown tool %q", name)
	}

	ch := make(chan deps.ProgressEvent, 8)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Fetch(ctx, name, ch) }()

	for ev := range ch {
		cb(ev)
	}
	if err := <-errCh; err != nil {
		return fmt.Errorf("firstrun: install %s: %w", name, err)
	}
	return nil
}

// InstallModel is the model-side analogue.
func InstallModel(ctx context.Context, m ModelInstaller, kind local.ModelKind, cb ProgressFunc) error {
	ch := make(chan deps.ProgressEvent, 8)
	errCh := make(chan error, 1)
	go func() { errCh <- m.Install(ctx, kind, ch) }()

	for ev := range ch {
		cb(ev)
	}
	if err := <-errCh; err != nil {
		return fmt.Errorf("firstrun: install model %s: %w", kind, err)
	}
	return nil
}
